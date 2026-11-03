package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
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

func newFederationStatusServer(t *testing.T, handlers ...http.HandlerFunc) *Server {
	t.Helper()
	var main *Server
	for index, handler := range handlers {
		key := "private-main-credential"
		if index > 0 {
			key = "private-secondary-credential"
		}
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.RawQuery != "" || r.ContentLength != 0 || r.Header.Get("Authorization") != "Bearer "+key {
				t.Error("untrusted input or another instance's credential reached upstream")
			}
			handler(w, r)
		}))
		t.Cleanup(up.Close)
		cfg := config.Config{CPAMPBaseURL: up.URL, CPAMPAdminKey: key, PublicAccess: true, RequestTimeout: time.Second, MaxUpstreamBodyBytes: 1 << 20}
		client := cpamp.New(up.URL, key, time.Second, 1<<20)
		reader := &Server{cfg: cfg, cpamp: client, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), usageStatus: newUsageStatusCache(client, time.Second), modelPriceStatus: newModelPriceStatusCache(client, time.Second)}
		if index == 0 {
			main = reader
		} else {
			main.quotaSources = append(main.quotaSources, quotaSource{id: federationSourceID(up.URL), kind: "cpamp", name: "副实例", reader: reader})
		}
	}
	return main
}

func federationStatusRequest(t *testing.T, server *Server, path string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("response invalid JSON: %v", err)
	}
	return response, body
}

func TestFederatedUsageStatusSumsKnownFieldsPreservesInstancesAndPrivacy(t *testing.T) {
	var calls atomic.Int32
	server := newFederationStatusServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if r.URL.Path != usageStatusPath {
				t.Error("wrong fixed usage route")
			}
			writeJSON(w, 200, usageStatusFixture())
		},
		func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if r.URL.Path != usageStatusPath {
				t.Error("wrong fixed usage route")
			}
			value := usageStatusFixture()
			value["raw_event_count"] = 14
			value["raw_deleted_event_count"] = 3
			value["raw_archived_event_count"] = 5
			value["raw_min_timestamp_ms"] = 50
			value["raw_max_timestamp_ms"] = 2000
			value["active_run"] = map[string]any{"status": "archiving", "private_id": "private-active-run"}
			value["active_lock"] = map[string]any{"operation": "archive"}
			writeJSON(w, 200, value)
		})
	response, body := federationStatusRequest(t, server, "/viewer/api/v1/usage-status")
	if response.Code != 200 || body["available"] != true || body["partial_failed"] != false {
		t.Fatalf("bad envelope %#v", body)
	}
	status := body["status"].(map[string]any)
	for field, want := range map[string]any{"raw_event_count": float64(20), "raw_archived_event_count": float64(9), "raw_deleted_event_count": float64(5), "raw_min_timestamp_ms": float64(50), "raw_max_timestamp_ms": float64(2000), "migration_ready": true, "hourly_aggregate_ready": false, "active_operation": "maintenance"} {
		if status[field] != want {
			t.Fatalf("%s=%#v want=%#v", field, status[field], want)
		}
	}
	if status["storage"].(map[string]any)["total_bytes"] != float64(11264) {
		t.Fatal("storage was not summed")
	}
	instances := body["instances"].([]any)
	if len(instances) != 2 || instances[0].(map[string]any)["instance_id"] == instances[1].(map[string]any)["instance_id"] {
		t.Fatal("instances missing or colliding")
	}
	for _, secret := range []string{"private", "http://", "run_id", "last_event_id", "last_error", "compact_requires_stopped_server"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("private metadata escaped: %s", secret)
		}
	}
	_, _ = federationStatusRequest(t, server, "/viewer/api/v1/usage-status")
	if calls.Load() != 2 {
		t.Fatal("per-source shared status cache lost")
	}
}

