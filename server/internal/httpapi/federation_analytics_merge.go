package httpapi

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
)

// mergeFederatedAnalytics accepts already-projected, instance-qualified DTOs.
// It never projects them again: doing so would discard their source identities.
// The response owns all returned maps/slices and does not mutate its inputs.
// Events and drilldown pagination are handled separately by the caller.
func mergeFederatedAnalytics(responses []map[string]any) map[string]any {
	result := map[string]any{}
	if len(responses) == 0 {
		return result
	}
	multi := len(responses) > 1
	warnings := []string{}
	granularityConsistent := true
	for _, response := range responses {
		if n, ok := federationMergeNumber(response["generated_at_ms"]); ok {
			previous, present := federationMergeNumber(result["generated_at_ms"])
			if !present || n > previous {
				result["generated_at_ms"] = n
			}
		}
		if granularity, ok := response["granularity"].(string); ok && granularity != "" {
			if old, exists := result["granularity"]; !exists {
				result["granularity"] = granularity
			} else if old != granularity {
				granularityConsistent = false
				warnings = append(warnings, "不同来源的时间粒度不一致，无法准确合并时间线，暂不显示。")
			}
		}
		warnings = append(warnings, federationMergeStrings(response["warnings"])...)
	}
	for _, field := range []string{"summary", "summary_comparison"} {
		rows := federationMergeObjects(responses, field)
		if len(rows) == 0 {
			continue
		}
		if field == "summary_comparison" && (len(rows) != len(responses) || !federationMergeComparisonInterval(rows)) {
			result[field] = nil
			warnings = append(warnings, "部分来源缺少上一周期数据或比较区间不一致，暂不显示跨来源周期对比。")
			continue
		}
		merged := federationMergeMetrics(rows, "total_calls", multi)
		if field == "summary" && multi {
			merged["average_latency_ms"] = nil
			for _, averageField := range []string{"avg_daily_requests", "avg_daily_tokens"} {
				for _, row := range rows {
					if _, present := row[averageField]; present {
						merged[averageField] = nil
						break
					}
				}
			}
			merged["p95_latency_ms"] = nil
			merged["p95_ttft_ms"] = nil
			if rate, ok := federationMergeSummaryCacheRate(responses); ok {
				merged["cache_hit_rate"] = rate
			} else {
				merged["cache_hit_rate"] = nil
				warnings = append(warnings, "跨来源缓存命中率缺少完整的命中分母，暂不显示。")
			}
		}
		if len(rows) != len(responses) {
			warnings = append(warnings, "部分来源未返回汇总指标，汇总仅包含已返回的数据。")
		}
		result[field] = merged
	}
	for _, field := range []string{"timeline", "hourly_distribution", "model_share", "model_stats", "heatmap"} {
		if field == "timeline" && !granularityConsistent {
			result[field] = []any{}
			continue
		}
		rows, exists := federationMergeRows(responses, field)
		if !exists {
			continue
		}
		keys := []string{"model"}
		switch field {
		case "timeline":
			keys = []string{"bucket_ms"}
		case "hourly_distribution":
			keys = []string{"hour"}
		case "heatmap":
			keys = []string{"weekday", "hour"}
		}
		merged := federationMergeGrouped(rows, keys, multi)
		if field == "timeline" || field == "hourly_distribution" || field == "heatmap" {
			federationMergeSortNumbers(merged, keys, false)
		}
		result[field] = merged
		if field == "heatmap" {
			for _, value := range merged {
				if value.(map[string]any)["contributors_partial"] == true {
					warnings = append(warnings, "热力图贡献排行仅汇总各来源返回的部分样本，可能遗漏或低估贡献项；格内总量仍按各来源总量合并。")
					break
				}
			}
		}
	}
	// Entity rows intentionally remain distinct even when aliases/emails match.
	for _, field := range []string{"channel_share", "failure_sources", "account_stats", "credential_stats", "credential_timeline", "api_key_timeline", "api_key_stats", "task_buckets", "recent_failures", "anomaly_points"} {
		rows, exists := federationMergeRows(responses, field)
		if !exists {
			continue
		}
		if field == "recent_failures" {
			federationMergeSortNumbers(rows, []string{"timestamp_ms"}, true)
			if len(rows) > 100 {
				rows = rows[:100]
			}
		}
		if field == "anomaly_points" {
			rows = federationMergeUniqueRows(rows)
			federationMergeSortNumbers(rows, []string{"bucket_ms", "timestamp_ms"}, false)
		}
		result[field] = rows
	}
	for _, field := range []string{"filter_options", "filter_selectors"} {
		if rows := federationMergeObjects(responses, field); len(rows) > 0 {
			result[field] = federationMergeFilters(rows)
		}
	}
	if coverage, incomplete := federationMergeCoverage(responses); coverage != nil {
		result["coverage"] = coverage
		if incomplete {
			warnings = append(warnings, "部分来源未返回覆盖范围，不能确认所有来源的原始记录均完整。")
		}
	}
	if multi {
		if _, ok := result["summary"]; ok {
			warnings = append(warnings, "跨来源 P95 无法从各实例的百分位准确合并，暂不显示。", "跨来源平均延迟缺少有效样本数，暂不显示。")
			if summary, ok := result["summary"].(map[string]any); ok {
				if _, exists := summary["avg_daily_requests"]; exists {
					warnings = append(warnings, "各来源的活跃天数可能不同，跨来源日均指标暂不显示。")
				} else if _, exists := summary["avg_daily_tokens"]; exists {
					warnings = append(warnings, "各来源的活跃天数可能不同，跨来源日均指标暂不显示。")
				}
			}
		}
	}
	if len(warnings) > 0 {
		result["warnings"] = federationMergeUniqueStrings(warnings)
	}
	return result
}

