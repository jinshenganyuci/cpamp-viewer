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
	"testing"
	"time"

	"cpamp-viewer/server/internal/config"
	"cpamp-viewer/server/internal/cpamp"
	"cpamp-viewer/server/internal/sub2api"
)

func TestSub2APIQuotaProjectsPassiveUsageWithoutSecrets(t *testing.T) {
	var requests []string
	var requestsMu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "private-admin-key" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected upstream authentication headers: %#v", r.Header)
		}
		requestsMu.Lock()
		requests = append(requests, r.Method+" "+r.URL.String())
		requestsMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/admin/accounts":
			_, _ = io.WriteString(w, `{"code":0,"data":{"items":[{"id":10,"name":"Alpha","platform":"openai","type":"oauth","status":"active","updated_at":"2026-09-30T00:00:00Z","extra":{"plan_type":"Plus","access_token":"secret-must-not-leak","codex_usage_updated_at":"2026-09-30T01:00:00Z","codex_5h_used_percent":25,"codex_5h_window_minutes":300,"codex_5h_reset_at":"2099-01-01T00:00:00Z","codex_7d_window_minutes":10080,"codex_7d_reset_at":"2099-01-07T00:00:00Z"}},{"id":11,"name":"Beta","platform":"anthropic","type":"oauth","status":"error","error_message":"Bearer secret-must-not-leak","updated_at":"2026-09-30T00:00:00Z","quota_limit":100,"quota_used":25}],"total":2,"page":1,"page_size":200,"pages":1}}`)
		case "/api/v1/admin/accounts/11/usage":
			if r.URL.Query().Get("source") != "passive" {
				t.Errorf("usage query is not passive: %s", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"source":"passive","updated_at":null,"five_hour":null}}`)
		default:
			t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	cpampUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/management/auth-files":
			_ = json.NewEncoder(w).Encode(map[string]any{"files": []any{storedQuotaTestFile("muse", "cpamp-one")}})
		case "/v0/management/monitoring/header-snapshots":
			_, _ = io.WriteString(w, `{"items":[]}`)
		default:
			t.Errorf("unexpected CPAMP request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer cpampUpstream.Close()

	server := &Server{
		cfg:     config.Config{PublicAccess: true},
		cpamp:   cpamp.New(cpampUpstream.URL, "private-cpamp-key", 3*time.Second, 1<<20),
		sub2api: sub2api.New(upstream.URL, "private-admin-key", "", 3*time.Second, 1<<20),
		logger:  slog.Default(),
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/viewer/api/v1/quota", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("quota status %d: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "secret-must-not-leak") || strings.Contains(response.Body.String(), "private-admin-key") || strings.Contains(response.Body.String(), `"id":10`) {
		t.Fatalf("quota response leaked upstream details: %s", response.Body.String())
	}
	var payload quotaResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Source != "combined-quota" || len(payload.Accounts) != 3 || len(payload.Warnings) != 0 {
		t.Fatalf("unexpected quota response: %#v", payload)
	}
	var codex, claude, cpampAccount quotaAccount
	for _, account := range payload.Accounts {
		if account.Source == "cpamp" {
			cpampAccount = account
		} else if account.Provider == "codex" {
			codex = account
		} else if account.Provider == "claude" {
			claude = account
		}
	}
	if codex.ID != pseudonym("sub2api-account:10") || codex.Source != "sub2api" || codex.Plan != "Plus" || len(codex.Windows) != 2 || codex.Windows[0].Used != 25 || codex.Windows[0].Pool != "codex_main" || !strings.Contains(response.Body.String(), `"remaining_percent":null`) {
		t.Fatalf("unexpected Codex projection: %#v", codex)
	}
	if claude.ID != pseudonym("sub2api-account:11") || !claude.Disabled || len(claude.Windows) != 1 || claude.Windows[0].Remaining != 75 {
		t.Fatalf("unexpected Claude projection: %#v", claude)
	}
	if cpampAccount.Provider != "meta" || cpampAccount.DisplayName != "same@example.test" {
		t.Fatalf("CPAMP account was not preserved: %#v", cpampAccount)
	}
	if len(requests) != 2 {
		t.Fatalf("expected one list and one Anthropic passive GET, got %v", requests)
	}
	for _, request := range requests {
		if !strings.HasPrefix(request, "GET ") || strings.Contains(request, "/batch") {
			t.Fatalf("unexpected upstream method: %s", request)
		}
	}
}

func TestSub2APIOpenAIUsesSavedQuotaWithoutUnsupportedPassiveRequest(t *testing.T) {
	var usageRequested bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/accounts" {
			usageRequested = true
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, `{"code":0,"data":{"items":[{"id":4,"name":"OpenAI Account","platform":"openai","type":"oauth","status":"active","credentials":{"email":"real@example.test","plan_type":"pro","access_token":"secret-must-not-leak"},"extra":{"email":"other@example.test","codex_usage_updated_at":"2026-09-30T01:00:00Z","codex_5h_used_percent":0,"codex_5h_window_minutes":0,"codex_5h_reset_at":"2026-09-30T01:00:00Z","codex_7d_used_percent":18,"codex_7d_window_minutes":10080,"codex_7d_reset_at":"2099-01-07T00:00:00Z","access_token":"secret-must-not-leak"}}],"total":1,"pages":1}}`)
	}))
	defer upstream.Close()
	server := &Server{sub2api: sub2api.New(upstream.URL, "private-sub2api-key", "", time.Second, 1<<20)}
	accounts, err := server.loadSub2APIQuota(context.Background())
	if err != nil || len(accounts) != 1 || usageRequested {
		t.Fatalf("OpenAI snapshot result = %#v, %v, usage requested = %t", accounts, err, usageRequested)
	}
	if accounts[0].DisplayName != "real@example.test" || accounts[0].Plan != "pro" || len(accounts[0].Windows) != 1 || accounts[0].Windows[0].WindowMins != 10080 || accounts[0].Windows[0].Used != 18 || accounts[0].StatusMessage != "" {
		t.Fatalf("unsupported passive usage broke saved Codex quota: %#v", accounts[0])
	}
	encoded, err := json.Marshal(accounts)
	if err != nil || strings.Contains(string(encoded), "secret-must-not-leak") {
		t.Fatalf("saved quota leaked account credentials: %v", err)
	}
}

func TestSub2APIPassiveUsageRequiresAnthropicSubscription(t *testing.T) {
	for _, test := range []struct {
		platform, accountType string
		want                  bool
	}{
		{"anthropic", "oauth", true},
		{"anthropic", "setup-token", true},
		{"anthropic", "api-key", false},
		{"openai", "oauth", false},
		{"gemini", "oauth", false},
	} {
		account := sub2APIAccount{Platform: test.platform, Type: test.accountType}
		if got := supportsSub2APIPassiveUsage(account); got != test.want {
			t.Fatalf("passive support for %s/%s = %t, want %t", test.platform, test.accountType, got, test.want)
		}
	}
}

func TestSub2APICodexSnapshotKeepsOnlyCurrentWindows(t *testing.T) {
	observedAt := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	extra := map[string]any{
		"codex_usage_updated_at":              observedAt.Format(time.RFC3339),
		"codex_5h_used_percent":               float64(25),
		"codex_5h_window_minutes":             float64(300),
		"codex_5h_reset_after_seconds":        float64(7200),
		"codex_7d_used_percent":               float64(80),
		"codex_7d_window_minutes":             float64(10080),
		"codex_7d_reset_at":                   observedAt.Format(time.RFC3339),
		"codex_primary_used_percent":          float64(25),
		"codex_primary_window_minutes":        float64(300),
		"codex_primary_reset_after_seconds":   float64(7200),
		"codex_secondary_used_percent":        float64(45),
		"codex_secondary_window_minutes":      float64(10080),
		"codex_secondary_reset_after_seconds": float64(7200),
	}
	var windows []quotaWindow
	appendSub2APICodexSnapshot(&windows, extra, "global")
	if len(windows) != 2 || windows[0].ID != "codex_5h_snapshot" || windows[0].ResetAtMS != observedAt.Add(2*time.Hour).UnixMilli() || windows[0].Remaining != 75 || windows[1].WindowMins != 10080 || windows[1].Remaining != 55 {
		t.Fatalf("expired or relative Codex windows were misread: %#v", windows)
	}
}

func TestSub2APIAccountDisplayNamePrefersValidatedEmail(t *testing.T) {
	for _, test := range []struct {
		name        string
		account     sub2APIAccount
		displayName string
	}{
		{name: "extra fallback", account: sub2APIAccount{Name: "Alias", Extra: map[string]any{"email": "saved@example.test"}}, displayName: "saved@example.test"},
		{name: "top level fallback", account: sub2APIAccount{Name: "Alias", Email: "top@example.test"}, displayName: "top@example.test"},
		{name: "invalid email", account: sub2APIAccount{Name: "Alias", Extra: map[string]any{"email": "Bearer secret@example.test"}}, displayName: "Alias"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := sub2APIAccountDisplayName(test.account); got != test.displayName {
				t.Fatalf("account name = %q, want %q", got, test.displayName)
			}
		})
	}
}

func TestSub2APIQuotaRejectsUnexpectedParameters(t *testing.T) {
	server := &Server{cfg: config.Config{PublicAccess: true}}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/viewer/api/v1/quota?force=true", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unexpected quota query status %d", response.Code)
	}
}

func TestCombinedQuotaKeepsCPAMPWhenSub2APIUnavailable(t *testing.T) {
	cpampUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/management/auth-files":
			_ = json.NewEncoder(w).Encode(map[string]any{"files": []any{storedQuotaTestFile("muse", "cpamp-one")}})
		case "/v0/management/monitoring/header-snapshots":
			_, _ = io.WriteString(w, `{"items":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer cpampUpstream.Close()
	sub2APIUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"Bearer secret-must-not-leak"}`)
	}))
	defer sub2APIUpstream.Close()
	server := &Server{
		cfg:     config.Config{PublicAccess: true},
		cpamp:   cpamp.New(cpampUpstream.URL, "private-cpamp-key", time.Second, 1<<20),
		sub2api: sub2api.New(sub2APIUpstream.URL, "private-sub2api-key", "", time.Second, 1<<20),
		logger:  slog.Default(),
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/viewer/api/v1/quota", nil))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "secret-must-not-leak") {
		t.Fatalf("unexpected partial quota response: %d %s", response.Code, response.Body.String())
	}
	var payload quotaResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Accounts) != 1 || payload.Accounts[0].Source != "cpamp" || len(payload.Warnings) != 1 || payload.Warnings[0] != "Sub2API 额度暂不可用" {
		t.Fatalf("incorrect partial quota result: %#v", payload)
	}
}

func TestCombinedQuotaKeepsSub2APIWhenCPAMPUnavailable(t *testing.T) {
	cpampUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer cpampUpstream.Close()
	sub2APIUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/admin/accounts":
			_, _ = io.WriteString(w, `{"code":0,"data":{"items":[{"id":4,"name":"Sub account","platform":"anthropic","type":"oauth","status":"active"}],"total":1,"pages":1}}`)
		case "/api/v1/admin/accounts/4/usage":
			_, _ = io.WriteString(w, `{"code":0,"data":{"updated_at":"2026-09-30T01:00:00Z","five_hour":{"utilization":50}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer sub2APIUpstream.Close()
	server := &Server{
		cfg:     config.Config{PublicAccess: true},
		cpamp:   cpamp.New(cpampUpstream.URL, "private-cpamp-key", time.Second, 1<<20),
		sub2api: sub2api.New(sub2APIUpstream.URL, "private-sub2api-key", "", time.Second, 1<<20),
		logger:  slog.Default(),
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/viewer/api/v1/quota", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("partial quota status %d: %s", response.Code, response.Body.String())
	}
	var payload quotaResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Accounts) != 1 || payload.Accounts[0].Source != "sub2api" || len(payload.Warnings) != 1 || payload.Warnings[0] != "CPA Manager Plus 额度暂不可用" {
		t.Fatalf("incorrect partial quota result: %#v", payload)
	}
}

