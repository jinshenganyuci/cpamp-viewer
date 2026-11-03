package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cpamp-viewer/server/internal/config"
)

func federationGuardBool(value bool) *bool { return &value }

func TestFederatedAccessGuardConfigSeparatesPublicationAndCredentials(t *testing.T) {
	base := config.Config{
		CPAMPBaseURL: "https://main.example.test", CPAMPAdminKey: "main-cpamp-secret",
		AccessGuardBaseURL: "https://direct-main.example.test", AccessGuardManagementKey: "main-direct-secret",
		AccessGuardPublicAll: true, AccessGuardPublicKeys: []config.AccessGuardPublicKey{{BindingID: "same-local-id", Name: "主实例名单"}},
		RequestTimeout: time.Second,
	}
	for _, tc := range []struct {
		name                 string
		all                  *bool
		keys                 []config.AccessGuardPublicKey
		globalAll            bool
		wantAll, wantEnabled bool
		wantName             string
	}{
		{name: "inherit explicit global all", globalAll: true, wantAll: true, wantEnabled: true},
		{name: "main allowlist does not cross instances"},
		{name: "explicit false closes inherited all", all: federationGuardBool(false), globalAll: true},
		{name: "extra opt-in without main opt-in", all: federationGuardBool(true), wantAll: true, wantEnabled: true},
		{name: "extra allowlist replaces global all", keys: []config.AccessGuardPublicKey{{BindingID: "same-local-id", Name: "副实例名单"}}, globalAll: true, wantEnabled: true, wantName: "副实例名单"},
		{name: "empty allowlist stays empty", keys: []config.AccessGuardPublicKey{}, globalAll: true, wantEnabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base.AccessGuardPublicAll = tc.globalAll
			source := config.QuotaSource{Type: "cpamp", BaseURL: "https://extra.example.test", AdminKey: "extra-cpamp-secret", AccessGuardPublicAll: tc.all, AccessGuardPublicKeys: tc.keys}
			got := federatedAccessGuardConfig(base, source)
			if got.CPAMPBaseURL != source.BaseURL || got.CPAMPAdminKey != source.AdminKey || got.AccessGuardBaseURL != "" || got.AccessGuardManagementKey != "" {
				t.Fatal("secondary instance reused main/direct credentials")
			}
			if got.AccessGuardViaCPAMP != tc.wantEnabled || got.AccessGuardPublicAll != tc.wantAll {
				t.Fatalf("publication flags = enabled:%v all:%v", got.AccessGuardViaCPAMP, got.AccessGuardPublicAll)
			}
			if tc.wantName != "" {
				if len(got.AccessGuardPublicKeys) != 1 || got.AccessGuardPublicKeys[0].Name != tc.wantName {
					t.Fatal("per-instance allowlist lost")
				}
				got.AccessGuardPublicKeys[0].Name = "changed"
				if source.AccessGuardPublicKeys[0].Name != tc.wantName {
					t.Fatal("allowlist slice aliased source config")
				}
			} else if len(got.AccessGuardPublicKeys) != 0 {
				t.Fatal("main allowlist leaked to secondary source")
			}
		})
	}
	if got := federatedAccessGuardConfig(base, config.QuotaSource{Type: "sub2api"}); got.AccessGuardViaCPAMP || got.AccessGuardBaseURL != "" || got.AccessGuardManagementKey != "" {
		t.Fatal("Sub2API was treated as Access Guard")
	}
}

func federationGuardSource(t *testing.T, name, credential string, handler http.HandlerFunc) cpampReadSource {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v0/management/plugins/access-guard/native-key-bindings" || r.URL.RawQuery != "" || r.ContentLength != 0 {
			t.Errorf("unexpected operation %s %s", r.Method, r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer "+credential {
			t.Error("source used another instance's credential")
		}
		handler(w, r)
	}))
	t.Cleanup(upstream.Close)
	cfg := config.Config{CPAMPBaseURL: upstream.URL, CPAMPAdminKey: credential, AccessGuardViaCPAMP: true, AccessGuardPublicAll: true, RequestTimeout: 2 * time.Second}
	cache := newAccessGuardQuotaCache(cfg)
	cache.now = func() time.Time { return accessGuardTestNow }
	return cpampReadSource{ID: pseudonym("instance:" + name), Name: name, Reader: &Server{cfg: cfg, accessGuard: cache}}
}

