package httpapi

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

func federationMergeTestJSON(t *testing.T, body string) map[string]any {
	t.Helper()
	var response map[string]any
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatal(err)
	}
	return response
}
func federationMergeTestNumber(t *testing.T, object map[string]any, key string, want float64) {
	t.Helper()
	got, ok := federationMergeNumber(object[key])
	if !ok || math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %v, want %v", key, object[key], want)
	}
}
func TestMergeFederatedAnalyticsWeightsRatesAndCacheDenominators(t *testing.T) {
	a := federationMergeTestJSON(t, `{
 "generated_at_ms":10,"granularity":"hour",
 "summary":{"total_calls":2,"success_calls":1,"failure_calls":1,"success_rate":0.5,"total_cost":4,"total_tokens":60,"average_latency_ms":100,"p95_latency_ms":120,"p95_ttft_ms":50,"cache_hit_rate":0.8,"rpm_30m":2,"tpm_30m":30,"approx_tasks":2,"approx_task_failures":1,"zero_token_models":["model-a"]},
 "summary_comparison":{"from_ms":1,"to_ms":10,"total_calls":2,"success_calls":1,"failure_calls":1,"total_cost":2,"total_tokens":10},
 "model_stats":[{"model":"same","calls":2,"success_calls":1,"failure_calls":1,"cache_hit_tokens":80,"cache_hit_input_tokens":100,"cache_hit_rate":0.8,"cost":4}]}`)
	b := federationMergeTestJSON(t, `{
 "generated_at_ms":20,"granularity":"hour",
 "summary":{"total_calls":8,"success_calls":8,"failure_calls":0,"success_rate":1,"total_cost":6,"total_tokens":40,"average_latency_ms":300,"p95_latency_ms":400,"p95_ttft_ms":90,"cache_hit_rate":0.2,"rpm_30m":5,"tpm_30m":20,"approx_tasks":8,"approx_task_failures":0,"zero_token_models":["model-a","model-b"]},
 "summary_comparison":{"from_ms":1,"to_ms":10,"total_calls":8,"success_calls":6,"failure_calls":2,"total_cost":6,"total_tokens":30},
 "model_stats":[{"model":"same","calls":8,"success_calls":8,"failure_calls":0,"cache_hit_tokens":180,"cache_hit_input_tokens":900,"cache_hit_rate":0.2,"cost":6}]}`)
	result := mergeFederatedAnalytics([]map[string]any{a, b})
	summary := result["summary"].(map[string]any)
	for key, value := range map[string]float64{"total_calls": 10, "success_calls": 9, "failure_calls": 1, "success_rate": 0.9, "failure_rate": 0.1, "total_cost": 10, "average_cost_per_call": 1, "total_tokens": 100, "rpm_30m": 7, "tpm_30m": 50, "cache_hit_rate": 0.26, "approx_task_success_rate": 0.9} {
		federationMergeTestNumber(t, summary, key, value)
	}
	if summary["average_latency_ms"] != nil || summary["p95_latency_ms"] != nil || summary["p95_ttft_ms"] != nil {
		t.Fatal("cannot combine source percentiles", summary)
	}
	if !reflect.DeepEqual(summary["zero_token_models"], []string{"model-a", "model-b"}) {
		t.Fatal("model union", summary)
	}
	comparison := result["summary_comparison"].(map[string]any)
	for key, value := range map[string]float64{"success_rate": 0.7, "total_tokens": 40, "from_ms": 1, "to_ms": 10} {
		federationMergeTestNumber(t, comparison, key, value)
	}
	model := result["model_stats"].([]any)[0].(map[string]any)
	federationMergeTestNumber(t, model, "cache_hit_rate", 0.26)
	federationMergeTestNumber(t, model, "success_rate", 0.9)
	federationMergeTestNumber(t, result, "generated_at_ms", 20)
}
func TestMergeFederatedAnalyticsMissingLatencyOrCacheDoesNotBecomeZero(t *testing.T) {
	for _, missing := range []any{nil, "unavailable"} {
		a := map[string]any{"summary": map[string]any{"total_calls": 2, "average_latency_ms": 100, "cache_hit_rate": 0.6}}
		b := map[string]any{"summary": map[string]any{"total_calls": 8, "average_latency_ms": missing, "cache_hit_rate": 0.1}}
		result := mergeFederatedAnalytics([]map[string]any{a, b})
		summary := result["summary"].(map[string]any)
		if summary["average_latency_ms"] != nil || summary["cache_hit_rate"] != nil {
			t.Fatal("unknown weighted values must be null", summary)
		}
		if len(result["warnings"].([]string)) == 0 {
			t.Fatal("missing denominator must be visible")
		}
	}
	a := map[string]any{"summary": map[string]any{"total_calls": 2, "average_latency_ms": 100}}
	b := map[string]any{"summary": map[string]any{"total_calls": 0, "average_latency_ms": nil}}
	result := mergeFederatedAnalytics([]map[string]any{a, b})
	if result["summary"].(map[string]any)["average_latency_ms"] != nil {
		t.Fatal("summary lacks latency sample counts even when another source reports zero calls")
	}
}
func TestMergeFederatedAnalyticsTimelineHeatmapAndDistinctContributors(t *testing.T) {
	a := federationMergeTestJSON(t, `{
 "timeline":[{"bucket_ms":200,"calls":2,"success":1,"failure":1,"average_latency_ms":100,"p95_latency_ms":130,"cache_hit_rate":0.5},{"bucket_ms":100,"calls":1,"success":1,"failure":0}],
 "hourly_distribution":[{"hour":2,"calls":2,"tokens":10}],"model_share":[{"model":"same","calls":2,"tokens":10,"cost":1}],
 "heatmap":[{"weekday":1,"hour":2,"calls":2,"success":1,"failure":1,"tokens":10,"cost":1,"model_contributors":[{"key":"same","calls":2,"success":1,"failure":1,"tokens":10,"cost":1}],"api_key_contributors":[{"key":"instance-a-key","instance_id":"a","calls":2,"success":1,"failure":1,"tokens":10,"cost":1}]}]}`)
	b := federationMergeTestJSON(t, `{
 "timeline":[{"bucket_ms":200,"calls":8,"success":8,"failure":0,"average_latency_ms":300,"p95_latency_ms":500,"cache_hit_rate":0.2}],
 "hourly_distribution":[{"hour":2,"calls":8,"tokens":30}],"model_share":[{"model":"same","calls":8,"tokens":30,"cost":3}],
 "heatmap":[{"weekday":1,"hour":2,"calls":8,"success":8,"failure":0,"tokens":30,"cost":3,"model_contributors":[{"key":"same","calls":4,"success":4,"failure":0,"tokens":20,"cost":2},{"key":"different","calls":4,"success":4,"failure":0,"tokens":10,"cost":1}],"api_key_contributors":[{"key":"instance-b-key","instance_id":"b","calls":8,"success":8,"failure":0,"tokens":30,"cost":3}]}]}`)
	result := mergeFederatedAnalytics([]map[string]any{a, b})
	timeline := result["timeline"].([]any)
	federationMergeTestNumber(t, timeline[0].(map[string]any), "bucket_ms", 100)
	point := timeline[1].(map[string]any)
	for key, value := range map[string]float64{"success_rate": 0.9, "failure_rate": 0.1} {
		federationMergeTestNumber(t, point, key, value)
	}
	if point["average_latency_ms"] != nil || point["cache_hit_rate"] != nil || point["p95_latency_ms"] != nil {
		t.Fatal("timeline must not guess rates/percentiles", point)
	}
	federationMergeTestNumber(t, result["hourly_distribution"].([]any)[0].(map[string]any), "tokens", 40)
	federationMergeTestNumber(t, result["model_share"].([]any)[0].(map[string]any), "cost", 4)
	heat := result["heatmap"].([]any)[0].(map[string]any)
	federationMergeTestNumber(t, heat, "calls", 10)
	federationMergeTestNumber(t, heat, "failure_rate", 0.1)
	models := heat["model_contributors"].([]any)
	federationMergeTestNumber(t, models[0].(map[string]any), "share", 0.6)
	federationMergeTestNumber(t, models[0].(map[string]any), "failure_rate", 1.0/6)
	keys := heat["api_key_contributors"].([]any)
	if len(keys) != 2 || keys[0].(map[string]any)["instance_id"] != "b" {
		t.Fatal("API key source identities lost", keys)
	}
	federationMergeTestNumber(t, keys[0].(map[string]any), "share", 0.8)
}
func TestMergeFederatedAnalyticsEntitiesFiltersAnomaliesAndNoMutation(t *testing.T) {
	a := federationMergeTestJSON(t, `{"account_stats":[{"id":"a:account","instance_id":"a","account_snapshot":"same"}],"api_key_stats":[{"id":"a:key","instance_id":"a","api_key_alias":"same"}],"filter_options":{"providers":["codex"],"models":["same"],"accounts":["a:account"],"account_count":1,"api_key_count":1,"api_key_stats":[{"id":"a:key","instance_id":"a"}]},"filter_selectors":{"api_key_ids":["a:key"]},"recent_failures":[{"timestamp_ms":1,"instance_id":"a"}],"anomaly_points":[{"bucket_ms":1,"instance_id":"a","instance_name":"A","severity":"high"}]}`)
	b := federationMergeTestJSON(t, `{"account_stats":[{"id":"b:account","instance_id":"b","account_snapshot":"same"}],"api_key_stats":[{"id":"b:key","instance_id":"b","api_key_alias":"same"}],"filter_options":{"providers":["codex","claude"],"models":["same","second"],"accounts":["b:account"],"account_count":1,"api_key_count":1,"api_key_stats":[{"id":"b:key","instance_id":"b"}]},"filter_selectors":{"api_key_ids":["b:key"]},"recent_failures":[{"timestamp_ms":3,"instance_id":"b"}],"anomaly_points":[{"bucket_ms":1,"instance_id":"b","instance_name":"B","severity":"high"}]}`)
	beforeA, _ := json.Marshal(a)
	beforeB, _ := json.Marshal(b)
	result := mergeFederatedAnalytics([]map[string]any{a, b})
	for _, field := range []string{"account_stats", "api_key_stats", "anomaly_points"} {
		if len(result[field].([]any)) != 2 {
			t.Fatal("matching labels must not combine source identities", field)
		}
	}
	options := result["filter_options"].(map[string]any)
	federationMergeTestNumber(t, options, "account_count", 2)
	federationMergeTestNumber(t, options, "api_key_count", 2)
	if !reflect.DeepEqual(options["providers"], []string{"codex", "claude"}) {
		t.Fatal("provider options not deduplicated", options)
	}
	if len(result["filter_selectors"].(map[string]any)["api_key_ids"].([]string)) != 2 {
		t.Fatal("key selectors lost an instance")
	}
	federationMergeTestNumber(t, result["recent_failures"].([]any)[0].(map[string]any), "timestamp_ms", 3)
	result["account_stats"].([]any)[0].(map[string]any)["id"] = "changed"
	options["api_key_stats"].([]any)[0].(map[string]any)["id"] = "changed"
	afterA, _ := json.Marshal(a)
	afterB, _ := json.Marshal(b)
	if string(beforeA) != string(afterA) || string(beforeB) != string(afterB) {
		t.Fatal("inputs were mutated")
	}
}
func TestMergeFederatedAnalyticsCoverageKeepsAllMissingRanges(t *testing.T) {
	a := federationMergeTestJSON(t, `{"coverage":{"scope":"requested_ranges","mode":"mixed","raw_complete":false,"core_aggregate_used":true,"raw_event_count":20,"raw_deleted_event_count":5,"min_deleted_timestamp_ms":10,"max_deleted_timestamp_ms":20,"comparison_raw_deleted_event_count":2,"fidelity_limitations":["event_details_require_raw_events"],"auxiliary_ranges":[{"scope":"rolling_30m","from_ms":1,"to_ms":100,"raw_event_count":5,"raw_deleted_event_count":3,"min_deleted_timestamp_ms":1,"max_deleted_timestamp_ms":10}]}}`)
	b := federationMergeTestJSON(t, `{"coverage":{"scope":"time_range","mode":"aggregate_only","raw_complete":false,"core_aggregate_used":true,"raw_event_count":0,"raw_deleted_event_count":8,"min_deleted_timestamp_ms":3,"max_deleted_timestamp_ms":30,"fidelity_limitations":["identity_metrics_require_raw_events","event_details_require_raw_events"],"auxiliary_ranges":[{"scope":"rolling_30m","from_ms":1,"to_ms":100,"raw_event_count":7,"raw_deleted_event_count":4,"min_deleted_timestamp_ms":2,"max_deleted_timestamp_ms":15}]}}`)
	result := mergeFederatedAnalytics([]map[string]any{a, b})
	coverage := result["coverage"].(map[string]any)
	if coverage["raw_complete"] != false || coverage["mode"] != "mixed" || coverage["scope"] != "requested_ranges" || coverage["core_aggregate_used"] != true {
		t.Fatal("coverage fidelity improved incorrectly", coverage)
	}
	for key, value := range map[string]float64{"raw_event_count": 20, "raw_deleted_event_count": 13, "min_deleted_timestamp_ms": 3, "max_deleted_timestamp_ms": 30, "comparison_raw_deleted_event_count": 2} {
		federationMergeTestNumber(t, coverage, key, value)
	}
	if len(coverage["fidelity_limitations"].([]string)) != 2 {
		t.Fatal("coverage limitations lost/duplicated", coverage)
	}
	aux := coverage["auxiliary_ranges"].([]any)[0].(map[string]any)
	for key, value := range map[string]float64{"raw_deleted_event_count": 7, "raw_event_count": 12, "min_deleted_timestamp_ms": 1, "max_deleted_timestamp_ms": 15} {
		federationMergeTestNumber(t, aux, key, value)
	}
	partial := mergeFederatedAnalytics([]map[string]any{a, {}})
	unknown := partial["coverage"].(map[string]any)
	if unknown["raw_complete"] != false {
		t.Fatal("missing coverage cannot claim raw completeness", unknown)
	}
	if _, exists := unknown["raw_event_count"]; exists {
		t.Fatal("partial count cannot claim complete count", unknown)
	}
	federationMergeTestNumber(t, unknown, "raw_deleted_event_count", 5)
	if len(partial["warnings"].([]string)) == 0 {
		t.Fatal("missing coverage must be visible")
	}
}
func TestMergeFederatedAnalyticsSingleSourcePreservesMeasuredPercentiles(t *testing.T) {
	response := federationMergeTestJSON(t, `{"summary":{"total_calls":2,"average_latency_ms":100,"p95_latency_ms":130,"p95_ttft_ms":40,"cache_hit_rate":0.25},"timeline":[{"bucket_ms":1,"calls":2,"p95_latency_ms":130,"cache_hit_rate":0.25}],"coverage":{"scope":"time_range","mode":"raw","raw_complete":true,"core_aggregate_used":false,"raw_event_count":2,"raw_deleted_event_count":0,"fidelity_limitations":[]}}`)
	result := mergeFederatedAnalytics([]map[string]any{response})
	federationMergeTestNumber(t, result["summary"].(map[string]any), "p95_latency_ms", 130)
	federationMergeTestNumber(t, result["summary"].(map[string]any), "cache_hit_rate", 0.25)
	federationMergeTestNumber(t, result["timeline"].([]any)[0].(map[string]any), "p95_latency_ms", 130)
	if result["coverage"].(map[string]any)["raw_complete"] != true {
		t.Fatal("single-source completeness changed", result)
	}
	if len(mergeFederatedAnalytics(nil)) != 0 {
		t.Fatal("empty input must produce empty DTO")
	}
}

