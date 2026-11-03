package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cpamp-viewer/server/internal/auth"
	"cpamp-viewer/server/internal/config"
)

const accessGuardTestPath = "/viewer/api/v1/access-guard/quotas"

var accessGuardTestNow = time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)

func accessGuardTestBinding(id string) map[string]any {
	return map[string]any{
		"id": id, "enabled": true, "name": "private-upstream-name",
		"key_preview": "sk-private-preview", "key": "sk-private-plaintext",
		"caller_scope": "private-caller-scope", "key_hash": "private-key-hash",
		"auth_ids": []string{"private-auth-id"}, "group": "private-group",
		"model_access": map[string]any{"mode": "allowlist", "models": []string{"private-model"}},
		"usage": map[string]any{
			"weekly_usd_limit": 800, "weekly_usd_used": 200, "weekly_calls": 3,
			"weekly_reset_at": accessGuardTestNow.Add(7 * 24 * time.Hour).Format(time.RFC3339),
			"daily_usd_used":  42, "daily_calls": 2, "rpm_used": 1,
		},
	}
}

func accessGuardRaw(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func newAccessGuardTestServer(baseURL string) *Server {
	cfg := config.Config{
		PublicAccess: true, RequestTimeout: time.Second, MaxUpstreamBodyBytes: 1 << 20,
		AccessGuardBaseURL: baseURL, AccessGuardManagementKey: "independent-management-secret",
		AccessGuardPublicKeys: []config.AccessGuardPublicKey{{BindingID: "native-key-1", Name: "公开名称"}},
	}
	cache := newAccessGuardQuotaCache(cfg)
	cache.now = func() time.Time { return accessGuardTestNow }
	return &Server{cfg: cfg, accessGuard: cache}
}

func accessGuardRequest(handler http.Handler) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, accessGuardTestPath, nil))
	return response
}

func decodeAccessGuardResponse(t *testing.T, response *httptest.ResponseRecorder) accessGuardQuotaResponse {
	t.Helper()
	var payload accessGuardQuotaResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestAccessGuardPublishesOnlyConfiguredNamesAndQuotaFields(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v0/management/plugins/access-guard/native-key-bindings" || r.URL.RawQuery != "" || r.ContentLength != 0 {
			t.Errorf("unexpected upstream request: %s %s length=%d", r.Method, r.URL.String(), r.ContentLength)
		}
		if r.Header.Get("Authorization") != "Bearer independent-management-secret" {
			t.Error("independent management credential was not used")
		}
		writeJSON(w, http.StatusOK, map[string]any{"bindings": []any{
			accessGuardTestBinding("private-unlisted-binding"), accessGuardTestBinding("native-key-1"),
		}})
	}))
	defer upstream.Close()
	server := newAccessGuardTestServer(upstream.URL)
	server.cfg.CPAMPBaseURL = "http://must-not-be-used.invalid"
	server.cfg.CPAMPAdminKey = "cpamp-key-must-not-be-used"
	response := accessGuardRequest(server.Handler())
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	payload := decodeAccessGuardResponse(t, response)
	if !payload.Configured || payload.Stale || payload.UpdatedAt != accessGuardTestNow.Format(time.RFC3339) || len(payload.Items) != 1 {
		t.Fatalf("unexpected envelope: %#v", payload)
	}
	item := payload.Items[0]
	if item.Name != "公开名称" || !apiKeyViewIDPattern.MatchString(item.ID) || item.State != "active" || *item.RemainingUSD != 600 || *item.RemainingPercent != 75 {
		t.Fatalf("unexpected public quota: %#v", item)
	}
	for _, forbidden := range []string{"private", "native-key-1", "management-secret", "caller_scope", "key_preview", "key_hash", "auth_ids", "model_access", "daily_calls", "rpm_used", "weekly_calls", "weekly_usd_used"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Errorf("public projection contains forbidden field/value %q", forbidden)
		}
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(accessGuardRaw(t, item), &object); err != nil {
		t.Fatal(err)
	}
	if len(object) != 10 || item.InstanceID == "" || item.InstanceName == "" {
		t.Fatalf("unexpected public item fields: %s", accessGuardRaw(t, item))
	}
	if calls.Load() != 1 || response.Header().Get("Cache-Control") != "no-store, private" {
		t.Fatalf("unexpected request count/cache headers: %d %#v", calls.Load(), response.Header())
	}
}