func TestSub2APIAccountListPaginatesWithoutTruncation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = io.WriteString(w, `{"code":0,"data":{"items":[{"id":1,"name":"one"}],"total":2,"page":1,"page_size":1,"pages":2}}`)
		case "2":
			_, _ = io.WriteString(w, `{"code":0,"data":{"items":[{"id":2,"name":"two"}],"total":2,"page":2,"page_size":1,"pages":2}}`)
		default:
			t.Errorf("unexpected account page %q", r.URL.Query().Get("page"))
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	server := &Server{sub2api: sub2api.New(upstream.URL, "test-key", "", time.Second, 1<<20)}
	accounts, err := server.fetchSub2APIAccounts(context.Background())
	if err != nil || len(accounts) != 2 || accounts[0].ID != 1 || accounts[1].ID != 2 {
		t.Fatalf("pagination result = %#v, %v", accounts, err)
	}
}

func TestSub2APIUnnamedAccountDoesNotExposeRawID(t *testing.T) {
	account := projectSub2APIAccount(sub2APIAccount{ID: 12345, Platform: "openai", Status: "active"}, sub2APIUsageInfo{}, false)
	if strings.Contains(account.DisplayName, "12345") || account.ID != pseudonym("sub2api-account:12345") {
		t.Fatalf("account identity was not projected: %#v", account)
	}
}