func TestMergeFederatedAnalyticsPartialModelStatsRemainUnknown(t *testing.T) {
	responses := []map[string]any{
		federationMergeTestJSON(t, `{"summary":{"total_calls":10,"cache_hit_rate":0.5},"model_stats":[{"model":"same","calls":5,"cache_hit_tokens":5,"cache_hit_input_tokens":10,"cache_hit_rate":0.5}],"summary_comparison":{"from_ms":0,"to_ms":100,"total_calls":10}}`),
		federationMergeTestJSON(t, `{"summary":{"total_calls":10,"cache_hit_rate":0.5},"model_stats":[{"model":"same","calls":10,"cache_hit_rate":0.5}],"summary_comparison":{"from_ms":0,"to_ms":100,"total_calls":10}}`),
	}
	result := mergeFederatedAnalytics(responses)
	if result["summary"].(map[string]any)["cache_hit_rate"] != nil {
		t.Fatal("partial model counters must not yield complete cache rate")
	}
	if result["model_stats"].([]any)[0].(map[string]any)["cache_hit_rate"] != nil {
		t.Fatal("missing hit denominator must not yield model cache rate")
	}
	federationMergeTestNumber(t, result["summary_comparison"].(map[string]any), "from_ms", 0)
}

func TestMergeFederatedAnalyticsCoverageCannotHideContradictoryDeletedCount(t *testing.T) {
	response := federationMergeTestJSON(t, `{"coverage":{"scope":"time_range","mode":"raw","raw_complete":true,"core_aggregate_used":false,"raw_event_count":2,"raw_deleted_event_count":1,"fidelity_limitations":[]}}`)
	result := mergeFederatedAnalytics([]map[string]any{response})
	if result["coverage"].(map[string]any)["raw_complete"] != false {
		t.Fatal("deleted records disprove raw completeness")
	}
}

