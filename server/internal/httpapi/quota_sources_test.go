package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cpamp-viewer/server/internal/config"
	"cpamp-viewer/server/internal/cpamp"
	"cpamp-viewer/server/internal/sub2api"
)

func TestMultipleQuotaSourcesKeepCredentialsCachesAndIdentitiesSeparate(t *testing.T) {
	var primaryCalls, secondCalls atomic.Int64
	newCPA := func(key string, used float64, resetCount int, calls *atomic.Int64) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+key || r.Header.Get("X-API-Key") != "" {
				t.Error("CPA received another instance's credential")
			}
			switch r.URL.Path {
			case "/v0/management/auth-files":
				_ = json.NewEncoder(w).Encode(map[string]any{"files": []any{codexLiveTestFile("same-index", "same-account")}})
			case "/v0/management/monitoring/header-snapshots":
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
			case "/v0/management/api-call":
				calls.Add(1)
				var delegated struct {
					AuthIndex string            `json:"auth_index"`
					Method    string            `json:"method"`
					URL       string            `json:"url"`
					Header    map[string]string `json:"header"`
				}
				if err := json.NewDecoder(r.Body).Decode(&delegated); err != nil {
					t.Error(err)
				}
				if r.Method != http.MethodPost || delegated.Method != http.MethodGet || delegated.AuthIndex != "same-index" || delegated.Header["Authorization"] != "Bearer $TOKEN$" || delegated.Header["Chatgpt-Account-Id"] != "same-account" {
					t.Errorf("unexpected delegated read: %#v", delegated)
				}
				switch delegated.URL {
				case codexUsageURL:
					codexLiveTestReply(w, http.StatusOK, codexLiveTestBody(used))
				case codexResetCreditsURL:
					credits := make([]map[string]any, resetCount)
					for index := range credits {
						credits[index] = map[string]any{
							"id": "PRIVATE_RESET_ID", "reset_type": "codex_rate_limits", "status": "available",
							"expires_at": codexLiveTestTime.Add(time.Duration(index+1) * 24 * time.Hour).Format(time.RFC3339),
						}
					}
					body, _ := json.Marshal(map[string]any{"available_count": resetCount, "credits": credits})
					codexLiveTestReply(w, http.StatusOK, string(body))
				default:
					t.Errorf("unexpected quota or reset mutation URL: %s", delegated.URL)
					w.WriteHeader(http.StatusBadRequest)
				}
			default:
				t.Errorf("unexpected CPA request: %s", r.URL.Path)
				http.NotFound(w, r)
			}
		}))
	}
	newSub := func(key string, jwt bool, used float64) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if jwt {
				if r.Header.Get("Authorization") != "Bearer "+key || r.Header.Get("X-API-Key") != "" {
					t.Error("Sub2API JWT was mixed with another credential")
				}
			} else if r.Header.Get("X-API-Key") != key || r.Header.Get("Authorization") != "" {
				t.Error("Sub2API API key was mixed with another credential")
			}
			if r.Method != http.MethodGet || r.URL.Path != "/api/v1/admin/accounts" {
				t.Errorf("unexpected Sub2API request: %s %s", r.Method, r.URL.Path)
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"items": []any{map[string]any{
					"id": 7, "name": "same alias", "platform": "openai", "type": "oauth", "status": "active",
					"credentials": map[string]any{"email": "same@example.test", "plan_type": "pro", "access_token": "PRIVATE_TOKEN"},
					"extra":       map[string]any{"codex_7d_used_percent": used, "codex_7d_window_minutes": 10080, "codex_7d_reset_at": "2099-01-01T00:00:00Z"},
				}}, "total": 1, "pages": 1,
			}})
		}))
	}
	primary := newCPA("PRIVATE_PRIMARY_KEY", 10, 1, &primaryCalls)
	defer primary.Close()
	second := newCPA("PRIVATE_SECOND_KEY", 20, 3, &secondCalls)
	defer second.Close()
	legacySub := newSub("PRIVATE_SUB_KEY", false, 30)
	defer legacySub.Close()
	secondSub := newSub("PRIVATE_JWT", true, 40)
	defer secondSub.Close()
	cfg := config.Config{PublicAccess: true, CPAMPBaseURL: primary.URL, RequestTimeout: time.Second, MaxUpstreamBodyBytes: 1 << 20,
		QuotaSources: []config.QuotaSource{
			{Type: "cpamp", Name: "CPA 二号", BaseURL: second.URL, AdminKey: "PRIVATE_SECOND_KEY"},
			{Type: "sub2api", Name: "Sub 二号", BaseURL: secondSub.URL, AdminJWT: "PRIVATE_JWT"},
		},
	}
	client := cpamp.New(primary.URL, "PRIVATE_PRIMARY_KEY", time.Second, 1<<20)
	server := &Server{cfg: cfg, cpamp: client, codexUsage: newCodexUsageCache(client, time.Second),
		sub2api: sub2api.New(legacySub.URL, "PRIVATE_SUB_KEY", "", time.Second, 1<<20), quotaSources: newAdditionalQuotaSources(cfg)}
	var firstIDs map[string]bool
	for request := 0; request < 2; request++ {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/viewer/api/v1/quota", nil))
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "PRIVATE_") || strings.Contains(response.Body.String(), second.URL) {
			t.Fatalf("bad or unsafe combined response: %d %s", response.Code, response.Body.String())
		}
		var payload quotaResponse
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Accounts) != 4 || len(payload.Warnings) != 0 || payload.Source != "combined-quota" {
			t.Fatalf("multiple sources were not combined: %#v", payload)
		}
		ids := make(map[string]bool)
		remaining := make(map[float64]bool)
		for _, account := range payload.Accounts {
			if ids[account.ID] || len(account.Windows) != 1 || account.Plan != "pro" {
				t.Fatalf("duplicate account or mixed metadata: %#v", account)
			}
			ids[account.ID] = true
			remaining[account.Windows[0].Remaining] = true
			if account.SourceName != "" && account.SourceID == "" {
				t.Fatal("named source has no opaque identity")
			}
			if account.Source == "cpamp" {
				if account.InstanceID == "" || account.InstanceID != account.SourceID || account.InstanceName != account.SourceName {
					t.Fatalf("CPAMP quota is missing its federated instance identity: %#v", account)
				}
				wantCount := int64(1)
				wantRemaining := float64(90)
				if account.InstanceID == federationSourceID(second.URL) {
					wantCount, wantRemaining = 3, 80
				} else if account.InstanceID != federationSourceID(primary.URL) {
					t.Fatalf("unknown quota instance: %q", account.InstanceID)
				}
				if account.ResetCredits == nil || account.ResetCredits.AvailableCount == nil || *account.ResetCredits.AvailableCount != wantCount || len(account.ResetCredits.Credits) != int(wantCount) || account.ResetCredits.Partial || account.ResetCredits.Stale || account.Windows[0].Remaining != wantRemaining {
					t.Fatalf("reset cards or quota crossed instance caches: %#v", account)
				}
			} else if account.ResetCredits != nil {
				t.Fatalf("Sub2API inherited CPAMP reset cards: %#v", account)
			}
			if firstIDs != nil && !firstIDs[account.ID] {
				t.Fatal("account identity changed when source order or name changed")
			}
		}
		if !remaining[90] || !remaining[80] || !remaining[70] || !remaining[60] {
			t.Fatal("quota values crossed source boundaries")
		}
		firstIDs = ids
		server.quotaSources[0], server.quotaSources[1] = server.quotaSources[1], server.quotaSources[0]
		server.quotaSources[0].name = "已改名"
	}
	if primaryCalls.Load() != 2 || secondCalls.Load() != 2 {
		t.Fatalf("usage and reset reads did not retain separate instance caches: primary=%d secondary=%d", primaryCalls.Load(), secondCalls.Load())
	}
}

func TestMultipleQuotaSourcesPartialFailureAndEmptySuccess(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer primary.Close()
	server := &Server{cpamp: cpamp.New(primary.URL, "key", time.Second, 1<<20), quotaSources: []quotaSource{
		{kind: "sub2api", name: "不可用实例", load: func(context.Context) ([]quotaAccount, error) { return nil, errors.New("PRIVATE_ERROR") }},
		{kind: "cpamp", name: "空实例", load: func(context.Context) ([]quotaAccount, error) { return []quotaAccount{}, nil }},
	}}
	response, err := server.loadCombinedQuota(context.Background())
	if err != nil || len(response.Accounts) != 0 || len(response.Warnings) != 2 || response.Warnings[1] != "不可用实例 额度暂不可用" {
		t.Fatalf("empty successful source should survive other failures: %#v %v", response, err)
	}
	server.quotaSources = server.quotaSources[:1]
	if _, err := server.loadCombinedQuota(context.Background()); err == nil {
		t.Fatal("all unavailable sources must return an error")
	}
}
