package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

var codexLiveTestTime = time.Date(2030, 9, 10, 12, 0, 0, 0, time.UTC)

const codexLiveOriginalUserAgent = "codex-tui/0.149.1 (Mac OS 26.5.2; arm64) iTerm.app/3.6.11 (codex-tui; 0.149.1)"

// Usage-specific fixtures count usage reads; reset-credit behavior and the
// combined request budget are exercised separately in reset_credits_test.go.
func codexLiveUsageTestServer(handler http.Handler) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/management/api-call" {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var request struct {
				URL string `json:"url"`
			}
			_ = json.Unmarshal(raw, &request)
			if request.URL == codexResetCreditsURL {
				codexLiveTestReply(w, http.StatusOK, `{"available_count":0,"credits":[]}`)
				return
			}
		}
		handler.ServeHTTP(w, r)
	}))
}

func codexLiveTestFile(index, account string) map[string]any {
	file := map[string]any{
		"name": "private-credential-" + index + ".json", "auth_index": index,
		"provider": "codex", "type": "codex", "disabled": false,
		"email": "private-account@example.test",
	}
	if account != "" {
		file["id_token"] = map[string]any{"chatgpt_account_id": account, "plan_type": "pro"}
	}
	return file
}

func codexLiveTestBody(used float64) string {
	body, _ := json.Marshal(map[string]any{
		"plan_type": "pro",
		"rate_limit": map[string]any{"primary_window": map[string]any{
			"used_percent": used, "limit_window_seconds": 604800,
			"reset_at": codexLiveTestTime.Add(6 * 24 * time.Hour).Unix(),
		}},
		"additional_rate_limits": []any{},
		"email":                  "PRIVATE_PROVIDER_EMAIL", "access_token": "PRIVATE_PROVIDER_TOKEN",
		"account_id": "PRIVATE_PROVIDER_ACCOUNT",
	})
	return string(body)
}

func codexLiveTestReply(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status_code": status,
		"header":      map[string][]string{"Set-Cookie": {"PRIVATE_PROVIDER_COOKIE"}},
		"body":        body,
	})
}

func codexLiveTestCache(url string) *codexUsageCache {
	cache := newCodexUsageCache(cpamp.New(url, "PRIVATE_CPAMP_ADMIN_KEY", 3*time.Second, 1<<20), 3*time.Second)
	cache.now = func() time.Time { return codexLiveTestTime }
	return cache
}

func codexLiveTestOne(t *testing.T, cache *codexUsageCache, file map[string]any) codexUsageResult {
	t.Helper()
	results := cache.snapshot(context.Background(), []map[string]any{file})
	return results[codexUsageKey(file)]
}

func codexLiveAssertRemaining(t *testing.T, result codexUsageResult, remaining float64) {
	t.Helper()
	if !result.Valid || len(result.Windows) != 1 || result.Windows[0].Pool != "codex_main" || result.Windows[0].WindowMins != 10080 || result.Windows[0].Remaining != remaining {
		t.Fatalf("unexpected authoritative quota: %#v", result)
	}
}