func TestMergeFederatedAnalyticsProjectedSelectorSchema(t *testing.T) {
	responses := []map[string]any{}
	for _, suffix := range []string{"a", "b"} {
		response := map[string]any{"filter_options": map[string]any{
			"api_key_hashes": []any{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + suffix},
			"accounts":       []any{"account-" + suffix},
			"auth_files":     []any{"credential-" + suffix + ".json"},
			"project_ids":    []any{"project-" + suffix},
			"providers":      []any{"codex"},
		}, "filter_selectors": map[string]any{"auth_files": []any{"credential-" + suffix + ".json"}}}
		projectAnalytics(response)
		responses = append(responses, response)
	}
	result := mergeFederatedAnalytics(responses)
	options := result["filter_options"].(map[string]any)
	for _, field := range []string{"api_key_ids", "account_ids", "auth_file_ids", "project_ids"} {
		if len(options[field].([]string)) != 2 {
			t.Fatalf("projected selector %s lost an instance: %v", field, options[field])
		}
	}
	for _, field := range []string{"auth_file_options", "project_options"} {
		rows, ok := options[field].([]any)
		if !ok || len(rows) != 2 {
			t.Fatalf("projected options %s lost an instance: %v", field, options[field])
		}
		for _, value := range rows {
			row := value.(map[string]any)
			if row["id"] == nil || row["display"] == nil {
				t.Fatalf("projected option identity/display lost: %v", row)
			}
		}
	}
	if len(result["filter_selectors"].(map[string]any)["auth_file_options"].([]any)) != 2 {
		t.Fatal("filter_selectors lost projected options")
	}
	for _, field := range []string{"api_key_hashes", "accounts", "auth_files"} {
		if _, exists := options[field]; exists {
			t.Fatalf("raw selector field unexpectedly published: %s", field)
		}
	}
}