var federationMergeAdditiveFields = []string{
	"total_calls", "calls", "success_calls", "failure_calls", "success", "failure",
	"input_tokens", "output_tokens", "cached_tokens", "reasoning_tokens", "tokens", "total_tokens",
	"cache_read_tokens", "cache_creation_tokens", "cache_hit_tokens", "cache_hit_input_tokens",
	"total_cost", "cost", "rpm_30m", "tpm_30m", "zero_token_calls",
	"approx_tasks", "approx_task_failures",
}

func federationMergeMetrics(rows []map[string]any, callsField string, multi bool) map[string]any {
	merged := federationMergeClone(rows[0]).(map[string]any)
	// Grouped rows have no single instance owner; entity arrays bypass this function.
	if len(rows) > 1 {
		delete(merged, "instance_id")
		delete(merged, "instance_name")
	}
	for _, field := range federationMergeAdditiveFields {
		var sum float64
		present := false
		for _, row := range rows {
			if value, ok := federationMergeNumber(row[field]); ok {
				sum += value
				present = true
			}
		}
		if present {
			merged[field] = sum
		}
	}
	for _, fields := range [][2]string{{"success_rate", "success_calls"}, {"failure_rate", "failure_calls"}} {
		rate, complete, present := federationMergeMetricRate(rows, fields[1], callsField, fields[0])
		if present {
			merged[fields[0]] = nil
			if complete {
				merged[fields[0]] = rate
			}
		}
	}
	if rate, complete, present := federationMergeMetricRate(rows, "total_cost", callsField, "average_cost_per_call"); present {
		merged["average_cost_per_call"] = nil
		if complete {
			merged["average_cost_per_call"] = rate
		}
	}
	if failureRate, complete, present := federationMergeMetricRate(rows, "approx_task_failures", "approx_tasks", "approx_task_success_rate"); present {
		merged["approx_task_success_rate"] = nil
		if complete {
			tasks, _ := federationMergeNumber(merged["approx_tasks"])
			merged["approx_task_success_rate"] = float64(0)
			if tasks > 0 {
				merged["approx_task_success_rate"] = math.Max(0, 1-failureRate)
			}
		}
	}
	if len(rows) > 1 {
		for _, row := range rows {
			if _, present := row["average_latency_ms"]; present {
				// Upstream averages nonzero latency samples, not request counts.
				// Its DTO omits that sample count, so pooled averages are unknown.
				merged["average_latency_ms"] = nil
				break
			}
		}
	}
	for _, field := range []string{"p95_latency_ms", "p95_ttft_ms"} {
		if multi {
			// Source percentiles cannot reconstruct the pooled distribution.
			if _, exists := merged[field]; exists {
				merged[field] = nil
			}
		}
	}
	if len(rows) > 1 {
		hit, denominator, complete := federationMergeCacheTotals(rows)
		if complete {
			merged["cache_hit_rate"] = math.Min(1, federationMergeRatio(hit, denominator))
		} else {
			for _, row := range rows {
				if _, present := row["cache_hit_rate"]; present {
					merged["cache_hit_rate"] = nil
					break
				}
			}
		}
	}
	var zeroModels []string
	for _, row := range rows {
		zeroModels = append(zeroModels, federationMergeStrings(row["zero_token_models"])...)
	}
	if len(zeroModels) > 0 {
		merged["zero_token_models"] = federationMergeUniqueStrings(zeroModels)
	}
	return merged
}