func TestCodexLiveUsesOnlyFixedReadRequestAndTrustedCredentialIdentity(t *testing.T) {
	var calls atomic.Int64
	upstream := codexLiveUsageTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v0/management/api-call" || r.URL.RawQuery != "" {
			t.Errorf("unexpected wrapper request: %s %s", r.Method, r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer PRIVATE_CPAMP_ADMIN_KEY" {
			t.Error("wrapper did not use the configured CPAMP admin credential")
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(body) != 4 {
			t.Errorf("unexpected wrapper fields: %#v", body)
		}
		for name, wanted := range map[string]string{"auth_index": "trusted-auth-index", "method": "GET", "url": "https://chatgpt.com/backend-api/wham/usage"} {
			var got string
			if json.Unmarshal(body[name], &got) != nil || got != wanted {
				t.Errorf("wrapper %s=%q, want %q", name, got, wanted)
			}
		}
		var headers map[string]string
		if err := json.Unmarshal(body["header"], &headers); err != nil {
			t.Error(err)
		}
		wantedHeaders := map[string]string{
			"Authorization": "Bearer $TOKEN$", "Content-Type": "application/json",
			"Accept": "application/json", "User-Agent": codexLiveOriginalUserAgent,
			"Chatgpt-Account-Id": "trusted-account-id",
		}
		if len(headers) != len(wantedHeaders) {
			t.Errorf("unexpected delegated headers: %#v", headers)
		}
		for name, wanted := range wantedHeaders {
			if headers[name] != wanted {
				t.Errorf("delegated header %s=%q, want %q", name, headers[name], wanted)
			}
		}
		codexLiveTestReply(w, http.StatusOK, codexLiveTestBody(43))
	}))
	defer upstream.Close()
	file := codexLiveTestFile("trusted-auth-index", "trusted-account-id")
	// Auth-file extras must not become an extensible outbound request template.
	file["url"] = "http://attacker.invalid/private"
	file["method"] = "DELETE"
	file["header"] = map[string]string{"Host": "attacker.invalid", "Authorization": "Bearer attacker"}
	file["proxy_url"] = "http://attacker.invalid:8080"
	file["data"] = "PRIVATE_UNTRUSTED_BODY"
	cache := codexLiveTestCache(upstream.URL)
	result := codexLiveTestOne(t, cache, file)
	codexLiveAssertRemaining(t, result, 57)
	if calls.Load() != 1 || result.Plan != "pro" || result.Stale || result.ObservedAt != codexLiveTestTime.UnixMilli() {
		t.Fatalf("unexpected successful read: %#v calls=%d", result, calls.Load())
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	cache.mu.Lock()
	cached, cacheErr := json.Marshal(cache.current)
	cache.mu.Unlock()
	if cacheErr != nil {
		t.Fatal(cacheErr)
	}
	for _, private := range []string{"PRIVATE_", "trusted-auth-index", "trusted-account-id", "attacker.invalid", "Set-Cookie", "access_token", "account_id", "status_code", "$TOKEN$"} {
		if strings.Contains(string(encoded), private) || strings.Contains(string(cached), private) {
			t.Errorf("cached/public result retained private source material %q: %s", private, encoded)
		}
	}
}