func TestMergeFederatedAnalyticsMissingSuccessCountsDoNotBecomeFailures(t *testing.T) {
	responses := []map[string]any{
		{"summary": map[string]any{"total_calls": 2, "success_calls": 1, "failure_calls": 1}, "timeline": []any{map[string]any{"bucket_ms": 1, "calls": 2, "success": 1, "failure": 1}}},
		{"summary": map[string]any{"total_calls": 8, "success_rate": 0.75}, "timeline": []any{map[string]any{"bucket_ms": 1, "calls": 8, "success_rate": 0.75}}},
	}
	result := mergeFederatedAnalytics(responses)
	for _, row := range []map[string]any{result["summary"].(map[string]any), result["timeline"].([]any)[0].(map[string]any)} {
		if row["success_rate"] != nil || row["failure_rate"] != nil {
			t.Fatal("missing numerator must yield unknown rate", row)
		}
	}
}

func TestMergeFederatedAnalyticsComparisonRequiresMatchingCompleteIntervals(t *testing.T) {
	base := map[string]any{"summary_comparison": map[string]any{"from_ms": 1, "to_ms": 10, "total_calls": 2}}
	for _, second := range []map[string]any{
		{},
		{"summary_comparison": map[string]any{"total_calls": 8}},
		{"summary_comparison": map[string]any{"from_ms": 2, "to_ms": 11, "total_calls": 8}},
	} {
		result := mergeFederatedAnalytics([]map[string]any{base, second})
		if result["summary_comparison"] != nil {
			t.Fatal("unknown/mismatched interval must not imply comparable totals", result)
		}
		if len(result["warnings"].([]string)) == 0 {
			t.Fatal("missing comparison must be visible")
		}
	}
}

