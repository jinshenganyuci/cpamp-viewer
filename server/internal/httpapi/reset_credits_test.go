package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func resetCreditsPayload(t *testing.T, text string) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestResetCreditsProjectionDistinguishesUnknownZeroAndPaidUnlimited(t *testing.T) {
	for _, test := range []struct {
		name, usage, details string
		count                int64
		known, partial       bool
	}{
		{"missing", `{}`, `{}`, 0, false, true},
		{"paid unlimited is unrelated", `{"credits":{"unlimited":true,"balance":"PRIVATE_BALANCE"}}`, `{}`, 0, false, true},
		{"known zero", `{"rate_limit_reset_credits":{"available_count":0}}`, `{}`, 0, true, true},
		{"empty list", `{}`, `{"credits":[]}`, 0, true, false},
		{"usage fallback", `{"rate_limit_reset_credits":{"available_count":2}}`, `{}`, 2, true, true},
		{"detail zero supersedes usage", `{"rate_limit_reset_credits":{"available_count":3}}`, `{"available_count":0,"credits":[]}`, 0, true, false},
		{"invalid count", `{}`, `{"available_count":-1}`, 0, false, true},
		{"fractional count", `{}`, `{"available_count":1.5}`, 0, false, true},
		{"invalid list", `{}`, `{"credits":[null]}`, 0, false, true},
		{"unknown expiry", `{}`, `{"available_count":1,"credits":[{"reset_type":"codex_rate_limits","status":"available","expires_at":"invalid"}]}`, 1, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := projectResetCredits(resetCreditsPayload(t, test.usage), resetCreditsPayload(t, test.details), codexLiveTestTime.UnixMilli())
			if (got.AvailableCount != nil) != test.known || (test.known && *got.AvailableCount != test.count) || got.Partial != test.partial {
				t.Fatalf("unexpected reset credits: %#v", got)
			}
		})
	}
}

func TestResetCreditsProjectionFiltersAndSortsWithoutPublishingIdentifiers(t *testing.T) {
	details := resetCreditsPayload(t, `{"available_count":3,"credits":[
		{"id":"PRIVATE_LATE","reset_type":"codex_rate_limits","status":"available","expires_at":"2030-10-07T12:00:00Z","granted_at":"PRIVATE_GRANT"},
		{"id":"PRIVATE_EXPIRED","reset_type":"codex_rate_limits","status":"available","expires_at":"2030-09-09T12:00:00Z"},
		{"id":"PRIVATE_EARLY","reset_type":"codex_rate_limits","status":"available","expires_at":"2030-09-13T12:00:00Z"},
		{"id":"PRIVATE_USED","reset_type":"codex_rate_limits","status":"consumed","expires_at":"2030-10-09T12:00:00Z"},
		{"id":"PRIVATE_OTHER","reset_type":"other","status":"available","expires_at":"2030-10-09T12:00:00Z"}
	]}`)
	got := projectResetCredits(nil, details, codexLiveTestTime.UnixMilli())
	if got.AvailableCount == nil || *got.AvailableCount != 2 || got.Partial || len(got.Credits) != 2 || got.Credits[0].ExpiresAtMS >= got.Credits[1].ExpiresAtMS {
		t.Fatalf("bad expiry projection: %#v", got)
	}
	encoded, _ := json.Marshal(got)
	for _, private := range []string{"PRIVATE", "granted", "reset_type", `"id"`} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("private reset metadata leaked: %s", encoded)
		}
	}
	expired := currentResetCredits(got, codexLiveTestTime.Add(4*24*time.Hour).UnixMilli(), false)
	if *expired.AvailableCount != 1 || len(expired.Credits) != 1 || len(got.Credits) != 2 || *got.AvailableCount != 2 {
		t.Fatalf("cache expiry filtering mutated shared data: original=%#v filtered=%#v", got, expired)
	}
}

func TestResetCreditsChangedCountDoesNotReuseUnidentifiedOldCards(t *testing.T) {
	previous := projectResetCredits(nil, resetCreditsPayload(t, `{"available_count":2,"credits":[{"reset_type":"codex_rate_limits","status":"available","expires_at":"2030-09-13T12:00:00Z"},{"reset_type":"codex_rate_limits","status":"available","expires_at":"2030-10-07T12:00:00Z"}]}`), codexLiveTestTime.UnixMilli())
	fresh := projectResetCredits(resetCreditsPayload(t, `{"rate_limit_reset_credits":{"available_count":1}}`), nil, codexLiveTestTime.Add(time.Minute).UnixMilli())
	got := mergeResetCredits(fresh, previous, codexLiveTestTime.Add(time.Minute).UnixMilli())
	if got.AvailableCount == nil || *got.AvailableCount != 1 || len(got.Credits) != 0 || !got.Partial || got.Stale {
		t.Fatalf("changed balance reused obsolete card identities: %#v", got)
	}
}