func TestFederatedUsageStatusUnknownIsNotZeroAndOverflowIsNotWrapped(t *testing.T) {
	server := newFederationStatusServer(t,
		func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, usageStatusFixture()) },
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(404)
			_, _ = io.WriteString(w, "private-management-token")
		})
	response, body := federationStatusRequest(t, server, "/viewer/api/v1/usage-status")
	status := body["status"].(map[string]any)
	if response.Code != 200 || body["available"] != true || body["partial_failed"] != true {
		t.Fatalf("partial result %#v", body)
	}
	for _, field := range []string{"raw_event_count", "raw_archived_event_count", "raw_deleted_event_count", "raw_min_timestamp_ms", "raw_max_timestamp_ms", "migration_ready"} {
		if value, exists := status[field]; !exists || value != nil {
			t.Fatalf("unknown %s became %#v", field, value)
		}
	}
	if status["hourly_aggregate_ready"] != false {
		t.Fatal("known false readiness did not remain false")
	}
	for field, value := range status["storage"].(map[string]any) {
		if value != nil {
			t.Fatalf("unknown storage %s became %#v", field, value)
		}
	}
	if len(body["warnings"].([]any)) != 1 || strings.Contains(response.Body.String(), "private") {
		t.Fatal("unsafe warning")
	}
	first := publicUsageStatus{RawEventCount: 9007199254740991, RawDeletedEventCount: 1}
	second := publicUsageStatus{RawEventCount: 1, RawDeletedEventCount: 2}
	instances := []federatedUsageStatusInstance{{usageStatusResponse: usageStatusResponse{Available: true, Status: &first}}, {usageStatusResponse: usageStatusResponse{Available: true, Status: &second}}}
	totals := aggregateFederatedUsageStatus(instances)
	if totals.RawEventCount != nil || totals.RawArchivedEventCount != nil || totals.RawMinTimestampMS != nil || totals.RawDeletedEventCount == nil || *totals.RawDeletedEventCount != 3 {
		t.Fatalf("invalid unknown/overflow totals %+v", totals)
	}
	first.RawEventCount, second.RawEventCount = 0, 0
	if totals = aggregateFederatedUsageStatus(instances); totals.RawEventCount == nil || *totals.RawEventCount != 0 || totals.RawMinTimestampMS != nil || totals.RawMaxTimestampMS != nil {
		t.Fatal("known empty inventory lost")
	}
}

func TestFederatedUsageStatusRetainsStaleEvidenceWithoutChangingCheckTime(t *testing.T) {
	var fail atomic.Bool
	var clock atomic.Int64
	clock.Store(time.Now().UnixMilli())
	server := newFederationStatusServer(t,
		func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, usageStatusFixture()) },
		func(w http.ResponseWriter, _ *http.Request) {
			if fail.Load() {
				w.WriteHeader(503)
				return
			}
			writeJSON(w, 200, usageStatusFixture())
		})
	for _, source := range server.cpampReadSources() {
		source.Reader.usageStatus.now = func() time.Time { return time.UnixMilli(clock.Load()) }
	}
	_, first := federationStatusRequest(t, server, "/viewer/api/v1/usage-status")
	fail.Store(true)
	clock.Add(30000)
	_, stale := federationStatusRequest(t, server, "/viewer/api/v1/usage-status")
	if stale["available"] != true || stale["stale"] != true || stale["partial_failed"] != true || stale["checked_at_ms"] != first["checked_at_ms"] {
		t.Fatalf("stale evidence changed %#v", stale)
	}
	if stale["status"].(map[string]any)["raw_event_count"] != float64(12) {
		t.Fatal("stale valid count disappeared")
	}
}