func TestCodexLiveOmitsMissingAccountHeaderAndSkipsDisabledOrMissingIndex(t *testing.T) {
	var calls atomic.Int64
	upstream := codexLiveUsageTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			AuthIndex string            `json:"auth_index"`
			Header    map[string]string `json:"header"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.AuthIndex != "enabled-without-account" {
			t.Errorf("disabled or unidentified credential was queried: %q", body.AuthIndex)
		}
		if _, exists := body.Header["Chatgpt-Account-Id"]; exists {
			t.Error("missing account identity produced a fabricated account header")
		}
		codexLiveTestReply(w, http.StatusOK, codexLiveTestBody(10))
	}))
	defer upstream.Close()
	cache := codexLiveTestCache(upstream.URL)
	enabled := codexLiveTestFile("enabled-without-account", "")
	disabled := codexLiveTestFile("disabled-auth", "disabled-account")
	disabled["disabled"] = true
	missing := codexLiveTestFile("", "missing-index-account")
	results := cache.snapshot(context.Background(), []map[string]any{enabled, disabled, missing})
	codexLiveAssertRemaining(t, results[codexUsageKey(enabled)], 90)
	if calls.Load() != 1 || results[codexUsageKey(disabled)].Valid || results[codexUsageKey(missing)].Valid {
		t.Fatalf("disabled/missing credential was treated as queried: calls=%d results=%#v", calls.Load(), results)
	}
}

func TestCodexLiveTreatsOuterSuccessWithInnerFailureAsFailure(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"provider forbidden", http.StatusForbidden, `{"error":"PRIVATE_UPSTREAM_ERROR"}`},
		{"provider failed with quota-looking body", http.StatusServiceUnavailable, codexLiveTestBody(0)},
		{"not quota JSON", http.StatusOK, `{"error":"PRIVATE_UPSTREAM_ERROR"}`},
		{"missing quota inventory", http.StatusOK, `{}`},
		{"HTML login response", http.StatusOK, `<html>PRIVATE_UPSTREAM_ERROR</html>`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			upstream := codexLiveUsageTestServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				codexLiveTestReply(w, test.status, test.body)
			}))
			defer upstream.Close()
			cache := codexLiveTestCache(upstream.URL)
			file := codexLiveTestFile("test-auth", "test-account")
			for i := 0; i < 3; i++ {
				result := codexLiveTestOne(t, cache, file)
				if result.Valid || len(result.Windows) != 0 {
					t.Fatalf("unsuccessful read manufactured valid quota: %#v", result)
				}
				encoded, _ := json.Marshal(result)
				if strings.Contains(string(encoded), "PRIVATE_") {
					t.Fatalf("provider error escaped to cached result: %s", encoded)
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("initial failure was not cooled down: calls=%d", calls.Load())
			}
		})
	}
}

func TestCodexLiveTTLFailureCooldownStaleDataAndRecovery(t *testing.T) {
	var calls atomic.Int64
	var elapsed atomic.Int64
	var fail atomic.Bool
	var used atomic.Int64
	used.Store(43)
	upstream := codexLiveUsageTestServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		if fail.Load() {
			codexLiveTestReply(w, http.StatusForbidden, `{"error":"PRIVATE_UPSTREAM_ERROR"}`)
			return
		}
		codexLiveTestReply(w, http.StatusOK, codexLiveTestBody(float64(used.Load())))
	}))
	defer upstream.Close()
	cache := codexLiveTestCache(upstream.URL)
	cache.now = func() time.Time { return codexLiveTestTime.Add(time.Duration(elapsed.Load())) }
	file := codexLiveTestFile("test-auth", "test-account")
	first := codexLiveTestOne(t, cache, file)
	codexLiveAssertRemaining(t, first, 57)
	elapsed.Store(int64(59 * time.Second))
	codexLiveTestOne(t, cache, file)
	if calls.Load() != 1 {
		t.Fatal("unexpired successful cache was not reused")
	}
	fail.Store(true)
	elapsed.Store(int64(60 * time.Second))
	for i := 0; i < 4; i++ {
		stale := codexLiveTestOne(t, cache, file)
		codexLiveAssertRemaining(t, stale, 57)
		if !stale.Stale || stale.ObservedAt != first.ObservedAt || stale.Windows[0].ObservedAt != first.Windows[0].ObservedAt {
			t.Fatalf("failed refresh lost stale provenance: %#v", stale)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("failure TTL did not suppress repeated provider calls: %d", calls.Load())
	}
	elapsed.Store(int64(120 * time.Second))
	fail.Store(false)
	used.Store(50)
	recovered := codexLiveTestOne(t, cache, file)
	codexLiveAssertRemaining(t, recovered, 50)
	if recovered.Stale || recovered.ObservedAt != codexLiveTestTime.Add(120*time.Second).UnixMilli() || calls.Load() != 3 {
		t.Fatalf("successful refresh did not recover: %#v calls=%d", recovered, calls.Load())
	}
}

func TestCodexLiveSuccessfulEmptyInventoryClearsOldWindows(t *testing.T) {
	var empty atomic.Bool
	var elapsed atomic.Int64
	upstream := codexLiveUsageTestServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if empty.Load() {
			codexLiveTestReply(w, http.StatusOK, `{"plan_type":"pro","rate_limit":{"primary_window":null,"secondary_window":null},"additional_rate_limits":[]}`)
			return
		}
		codexLiveTestReply(w, http.StatusOK, codexLiveTestBody(43))
	}))
	defer upstream.Close()
	cache := codexLiveTestCache(upstream.URL)
	cache.now = func() time.Time { return codexLiveTestTime.Add(time.Duration(elapsed.Load())) }
	file := codexLiveTestFile("test-auth", "test-account")
	codexLiveAssertRemaining(t, codexLiveTestOne(t, cache, file), 57)
	empty.Store(true)
	elapsed.Store(int64(60 * time.Second))
	result := codexLiveTestOne(t, cache, file)
	if !result.Valid || result.Stale || len(result.Windows) != 0 || result.ObservedAt != codexLiveTestTime.Add(time.Minute).UnixMilli() {
		t.Fatalf("complete empty inventory retained obsolete windows: %#v", result)
	}
}

func TestCodexLiveCredentialAccountSwitchCannotReuseAnotherAccountCache(t *testing.T) {
	var calls atomic.Int64
	var elapsed atomic.Int64
	upstream := codexLiveUsageTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request struct {
			Header map[string]string `json:"header"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Header["Chatgpt-Account-Id"] == "account-two" {
			codexLiveTestReply(w, http.StatusForbidden, `{"error":"new account unavailable"}`)
			return
		}
		codexLiveTestReply(w, http.StatusOK, codexLiveTestBody(43))
	}))
	defer upstream.Close()
	cache := codexLiveTestCache(upstream.URL)
	cache.now = func() time.Time { return codexLiveTestTime.Add(time.Duration(elapsed.Load())) }
	first := codexLiveTestFile("same-auth-index", "account-one")
	second := codexLiveTestFile("same-auth-index", "account-two")
	if codexUsageKey(first) == codexUsageKey(second) {
		t.Fatal("cache key does not distinguish a changed upstream account")
	}
	codexLiveAssertRemaining(t, codexLiveTestOne(t, cache, first), 57)
	other := codexLiveTestOne(t, cache, second)
	if other.Valid || len(other.Windows) != 0 {
		t.Fatalf("new account inherited an old account's successful/stale balance: %#v", other)
	}
	if calls.Load() != 1 {
		t.Fatal("changing credential metadata bypassed the shared batch cooldown")
	}
	elapsed.Store(int64(time.Minute))
	other = codexLiveTestOne(t, cache, second)
	if other.Valid || len(other.Windows) != 0 {
		t.Fatalf("failed new-account refresh fell back to the old account: %#v", other)
	}
	codexLiveTestOne(t, cache, second)
	if calls.Load() != 2 {
		t.Fatalf("account switch did not respect independent identity and batch cooldown: %d", calls.Load())
	}
}