func TestFederatedAccessGuardKeepsIdenticalBindingIDsAndStatesSeparate(t *testing.T) {
	var calls atomic.Int32
	makeHandler := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			active := accessGuardTestBinding("same-private-binding-id")
			active["name"] = name
			inactive := accessGuardTestBinding("private-disabled")
			inactive["enabled"] = false
			inactive["name"] = "已停绑定"
			unlimited := accessGuardTestBinding("private-unlimited")
			unlimited["name"] = "不限额"
			unlimited["usage"].(map[string]any)["weekly_usd_limit"] = 0
			unknown := accessGuardTestBinding("private-unknown")
			unknown["name"] = "未知"
			delete(unknown, "usage")
			writeJSON(w, 200, map[string]any{"bindings": []any{active, inactive, unlimited, unknown}})
		}
	}
	sources := []cpampReadSource{federationGuardSource(t, "实例甲", "private-alpha-key", makeHandler("公开A")), federationGuardSource(t, "实例乙", "private-beta-key", makeHandler("公开B"))}
	response, err := federatedAccessGuardSnapshot(context.Background(), sources)
	if err != nil || !response.Configured || response.PartialFailed || response.Stale || len(response.Items) != 8 || calls.Load() != 2 {
		t.Fatalf("bad federation response %+v err=%v calls=%d", response, err, calls.Load())
	}
	seen := map[string]bool{}
	for i, item := range response.Items {
		if seen[item.ID] || !apiKeyViewIDPattern.MatchString(item.ID) || item.InstanceID != sources[i/4].ID || item.InstanceName != sources[i/4].Name {
			t.Fatalf("unscoped/colliding item %+v", item)
		}
		seen[item.ID] = true
		want := []string{"active", "inactive", "unlimited", "unavailable"}[i%4]
		if item.State != want {
			t.Fatalf("state=%s want=%s", item.State, want)
		}
	}
	raw, _ := json.Marshal(response)
	for _, secret := range []string{"private", "same-private-binding-id", "caller_scope", "key_preview", "key_hash", "auth_ids", "model_access"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("federation leaked %q", secret)
		}
	}
	// Namespacing must not mutate the original per-instance cached projection.
	first, _ := sources[0].Reader.accessGuard.snapshot(context.Background())
	if first.Items[0].InstanceID != "" || first.Items[0].InstanceName != "" || first.Items[0].ID == response.Items[0].ID {
		t.Fatal("federation mutated instance cache")
	}
	again, _ := federatedAccessGuardSnapshot(context.Background(), sources)
	if calls.Load() != 2 || again.Items[0].ID != response.Items[0].ID {
		t.Fatal("cached federation changed identities or repeated upstream calls")
	}
}