func TestFederatedModelPriceStatusCountsInstanceModelPairsAndUnknownTotals(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "all-known", true: "one-unknown"}[failed], func(t *testing.T) {
			server := newFederationStatusServer(t,
				func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != modelPriceStatusPath {
						t.Error("wrong pricing status route")
					}
					writeJSON(w, 200, map[string]any{"models": []string{"shared", "main-only"}, "unpricedModels": []string{"shared"}})
				},
				func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != modelPriceStatusPath {
						t.Error("wrong pricing status route")
					}
					if failed {
						w.WriteHeader(405)
						_, _ = io.WriteString(w, "private-upstream-error")
						return
					}
					writeJSON(w, 200, map[string]any{"models": []string{"shared", "extra-only"}, "unpricedModels": []string{"shared", "extra-only"}})
				})
			response, body := federationStatusRequest(t, server, "/viewer/api/v1/model-price-status")
			if response.Code != 200 || body["available"] != true || body["partial_failed"] != failed || len(body["instances"].([]any)) != 2 {
				t.Fatalf("bad status %#v", body)
			}
			if failed {
				if body["model_count"] != nil || body["unpriced_count"] != nil {
					t.Fatal("unknown counts fabricated zero/partial total")
				}
			} else {
				if body["model_count"] != float64(4) || body["unpriced_count"] != float64(3) || len(body["unpriced_models"].([]any)) != 3 {
					t.Fatal("same model in two instances was incorrectly deduplicated")
				}
			}
			if strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "http://") {
				t.Fatal("private source details escaped")
			}
		})
	}
}

func TestFederatedModelPricesPreservePerSourcePricesAndNeverSelectPrimaryConflict(t *testing.T) {
	server := newFederationStatusServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v0/management/model-prices" {
				t.Error("wrong prices route")
			}
			writeJSON(w, 200, map[string]any{"prices": map[string]any{"shared": map[string]any{"prompt": 1, "completion": 2}, "conflict": map[string]any{"prompt": 3}, "main-only": map[string]any{"prompt": 8}, "sk-private-key": map[string]any{"prompt": 9}}})
		},
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v0/management/model-prices" {
				t.Error("wrong prices route")
			}
			writeJSON(w, 200, map[string]any{"prices": map[string]any{"shared": map[string]any{"prompt": 1, "completion": 2}, "conflict": map[string]any{"prompt": 5}, "extra-only": map[string]any{"prompt": 10}}})
		})
	response, body := federationStatusRequest(t, server, "/viewer/api/v1/model-prices")
	if response.Code != 200 || body["partial_failed"] != false {
		t.Fatalf("bad envelope %#v", body)
	}
	prices := body["prices"].(map[string]any)
	if len(prices) != 1 || prices["shared"] == nil {
		t.Fatalf("conflicting/missing model got global price %#v", prices)
	}
	conflicts := body["conflicting_models"].([]any)
	if len(conflicts) != 1 || conflicts[0] != "conflict" {
		t.Fatal("price conflict not explicit")
	}
	instances := body["instances"].([]any)
	for i, want := range []float64{3, 5} {
		got := instances[i].(map[string]any)["prices"].(map[string]any)["conflict"].(map[string]any)["prompt"]
		if got != want {
			t.Fatalf("per-instance price=%v want=%v", got, want)
		}
	}
	if strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "http://") {
		t.Fatal("price projection leaked sensitive model/source")
	}
}

func TestFederatedModelPricesPartialReadCannotCreateGlobalPrice(t *testing.T) {
	for _, allFailed := range []bool{false, true} {
		t.Run(map[bool]string{false: "partial", true: "all"}[allFailed], func(t *testing.T) {
			server := newFederationStatusServer(t,
				func(w http.ResponseWriter, _ *http.Request) {
					if allFailed {
						w.WriteHeader(503)
						_, _ = io.WriteString(w, "private-body")
						return
					}
					writeJSON(w, 200, map[string]any{"prices": map[string]any{"shared": map[string]any{"prompt": 1}}})
				},
				func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(503)
					_, _ = io.WriteString(w, "private-body")
				})
			response, body := federationStatusRequest(t, server, "/viewer/api/v1/model-prices")
			if strings.Contains(response.Body.String(), "private") {
				t.Fatal("raw error exposed")
			}
			if allFailed {
				if response.Code != 502 {
					t.Fatalf("total outage HTTP%d", response.Code)
				}
				return
			}
			if response.Code != 200 || body["partial_failed"] != true || len(body["prices"].(map[string]any)) != 0 {
				t.Fatalf("partial price treated as global %#v", body)
			}
		})
	}
}