func TestSub2APICodexQuotaDimensionPreservesPool(t *testing.T) {
	for _, test := range []struct {
		name, dimension, wantPool string
	}{
		{"legacy default", "", "codex_main"},
		{"global", "global", "codex_main"},
		{"spark shadow", "spark", "codex_spark"},
		{"unknown dimension", "future-pool", "unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Sub2API uses the same codex_5h_/codex_7d_ keys for global
			// accounts and Spark shadows; the top-level dimension identifies the pool.
			payload := map[string]any{
				"id": 14, "platform": "openai", "type": "oauth", "status": "active",
				"extra": map[string]any{
					"codex_5h_used_percent": 25, "codex_5h_window_minutes": 300,
					"codex_5h_reset_at":     "2099-01-01T00:00:00Z",
					"codex_7d_used_percent": 60, "codex_7d_window_minutes": 10080,
					"codex_7d_reset_at":          "2099-01-07T00:00:00Z",
					"codex_primary_used_percent": 25, "codex_primary_window_minutes": 300,
					"codex_primary_reset_at":       "2099-01-01T00:00:00Z",
					"codex_secondary_used_percent": 40, "codex_secondary_window_minutes": 1440,
					"codex_secondary_reset_at": "2099-01-01T00:00:00Z",
				},
			}
			if test.dimension != "" {
				payload["quota_dimension"] = test.dimension
			}
			if test.dimension == "spark" {
				payload["parent_account_id"] = 7
			}
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			var account sub2APIAccount
			if err := json.Unmarshal(encoded, &account); err != nil {
				t.Fatal(err)
			}
			got := projectSub2APIAccount(account, sub2APIUsageInfo{}, false)
			if len(got.Windows) != 3 {
				t.Fatalf("expected three unique saved windows, got %#v", got.Windows)
			}
			for _, window := range got.Windows {
				if window.Pool != test.wantPool {
					t.Errorf("%s pool = %q, want %q", window.ID, window.Pool, test.wantPool)
				}
			}
			if got.Windows[0].Remaining != 75 || got.Windows[1].Remaining != 40 || got.Windows[2].Remaining != 60 {
				t.Fatalf("pool selection changed quota values: %#v", got.Windows)
			}
		})
	}
}

