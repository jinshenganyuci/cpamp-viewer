package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cpamp-viewer/server/internal/config"
	"cpamp-viewer/server/internal/cpamp"
)

type federationDashboardFixture struct {
	Calls         int64
	Success       int64
	Latency       *float64
	Fail          bool
	BeforeSummary func(*http.Request)
	Models        []any
	ExtraBucket   bool
}

func newFederationDashboardSource(t *testing.T, id string, f federationDashboardFixture) cpampReadSource {
	t.Helper()
	const rawKey = "sk-synthetic-inference-secret"
	const rawHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected upstream mutation %s %s", r.Method, r.URL.Path)
			w.WriteHeader(405)
			return
		}
		if r.URL.Path != "/v1/models" && r.Header.Get("Authorization") != "Bearer synthetic-admin" {
			t.Error("source admin authentication missing")
		}
		switch r.URL.Path {
		case "/v0/management/dashboard/summary":
			if f.BeforeSummary != nil {
				f.BeforeSummary(r)
			}
			if f.Fail {
				w.WriteHeader(502)
				_, _ = w.Write([]byte("private source URL https://private.invalid Bearer synthetic-admin"))
				return
			}
			models := f.Models
			if models == nil {
				models = []any{map[string]any{"model": "shared-model", "calls": f.Calls, "tokens": f.Calls * 100, "success_rate": dashboardRatio(f.Success, f.Calls), "cost": float64(f.Calls) / 10}}
			}
			timeline := []any{map[string]any{"bucket_ms": 1699999800000, "calls": f.Calls, "tokens": f.Calls * 100, "success": f.Success, "failure": f.Calls - f.Success, "cost": float64(f.Calls) / 10, "average_latency_ms": f.Latency, "calls_share": 99, "tokens_share": 99}}
			healthPoints := []any{map[string]any{"bucket_ms": 1699999800000, "calls": f.Calls, "tokens": f.Calls * 100, "success": f.Success, "failure": f.Calls - f.Success, "success_rate": 99, "tone": "success"}}
			if f.ExtraBucket {
				point := map[string]any{"bucket_ms": 1699999200000, "calls": f.Calls / 2, "tokens": f.Calls * 50, "success": f.Calls / 2}
				timeline = append(timeline, point)
				healthPoints = append(healthPoints, point)
			}
			writeJSON(w, 200, map[string]any{
				"generated_at_ms":               int64(1700000000000),
				"window":                        map[string]any{"today_start_ms": 1699920000000, "now_ms": 1700000000000, "rolling_30m_start_ms": 1699998200000},
				"today":                         map[string]any{"total_calls": f.Calls, "success_calls": f.Success, "failure_calls": f.Calls - f.Success, "success_rate": 0.123, "total_tokens": f.Calls * 100, "input_tokens": f.Calls * 40, "output_tokens": f.Calls * 60, "cached_tokens": f.Calls * 10, "cache_read_tokens": f.Calls * 8, "cache_creation_tokens": f.Calls * 2, "total_cost": float64(f.Calls) / 10, "average_latency_ms": f.Latency, "p95_latency_ms": 999},
				"rolling_30m":                   map[string]any{"total_calls": f.Calls, "total_tokens": f.Calls * 100, "rpm": float64(f.Calls) / 30, "tpm": float64(f.Calls) * 100 / 30},
				"traffic_timeline":              timeline,
				"today_request_health_timeline": map[string]any{"from_ms": 1699920000000, "to_ms": 1700000000000, "bucket_ms": 60000, "total_calls": f.Calls, "success_calls": f.Success, "failure_calls": f.Calls - f.Success, "success_rate": 99, "points": healthPoints},
				"top_models_today":              models,
				"model_cost_rank":               models,
				"token_mix":                     []any{map[string]any{"key": "input", "tokens": f.Calls * 40, "share": 0.9}, map[string]any{"key": "output", "tokens": f.Calls * 60, "share": 0.1}},
				"channel_health":                []any{map[string]any{"auth_index": "same-private-index", "account_snapshot": "same-account", "auth_label_snapshot": "same-label", "source": "same-source", "auth_provider_snapshot": "codex", "calls": f.Calls, "failures": f.Calls - f.Success}},
				"recent_failures":               []any{map[string]any{"timestamp_ms": 1699999900000, "model": "shared-model", "api_key_hash": rawHash, "auth_index": "same-private-index", "account_snapshot": "same-account", "source": "same-source", "auth_project_id_snapshot": "same-project", "fail_summary": "Bearer synthetic-admin", "id": "private-upstream-id", "instance_id": "private-upstream-instance", "instance_name": "https://private.invalid", "unknown_secret": rawKey}},
				"api_key":                       rawKey, "upstream_url": "https://private.invalid",
			})
		case "/v0/management/config":
			w.Header().Set("X-CPA-Version", "version-"+id)
			writeJSON(w, 200, map[string]any{"api-keys": []string{rawKey}, "codex-api-key": []any{map[string]any{"api-key": rawKey}}, "debug": id == "one", "request-retry": 2, "ws-auth": true, "routing": map[string]any{"strategy": "round-robin"}, "proxy-url": "https://user:pass@private-proxy.invalid"})
		case "/v0/management/api-key-aliases":
			writeJSON(w, 200, map[string]any{"items": []any{map[string]any{"apiKeyHash": rawHash, "alias": "Shared Alias"}}})
		case "/usage-service/info":
			writeJSON(w, 200, map[string]any{"service": "CPAMP", "mode": "managed", "configured": true, "adminReady": true, "projectInitialized": true, "migrationStatus": "ready"})
		case "/usage-service/config":
			writeJSON(w, 200, map[string]any{"config": map[string]any{"cpaConnection": map[string]any{"cpaBaseUrl": "https://private-cpa.invalid"}}})
		case "/status":
			writeJSON(w, 200, map[string]any{"events": f.Calls, "deadLetters": 1, "collector": map[string]any{"queue": "usage", "totalInserted": f.Calls}})
		case "/v0/management/auth-files":
			writeJSON(w, 200, map[string]any{"files": []any{map[string]any{"name": "private-file.json"}}})
		case "/v0/management/request-error-logs":
			writeJSON(w, 200, map[string]any{"files": []any{map[string]any{"path": "/private/error.log"}}})
		case "/v1/models":
			if r.Header.Get("Authorization") != "Bearer "+rawKey {
				t.Error("model catalog did not use the source's own inference key")
			}
			writeJSON(w, 200, map[string]any{"data": []any{map[string]any{"id": "model-1"}, map[string]any{"id": "model-2"}}})
		default:
			t.Errorf("unexpected upstream path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(upstream.Close)
	reader := &Server{cfg: config.Config{CPAMPBaseURL: upstream.URL, RequestTimeout: time.Second}, cpamp: cpamp.New(upstream.URL, "synthetic-admin", time.Second, 4<<20), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return cpampReadSource{ID: id, Name: "Instance " + id, Reader: reader}
}

func TestFederatedDashboardAggregatesMetricsAndKeepsInstanceIdentity(t *testing.T) {
	var mu sync.Mutex
	windows := []string{}
	record := func(r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		windows = append(windows, r.URL.Query().Get("now_ms")+":"+r.URL.Query().Get("today_start_ms"))
		if r.URL.Query().Get("top_models") != "500" {
			t.Error("federated model fetch did not use bounded expanded limit")
		}
	}
	first := newFederationDashboardSource(t, "one", federationDashboardFixture{Calls: 2, Success: 1, Latency: floatPointer(100), BeforeSummary: record})
	second := newFederationDashboardSource(t, "two", federationDashboardFixture{Calls: 6, Success: 6, Latency: floatPointer(300), BeforeSummary: record})
	result, err := federateDashboard(httptest.NewRequest("GET", "/viewer/api/v1/dashboard", nil), []cpampReadSource{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if result.Today.TotalCalls != 8 || result.Today.SuccessCalls != 7 || result.Today.FailureCalls != 1 || result.Today.TotalTokens != 800 || result.Today.TotalCost != 0.8 || result.Today.SuccessRate != 0.875 {
		t.Fatalf("incorrect totals: %+v", result.Today)
	}
	if result.Today.AverageLatencyMS != nil || result.Today.P95LatencyMS != nil || !strings.Contains(strings.Join(result.Warnings, " "), "有效样本数") {
		t.Fatalf("incorrect latency: %+v", result.Today)
	}
	if result.Today.CacheReadTokens != 64 || result.Today.CacheCreationToken != 16 || result.Rolling30m.TotalCalls != 8 || math.Abs(result.Rolling30m.RPM-8.0/30) > 1e-12 {
		t.Fatal("rolling or cache token totals differ")
	}
	if len(windows) != 2 || windows[0] != windows[1] {
		t.Fatalf("instances queried different time ranges: %v", windows)
	}
	if result.Stats.ManagementKeys != 2 || result.Stats.AuthFiles != 2 || result.Stats.Providers.Codex != 2 || result.Stats.AvailableModels == nil || *result.Stats.AvailableModels != 4 {
		t.Fatalf("incorrect source counters: %+v", result.Stats)
	}
	if result.Config != nil || result.System.ServerVersion != "" || result.System.CPABase != "" || len(result.Instances) != 2 {
		t.Fatal("single-instance configuration presented as global")
	}
	if !result.Instances[0].Config.Debug || result.Instances[1].Config.Debug || result.Instances[0].System.ServerVersion == result.Instances[1].System.ServerVersion {
		t.Fatal("instance-specific config and system status lost")
	}
	if len(result.ChannelHealth) != 2 || result.ChannelHealth[0].ID == result.ChannelHealth[1].ID || result.ChannelHealth[0].AccountID == result.ChannelHealth[1].AccountID {
		t.Fatal("same-named channels collapsed across instances")
	}
	if len(result.RecentFailures) != 2 || result.RecentFailures[0].ID == result.RecentFailures[1].ID || result.RecentFailures[0].APIKeyID == result.RecentFailures[1].APIKeyID {
		t.Fatal("failures or API key identities collided")
	}
	for _, row := range result.RecentFailures {
		if row.InstanceID == "" || row.InstanceName == "" {
			t.Fatal("failure missing instance metadata")
		}
	}
	if len(result.TopModelsToday) != 1 || result.TopModelsToday[0].Calls != 8 || result.TopModelsToday[0].SuccessRate != 0.875 || result.ModelCostRank[0].CostShare != 1 {
		t.Fatal("same-model aggregate and recalculated shares differ")
	}
	if len(result.TrafficTimeline) != 1 || result.TrafficTimeline[0].Calls != 8 || result.TrafficTimeline[0].SuccessRate != 0.875 || result.TrafficTimeline[0].FailureRate != 0.125 || result.TrafficTimeline[0].CallsShare != 1 || result.TrafficTimeline[0].AverageLatencyMS != nil {
		t.Fatalf("incorrect timeline: %+v", result.TrafficTimeline)
	}
	if len(result.TodayRequestHealthTimeline.Points) != 1 || result.TodayRequestHealthTimeline.SuccessRate != 0.875 || result.TodayRequestHealthTimeline.Points[0].SuccessRate != 0.875 {
		t.Fatal("health timeline failed to merge")
	}
	if len(result.TokenMix) != 2 || result.TokenMix[0].Share != 0.4 || result.TokenMix[1].Share != 0.6 {
		t.Fatalf("token mix shares: %+v", result.TokenMix)
	}
	encoded, _ := json.Marshal(result)
	for _, private := range []string{"synthetic-admin", "sk-synthetic", "aaaaaaaaaaaaaaaa", "same-private-index", "private-upstream", "private.invalid", "private-proxy.invalid", "private-cpa.invalid", "private-file.json", "/private/error.log", "\"api_key_hash\"", "\"auth_index\""} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("private source data leaked: %s", private)
		}
	}
}

func TestFederatedDashboardPartialFailureAndUnknownLatency(t *testing.T) {
	first := newFederationDashboardSource(t, "one", federationDashboardFixture{Calls: 2, Success: 2, Latency: floatPointer(100)})
	unknown := newFederationDashboardSource(t, "unknown", federationDashboardFixture{Calls: 4, Success: 2})
	failed := newFederationDashboardSource(t, "failed", federationDashboardFixture{Fail: true})
	result, err := federateDashboard(httptest.NewRequest("GET", "/viewer/api/v1/dashboard", nil), []cpampReadSource{first, unknown, failed})
	if err != nil {
		t.Fatal(err)
	}
	if !result.PartialFailed || len(result.Warnings) != 2 || result.Today.TotalCalls != 6 || result.Instances[2].Available || result.Instances[2].System != nil {
		t.Fatalf("partial data not retained: %+v", result)
	}
	if result.Today.AverageLatencyMS != nil || result.TrafficTimeline[0].AverageLatencyMS != nil {
		t.Fatal("missing latency was interpreted as zero or ignored")
	}
	if strings.Contains(strings.Join(result.Warnings, " "), "https://") || strings.Contains(strings.Join(result.Warnings, " "), "synthetic-admin") {
		t.Fatal("upstream failure body leaked")
	}
	if _, err := federateDashboard(httptest.NewRequest("GET", "/viewer/api/v1/dashboard", nil), []cpampReadSource{failed, failed}); err == nil {
		t.Fatal("all failed sources fabricated a zero dashboard")
	}
}

func TestFederatedDashboardKeepsExplicitZeroLatencyAndZeroCallUnknown(t *testing.T) {
	for _, tc := range []struct {
		value *float64
		calls int64
		want  *float64
	}{{nil, 0, floatPointer(0)}, {floatPointer(0), 2, nil}, {nil, 1, nil}} {
		var mean dashboardObservedMean
		mean.add(floatPointer(0), 1)
		mean.add(tc.value, tc.calls)
		got := mean.value()
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Fatalf("observed mean=%v want=%v", got, tc.want)
		}
	}
}

func TestFederatedDashboardSingleSourcePreservesExistingShape(t *testing.T) {
	source := newFederationDashboardSource(t, "one", federationDashboardFixture{Calls: 2, Success: 1, Latency: floatPointer(100)})
	w := httptest.NewRecorder()
	source.Reader.handleDashboard(w, httptest.NewRequest("GET", "/viewer/api/v1/dashboard", nil))
	var result dashboardResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatalf("single dashboard failed: %d", w.Code)
	}
	if result.Config == nil || result.Today.P95LatencyMS == nil || len(result.Instances) != 0 || result.RecentFailures[0].InstanceID != "" || result.RecentFailures[0].ID != "" {
		t.Fatal("single-source contract changed")
	}
}