func TestFederatedAuxiliaryReadsRejectVisitorInputsBeforeAnyUpstream(t *testing.T) {
	var calls atomic.Int32
	server := newFederationStatusServer(t, func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); writeJSON(w, 200, map[string]any{}) }, func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); writeJSON(w, 200, map[string]any{}) })
	handlers := map[string]func(http.ResponseWriter, *http.Request){"/viewer/api/v1/usage-status": server.handleUsageStatus, "/viewer/api/v1/model-price-status": server.handleModelPriceStatus, "/viewer/api/v1/model-prices": server.handleModelPrices}
	for path, handler := range handlers {
		for _, tc := range []struct {
			method, query, body string
			want                int
		}{{"POST", "", "", 405}, {"HEAD", "", "", 405}, {"GET", "?instance_id=attacker", "", 400}, {"GET", "?", "", 400}, {"GET", "", `{"url":"https://attacker.invalid"}`, 400}} {
			req := httptest.NewRequest(tc.method, path+tc.query, strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			handler(w, req)
			if w.Code != tc.want {
				t.Fatalf("%s %s got%d want%d", tc.method, path+tc.query, w.Code, tc.want)
			}
		}
		for _, chunked := range []bool{false, true} {
			req := httptest.NewRequest("GET", path, nil)
			req.Body = io.NopCloser(strings.NewReader("hidden-body"))
			if chunked {
				req.TransferEncoding = []string{"chunked"}
			}
			w := httptest.NewRecorder()
			handler(w, req)
			if w.Code != 400 {
				t.Fatalf("hidden body reached federation %s", path)
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid request made %d upstream reads", calls.Load())
	}
}

func TestFederatedAuxiliarySingleInstanceKeepsOriginalResponseShape(t *testing.T) {
	server := newFederationStatusServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case usageStatusPath:
			writeJSON(w, 200, usageStatusFixture())
		case modelPriceStatusPath:
			writeJSON(w, 200, map[string]any{"models": []string{}, "unpricedModels": []string{}})
		case "/v0/management/model-prices":
			writeJSON(w, 200, map[string]any{"prices": map[string]any{}})
		default:
			t.Error("unexpected upstream route")
		}
	})
	for _, path := range []string{"/viewer/api/v1/usage-status", "/viewer/api/v1/model-price-status", "/viewer/api/v1/model-prices"} {
		w, body := federationStatusRequest(t, server, path)
		if w.Code != 200 {
			t.Fatalf("single instance failed %s", path)
		}
		for _, field := range []string{"instances", "partial_failed", "conflicting_models"} {
			if _, present := body[field]; present {
				t.Fatalf("single instance response changed with %s", field)
			}
		}
	}
}

func TestFederatedAuxiliaryCanceledVisitorReturnsPromptlyAndSharedCachesComplete(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	upstream := func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-release:
			writeJSON(w, 200, usageStatusFixture())
		case <-r.Context().Done():
			t.Error("visitor cancellation aborted shared cache refresh")
		}
	}
	server := newFederationStatusServer(t, upstream, upstream)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/viewer/api/v1/usage-status", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { server.handleUsageStatus(w, req); close(done) }()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("refresh did not start")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		close(release)
		t.Fatal("canceled visitor blocked")
	}
	close(release)
	var visitors sync.WaitGroup
	for i := 0; i < 8; i++ {
		visitors.Add(1)
		go func() {
			defer visitors.Done()
			recorder := httptest.NewRecorder()
			server.handleUsageStatus(recorder, httptest.NewRequest("GET", "/viewer/api/v1/usage-status", nil))
			if recorder.Code != 200 {
				t.Errorf("shared refresh HTTP%d", recorder.Code)
			}
		}()
	}
	visitors.Wait()
	if calls.Load() != 2 {
		t.Fatalf("shared refresh duplicated calls %d", calls.Load())
	}
}
