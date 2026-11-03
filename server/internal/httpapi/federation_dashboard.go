package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type dashboardPublicSystem struct {
	ManagementVersion string `json:"management_version,omitempty"`
	ServerVersion     string `json:"server_version,omitempty"`
	BuildTime         any    `json:"build_time,omitempty"`
	CPABase           string `json:"cpa_base,omitempty"`
}
type dashboardPublicConfig struct {
	Debug           bool   `json:"debug"`
	LoggingToFile   bool   `json:"logging_to_file"`
	RequestRetry    int64  `json:"request_retry"`
	WSAuth          bool   `json:"ws_auth"`
	RoutingStrategy string `json:"routing_strategy,omitempty"`
	ProxyURL        string `json:"proxy_url,omitempty"`
}
type dashboardPublicHealth struct {
	UsageMonitor string `json:"usage_monitor"`
	RequestLog   string `json:"request_log"`
	DataSource   string `json:"data_source"`
	ErrorLogs    any    `json:"error_logs"`
}
type federatedDashboardInstance struct {
	InstanceID    string                 `json:"instance_id"`
	InstanceName  string                 `json:"instance_name"`
	Available     bool                   `json:"available"`
	GeneratedAtMS int64                  `json:"generated_at_ms,omitempty"`
	System        *dashboardPublicSystem `json:"system,omitempty"`
	Config        *dashboardPublicConfig `json:"config,omitempty"`
	Health        *dashboardPublicHealth `json:"health,omitempty"`
}

type federationDashboardClock struct{}

// CPAMP v1.14.1 accepts an integer without an upstream maximum. Keep our
// per-instance request bounded and identify results that may be truncated.
const federationDashboardModelLimit = 500

func federationDashboardRequestTime(r *http.Request) time.Time {
	if value, ok := r.Context().Value(federationDashboardClock{}).(time.Time); ok {
		return value
	}
	return time.Now()
}

// The only handlers invoked here are the existing fixed, sanitized single-source
// readers. This collector is bounded independently of upstream response sizes.
type federationJSONWriter struct {
	header   http.Header
	body     bytes.Buffer
	status   int
	tooLarge bool
}