// Rates are available only when every participating, nonempty source reports
// the required counts. Missing numerators must never be interpreted as zero.
func federationMergeMetricRate(rows []map[string]any, numeratorField, denominatorField, rateField string) (float64, bool, bool) {
	var numerator, denominator float64
	complete, present := true, false
	for _, row := range rows {
		_, ratePresent := row[rateField]
		n, numeratorKnown := federationMergeNumber(row[numeratorField])
		if !numeratorKnown {
			n, numeratorKnown = federationMergeNumber(row[strings.TrimSuffix(numeratorField, "_calls")])
		}
		present = present || ratePresent || numeratorKnown
		d, denominatorKnown := federationMergeNumber(row[denominatorField])
		if !denominatorKnown {
			complete = false
			continue
		}
		denominator += d
		if d == 0 {
			continue
		}
		if !numeratorKnown {
			complete = false
			continue
		}
		numerator += n
	}
	return federationMergeRatio(numerator, denominator), complete, present
}

func federationMergeComparisonInterval(rows []map[string]any) bool {
	var from, to float64
	for i, row := range rows {
		f, fromKnown := federationMergeNumber(row["from_ms"])
		t, toKnown := federationMergeNumber(row["to_ms"])
		if !fromKnown || !toKnown || t <= f {
			return false
		}
		if i == 0 {
			from, to = f, t
		} else if f != from || t != to {
			return false
		}
	}
	return true
}

func federationMergeGrouped(rows []any, keys []string, multi bool) []any {
	groups := map[string][]map[string]any{}
	order := []string{}
	for _, value := range rows {
		row, ok := value.(map[string]any)
		if !ok {
			continue
		}
		key := federationMergeRowKey(row, keys)
		if _, exists := groups[key]; !exists {
			order = append(order, key)
		}
		groups[key] = append(groups[key], row)
	}
	result := make([]any, 0, len(order))
	for _, key := range order {
		group := groups[key]
		merged := federationMergeMetrics(group, "calls", multi)
		if len(keys) == 2 && keys[0] == "weekday" && keys[1] == "hour" && federationMergeHeatmapContributorsPartial(group) {
			merged["contributors_partial"] = true
		}
		for _, field := range []string{"model_contributors", "api_key_contributors", "provider_contributors"} {
			contributors, exists := federationMergeRows(group, field)
			if !exists {
				continue
			}
			// API contributor keys are already instance-qualified, while model
			// and provider keys deliberately combine across instances.
			for _, value := range contributors {
				row := value.(map[string]any)
				if field == "api_key_contributors" {
					if id, ok := row["api_key_id"].(string); ok && id != "" {
						row["key"] = id
					}
				}
			}
			mergedContributors := federationMergeGrouped(contributors, []string{"key"}, multi)
			totalCalls, _ := federationMergeNumber(merged["calls"])
			for _, value := range mergedContributors {
				row := value.(map[string]any)
				calls, _ := federationMergeNumber(row["calls"])
				row["share"] = federationMergeRatio(calls, totalCalls)
			}
			federationMergeSortNumbers(mergedContributors, []string{"calls", "cost"}, true)
			merged[field] = mergedContributors
		}
		result = append(result, merged)
	}
	return result
}

