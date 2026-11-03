package httpapi

import (
	"context"
	"cpamp-viewer/server/internal/config"
	"cpamp-viewer/server/internal/cpamp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFederatedAliasesKeepSameKeyDistinctAndFailuresSafe(t *testing.T) {
	var failed atomic.Bool
	hash := sha256.Sum256([]byte("PRIVATE_CALLER_KEY"))
	rawHash := hex.EncodeToString(hash[:])
	makeUpstream := func(key string, second bool) *httptest.Server {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+key {
				t.Error("unexpected read or mixed credential")
			}
			if second && failed.Load() {
				http.Error(w, "PRIVATE_UPSTREAM_ERROR", 503)
				return
			}
			switch r.URL.Path {
			case "/v0/management/api-key-aliases":
				writeJSON(w, 200, map[string]any{"items": []any{map[string]any{"apiKeyHash": rawHash, "alias": "同名密钥"}, map[string]any{"apiKeyHash": strings.Repeat("b", 64), "alias": "已删除密钥"}}})
			case "/v0/management/api-keys":
				writeJSON(w, 200, map[string]any{"api-keys": []string{"PRIVATE_CALLER_KEY"}})
			default:
				t.Errorf("unexpected read path %s", r.URL.Path)
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(upstream.Close)
		return upstream
	}
	main, extra := makeUpstream("PRIVATE_MAIN", false), makeUpstream("PRIVATE_EXTRA", true)
	cfg := config.Config{CPAMPBaseURL: main.URL, CPAMPAdminKey: "PRIVATE_MAIN", RequestTimeout: time.Second, MaxUpstreamBodyBytes: 1 << 20, QuotaSources: []config.QuotaSource{{Type: "cpamp", Name: "第二实例", BaseURL: extra.URL, AdminKey: "PRIVATE_EXTRA"}}}
	s := &Server{cfg: cfg, cpamp: cpamp.New(main.URL, cfg.CPAMPAdminKey, time.Second, 1<<20), logger: slog.New(slog.NewTextHandler(io.Discard, nil)), quotaSources: newAdditionalQuotaSources(cfg)}
	read := func() map[string]any {
		w := httptest.NewRecorder()
		s.handleAliases(w, httptest.NewRequest(http.MethodGet, "/api/viewer/aliases", nil))
		if w.Code != 200 {
			t.Fatalf("status %d", w.Code)
		}
		if strings.Contains(w.Body.String(), "PRIVATE") || strings.Contains(w.Body.String(), rawHash) || strings.Contains(w.Body.String(), "已删除") {
			t.Fatal("private or inactive aliases leaked")
		}
		var value map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	response := read()
	items := response["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items=%v", items)
	}
	a, b := items[0].(map[string]any), items[1].(map[string]any)
	if a["id"] == b["id"] || a["instance_id"] == b["instance_id"] || a["alias"] != b["alias"] {
		t.Fatal("instances collapsed")
	}
	for _, item := range items {
		v := item.(map[string]any)
		expected := federationViewID(v["instance_id"].(string), pseudonym(rawHash))
		if v["id"] != expected {
			t.Fatal("alias and analytics IDs differ")
		}
	}
	failed.Store(true)
	partial := read()
	if partial["partial_failed"] != true || len(partial["items"].([]any)) != 1 || len(partial["warnings"].([]any)) != 1 {
		t.Fatal("healthy aliases lost on partial failure")
	}
}
func TestCaptureFederationReadDoesNotInvokeCanceledHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := captureFederationRead(ctx, httptest.NewRequest(http.MethodGet, "/", nil), func(http.ResponseWriter, *http.Request) { called = true }, &map[string]any{})
	if err == nil || called {
		t.Fatal("canceled read invoked handler")
	}
}
func TestFederationSourceIDNormalizesEquivalentURLs(t *testing.T) {
	if federationSourceID("HTTPS://EXAMPLE.TEST:443/path/") != federationSourceID("https://example.test/path") {
		t.Fatal("equivalent source URLs have different identity")
	}
	if federationSourceID("https://example.test/path") == federationSourceID("https://example.test/other") {
		t.Fatal("distinct source paths collided")
	}
}