func TestMergeFederatedAnalyticsMeansNeedActualDenominators(t *testing.T) {
	// Each source has 10 calls, but only 2 versus 8 valid latency samples.
	// A calls-weighted mean would be 500; the actual pooled mean would be 740.
	// Neither the latency samples nor active-day counts are exposed in the DTO.
	a := federationMergeTestJSON(t, `{"summary":{"total_calls":10,"average_latency_ms":100,"avg_daily_requests":5,"avg_daily_tokens":50},"timeline":[{"bucket_ms":1,"calls":10,"average_latency_ms":100},{"bucket_ms":2,"calls":10,"average_latency_ms":123}]}`)
	b := federationMergeTestJSON(t, `{"summary":{"total_calls":10,"average_latency_ms":900,"avg_daily_requests":10,"avg_daily_tokens":100},"timeline":[{"bucket_ms":1,"calls":10,"average_latency_ms":900}]}`)
	result := mergeFederatedAnalytics([]map[string]any{a, b})
	summary := result["summary"].(map[string]any)
	for _, field := range []string{"average_latency_ms", "avg_daily_requests", "avg_daily_tokens"} {
		if summary[field] != nil {
			t.Fatalf("%s has no exact denominator: %v", field, summary[field])
		}
	}
	timeline := result["timeline"].([]any)
	if timeline[0].(map[string]any)["average_latency_ms"] != nil {
		t.Fatal("combined timeline guessed latency sample count")
	}
	federationMergeTestNumber(t, timeline[1].(map[string]any), "average_latency_ms", 123)
	single := mergeFederatedAnalytics([]map[string]any{a})["summary"].(map[string]any)
	federationMergeTestNumber(t, single, "average_latency_ms", 100)
	federationMergeTestNumber(t, single, "avg_daily_requests", 5)
}