func TestResetCreditsLiveFixedReadPartialFailureCooldownAndRecovery(t *testing.T) {
	var calls, elapsed atomic.Int64
	var failDetails, failUsage atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request struct {
			AuthIndex string            `json:"auth_index"`
			Method    string            `json:"method"`
			URL       string            `json:"url"`
			Header    map[string]string `json:"header"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v0/management/api-call" || request.Method != "GET" || request.AuthIndex != "trusted-auth" || request.Header["Authorization"] != "Bearer $TOKEN$" || request.Header["Chatgpt-Account-Id"] != "trusted-account" {
			t.Errorf("unexpected reset read: %#v", request)
		}
		switch request.URL {
		case codexUsageURL:
			if failUsage.Load() {
				codexLiveTestReply(w, http.StatusServiceUnavailable, `{"error":"PRIVATE_USAGE_ERROR"}`)
				return
			}
			body := resetCreditsPayload(t, codexLiveTestBody(25))
			body["rate_limit_reset_credits"] = map[string]any{"available_count": 1}
			encoded, _ := json.Marshal(body)
			codexLiveTestReply(w, http.StatusOK, string(encoded))
		case codexResetCreditsURL:
			if request.Header["OpenAI-Beta"] != "codex-1" || request.Header["Originator"] != "Codex Desktop" {
				t.Error("reset read missing official headers")
			}
			if failDetails.Load() {
				codexLiveTestReply(w, http.StatusForbidden, `{"error":"PRIVATE_DETAILS_ERROR"}`)
				return
			}
			codexLiveTestReply(w, http.StatusOK, `{"available_count":1,"credits":[{"id":"PRIVATE_CREDIT","reset_type":"codex_rate_limits","status":"available","expires_at":"2030-09-13T12:00:00Z"}]}`)
		default:
			t.Errorf("unexpected destination or consume operation: %q", request.URL)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer upstream.Close()
	cache := codexLiveTestCache(upstream.URL)
	cache.now = func() time.Time { return codexLiveTestTime.Add(time.Duration(elapsed.Load())) }
	file := codexLiveTestFile("trusted-auth", "trusted-account")
	first := codexLiveTestOne(t, cache, file)
	if !first.Valid || first.ResetCredits.Partial || *first.ResetCredits.AvailableCount != 1 || len(first.ResetCredits.Credits) != 1 || calls.Load() != 2 {
		t.Fatalf("unexpected combined inventory: %#v calls=%d", first, calls.Load())
	}
	failDetails.Store(true)
	elapsed.Store(int64(time.Minute))
	for i := 0; i < 3; i++ {
		partial := codexLiveTestOne(t, cache, file)
		if !partial.Valid || partial.Stale || !partial.ResetCredits.Partial || !partial.ResetCredits.Stale || *partial.ResetCredits.AvailableCount != 1 {
			t.Fatalf("detail failure corrupted usage or lost provenance: %#v", partial)
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("failure cooldown did not bound combined calls: %d", calls.Load())
	}
	failDetails.Store(false)
	failUsage.Store(true)
	elapsed.Store(int64(2 * time.Minute))
	recovered := codexLiveTestOne(t, cache, file)
	if !recovered.Stale || recovered.ResetCredits.Stale || recovered.ResetCredits.Partial || calls.Load() != 6 {
		t.Fatalf("independent reset recovery failed: %#v", recovered)
	}
	encoded, _ := json.Marshal(recovered)
	if strings.Contains(string(encoded), "PRIVATE") {
		t.Fatalf("upstream secret leaked: %s", encoded)
	}
}

func TestResetCreditsCombinedRefreshStaysWithinFourRequestsAndSharesCooldown(t *testing.T) {
	var calls, active, maximum atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for previous := maximum.Load(); n > previous; previous = maximum.Load() {
			if maximum.CompareAndSwap(previous, n) {
				break
			}
		}
		var request struct{ URL string }
		_ = json.NewDecoder(r.Body).Decode(&request)
		time.Sleep(2 * time.Millisecond)
		if request.URL == codexResetCreditsURL {
			codexLiveTestReply(w, http.StatusOK, `{"available_count":0,"credits":[]}`)
		} else {
			codexLiveTestReply(w, http.StatusOK, codexLiveTestBody(25))
		}
	}))
	defer upstream.Close()
	cache := codexLiveTestCache(upstream.URL)
	files := make([]map[string]any, 8)
	for i := range files {
		files[i] = codexLiveTestFile(string(rune('a'+i)), "account")
	}
	var visitors sync.WaitGroup
	for i := 0; i < 8; i++ {
		visitors.Add(1)
		go func() {
			defer visitors.Done()
			results := cache.snapshot(context.Background(), files)
			if len(results) != len(files) {
				t.Errorf("incomplete batch: %d", len(results))
			}
		}()
	}
	visitors.Wait()
	if maximum.Load() > 4 || calls.Load() != 16 {
		t.Fatalf("combined reads were not bounded and coalesced: max=%d calls=%d", maximum.Load(), calls.Load())
	}
}