// CPAMP only exposes each source's top five contributors. A contributor that
// ranks sixth everywhere can lead the combined ranking, and an exposed entry
// can have unreported contributions elsewhere. Do not claim a complete ranking.
func federationMergeHeatmapContributorsPartial(rows []map[string]any) bool {
	for _, row := range rows {
		if row["contributors_partial"] == true {
			return true
		}
		cellCalls, callsKnown := federationMergeNumber(row["calls"])
		for _, field := range []string{"model_contributors", "api_key_contributors", "provider_contributors"} {
			contributors, exists := federationMergeRows([]map[string]any{row}, field)
			if len(contributors) >= 5 {
				return true
			}
			if !callsKnown || (!exists && cellCalls > 0) {
				return true
			}
			var representedCalls float64
			for _, value := range contributors {
				calls, known := federationMergeNumber(value.(map[string]any)["calls"])
				if !known {
					return true
				}
				representedCalls += calls
			}
			if representedCalls < cellCalls {
				return true
			}
		}
	}
	return false
}

func federationMergeFilters(rows []map[string]any) map[string]any {
	merged := map[string]any{}
	for _, field := range []string{"account_stats", "api_key_stats", "channel_share", "auth_file_options", "project_options"} {
		if values, exists := federationMergeRows(rows, field); exists {
			merged[field] = values
		}
	}
	if values, exists := federationMergeRows(rows, "model_stats"); exists {
		merged["model_stats"] = federationMergeGrouped(values, []string{"model"}, len(rows) > 1)
	}
	for _, field := range []string{"models", "api_key_ids", "providers", "account_ids", "auth_file_ids", "project_ids", "request_types", "header_error_kinds", "header_error_codes", "header_quota_plans"} {
		all := []string{}
		present := false
		for _, row := range rows {
			if _, exists := row[field]; exists {
				present = true
				all = append(all, federationMergeStrings(row[field])...)
			}
		}
		if present {
			merged[field] = federationMergeUniqueStrings(all)
		}
	}
	for _, field := range []string{"account_count", "api_key_count"} {
		var count float64
		present := false
		for _, row := range rows {
			if n, ok := federationMergeNumber(row[field]); ok {
				count += n
				present = true
			}
		}
		if present {
			merged[field] = count
		}
	}
	return merged
}

func federationMergeSummaryCacheRate(responses []map[string]any) (float64, bool) {
	var hit, denominator float64
	for _, response := range responses {
		summary, ok := response["summary"].(map[string]any)
		if !ok {
			return 0, false
		}
		calls, known := federationMergeNumber(summary["total_calls"])
		if !known {
			return 0, false
		}
		rows, _ := federationMergeRows([]map[string]any{response}, "model_stats")
		models := make([]map[string]any, 0, len(rows))
		var modelCalls float64
		for _, value := range rows {
			row := value.(map[string]any)
			count, ok := federationMergeNumber(row["calls"])
			if !ok {
				return 0, false
			}
			modelCalls += count
			models = append(models, row)
		}
		if modelCalls != calls {
			return 0, false
		}
		if calls == 0 && len(models) == 0 {
			continue
		}
		h, d, complete := federationMergeCacheTotals(models)
		if !complete {
			return 0, false
		}
		hit += h
		denominator += d
	}
	return math.Min(1, federationMergeRatio(hit, denominator)), true
}