func TestMergeFederatedAnalyticsHeatmapHiddenSixthContributorIsPartial(t *testing.T) {
	// Each source has five distinct local leaders with 10 calls, followed by the
	// same hidden model with 9 calls. The omitted model's combined 18 calls would
	// lead every exposed model, so the merged local top-five lists are not a
	// complete global ranking. The cell totals still include all 59 calls/source.
	responses := []map[string]any{}
	for _, source := range []string{"a", "b"} {
		contributors := []any{}
		for _, model := range []string{"1", "2", "3", "4", "5"} {
			contributors = append(contributors, map[string]any{"key": source + model, "calls": 10, "tokens": 100, "cost": 1})
		}
		responses = append(responses, map[string]any{"heatmap": []any{map[string]any{
			"weekday": 1, "hour": 2, "calls": 59, "tokens": 590, "cost": 5.9,
			"model_contributors":    contributors,
			"api_key_contributors":  []any{map[string]any{"api_key_id": source + "key", "calls": 59}},
			"provider_contributors": []any{map[string]any{"key": "codex", "calls": 59}},
		}}})
	}
	result := mergeFederatedAnalytics(responses)
	cell := result["heatmap"].([]any)[0].(map[string]any)
	if cell["contributors_partial"] != true {
		t.Fatal("truncated source rankings were presented as complete", cell)
	}
	federationMergeTestNumber(t, cell, "calls", 118)
	federationMergeTestNumber(t, cell, "tokens", 1180)
	federationMergeTestNumber(t, cell, "cost", 11.8)
	contributors := cell["model_contributors"].([]any)
	if len(contributors) != 10 {
		t.Fatal("must preserve known contributors without inventing omitted entries", contributors)
	}
	for _, value := range contributors {
		federationMergeTestNumber(t, value.(map[string]any), "calls", 10)
	}
	if len(result["warnings"].([]string)) == 0 {
		t.Fatal("partial contributor ranking must have a visible warning")
	}
}