func TestAccessGuardQuotaStatesAndWindowSemantics(t *testing.T) {
	tests := []struct {
		name      string
		edit      func(map[string]any, map[string]any)
		state     string
		remaining float64
		started   bool
		wantReset bool
	}{
		{name: "active", state: "active", remaining: 600, started: true, wantReset: true},
		{name: "exhausted", edit: func(_ map[string]any, u map[string]any) { u["weekly_usd_used"] = 801 }, state: "active", remaining: 0, started: true, wantReset: true},
		{name: "never used", edit: func(_ map[string]any, u map[string]any) {
			u["weekly_usd_used"] = 0
			u["weekly_calls"] = 0
			delete(u, "weekly_reset_at")
		}, state: "active", remaining: 800},
		{name: "expired window projected by plugin", edit: func(_ map[string]any, u map[string]any) { u["weekly_usd_used"] = 0; u["weekly_calls"] = 0 }, state: "active", remaining: 800},
		{name: "unlimited", edit: func(_ map[string]any, u map[string]any) { u["weekly_usd_limit"] = 0 }, state: "unlimited"},
		{name: "inactive without usage", edit: func(b map[string]any, _ map[string]any) { b["enabled"] = false; delete(b, "usage") }, state: "inactive"},
		{name: "missing enabled", edit: func(b map[string]any, _ map[string]any) { delete(b, "enabled") }, state: "unavailable"},
		{name: "missing usage", edit: func(b map[string]any, _ map[string]any) { delete(b, "usage") }, state: "unavailable"},
		{name: "missing limit", edit: func(_ map[string]any, u map[string]any) { delete(u, "weekly_usd_limit") }, state: "unavailable"},
		{name: "negative used", edit: func(_ map[string]any, u map[string]any) { u["weekly_usd_used"] = -1 }, state: "unavailable"},
		{name: "bad number type", edit: func(_ map[string]any, u map[string]any) { u["weekly_usd_used"] = "private-invalid-value" }, state: "unavailable"},
		{name: "negative calls", edit: func(_ map[string]any, u map[string]any) { u["weekly_calls"] = -1 }, state: "unavailable"},
		{name: "inconsistent idle spend", edit: func(_ map[string]any, u map[string]any) { u["weekly_calls"] = 0 }, state: "unavailable"},
		{name: "started without reset", edit: func(_ map[string]any, u map[string]any) { delete(u, "weekly_reset_at") }, state: "unavailable"},
		{name: "started with malformed reset", edit: func(_ map[string]any, u map[string]any) { u["weekly_reset_at"] = "private-invalid-time" }, state: "unavailable"},
		{name: "inconsistent expired reset", edit: func(_ map[string]any, u map[string]any) {
			u["weekly_reset_at"] = accessGuardTestNow.Format(time.RFC3339)
		}, state: "unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binding := accessGuardTestBinding("native-key-1")
			if tt.edit != nil {
				tt.edit(binding, binding["usage"].(map[string]any))
			}
			items := projectAccessGuardQuotas([]config.AccessGuardPublicKey{{BindingID: "native-key-1", Name: "公开"}}, []json.RawMessage{accessGuardRaw(t, binding)}, accessGuardTestNow)
			item := items[0]
			if item.State != tt.state {
				t.Fatalf("state=%s want=%s", item.State, tt.state)
			}
			if tt.state != "active" {
				if item.WeeklyLimitUSD != nil || item.RemainingUSD != nil || item.RemainingPercent != nil || item.WindowStarted != nil || item.ResetAt != "" {
					t.Fatalf("nonactive state leaked amounts/window: %#v", item)
				}
				return
			}
			if item.RemainingUSD == nil || *item.RemainingUSD != tt.remaining || item.WindowStarted == nil || *item.WindowStarted != tt.started || (item.ResetAt != "") != tt.wantReset {
				t.Fatalf("unexpected quota/window: %#v", item)
			}
		})
	}
}