func TestSub2APIPassiveUsageDoesNotInventUnobservedFullQuota(t *testing.T) {
	for _, test := range []struct {
		name, payload string
		wantWindows   int
	}{
		{
			"no sample", `{"source":"passive","five_hour":{"utilization":0,"resets_at":null,"remaining_seconds":0}}`, 0,
		},
		{
			"observed zero", `{"source":"passive","updated_at":"2026-09-30T00:00:00Z","five_hour":{"utilization":0,"resets_at":null,"remaining_seconds":0}}`, 1,
		},
		{
			"known window", `{"source":"passive","five_hour":{"utilization":0,"resets_at":"2099-01-01T00:00:00Z","remaining_seconds":60}}`, 1,
		},
		{
			"known usage", `{"source":"passive","five_hour":{"utilization":25,"resets_at":null,"remaining_seconds":0}}`, 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var usage sub2APIUsageInfo
			if err := json.Unmarshal([]byte(test.payload), &usage); err != nil {
				t.Fatal(err)
			}
			account := projectSub2APIAccount(sub2APIAccount{ID: 21, Platform: "anthropic", Type: "oauth", Status: "active"}, usage, false)
			if len(account.Windows) != test.wantWindows {
				t.Fatalf("projected %d windows, want %d: %#v", len(account.Windows), test.wantWindows, account.Windows)
			}
		})
	}
}