func TestMergeFederatedAnalyticsHeatmapContributorCapAndIncompleteSums(t *testing.T) {
	for _, test := range []struct {
		name         string
		calls        int
		contributors []any
		partial      bool
	}{
		{"complete", 3, []any{map[string]any{"key": "known", "calls": 3}}, false},
		{"underreported", 3, []any{map[string]any{"key": "known", "calls": 2}}, true},
		{"at_upstream_cap", 5, []any{map[string]any{"key": "1", "calls": 1}, map[string]any{"key": "2", "calls": 1}, map[string]any{"key": "3", "calls": 1}, map[string]any{"key": "4", "calls": 1}, map[string]any{"key": "5", "calls": 1}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := map[string]any{"heatmap": []any{map[string]any{"weekday": 1, "hour": 2, "calls": test.calls, "model_contributors": test.contributors, "api_key_contributors": test.contributors, "provider_contributors": test.contributors}}}
			result := mergeFederatedAnalytics([]map[string]any{response})
			cell := result["heatmap"].([]any)[0].(map[string]any)
			partial, _ := cell["contributors_partial"].(bool)
			if partial != test.partial {
				t.Fatalf("partial=%v want %v", partial, test.partial)
			}
		})
	}
}

func TestMergeFederatedAnalyticsDifferentGranularitiesDoNotMixTimeBuckets(t *testing.T) {
	a := federationMergeTestJSON(t, `{"granularity":"hour","summary":{"total_calls":2,"success_calls":2,"failure_calls":0},"timeline":[{"bucket_ms":1000,"calls":1},{"bucket_ms":3601000,"calls":1}],"heatmap":[{"weekday":1,"hour":0,"calls":1}]}`)
	b := federationMergeTestJSON(t, `{"granularity":"day","summary":{"total_calls":48,"success_calls":48,"failure_calls":0},"timeline":[{"bucket_ms":1000,"calls":48}],"heatmap":[{"weekday":1,"hour":0,"calls":2}]}`)
	result := mergeFederatedAnalytics([]map[string]any{a, b})
	if len(result["timeline"].([]any)) != 0 {
		t.Fatalf("daily counts mislabeled as an hourly bucket: %#v", result["timeline"])
	}
	if intValue(result["summary"].(map[string]any)["total_calls"]) != 50 {
		t.Fatal("unrelated summary totals were lost")
	}
	// Heatmap's weekday/hour coordinates are fixed one-hour bins independent of
	// the requested hour/day timeline granularity.
	if rows := result["heatmap"].([]any); len(rows) != 1 || intValue(rows[0].(map[string]any)["calls"]) != 3 {
		t.Fatalf("fixed-coordinate heatmap totals changed: %#v", rows)
	}
	if len(result["warnings"].([]string)) == 0 {
		t.Fatal("incompatible timeline had no warning")
	}
}