func TestFederatedAccessGuardPartialFailuresEmptyAndClosedSources(t *testing.T) {
	failed := federationGuardSource(t, "不可用实例", "private-failed", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"Bearer private-management-secret"}`))
	})
	empty := federationGuardSource(t, "空实例", "private-empty", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]any{"bindings": []any{}}) })
	closed := cpampReadSource{ID: pseudonym("closed"), Name: "关闭实例", Reader: &Server{}}
	response, err := federatedAccessGuardSnapshot(context.Background(), []cpampReadSource{failed, empty, closed})
	if err != nil || !response.Configured || !response.PartialFailed || response.Stale || len(response.Items) != 0 || len(response.Warnings) != 1 {
		t.Fatalf("empty success/partial failed lost: %+v %v", response, err)
	}
	encoded, _ := json.Marshal(response)
	if strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "http") {
		t.Fatal("raw source error or URL escaped")
	}
	if _, err := federatedAccessGuardSnapshot(context.Background(), []cpampReadSource{failed, closed}); err == nil {
		t.Fatal("closed source hid total failure")
	}
	response, err = federatedAccessGuardSnapshot(context.Background(), []cpampReadSource{closed})
	if err != nil || response.Configured || response.PartialFailed || response.Items == nil {
		t.Fatalf("closed source response %+v %v", response, err)
	}
}

func TestFederatedAccessGuardStaleSourceRetainsOnlyItsOwnSuccessfulData(t *testing.T) {
	var fail atomic.Bool
	var elapsed atomic.Int64
	var calls atomic.Int32
	source := federationGuardSource(t, "实例甲", "private-alpha", func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		if fail.Load() {
			w.WriteHeader(503)
			_, _ = w.Write([]byte("private-upstream-token"))
			return
		}
		binding := accessGuardTestBinding("same-id")
		binding["name"] = "公开"
		writeJSON(w, 200, map[string]any{"bindings": []any{binding}})
	})
	source.Reader.accessGuard.now = func() time.Time { return accessGuardTestNow.Add(time.Duration(elapsed.Load())) }
	first, err := federatedAccessGuardSnapshot(context.Background(), []cpampReadSource{source})
	if err != nil || len(first.Items) != 1 {
		t.Fatal("first snapshot missing")
	}
	fail.Store(true)
	elapsed.Store(int64(15 * time.Second))
	for i := 0; i < 3; i++ {
		stale, err := federatedAccessGuardSnapshot(context.Background(), []cpampReadSource{source})
		if err != nil || !stale.Stale || !stale.PartialFailed || len(stale.Warnings) != 1 || len(stale.Items) != 1 || stale.UpdatedAt != first.UpdatedAt || stale.Items[0].ID != first.Items[0].ID {
			t.Fatalf("stale result %+v %v", stale, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("failure cooldown did not hold across aggregate requests")
	}
}

func TestFederatedAccessGuardLimitsSharedRefreshesAcrossVisitors(t *testing.T) {
	var active, maximum, calls atomic.Int32
	entered := make(chan struct{}, 16)
	release := make(chan struct{})
	sources := make([]cpampReadSource, 10)
	for i := range sources {
		sources[i] = federationGuardSource(t, fmt.Sprintf("实例%d", i), fmt.Sprintf("private-key-%d", i), func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			n := active.Add(1)
			defer active.Add(-1)
			for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
			}
			entered <- struct{}{}
			<-release
			writeJSON(w, 200, map[string]any{"bindings": []any{}})
		})
	}
	var visitors sync.WaitGroup
	for i := 0; i < 8; i++ {
		visitors.Add(1)
		go func() {
			defer visitors.Done()
			result, err := federatedAccessGuardSnapshot(context.Background(), sources)
			if err != nil || result.PartialFailed {
				t.Errorf("shared refresh failed: %v", err)
			}
		}()
	}
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("four concurrent reads did not begin")
		}
	}
	if active.Load() > 4 {
		t.Fatal("unbounded upstream concurrency")
	}
	close(release)
	visitors.Wait()
	if maximum.Load() > 4 || calls.Load() != 10 {
		t.Fatalf("maximum=%d calls=%d", maximum.Load(), calls.Load())
	}
}

func TestFederatedAccessGuardQueueDoesNotConsumeIndividualRequestTimeout(t *testing.T) {
	sources := make([]cpampReadSource, 8)
	var calls atomic.Int32
	for i := range sources {
		sources[i] = federationGuardSource(t, fmt.Sprintf("排队实例%d", i), "private-key", func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			select {
			case <-time.After(150 * time.Millisecond):
				writeJSON(w, http.StatusOK, map[string]any{"bindings": []any{}})
			case <-r.Context().Done():
			}
		})
		sources[i].Reader.accessGuard.timeout = 250 * time.Millisecond
	}
	result, err := federatedAccessGuardSnapshot(context.Background(), sources)
	if err != nil || result.PartialFailed || calls.Load() != 8 {
		t.Fatalf("queue consumed the second batch's HTTP budget: partial=%v calls=%d error=%v", result.PartialFailed, calls.Load(), err)
	}
}

func TestFederatedAccessGuardHandlerUsesConfiguredCPAMPReaders(t *testing.T) {
	for _, mode := range []string{"global-all", "main-only-list", "secondary-list", "secondary-closed"} {
		t.Run(mode, func(t *testing.T) {
			var mainCalls, extraCalls atomic.Int32
			makeUpstream := func(key string, count *atomic.Int32) *httptest.Server {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					count.Add(1)
					if r.Method != http.MethodGet || r.URL.Path != "/v0/management/plugins/access-guard/native-key-bindings" || r.Header.Get("Authorization") != "Bearer "+key {
						t.Error("configured reader selected the wrong credentials or operation")
					}
					binding := accessGuardTestBinding("native-key-1")
					binding["name"] = "公开绑定"
					writeJSON(w, http.StatusOK, map[string]any{"bindings": []any{binding}})
				}))
				t.Cleanup(upstream.Close)
				return upstream
			}
			main := makeUpstream("main-key", &mainCalls)
			extra := makeUpstream("extra-key", &extraCalls)
			cfg := config.Config{PublicAccess: true, CPAMPBaseURL: main.URL, CPAMPAdminKey: "main-key", RequestTimeout: time.Second, MaxUpstreamBodyBytes: 1 << 20, AccessGuardViaCPAMP: true}
			source := config.QuotaSource{Type: "cpamp", Name: "二号实例", BaseURL: extra.URL, AdminKey: "extra-key"}
			wantItems, wantExtraCalls := 2, int32(1)
			switch mode {
			case "global-all":
				cfg.AccessGuardPublicAll = true
			case "main-only-list":
				cfg.AccessGuardPublicKeys = []config.AccessGuardPublicKey{{BindingID: "native-key-1", Name: "仅主实例"}}
				wantItems, wantExtraCalls = 1, 0
			case "secondary-list":
				cfg.AccessGuardPublicKeys = []config.AccessGuardPublicKey{{BindingID: "native-key-1", Name: "仅主实例"}}
				source.AccessGuardPublicKeys = []config.AccessGuardPublicKey{{BindingID: "native-key-1", Name: "仅副实例"}}
			case "secondary-closed":
				cfg.AccessGuardPublicAll = true
				source.AccessGuardPublicAll = federationGuardBool(false)
				wantItems, wantExtraCalls = 1, 0
			}
			cfg.QuotaSources = []config.QuotaSource{source}
			server := &Server{cfg: cfg, accessGuard: newAccessGuardQuotaCache(cfg), quotaSources: newAdditionalQuotaSources(cfg)}
			recorder := accessGuardRequest(server.Handler())
			response := decodeAccessGuardResponse(t, recorder)
			if recorder.Code != http.StatusOK || len(response.Items) != wantItems || mainCalls.Load() != 1 || extraCalls.Load() != wantExtraCalls {
				t.Fatalf("mode=%s status=%d items=%d main=%d extra=%d", mode, recorder.Code, len(response.Items), mainCalls.Load(), extraCalls.Load())
			}
			if mode == "secondary-list" && (response.Items[0].Name != "仅主实例" || response.Items[1].Name != "仅副实例") {
				t.Fatal("explicit same binding IDs crossed instance publication scopes")
			}
		})
	}
}