func TestCodexLiveDisabledAndRemovedAccountsDoNotExposeFreshCachedResults(t *testing.T) {
	var calls atomic.Int64
	upstream := codexLiveUsageTestServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		codexLiveTestReply(w, http.StatusOK, codexLiveTestBody(43))
	}))
	defer upstream.Close()
	cache := codexLiveTestCache(upstream.URL)
	enabled := codexLiveTestFile("existing-auth", "existing-account")
	first := codexLiveTestOne(t, cache, enabled)
	codexLiveAssertRemaining(t, first, 57)
	disabled := codexLiveTestFile("existing-auth", "existing-account")
	disabled["disabled"] = true
	old := codexLiveTestOne(t, cache, disabled)
	codexLiveAssertRemaining(t, old, 57)
	if !old.Stale || old.ObservedAt != first.ObservedAt || calls.Load() != 1 {
		t.Fatalf("disabled credential looked freshly queried: %#v calls=%d", old, calls.Load())
	}
	if removed := cache.snapshot(context.Background(), nil); len(removed) != 0 {
		t.Fatalf("removed credentials survived result filtering: %#v", removed)
	}
	reenabled := codexLiveTestOne(t, cache, enabled)
	if !reenabled.Valid || reenabled.Stale || reenabled.ObservedAt != first.ObservedAt || calls.Load() != 1 {
		t.Fatalf("per-request disabled filtering mutated the shared successful cache: %#v", reenabled)
	}
}