func TestFederatedDashboardCancellationAndConcurrencyBound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var active, peak atomic.Int32
	entered := make(chan struct{}, 8)
	sources := make([]cpampReadSource, 8)
	for index := range sources {
		sources[index] = newFederationDashboardSource(t, fmt.Sprintf("source-%d", index), federationDashboardFixture{BeforeSummary: func(r *http.Request) {
			current := active.Add(1)
			defer active.Add(-1)
			for {
				old := peak.Load()
				if current <= old || peak.CompareAndSwap(old, current) {
					break
				}
			}
			entered <- struct{}{}
			<-r.Context().Done()
		}})
	}
	done := make(chan error, 1)
	go func() {
		_, err := federateDashboard(httptest.NewRequest("GET", "/viewer/api/v1/dashboard", nil).WithContext(ctx), sources)
		done <- err
	}()
	for index := 0; index < 4; index++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("fanout did not start")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation was swallowed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled federation did not finish")
	}
	if peak.Load() > 4 {
		t.Fatalf("source concurrency exceeded bound: %d", peak.Load())
	}
}

func TestFederationJSONCollectorRejectsOversizedProjection(t *testing.T) {
	w := &federationJSONWriter{header: make(http.Header)}
	if _, err := w.Write(make([]byte, (4<<20)+1)); err == nil || !w.tooLarge || w.body.Len() != 0 {
		t.Fatal("unbounded projected response accepted")
	}
}

