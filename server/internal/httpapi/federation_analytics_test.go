package httpapi

import (
	"bytes"
	"cpamp-viewer/server/internal/config"
	"cpamp-viewer/server/internal/cpamp"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const federationTestMS int64 = 1700000500000
const federationTestAccount = "same@example.test"
const federationTestHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type federationTestUpstream struct {
	server  *httptest.Server
	fail    atomic.Bool
	mutate  atomic.Bool
	mu      sync.Mutex
	queries []map[string]any
}

func newFederationTestUpstream(t *testing.T, key string, calls int) *federationTestUpstream {
	t.Helper()
	f := &federationTestUpstream{}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("mixed source credentials")
			http.Error(w, "PRIVATE_ADMIN", 401)
			return
		}
		if f.fail.Load() {
			http.Error(w, "PRIVATE_FAILURE_DIAGNOSTIC", 503)
			return
		}
		if r.URL.Path == "/v0/management/api-key-aliases" && r.Method == http.MethodGet {
			writeJSON(w, 200, map[string]any{"items": []any{map[string]any{"apiKeyHash": federationTestHash, "alias": "shared key"}}})
			return
		}
		if r.URL.Path != "/v0/management/monitoring/analytics" || r.Method != http.MethodPost {
			t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var p map[string]any
		d := json.NewDecoder(r.Body)
		d.UseNumber()
		if d.Decode(&p) != nil {
			t.Error("bad request")
			return
		}
		f.mu.Lock()
		f.queries = append(f.queries, cloneFederationPayload(p))
		f.mu.Unlock()
		inc, _ := p["include"].(map[string]any)
		response := map[string]any{"generated_at_ms": federationTestMS, "granularity": "hour", "PRIVATE_EXTRA": "PRIVATE_SECRET"}
		account := map[string]any{"id": "PRIVATE_ACCOUNT_GROUP", "account_snapshot": federationTestAccount, "auth_label_snapshot": "same label", "auth_indices": []any{"PRIVATE_AUTH_INDEX"}, "source_hashes": []any{"PRIVATE_SOURCE_HASH"}, "auth_provider_snapshot": "codex"}
		keyrow := map[string]any{"id": "PRIVATE_KEY_GROUP", "api_key_hash": federationTestHash, "calls": calls, "api_key": "PRIVATE_KEY_BODY"}
		if inc["filter_options"] == true || inc["filter_selectors"] == true {
			options := map[string]any{"account_stats": []any{account}, "api_key_stats": []any{keyrow}, "api_key_hashes": []any{federationTestHash}, "auth_files": []any{"PRIVATE_AUTH_FILE"}, "project_ids": []any{"PRIVATE_PROJECT"}, "models": []any{"model"}, "providers": []any{"codex"}}
			response["filter_options"] = options
			response["filter_selectors"] = options
		}
		if inc["credential_stats"] == true {
			response["credential_stats"] = []any{map[string]any{"id": "PRIVATE_CREDENTIAL_GROUP", "auth_file_snapshot": "PRIVATE_AUTH_FILE", "account_snapshot": federationTestAccount, "calls": calls}}
		}
		if inc["account_stats"] == true {
			row := cloneFederationPayload(account)
			row["calls"] = calls
			response["account_stats"] = []any{row}
		}
		if inc["api_key_stats"] == true {
			response["api_key_stats"] = []any{keyrow}
		}
		if inc["summary"] == true {
			response["summary"] = map[string]any{"total_calls": calls, "success_calls": calls - 1, "failure_calls": 1, "success_rate": float64(calls-1) / float64(calls), "total_tokens": calls * 10, "total_cost": float64(calls) / 10, "average_latency_ms": calls * 100}
		}
		if page, ok := inc["events_page"].(map[string]any); ok {
			ms, id, limit := intValue(page["before_ms"]), intValue(page["before_id"]), int(intValue(page["limit"]))
			ids := []int{1, 2, 3, 4, 5}
			if f.mutate.Load() {
				ids = append(ids, 6)
			}
			sort.Sort(sort.Reverse(sort.IntSlice(ids)))
			matching := []int{}
			for _, v := range ids {
				stamp := federationTestMS + int64(v/2)
				if ms > 0 && (stamp > ms || stamp == ms && int64(v) >= id) {
					continue
				}
				matching = append(matching, v)
			}
			more := len(matching) > limit
			if more {
				matching = matching[:limit]
			}
			rows := []any{}
			for _, v := range matching {
				rows = append(rows, map[string]any{"event_hash": fmt.Sprintf("PRIVATE_EVENT_%d", v), "timestamp_ms": federationTestMS + int64(v/2), "api_key_hash": federationTestHash, "account_snapshot": federationTestAccount, "source_hash": "PRIVATE_SOURCE_HASH", "auth_index": "PRIVATE_AUTH_INDEX", "model": "model", "input_tokens": v, "session_id": "PRIVATE_SESSION", "client_ip": "PRIVATE_IP", "authorization": "PRIVATE_AUTH"})
			}
			result := map[string]any{"items": rows, "has_more": more, "total_count": len(ids), "next_cursor": strings.Repeat("X", 70)}
			if more {
				last := matching[len(matching)-1]
				result["next_before_ms"] = federationTestMS + int64(last/2)
				result["next_before_id"] = last
			}
			response["events"] = result
		}
		writeJSON(w, 200, response)
	}))
	t.Cleanup(f.server.Close)
	return f
}
func federationTestServer(t *testing.T) (*Server, *federationTestUpstream, *federationTestUpstream) {
	t.Helper()
	a, b := newFederationTestUpstream(t, "PRIVATE_ADMIN_A", 3), newFederationTestUpstream(t, "PRIVATE_ADMIN_B", 7)
	cfg := testConfig()
	cfg.CPAMPBaseURL = a.server.URL
	cfg.CPAMPAdminKey = "PRIVATE_ADMIN_A"
	cfg.RequestTimeout = time.Second
	cfg.MaxUpstreamBodyBytes = 1 << 20
	cfg.QuotaSources = []config.QuotaSource{{Type: "cpamp", Name: "Second CPAMP", BaseURL: b.server.URL, AdminKey: "PRIVATE_ADMIN_B"}}
	return &Server{cfg: cfg, cpamp: cpamp.New(cfg.CPAMPBaseURL, cfg.CPAMPAdminKey, cfg.RequestTimeout, cfg.MaxUpstreamBodyBytes), logger: slog.Default(), analyticsSem: make(chan struct{}, 4), quotaSources: newAdditionalQuotaSources(cfg)}, a, b
}
func federationTestPayload(include map[string]any) map[string]any {
	return map[string]any{"from_ms": federationTestMS - 1000, "to_ms": federationTestMS + 1000, "now_ms": federationTestMS + 1000, "include": include}
}
func federationTestRequest(t *testing.T, s *Server, p map[string]any) (int, map[string]any, string) {
	t.Helper()
	data, _ := json.Marshal(p)
	w := httptest.NewRecorder()
	s.handleAnalytics(w, httptest.NewRequest(http.MethodPost, "/viewer/api/v1/analytics", bytes.NewReader(data)))
	var result map[string]any
	d := json.NewDecoder(bytes.NewReader(w.Body.Bytes()))
	d.UseNumber()
	if d.Decode(&result) != nil {
		t.Fatal(w.Body.String())
	}
	return w.Code, result, w.Body.String()
}
func TestFederationAnalyticsSeparatesIdenticalSourceIdentities(t *testing.T) {
	s, _, _ := federationTestServer(t)
	p := federationTestPayload(map[string]any{"summary": true, "account_stats": true, "api_key_stats": true, "filter_options": true, "filter_selectors": true})
	code, result, raw := federationTestRequest(t, s, p)
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	accounts, keys := result["account_stats"].([]any), result["api_key_stats"].([]any)
	if len(accounts) != 2 || len(keys) != 2 {
		t.Fatal(raw)
	}
	a, b := accounts[0].(map[string]any), accounts[1].(map[string]any)
	if a["account_id"] == b["account_id"] || a["id"] == b["id"] || a["instance_id"] == b["instance_id"] {
		t.Fatalf("identities collide %s", raw)
	}
	if keys[0].(map[string]any)["api_key_id"] == keys[1].(map[string]any)["api_key_id"] {
		t.Fatal("API key identities collide")
	}
	if intValue(result["summary"].(map[string]any)["total_calls"]) != 10 {
		t.Fatal(raw)
	}
	for _, private := range []string{"PRIVATE_KEY_BODY", "PRIVATE_EXTRA", "PRIVATE_SECRET", federationTestHash, s.cfg.CPAMPBaseURL, s.cfg.QuotaSources[0].BaseURL} {
		if strings.Contains(raw, private) {
			t.Fatalf("private value escaped: %s", private)
		}
	}
}
func TestFederationAnalyticsIdentityFiltersNeverCrossSources(t *testing.T) {
	for _, f := range []struct{ field, raw string }{{"accounts", federationTestAccount}, {"api_key_ids", federationTestHash}, {"auth_indices", "PRIVATE_AUTH_INDEX"}, {"source_hashes", "PRIVATE_SOURCE_HASH"}, {"auth_files", "PRIVATE_AUTH_FILE"}, {"project_ids", "PRIVATE_PROJECT"}, {"credential_ids", "PRIVATE_CREDENTIAL_GROUP"}} {
		t.Run(f.field, func(t *testing.T) {
			s, a, b := federationTestServer(t)
			source := s.cpampReadSources()[1]
			selected := federationViewID(source.ID, pseudonym(f.raw))
			p := federationTestPayload(map[string]any{"summary": true})
			p["filters"] = map[string]any{f.field: []any{selected}}
			code, r, raw := federationTestRequest(t, s, p)
			if code != 200 {
				t.Fatalf("%d %s", code, raw)
			}
			if intValue(r["summary"].(map[string]any)["total_calls"]) != 7 {
				t.Fatalf("wrong source %s", raw)
			}
			a.mu.Lock()
			defer a.mu.Unlock()
			for _, q := range a.queries {
				if q["include"].(map[string]any)["summary"] == true {
					t.Fatalf("nonmatching source queried: %#v", q)
				}
			}
			b.mu.Lock()
			defer b.mu.Unlock()
			found := false
			for _, q := range b.queries {
				if q["include"].(map[string]any)["summary"] == true {
					found = true
					target := f.field
					if target == "api_key_ids" {
						target = "api_key_hashes"
					}
					values := q["filters"].(map[string]any)[target].([]any)
					if len(values) != 1 || values[0] != f.raw {
						t.Fatalf("wrong raw identity %#v", q)
					}
				}
			}
			if !found {
				t.Fatal("selected source not queried")
			}
		})
	}
}
func TestFederationAnalyticsCombinedFiltersRequireSameInstance(t *testing.T) {
	s, a, b := federationTestServer(t)
	sources := s.cpampReadSources()
	p := federationTestPayload(map[string]any{"summary": true})
	p["filters"] = map[string]any{"accounts": []any{federationViewID(sources[0].ID, pseudonym(federationTestAccount))}, "api_key_ids": []any{federationViewID(sources[1].ID, pseudonym(federationTestHash))}}
	code, _, raw := federationTestRequest(t, s, p)
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	for _, f := range []*federationTestUpstream{a, b} {
		f.mu.Lock()
		for _, q := range f.queries {
			if q["include"].(map[string]any)["summary"] == true {
				t.Errorf("mixed identity filters broadened request: %#v", q)
			}
		}
		f.mu.Unlock()
	}
}
func TestFederationAnalyticsGlobalPaginationHasNoGapsOrDuplicates(t *testing.T) {
	s, _, _ := federationTestServer(t)
	p := federationTestPayload(map[string]any{"events_page": map[string]any{"limit": 2}})
	seen := map[string]bool{}
	last := int64(1 << 62)
	pages := 0
	for {
		code, r, raw := federationTestRequest(t, s, p)
		if code != 200 {
			t.Fatalf("page %d: %d %s", pages, code, raw)
		}
		page := r["events"].(map[string]any)
		if intValue(page["total_count"]) != 10 {
			t.Fatalf("total drifted %s", raw)
		}
		for _, item := range page["items"].([]any) {
			row := item.(map[string]any)
			id := stringValue(row["event_hash"])
			if seen[id] {
				t.Fatalf("duplicate %s", id)
			}
			seen[id] = true
			stamp := intValue(row["timestamp_ms"])
			if stamp > last {
				t.Fatal("global order violated")
			}
			last = stamp
		}
		pages++
		if pages > 20 {
			t.Fatal("did not terminate")
		}
		if page["has_more"] != true {
			break
		}
		token := stringValue(page["next_cursor"])
		sealed, _ := base64.RawURLEncoding.DecodeString(token)
		if token == "" || bytes.Contains(sealed, []byte("PRIVATE_")) || bytes.Contains(sealed, []byte(`"i":`)) {
			t.Fatal("cursor not encrypted")
		}
		p["include"].(map[string]any)["events_page"].(map[string]any)["cursor"] = token
		p["now_ms"] = federationTestMS + 2000
		p["to_ms"] = federationTestMS + 2000
	}
	if len(seen) != 10 {
		t.Fatalf("lost rows: %d in %d pages", len(seen), pages)
	}
}
func TestFederationAnalyticsCursorRejectsTamperingAndFilterReuse(t *testing.T) {
	s, _, _ := federationTestServer(t)
	p := federationTestPayload(map[string]any{"events_page": map[string]any{"limit": 2}})
	_, r, _ := federationTestRequest(t, s, p)
	token := stringValue(r["events"].(map[string]any)["next_cursor"])
	changed := cloneFederationPayload(p)
	changed["include"].(map[string]any)["events_page"].(map[string]any)["cursor"] = token
	changed["filters"] = map[string]any{"models": []any{"different"}}
	if code, _, raw := federationTestRequest(t, s, changed); code != 400 {
		t.Fatalf("changed filter accepted %d %s", code, raw)
	}
	tampered := []byte(token)
	if tampered[15] == 'A' {
		tampered[15] = 'B'
	} else {
		tampered[15] = 'A'
	}
	p["include"].(map[string]any)["events_page"].(map[string]any)["cursor"] = string(tampered)
	if code, _, raw := federationTestRequest(t, s, p); code != 400 {
		t.Fatalf("tampered cursor accepted %d %s", code, raw)
	}
}
func TestFederationAnalyticsPartialFailurePreservesHealthyData(t *testing.T) {
	s, _, b := federationTestServer(t)
	b.fail.Store(true)
	code, r, raw := federationTestRequest(t, s, federationTestPayload(map[string]any{"summary": true, "account_stats": true}))
	if code != 200 || r["partial_failed"] != true || intValue(r["summary"].(map[string]any)["total_calls"]) != 3 || len(r["warnings"].([]any)) == 0 {
		t.Fatalf("lost good data %d %s", code, raw)
	}
	if strings.Contains(raw, "PRIVATE_FAILURE") {
		t.Fatal("diagnostics escaped")
	}
}
func TestFederationAnalyticsPaginationPausesOnSourceFailure(t *testing.T) {
	s, _, b := federationTestServer(t)
	p := federationTestPayload(map[string]any{"events_page": map[string]any{"limit": 2}, "summary": true})
	_, r, _ := federationTestRequest(t, s, p)
	token := stringValue(r["events"].(map[string]any)["next_cursor"])
	p["include"].(map[string]any)["events_page"].(map[string]any)["cursor"] = token
	b.fail.Store(true)
	code, r, raw := federationTestRequest(t, s, p)
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	page := r["events"].(map[string]any)
	if len(page["items"].([]any)) != 0 || page["next_cursor"] != token || r["partial_failed"] != true {
		t.Fatalf("advanced on failure %s", raw)
	}
	b.fail.Store(false)
	code, r, raw = federationTestRequest(t, s, p)
	if code != 200 || len(r["events"].(map[string]any)["items"].([]any)) != 2 {
		t.Fatalf("bad resume %d %s", code, raw)
	}
}
func TestFederationAnalyticsChangedPartialUpstreamPageRequiresRefresh(t *testing.T) {
	s, a, b := federationTestServer(t)
	p := federationTestPayload(map[string]any{"events_page": map[string]any{"limit": 1}})
	_, r, _ := federationTestRequest(t, s, p)
	token := stringValue(r["events"].(map[string]any)["next_cursor"])
	state, err := s.decodeFederationCursor(token)
	if err != nil {
		t.Fatal(err)
	}
	index := -1
	for i, position := range state.Positions {
		if position.Fingerprint != "" {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatal("missing partial page checkpoint")
	}
	if index == 0 {
		a.mutate.Store(true)
	} else {
		b.mutate.Store(true)
	}
	p["include"].(map[string]any)["events_page"].(map[string]any)["cursor"] = token
	if code, _, raw := federationTestRequest(t, s, p); code != 409 {
		t.Fatalf("page silently changed %d %s", code, raw)
	}
}

func TestFederationAnalyticsMultipleSelectedAccountsUnionAndUnknownID(t *testing.T) {
	server, _, _ := federationTestServer(t)
	sources := server.cpampReadSources()
	payload := federationTestPayload(map[string]any{"summary": true})
	payload["filters"] = map[string]any{"accounts": []any{federationViewID(sources[0].ID, pseudonym(federationTestAccount)), federationViewID(sources[1].ID, pseudonym(federationTestAccount))}}
	code, response, raw := federationTestRequest(t, server, payload)
	if code != 200 || intValue(response["summary"].(map[string]any)["total_calls"]) != 10 {
		t.Fatalf("multi-select did not union within a filter %d %s", code, raw)
	}
	payload["filters"] = map[string]any{"accounts": []any{pseudonym("unknown-public-identity")}}
	if code, _, raw := federationTestRequest(t, server, payload); code != 400 {
		t.Fatalf("unknown identity accepted: %d %s", code, raw)
	}
	payload["filters"] = map[string]any{"accounts": []any{federationTestAccount}}
	if code, _, raw := federationTestRequest(t, server, payload); code != 400 {
		t.Fatalf("raw identity accepted: %d %s", code, raw)
	}
}

func TestFederationCursorSupportsMaximumSourceCountAndIsDomainSeparated(t *testing.T) {
	server := &Server{cfg: testConfig()}
	state := federationEventsCursor{Version: 1, Query: strings.Repeat("a", 64), FromMS: federationTestMS - 1000, ToMS: federationTestMS + 1000, Expires: time.Now().Add(time.Minute).Unix()}
	for i := 0; i < 16; i++ {
		state.Positions = append(state.Positions, federationPagePosition{ID: pseudonym(fmt.Sprintf("instance-%d", i)), BeforeMS: federationTestMS, BeforeID: 999, Offset: 190, Fingerprint: strings.Repeat("b", 64), Count: 50000})
	}
	token, err := server.encodeFederationCursor(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) <= 512 || len(token) > maxFederationCursorBytes {
		t.Fatalf("unexpected cursor length %d", len(token))
	}
	decoded, err := server.decodeFederationCursor(token)
	if err != nil || len(decoded.Positions) != 16 {
		t.Fatalf("max source cursor rejected: %v", err)
	}
	if _, err := server.decodeEventsCursor(token); err == nil {
		t.Fatal("federated cursor accepted by legacy cursor decoder")
	}
	local := server.encodeEventsCursor(eventsCursor{BeforeMS: federationTestMS, BeforeID: 999})
	if _, err := server.decodeFederationCursor(local); err == nil {
		t.Fatal("legacy cursor accepted by federated decoder")
	}
	state.Expires = time.Now().Add(-time.Minute).Unix()
	expired, _ := server.encodeFederationCursor(state)
	if _, err := server.decodeFederationCursor(expired); err == nil {
		t.Fatal("expired cursor accepted")
	}
}

func TestFederationAnalyticsEventProjectionRejectsPrivateMetadata(t *testing.T) {
	server, _, _ := federationTestServer(t)
	code, _, raw := federationTestRequest(t, server, federationTestPayload(map[string]any{"events_page": map[string]any{"limit": 2}}))
	if code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	for _, private := range []string{"PRIVATE_", federationTestHash, "session_id", "client_ip", "authorization", "next_before_id", strings.Repeat("X", 70)} {
		if strings.Contains(raw, private) {
			t.Fatalf("event leaked %s: %s", private, raw)
		}
	}
}