func TestAccessGuardMissingOrDuplicateBindingIsUnavailable(t *testing.T) {
	keys := []config.AccessGuardPublicKey{{BindingID: "native-key-1", Name: "第一"}, {BindingID: "missing", Name: "第二"}}
	for _, bindings := range [][]json.RawMessage{
		{},
		{accessGuardRaw(t, accessGuardTestBinding("native-key-1")), accessGuardRaw(t, accessGuardTestBinding("native-key-1"))},
	} {
		items := projectAccessGuardQuotas(keys, bindings, accessGuardTestNow)
		if len(items) != 2 || items[0].Name != "第一" || items[1].Name != "第二" || items[0].State != "unavailable" || items[1].State != "unavailable" {
			t.Fatalf("missing/duplicate binding projected as valid: %#v", items)
		}
	}
}

func TestAccessGuardPublicIDsDoNotEncodeManagementBindingIDs(t *testing.T) {
	first := projectAccessGuardQuotas([]config.AccessGuardPublicKey{{BindingID: "native-key-1", Name: "公开"}}, nil, accessGuardTestNow)
	second := projectAccessGuardQuotas([]config.AccessGuardPublicKey{{BindingID: "private-completely-different-binding", Name: "公开"}}, nil, accessGuardTestNow)
	if first[0].ID != second[0].ID || !apiKeyViewIDPattern.MatchString(first[0].ID) {
		t.Fatal("public slot ID reveals which management binding was configured")
	}
}

func TestAccessGuardRejectsMethodsParametersAndBodiesBeforeUpstream(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeJSON(w, http.StatusOK, map[string]any{"bindings": []any{}})
	}))
	defer upstream.Close()
	handler := newAccessGuardTestServer(upstream.URL).Handler()
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions, http.MethodTrace, "PROPFIND"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, accessGuardTestPath, nil))
		if response.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s status=%d want=405", method, response.Code)
		}
	}
	for _, query := range []string{"?id=native-key-1", "?url=http://private.test", "?path=/api-keys", "?%zz", "?"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, accessGuardTestPath+query, nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("query %q status=%d want=400", query, response.Code)
		}
	}
	for _, chunked := range []bool{false, true} {
		request := httptest.NewRequest(http.MethodGet, accessGuardTestPath, strings.NewReader(`{"id":"private"}`))
		if chunked {
			request.ContentLength = -1
			request.TransferEncoding = []string{"chunked"}
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("body chunked=%v status=%d want=400", chunked, response.Code)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("rejected requests reached upstream %d times", calls.Load())
	}
}

func TestAccessGuardDisabledAndProtectedMode(t *testing.T) {
	server := &Server{cfg: config.Config{PublicAccess: true}}
	response := accessGuardRequest(server.Handler())
	payload := decodeAccessGuardResponse(t, response)
	if response.Code != http.StatusOK || payload.Configured || payload.Stale || payload.PartialFailed || payload.Items == nil || len(payload.Items) != 0 || len(payload.Warnings) != 0 {
		t.Fatalf("disabled response=%d %s", response.Code, response.Body.String())
	}
	server.cfg.PublicAccess = false
	server.auth = auth.New("test-password", []byte("test-secret-at-least-thirty-two-bytes"), time.Hour, false)
	response = accessGuardRequest(server.Handler())
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("protected quota endpoint status=%d want=401", response.Code)
	}
}