func TestFederatedDashboardUsesUpstreamChartScaleAndModelRates(t *testing.T) {
	first := newFederationDashboardSource(t, "one", federationDashboardFixture{Calls: 2, Success: 1, ExtraBucket: true})
	second := newFederationDashboardSource(t, "two", federationDashboardFixture{Calls: 6, Success: 6, ExtraBucket: true})
	result, err := federateDashboard(httptest.NewRequest("GET", "/viewer/api/v1/dashboard", nil), []cpampReadSource{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.TrafficTimeline) != 2 || result.TrafficTimeline[0].CallsShare != 0.5 || result.TrafficTimeline[1].CallsShare != 1 || result.TrafficTimeline[0].TokensShare != 0.5 {
		t.Fatalf("chart shares must use largest bucket rather than daily total: %+v", result.TrafficTimeline)
	}
	points := result.TodayRequestHealthTimeline.Points
	if points[0].Intensity != 0.5 || points[1].Intensity != 1 || points[0].Tone != "good" || points[1].Tone != "bad" {
		t.Fatalf("health intensity or tone differs from upstream contract: %+v", points)
	}
	models := map[string]dashboardModel{}
	mergeDashboardModels(models, []dashboardModel{{Model: "shared", Calls: 2, SuccessRate: 0.5, Cost: 1, Tokens: 100}, {Model: "other", Calls: 4, SuccessRate: 0.25, Cost: 2}})
	mergeDashboardModels(models, []dashboardModel{{Model: "shared", Calls: 6, SuccessRate: 1, Cost: 3, Tokens: 300}})
	rows := dashboardModelRows(models, true)
	if rows[0].SuccessRate != 0.875 || rows[0].CostShare != 1 || rows[1].CostShare != 0.5 || rows[0].TotalTokens != 400 {
		t.Fatalf("upstream model rate-only schema was aggregated incorrectly: %+v", rows)
	}
}

func TestFederatedDashboardMarksCappedAndIncompleteModelRankings(t *testing.T) {
	for _, count := range []int{1, federationDashboardModelLimit} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			models := make([]any, count)
			for index := range models {
				models[index] = map[string]any{"model": fmt.Sprintf("model-%d", index), "calls": 1, "success_rate": 1, "tokens": 100, "cost": 0.1}
			}
			calls := int64(count)
			if count == 1 {
				calls = 2
			}
			first := newFederationDashboardSource(t, "one", federationDashboardFixture{Calls: calls, Success: calls, Models: models})
			second := newFederationDashboardSource(t, "two", federationDashboardFixture{})
			result, err := federateDashboard(httptest.NewRequest("GET", "/viewer/api/v1/dashboard", nil), []cpampReadSource{first, second})
			if err != nil {
				t.Fatal(err)
			}
			if !result.ModelRankingsPartial || result.PartialFailed || len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "模型排行") {
				t.Fatalf("truncated model rows were presented as complete: %+v", result.Warnings)
			}
		})
	}
}