func federationMergeCacheTotals(rows []map[string]any) (float64, float64, bool) {
	var hit, denominator float64
	for _, row := range rows {
		h, hitKnown := federationMergeNumber(row["cache_hit_tokens"])
		d, denominatorKnown := federationMergeNumber(row["cache_hit_input_tokens"])
		if !hitKnown || !denominatorKnown {
			return 0, 0, false
		}
		hit += h
		denominator += d
	}
	return hit, denominator, len(rows) > 0
}

func federationMergeCoverage(responses []map[string]any) (map[string]any, bool) {
	rows := federationMergeObjects(responses, "coverage")
	if len(rows) == 0 {
		return nil, false
	}
	merged := map[string]any{"scope": "time_range", "mode": "raw", "raw_complete": true, "core_aggregate_used": false}
	missing := len(rows) != len(responses)
	complete, aggregate, allRaw, allAggregate := !missing, false, !missing, !missing
	limitations := []string{}
	for _, row := range rows {
		rawComplete, known := row["raw_complete"].(bool)
		complete = complete && known && rawComplete
		for _, field := range []string{"raw_deleted_event_count", "comparison_raw_deleted_event_count"} {
			if deleted, ok := federationMergeNumber(row[field]); ok && deleted > 0 {
				complete = false
			}
		}
		auxiliary, _ := federationMergeRows([]map[string]any{row}, "auxiliary_ranges")
		for _, value := range auxiliary {
			if deleted, ok := federationMergeNumber(value.(map[string]any)["raw_deleted_event_count"]); ok && deleted > 0 {
				complete = false
			}
		}
		usesAggregate, _ := row["core_aggregate_used"].(bool)
		aggregate = aggregate || usesAggregate
		allRaw = allRaw && row["mode"] == "raw"
		allAggregate = allAggregate && row["mode"] == "aggregate_only"
		if row["scope"] == "requested_ranges" {
			merged["scope"] = "requested_ranges"
		}
		limitations = append(limitations, federationMergeStrings(row["fidelity_limitations"])...)
	}
	if missing {
		limitations = append(limitations, "other_metrics_require_raw_events")
	}
	merged["raw_complete"] = complete
	merged["core_aggregate_used"] = aggregate
	if allAggregate {
		merged["mode"] = "aggregate_only"
	} else if !allRaw {
		merged["mode"] = "mixed"
	}
	merged["fidelity_limitations"] = federationMergeUniqueStrings(limitations)
	federationMergeCoverageNumbers(merged, rows)
	// Missing coverage must not turn known partial raw counts into a complete count.
	if missing {
		delete(merged, "raw_event_count")
		delete(merged, "comparison_raw_event_count")
	}
	if ranges, exists := federationMergeRows(rows, "auxiliary_ranges"); exists {
		groups := map[string][]map[string]any{}
		order := []string{}
		for _, value := range ranges {
			row := value.(map[string]any)
			key := federationMergeRowKey(row, []string{"scope", "from_ms", "to_ms"})
			if _, exists := groups[key]; !exists {
				order = append(order, key)
			}
			groups[key] = append(groups[key], row)
		}
		mergedRanges := make([]any, 0, len(order))
		for _, key := range order {
			group := groups[key]
			rangeResult := federationMergeClone(group[0]).(map[string]any)
			federationMergeCoverageNumbers(rangeResult, group)
			if len(group) != len(responses) {
				delete(rangeResult, "raw_event_count")
			}
			mergedRanges = append(mergedRanges, rangeResult)
		}
		merged["auxiliary_ranges"] = mergedRanges
	}
	return merged, missing
}

func federationMergeCoverageNumbers(merged map[string]any, rows []map[string]any) {
	for _, field := range []string{"raw_event_count", "raw_deleted_event_count", "comparison_raw_event_count", "comparison_raw_deleted_event_count"} {
		var sum float64
		present, complete := false, true
		for _, row := range rows {
			value, ok := federationMergeNumber(row[field])
			complete = complete && ok
			if ok {
				sum += value
				present = true
			}
		}
		if present && (complete || strings.Contains(field, "deleted")) {
			merged[field] = sum
		} else {
			delete(merged, field)
		}
	}
	for _, pair := range [][2]string{{"min_deleted_timestamp_ms", "max_deleted_timestamp_ms"}, {"comparison_min_deleted_timestamp_ms", "comparison_max_deleted_timestamp_ms"}} {
		federationMergeBounds(merged, rows, pair[0], pair[1])
	}
}