func TestAccessGuardCacheSharedAcrossConcurrentVisitors(t *testing.T) {
	var calls atomic.Int64
	entered := make(chan struct{})
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		writeJSON(w, http.StatusOK, map[string]any{"bindings": []any{accessGuardTestBinding("native-key-1")}})
	}))
	defer upstream.Close()
	server := newAccessGuardTestServer(upstream.URL)
	handler := server.Handler()
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := accessGuardRequest(handler)
			if response.Code != http.StatusOK {
				t.Errorf("concurrent request status=%d", response.Code)
			}
		}()
	}
	<-entered
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("concurrent visitors made %d upstream calls", calls.Load())
	}
	if bytes.Contains(accessGuardRaw(t, server.accessGuard.current), []byte("private")) {
		t.Fatal("cache retained unprojected upstream fields")
	}
}

func TestAccessGuardCacheFailureCooldownAndRecovery(t *testing.T) {
	var calls atomic.Int64
	var elapsed atomic.Int64
	var fail atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		if fail.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"private-management-secret","caller_scope":"private-scope"}`))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"bindings": []any{accessGuardTestBinding("native-key-1")}})
	}))
	defer upstream.Close()
	server := newAccessGuardTestServer(upstream.URL)
	server.accessGuard.now = func() time.Time { return accessGuardTestNow.Add(time.Duration(elapsed.Load())) }
	handler := server.Handler()
	first := decodeAccessGuardResponse(t, accessGuardRequest(handler))
	elapsed.Store(int64(14 * time.Second))
	accessGuardRequest(handler)
	if calls.Load() != 1 {
		t.Fatal("fresh cache was not reused")
	}
	fail.Store(true)
	elapsed.Store(int64(15 * time.Second))
	for i := 0; i < 4; i++ {
		response := accessGuardRequest(handler)
		payload := decodeAccessGuardResponse(t, response)
		if response.Code != http.StatusOK || !payload.Stale || payload.UpdatedAt != first.UpdatedAt || *payload.Items[0].RemainingUSD != 600 || strings.Contains(response.Body.String(), "private") {
			t.Fatalf("unsafe/missing stale response: %d %s", response.Code, response.Body.String())
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("failure cooldown allowed %d calls", calls.Load())
	}
	fail.Store(false)
	elapsed.Store(int64(30 * time.Second))
	recovered := decodeAccessGuardResponse(t, accessGuardRequest(handler))
	if recovered.Stale || recovered.UpdatedAt == first.UpdatedAt || calls.Load() != 3 {
		t.Fatalf("cache did not recover: %#v calls=%d", recovered, calls.Load())
	}
}

func TestAccessGuardInitialFailuresRemainSafeAndUseCooldown(t *testing.T) {
	for _, body := range []string{`{"error":"private-secret"}`, `{"bindings":"private-bad-type"}`, `{`, `{}`, `{"bindings":null}`} {
		t.Run(body, func(t *testing.T) {
			var calls atomic.Int64
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				_, _ = w.Write([]byte(body))
			}))
			defer upstream.Close()
			server := newAccessGuardTestServer(upstream.URL)
			handler := server.Handler()
			for i := 0; i < 3; i++ {
				response := accessGuardRequest(handler)
				if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), upstream.URL) {
					t.Fatalf("unsafe initial error: %d %s", response.Code, response.Body.String())
				}
			}
			if calls.Load() != 1 || server.accessGuard.current != nil {
				t.Fatalf("failure was not cooled down safely: calls=%d cached=%v", calls.Load(), server.accessGuard.current)
			}
		})
	}
}

func TestAccessGuardSharedRefreshSurvivesVisitorCancellation(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
		case <-r.Context().Done():
			t.Error("visitor cancellation canceled shared read")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"bindings": []any{accessGuardTestBinding("native-key-1")}})
	}))
	defer upstream.Close()
	server := newAccessGuardTestServer(upstream.URL)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := server.accessGuard.snapshot(ctx)
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled waiter should stop waiting")
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("initiating visitor waited for shared refresh after cancellation")
	}
	close(release)
	response, err := server.accessGuard.snapshot(context.Background())
	if err != nil || !response.Configured || len(response.Items) != 1 || calls.Load() != 1 {
		t.Fatalf("visitor cancellation invalidated shared refresh: %#v %v", response, err)
	}
}

func TestAccessGuardAutomaticModeUsesOnlyCPAMPPluginProxy(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v0/management/plugins/access-guard/native-key-bindings" || r.URL.RawQuery != "" {
			t.Errorf("automatic mode requested an unexpected route: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer cpamp-existing-admin" {
			t.Error("automatic mode did not authenticate to CPAMP with its existing credential")
		}
		first, second := accessGuardTestBinding("private-binding-a"), accessGuardTestBinding("private-binding-b")
		first["name"], second["name"] = "公开甲", "公开乙"
		writeJSON(w, http.StatusOK, map[string]any{"bindings": []any{first, second}})
	}))
	defer upstream.Close()
	cfg := config.Config{
		PublicAccess: true, RequestTimeout: time.Second,
		CPAMPBaseURL: upstream.URL, CPAMPAdminKey: "cpamp-existing-admin",
		AccessGuardViaCPAMP: true, AccessGuardPublicAll: true,
	}
	server := &Server{cfg: cfg, accessGuard: newAccessGuardQuotaCache(cfg)}
	server.accessGuard.now = func() time.Time { return accessGuardTestNow }
	response := accessGuardRequest(server.Handler())
	payload := decodeAccessGuardResponse(t, response)
	if response.Code != http.StatusOK || !payload.Configured || len(payload.Items) != 2 || payload.Items[0].Name != "公开甲" || payload.Items[1].Name != "公开乙" {
		t.Fatalf("unexpected automatic public response: %d %#v", response.Code, payload)
	}
	for _, forbidden := range []string{"private", "cpamp-existing-admin", "caller_scope", "key_preview", "key_hash", "auth_ids", "model_access", "daily_calls", "weekly_calls", "rpm_used"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Errorf("automatic mode leaked %q", forbidden)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("expected one fixed proxy GET, got %d", calls.Load())
	}
}

func TestAccessGuardAutomaticNamesNeverFallBackToInternalIDs(t *testing.T) {
	emptyName, secretName := accessGuardTestBinding("private-one"), accessGuardTestBinding("private-two")
	emptyName["name"], secretName["name"] = "  ", "Bearer private-token"
	bindings := []json.RawMessage{accessGuardRaw(t, emptyName), accessGuardRaw(t, secretName), accessGuardRaw(t, emptyName)}
	keys := accessGuardAutomaticPublicKeys(bindings)
	items := projectAccessGuardQuotas(keys, bindings, accessGuardTestNow)
	if len(items) != 2 || items[0].Name != "Key 1" || items[1].Name != "Key 2" || items[0].State != "unavailable" {
		t.Fatalf("unexpected duplicate/unnamed projection: %#v", items)
	}
	if strings.Contains(string(accessGuardRaw(t, items)), "private") {
		t.Fatal("automatic name fallback exposed internal identity or credentials")
	}
}

func TestAccessGuardCPAMPModePreservesExplicitEmptyAndSelectedLists(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"bindings": []any{
			accessGuardTestBinding("private-unlisted"), accessGuardTestBinding("native-key-1"),
		}})
	}))
	defer upstream.Close()
	for _, keys := range [][]config.AccessGuardPublicKey{
		{}, {{BindingID: "native-key-1", Name: "名单指定名"}},
	} {
		cfg := config.Config{CPAMPBaseURL: upstream.URL, CPAMPAdminKey: "existing-cpamp", AccessGuardViaCPAMP: true, AccessGuardPublicKeys: keys, RequestTimeout: time.Second}
		cache := newAccessGuardQuotaCache(cfg)
		cache.now = func() time.Time { return accessGuardTestNow }
		response, err := cache.snapshot(context.Background())
		if err != nil || len(response.Items) != len(keys) {
			t.Fatalf("CPAMP mode expanded explicit public scope: %#v %v", response, err)
		}
		if len(keys) != 0 && response.Items[0].Name != "名单指定名" {
			t.Fatal("configured name was replaced by upstream name")
		}
	}
}