func TestCodexLiveViewerRejectsProxyInputsBeforeAnyUpstreamCall(t *testing.T) {
	var calls atomic.Int64
	var delegated atomic.Int64
	file := codexLiveTestFile("trusted-auth-index", "trusted-account-id")
	file["quota"] = map[string]any{
		"observed_at": codexLiveTestTime.Add(-time.Hour).Format(time.RFC3339),
		"signals": map[string]any{
			"x-codex-active-limit":         "unknown-legacy-pool",
			"x-codex-primary-used-percent": "1", "x-codex-primary-window-minutes": "300",
		},
	}
	upstream := codexLiveUsageTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer PRIVATE_CPAMP_ADMIN_KEY" || r.Header.Get("X-HTTP-Method-Override") != "" || r.Header.Get("X-Target-URL") != "" {
			t.Error("visitor headers escaped to CPAMP")
		}
		switch r.URL.Path {
		case "/v0/management/auth-files":
			writeJSON(w, http.StatusOK, map[string]any{"files": []map[string]any{file}})
		case "/v0/management/monitoring/header-snapshots":
			// A failed historical source must not block an authoritative live read.
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "PRIVATE_HEADER_SOURCE_ERROR"})
		case "/v0/management/api-call":
			delegated.Add(1)
			var body struct {
				AuthIndex string            `json:"auth_index"`
				Method    string            `json:"method"`
				URL       string            `json:"url"`
				Header    map[string]string `json:"header"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if r.Method != http.MethodPost || body.Method != "GET" || body.URL != "https://chatgpt.com/backend-api/wham/usage" || body.AuthIndex != "trusted-auth-index" || body.Header["Chatgpt-Account-Id"] != "trusted-account-id" || body.Header["Authorization"] != "Bearer $TOKEN$" {
				t.Errorf("visitor changed the delegated read: %#v", body)
			}
			codexLiveTestReply(w, http.StatusOK, codexLiveTestBody(43))
		default:
			t.Errorf("unexpected upstream path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	client := cpamp.New(upstream.URL, "PRIVATE_CPAMP_ADMIN_KEY", 3*time.Second, 1<<20)
	cache := newCodexUsageCache(client, 3*time.Second)
	cache.now = func() time.Time { return codexLiveTestTime }
	server := &Server{cfg: config.Config{PublicAccess: true}, cpamp: client, codexUsage: cache}
	handler := server.Handler()
	endpoint := "/viewer/api/v1/quota"
	for _, query := range []string{"?url=http://attacker.invalid", "?method=DELETE", "?auth_index=attacker", "?header=attacker", "?"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, endpoint+query, nil))
		if response.Code != http.StatusBadRequest || calls.Load() != 0 {
			t.Fatalf("query reached upstream: query=%q status=%d calls=%d", query, response.Code, calls.Load())
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, endpoint, nil))
		if response.Code != http.StatusMethodNotAllowed || calls.Load() != 0 {
			t.Fatalf("write method reached upstream: method=%s status=%d calls=%d", method, response.Code, calls.Load())
		}
	}
	bodyRequest := httptest.NewRequest(http.MethodGet, endpoint, strings.NewReader(`{"url":"http://attacker.invalid","method":"DELETE"}`))
	bodyResponse := httptest.NewRecorder()
	handler.ServeHTTP(bodyResponse, bodyRequest)
	if bodyResponse.Code != http.StatusBadRequest || calls.Load() != 0 {
		t.Fatalf("GET body reached upstream: status=%d calls=%d", bodyResponse.Code, calls.Load())
	}
	request := httptest.NewRequest(http.MethodGet, endpoint, nil)
	request.Header.Set("Authorization", "Bearer ATTACKER_TOKEN")
	request.Header.Set("X-HTTP-Method-Override", "DELETE")
	request.Header.Set("X-Target-URL", "http://attacker.invalid")
	request.Header.Set("Chatgpt-Account-Id", "ATTACKER_ACCOUNT")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || delegated.Load() != 1 {
		t.Fatalf("safe live read failed: status=%d body=%s", response.Code, response.Body.String())
	}
	var payload quotaResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Accounts) != 1 || len(payload.Accounts[0].Windows) != 1 || payload.Accounts[0].Windows[0].Pool != "codex_main" || payload.Accounts[0].Windows[0].Remaining != 57 {
		t.Fatalf("live inventory was mixed with historical unknown windows: %#v", payload)
	}
	for _, private := range []string{"PRIVATE_", "ATTACKER_", "attacker.invalid", "trusted-auth-index", "trusted-account-id", "signals", "api-call"} {
		if strings.Contains(response.Body.String(), private) {
			t.Fatalf("public handler leaked private request/source data %q", private)
		}
	}
}

func TestCodexLiveConcurrentVisitorsShareRefreshWithFourProviderCallsMaximum(t *testing.T) {
	var calls atomic.Int64
	var active atomic.Int64
	var maximum atomic.Int64
	entered := make(chan struct{}, 128)
	release := make(chan struct{})
	var releaseOnce sync.Once
	upstream := codexLiveUsageTestServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		current := active.Add(1)
		defer active.Add(-1)
		for prior := maximum.Load(); current > prior; prior = maximum.Load() {
			if maximum.CompareAndSwap(prior, current) {
				break
			}
		}
		entered <- struct{}{}
		<-release
		codexLiveTestReply(w, http.StatusOK, codexLiveTestBody(43))
	}))
	defer upstream.Close()
	defer releaseOnce.Do(func() { close(release) })
	cache := codexLiveTestCache(upstream.URL)
	files := make([]map[string]any, 8)
	for i := range files {
		index := string(rune('a' + i))
		files[i] = codexLiveTestFile("auth-"+index, "account-"+index)
	}
	var visitors sync.WaitGroup
	for i := 0; i < 12; i++ {
		visitors.Add(1)
		go func() {
			defer visitors.Done()
			results := cache.snapshot(context.Background(), files)
			for _, file := range files {
				result := results[codexUsageKey(file)]
				if !result.Valid || result.Stale || len(result.Windows) != 1 {
					t.Errorf("concurrent visitor received incomplete refresh: %#v", result)
				}
			}
		}()
	}
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case <-deadline.C:
			releaseOnce.Do(func() { close(release) })
			visitors.Wait()
			t.Fatalf("expected four concurrent provider reads; observed %d", maximum.Load())
		}
	}
	releaseOnce.Do(func() { close(release) })
	visitors.Wait()
	if maximum.Load() > 4 || calls.Load() != int64(len(files)) {
		t.Fatalf("refresh was not globally bounded/coalesced: maximum=%d calls=%d", maximum.Load(), calls.Load())
	}
}

func TestCodexLiveCancelledAndDeadlineWaitersDoNotCancelSharedRefresh(t *testing.T) {
	var calls atomic.Int64
	entered := make(chan struct{}, 1)
	providerCancelled := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	upstream := codexLiveUsageTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
			select {
			case providerCancelled <- struct{}{}:
			default:
			}
			return
		case <-release:
			codexLiveTestReply(w, http.StatusOK, codexLiveTestBody(43))
		}
	}))
	defer upstream.Close()
	defer releaseOnce.Do(func() { close(release) })
	cache := codexLiveTestCache(upstream.URL)
	file := codexLiveTestFile("shared-auth", "shared-account")
	files := []map[string]any{file}
	initiatorCtx, cancelInitiator := context.WithCancel(context.Background())
	defer cancelInitiator()
	initiatorDone := make(chan map[string]codexUsageResult, 1)
	go func() { initiatorDone <- cache.snapshot(initiatorCtx, files) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("shared provider refresh did not start")
	}

	healthyDone := make(chan map[string]codexUsageResult, 1)
	go func() { healthyDone <- cache.snapshot(context.Background(), files) }()
	deadlineCtx, cancelDeadline := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancelDeadline()
	deadlineDone := make(chan map[string]codexUsageResult, 1)
	go func() { deadlineDone <- cache.snapshot(deadlineCtx, files) }()
	cancelInitiator()
	select {
	case result := <-initiatorDone:
		if len(result) != 0 {
			t.Fatalf("cancelled initiator received a fabricated result: %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("refresh initiator kept waiting after its request was cancelled")
	}
	select {
	case result := <-deadlineDone:
		if len(result) != 0 || deadlineCtx.Err() != context.DeadlineExceeded {
			t.Fatalf("deadline waiter returned unexpected result/error: %#v %v", result, deadlineCtx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("short-deadline waiter remained blocked behind the provider request")
	}
	select {
	case <-providerCancelled:
		t.Fatal("one visitor's cancellation cancelled the shared provider refresh")
	default:
	}
	if calls.Load() != 1 {
		t.Fatalf("cancelled waiters started duplicate provider calls: %d", calls.Load())
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case results := <-healthyDone:
		result := results[codexUsageKey(file)]
		codexLiveAssertRemaining(t, result, 57)
		if result.Stale {
			t.Fatal("a successful shared refresh was marked stale after another visitor left")
		}
	case <-time.After(time.Second):
		t.Fatal("healthy waiter did not receive the completed shared refresh")
	}
	codexLiveAssertRemaining(t, codexLiveTestOne(t, cache, file), 57)
	if calls.Load() != 1 {
		t.Fatalf("completed shared refresh was not cached: %d provider calls", calls.Load())
	}
}