func federationMergeBounds(merged map[string]any, rows []map[string]any, minField, maxField string) {
	for _, field := range []string{minField, maxField} {
		var bound float64
		present := false
		for _, row := range rows {
			value, ok := federationMergeNumber(row[field])
			if !ok || (value == 0 && strings.Contains(field, "deleted_timestamp")) {
				continue
			}
			if !present || (field == minField && value < bound) || (field == maxField && value > bound) {
				bound = value
				present = true
			}
		}
		if present {
			merged[field] = bound
		} else {
			delete(merged, field)
		}
	}
}

func federationMergeObjects(responses []map[string]any, field string) []map[string]any {
	rows := []map[string]any{}
	for _, response := range responses {
		if row, ok := response[field].(map[string]any); ok && row != nil {
			rows = append(rows, row)
		}
	}
	return rows
}

func federationMergeRows(responses []map[string]any, field string) ([]any, bool) {
	result := []any{}
	present := false
	for _, response := range responses {
		var values []any
		switch list := response[field].(type) {
		case []any:
			values, present = list, true
		case []map[string]any:
			present = true
			for _, row := range list {
				values = append(values, row)
			}
		}
		for _, value := range values {
			if row, ok := value.(map[string]any); ok {
				result = append(result, federationMergeClone(row))
			}
		}
	}
	return result, present
}

func federationMergeRowKey(row map[string]any, fields []string) string {
	parts := make([]any, 0, len(fields))
	for _, field := range fields {
		parts = append(parts, row[field])
	}
	encoded, _ := json.Marshal(parts)
	return string(encoded)
}

func federationMergeUniqueRows(rows []any) []any {
	seen := map[string]bool{}
	result := []any{}
	for _, row := range rows {
		encoded, err := json.Marshal(row)
		if err == nil && !seen[string(encoded)] {
			seen[string(encoded)] = true
			result = append(result, row)
		}
	}
	return result
}

func federationMergeSortNumbers(rows []any, fields []string, descending bool) {
	sort.SliceStable(rows, func(i, j int) bool {
		left, right := rows[i].(map[string]any), rows[j].(map[string]any)
		for _, field := range fields {
			a, _ := federationMergeNumber(left[field])
			b, _ := federationMergeNumber(right[field])
			if a != b {
				if descending {
					return a > b
				}
				return a < b
			}
		}
		return false
	})
}

func federationMergeNumber(value any) (float64, bool) {
	var n float64
	switch value := value.(type) {
	case float64:
		n = value
	case float32:
		n = float64(value)
	case int:
		n = float64(value)
	case int64:
		n = float64(value)
	case int32:
		n = float64(value)
	case json.Number:
		var err error
		n, err = value.Float64()
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return n, !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0
}

func federationMergeRatio(numerator, denominator float64) float64 {
	if denominator <= 0 {
		return 0
	}
	return numerator / denominator
}

func federationMergeStrings(value any) []string {
	result := []string{}
	switch values := value.(type) {
	case []string:
		result = append(result, values...)
	case []any:
		for _, item := range values {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
	}
	return result
}

func federationMergeUniqueStrings(values []string) []string {
	result := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func federationMergeClone(value any) any {
	switch value := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, child := range value {
			result[key] = federationMergeClone(child)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for i, child := range value {
			result[i] = federationMergeClone(child)
		}
		return result
	case []string:
		return append([]string{}, value...)
	case []map[string]any:
		result := make([]any, len(value))
		for i, child := range value {
			result[i] = federationMergeClone(child)
		}
		return result
	default:
		return value
	}
}