func (w *federationJSONWriter) Header() http.Header { return w.header }
func (w *federationJSONWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *federationJSONWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.body.Len()+len(data) > 4<<20 {
		w.tooLarge = true
		return 0, errors.New("projected response exceeds limit")
	}
	return w.body.Write(data)
}
func federationProjectedJSON[T any](r *http.Request, reader *Server, handler func(*Server, http.ResponseWriter, *http.Request)) (T, error) {
	var result T
	if reader == nil || reader.cpamp == nil {
		return result, errors.New("instance unavailable")
	}
	local := *reader
	if local.logger == nil {
		local.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	timeout := local.cfg.RequestTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	w := &federationJSONWriter{header: make(http.Header)}
	handler(&local, w, r.Clone(ctx))
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if w.status != http.StatusOK || w.tooLarge {
		return result, errors.New("instance unavailable")
	}
	if err := json.Unmarshal(w.body.Bytes(), &result); err != nil {
		return result, errors.New("invalid projected instance response")
	}
	return result, nil
}

func dashboardInstanceName(source cpampReadSource) string {
	name := cleanText(strings.TrimSpace(source.Name), 80)
	if name == "" || name == "敏感错误详情已隐藏" || name == "内部错误详情已隐藏" || strings.Contains(name, "://") {
		return "CPA Manager Plus"
	}
	return name
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	sources := s.cpampReadSources()
	if len(sources) <= 1 {
		s.handleSingleDashboard(w, r)
		return
	}
	response, err := federateDashboard(r, sources)
	if err != nil {
		writeError(w, http.StatusBadGateway, "dashboard instances are unavailable")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func federateDashboard(r *http.Request, sources []cpampReadSource) (dashboardResponse, error) {
	type outcome struct {
		value dashboardResponse
		err   error
	}
	results := make([]outcome, len(sources))
	r = r.Clone(context.WithValue(r.Context(), federationDashboardClock{}, time.Now()))
	var workers sync.WaitGroup
	sem := make(chan struct{}, 4)
	for index, source := range sources {
		workers.Add(1)
		go func() {
			defer workers.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-r.Context().Done():
				results[index].err = r.Context().Err()
				return
			}
			results[index].value, results[index].err = federationProjectedJSON[dashboardResponse](r, source.Reader, (*Server).handleSingleDashboard)
		}()
	}
	workers.Wait()
	if err := r.Context().Err(); err != nil {
		return dashboardResponse{}, err
	}
	response := dashboardResponse{GeneratedAtMS: time.Now().UnixMilli(),
		TopModelsToday: []dashboardModel{}, ModelCostRank: []dashboardModel{}, TrafficTimeline: []dashboardTimelinePoint{},
		TokenMix: []dashboardTokenMix{}, ChannelHealth: []dashboardChannel{}, RecentFailures: []dashboardFailure{},
		Instances: []federatedDashboardInstance{}, Warnings: []string{}}
	response.System.ManagementVersion = viewerVersion
	response.Health.UsageMonitor, response.Health.RequestLog, response.Health.DataSource = "正常", "正常", "multiple"
	response.TodayRequestHealthTimeline.Points = []dashboardHealthPoint{}
	var successes int
	var latency dashboardObservedMean
	models, costs := map[string]dashboardModel{}, map[string]dashboardModel{}
	timeline, timelineMeans := map[int64]dashboardTimelinePoint{}, map[int64]*dashboardObservedMean{}
	health := map[int64]dashboardHealthPoint{}
	mix := map[string]int64{}
	modelCount, modelCountKnown, logCount, logCountKnown := 0, true, int64(0), true
	for index, result := range results {
		source := sources[index]
		name := dashboardInstanceName(source)
		instance := federatedDashboardInstance{InstanceID: source.ID, InstanceName: name, Available: result.err == nil}
		if result.err != nil {
			response.PartialFailed = true
			response.Warnings = append(response.Warnings, name+" 仪表盘暂不可用")
			response.Instances = append(response.Instances, instance)
			continue
		}
		successes++
		value := result.value
		value.System.CPABase = ""
		if value.Config != nil {
			value.Config.ProxyURL = ""
		}
		instance.GeneratedAtMS, instance.System, instance.Config, instance.Health = value.GeneratedAtMS, &value.System, value.Config, &value.Health
		response.Instances = append(response.Instances, instance)
		mergeDashboardWindow(&response.Window, value.Window)
		mergeDashboardMetrics(&response.Today, value.Today)
		latency.add(value.Today.AverageLatencyMS, value.Today.TotalCalls)
		response.Rolling30m.RPM += value.Rolling30m.RPM
		response.Rolling30m.TPM += value.Rolling30m.TPM
		response.Rolling30m.TotalCalls += value.Rolling30m.TotalCalls
		response.Rolling30m.TotalTokens += value.Rolling30m.TotalTokens
		response.Stats.ManagementKeys += value.Stats.ManagementKeys
		response.Stats.AuthFiles += value.Stats.AuthFiles
		response.Stats.Providers.Gemini += value.Stats.Providers.Gemini
		response.Stats.Providers.Codex += value.Stats.Providers.Codex
		response.Stats.Providers.XAI += value.Stats.Providers.XAI
		response.Stats.Providers.Meta += value.Stats.Providers.Meta
		response.Stats.Providers.Claude += value.Stats.Providers.Claude
		response.Stats.Providers.OpenAI += value.Stats.Providers.OpenAI
		response.Stats.Providers.Total += value.Stats.Providers.Total
		if value.Stats.AvailableModels == nil {
			modelCountKnown = false
		} else {
			modelCount += *value.Stats.AvailableModels
		}
		if count, ok := publicUsageInteger(value.Health.ErrorLogs); ok {
			logCount += count
		} else {
			logCountKnown = false
		}
		if value.Health.UsageMonitor != "正常" {
			response.Health.UsageMonitor = "部分不可用"
		}
		if value.Health.RequestLog != "正常" {
			response.Health.RequestLog = "部分不可用"
		}
		response.Collector.Events += value.Collector.Events
		response.Collector.DeadLetters += value.Collector.DeadLetters
		response.Collector.Collector.TotalInserted += value.Collector.Collector.TotalInserted
		response.Collector.Collector.TotalSkipped += value.Collector.Collector.TotalSkipped
		response.Collector.Collector.LastConsumedAt = max(response.Collector.Collector.LastConsumedAt, value.Collector.Collector.LastConsumedAt)
		response.Collector.Collector.LastInsertedAt = max(response.Collector.Collector.LastInsertedAt, value.Collector.Collector.LastInsertedAt)
		mergeDashboardModels(models, value.TopModelsToday)
		mergeDashboardModels(costs, value.ModelCostRank)
		var returnedModelCalls int64
		for _, model := range value.TopModelsToday {
			returnedModelCalls += model.Calls
		}
		if len(value.TopModelsToday) >= federationDashboardModelLimit || len(value.ModelCostRank) >= federationDashboardModelLimit || returnedModelCalls < value.Today.TotalCalls {
			response.ModelRankingsPartial = true
		}
		for _, point := range value.TrafficTimeline {
			merged := timeline[point.BucketMS]
			merged.BucketMS, merged.Label = point.BucketMS, point.Label
			merged.Calls += point.Calls
			merged.Tokens += point.Tokens
			merged.Success += point.Success
			merged.Failure += point.Failure
			merged.Cost += point.Cost
			mean := timelineMeans[point.BucketMS]
			if mean == nil {
				mean = &dashboardObservedMean{}
				timelineMeans[point.BucketMS] = mean
			}
			mean.add(point.AverageLatencyMS, point.Calls)
			timeline[point.BucketMS] = merged
		}
		for _, point := range value.TodayRequestHealthTimeline.Points {
			merged := health[point.BucketMS]
			merged.BucketMS = point.BucketMS
			merged.Calls += point.Calls
			merged.Tokens += point.Tokens
			merged.Success += point.Success
			merged.Failure += point.Failure
			merged.Future = point.Future && merged.Calls == 0
			health[point.BucketMS] = merged
		}
		response.TodayRequestHealthTimeline.FromMS = minPositiveDashboard(response.TodayRequestHealthTimeline.FromMS, value.TodayRequestHealthTimeline.FromMS)
		response.TodayRequestHealthTimeline.ToMS = max(response.TodayRequestHealthTimeline.ToMS, value.TodayRequestHealthTimeline.ToMS)
		if successes == 1 {
			response.TodayRequestHealthTimeline.BucketMS = value.TodayRequestHealthTimeline.BucketMS
		} else if response.TodayRequestHealthTimeline.BucketMS != value.TodayRequestHealthTimeline.BucketMS {
			response.TodayRequestHealthTimeline.BucketMS = 0
		}
		response.TodayRequestHealthTimeline.TotalCalls += value.TodayRequestHealthTimeline.TotalCalls
		response.TodayRequestHealthTimeline.SuccessCalls += value.TodayRequestHealthTimeline.SuccessCalls
		response.TodayRequestHealthTimeline.FailureCalls += value.TodayRequestHealthTimeline.FailureCalls
		for _, item := range value.TokenMix {
			mix[item.Key] += item.Tokens
		}
		for _, channel := range value.ChannelHealth {
			channel.InstanceID, channel.InstanceName = source.ID, name
			for _, id := range []*string{&channel.ID, &channel.AuthID, &channel.SourceID, &channel.AccountID, &channel.AuthLabelID} {
				if *id != "" {
					*id = federationViewID(source.ID, *id)
				}
			}
			response.ChannelHealth = append(response.ChannelHealth, channel)
		}
		for index, failure := range value.RecentFailures {
			failure.InstanceID, failure.InstanceName = source.ID, name
			failure.ID = federationViewID(source.ID, fmt.Sprintf("failure:%d:%d:%s", failure.TimestampMS, index, failure.APIKeyID))
			for _, id := range []*string{&failure.APIKeyID, &failure.AuthID, &failure.SourceID, &failure.AccountID, &failure.AuthLabelID, &failure.ProjectID} {
				if *id != "" {
					*id = federationViewID(source.ID, *id)
				}
			}
			response.RecentFailures = append(response.RecentFailures, failure)
		}
	}
	if successes == 0 {
		return dashboardResponse{}, errors.New("dashboard instances are unavailable")
	}
	response.Connection.Connected = true
	response.Today.SuccessRate = dashboardRatio(response.Today.SuccessCalls, response.Today.TotalCalls)
	response.Today.AverageLatencyMS = latency.value()
	response.Today.P95LatencyMS = nil
	if latency.sources > 1 {
		response.Warnings = append(response.Warnings, "跨实例延迟缺少有效样本数，平均值与 P95 暂不合并")
	}
	if response.ModelRankingsPartial {
		response.Warnings = append(response.Warnings, "模型排行仅基于各实例当前返回的记录，可能不完整")
	}
	if modelCountKnown {
		response.Stats.AvailableModels = &modelCount
	}
	if logCountKnown {
		response.Health.ErrorLogs = logCount
	} else {
		response.Health.ErrorLogs = "未检查"
	}
	if response.PartialFailed {
		response.Health.UsageMonitor = "部分不可用"
		response.Health.RequestLog = "部分不可用"
	}
	response.Collector.Collector.Mode = "multiple"
	response.Collector.Collector.Collector = "multiple"
	response.Collector.Collector.Queue = "multiple"
	response.TopModelsToday = dashboardModelRows(models, false)
	response.ModelCostRank = dashboardModelRows(costs, true)
	var maxCalls, maxTokens int64
	for _, point := range timeline {
		maxCalls = max(maxCalls, point.Calls)
		maxTokens = max(maxTokens, point.Tokens)
	}
	for _, point := range timeline {
		point.CallsShare = dashboardRatio(point.Calls, maxCalls)
		point.TokensShare = dashboardRatio(point.Tokens, maxTokens)
		point.SuccessRate = dashboardRatio(point.Success, point.Calls)
		point.FailureRate = dashboardRatio(point.Failure, point.Calls)
		point.AverageLatencyMS = timelineMeans[point.BucketMS].value()
		response.TrafficTimeline = append(response.TrafficTimeline, point)
	}
	sort.Slice(response.TrafficTimeline, func(i, j int) bool {
		return response.TrafficTimeline[i].BucketMS < response.TrafficTimeline[j].BucketMS
	})
	response.TodayRequestHealthTimeline.SuccessRate = dashboardRatio(response.TodayRequestHealthTimeline.SuccessCalls, response.TodayRequestHealthTimeline.TotalCalls)
	var maxHealthCalls int64
	for _, point := range health {
		maxHealthCalls = max(maxHealthCalls, point.Calls)
	}
	for _, point := range health {
		point.SuccessRate = dashboardRatio(point.Success, point.Calls)
		point.Future = point.BucketMS > response.Window.NowMS
		point.Intensity = dashboardRatio(point.Calls, maxHealthCalls)
		switch {
		case point.Future:
			point.Tone = "future"
		case point.Calls == 0:
			point.Tone = "empty"
		case dashboardRatio(point.Failure, point.Calls) >= 0.1:
			point.Tone = "bad"
		case point.Failure > 0:
			point.Tone = "warn"
		default:
			point.Tone = "good"
		}
		response.TodayRequestHealthTimeline.Points = append(response.TodayRequestHealthTimeline.Points, point)
	}
	sort.Slice(response.TodayRequestHealthTimeline.Points, func(i, j int) bool {
		return response.TodayRequestHealthTimeline.Points[i].BucketMS < response.TodayRequestHealthTimeline.Points[j].BucketMS
	})
	var mixTotal int64
	for _, tokens := range mix {
		mixTotal += tokens
	}
	for key, tokens := range mix {
		response.TokenMix = append(response.TokenMix, dashboardTokenMix{Key: key, Tokens: tokens, Share: dashboardRatio(tokens, mixTotal)})
	}
	sort.Slice(response.TokenMix, func(i, j int) bool { return response.TokenMix[i].Key < response.TokenMix[j].Key })
	sort.SliceStable(response.ChannelHealth, func(i, j int) bool {
		left, right := response.ChannelHealth[i], response.ChannelHealth[j]
		severity := func(tone string) int {
			if tone == "bad" {
				return 3
			}
			if tone == "warn" {
				return 2
			}
			return 1
		}
		if severity(left.Tone) != severity(right.Tone) {
			return severity(left.Tone) > severity(right.Tone)
		}
		if left.Failures != right.Failures {
			return left.Failures > right.Failures
		}
		return left.Calls > right.Calls
	})
	sort.SliceStable(response.RecentFailures, func(i, j int) bool {
		return response.RecentFailures[i].TimestampMS > response.RecentFailures[j].TimestampMS
	})
	return response, nil
}

// CPAMP averages only non-zero measured latencies, but does not expose their
// sample count. Request counts are not valid weights for combining these means.
type dashboardObservedMean struct {
	observed *float64
	sources  int
}

func (m *dashboardObservedMean) add(value *float64, calls int64) {
	if calls <= 0 {
		return
	}
	m.sources++
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 {
		return
	}
	copy := *value
	m.observed = &copy
}
func (m *dashboardObservedMean) value() *float64 {
	if m.sources != 1 {
		return nil
	}
	return m.observed
}
func dashboardRatio(numerator, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
func minPositiveDashboard(a, b int64) int64 {
	if a <= 0 {
		return b
	}
	if b <= 0 {
		return a
	}
	return min(a, b)
}
func mergeDashboardWindow(target *dashboardWindow, value dashboardWindow) {
	target.TodayStartMS = minPositiveDashboard(target.TodayStartMS, value.TodayStartMS)
	target.NowMS = max(target.NowMS, value.NowMS)
	target.Rolling30MStartMS = minPositiveDashboard(target.Rolling30MStartMS, value.Rolling30MStartMS)
}
func mergeDashboardMetrics(target *dashboardMetrics, value dashboardMetrics) {
	target.TotalCalls += value.TotalCalls
	target.SuccessCalls += value.SuccessCalls
	target.FailureCalls += value.FailureCalls
	target.InputTokens += value.InputTokens
	target.OutputTokens += value.OutputTokens
	target.CachedTokens += value.CachedTokens
	target.ReasoningTokens += value.ReasoningTokens
	target.TotalTokens += value.TotalTokens
	target.TotalCost += value.TotalCost
	target.RPM30m += value.RPM30m
	target.TPM30m += value.TPM30m
	target.ZeroTokenCalls += value.ZeroTokenCalls
	target.CacheReadTokens += value.CacheReadTokens
	target.CacheCreationToken += value.CacheCreationToken
}
func mergeDashboardModels(target map[string]dashboardModel, values []dashboardModel) {
	for _, value := range values {
		merged := target[value.Model]
		merged.Model = value.Model
		merged.Calls += value.Calls
		merged.Tokens += value.Tokens
		merged.SuccessCalls += value.SuccessCalls
		merged.FailureCalls += value.FailureCalls
		merged.InputTokens += value.InputTokens
		merged.OutputTokens += value.OutputTokens
		merged.CachedTokens += value.CachedTokens
		merged.TotalTokens += max(value.TotalTokens, value.Tokens)
		merged.Cost += value.Cost
		// The dashboard endpoint supplies a success ratio, not success/failure
		// counts for model rows. Weight that ratio by its actual request count.
		merged.SuccessRate += value.SuccessRate * float64(value.Calls)
		target[value.Model] = merged
	}
}
func dashboardModelRows(values map[string]dashboardModel, byCost bool) []dashboardModel {
	rows := make([]dashboardModel, 0, len(values))
	var maxCost float64
	for _, row := range values {
		maxCost = max(maxCost, row.Cost)
	}
	for _, row := range values {
		if row.Calls > 0 {
			row.SuccessRate /= float64(row.Calls)
		}
		if maxCost > 0 {
			row.CostShare = row.Cost / maxCost
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if byCost && rows[i].Cost != rows[j].Cost {
			return rows[i].Cost > rows[j].Cost
		}
		if rows[i].Calls != rows[j].Calls {
			return rows[i].Calls > rows[j].Calls
		}
		return rows[i].Model < rows[j].Model
	})
	return rows
}
