package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A partly consumed source page may hide newer rows than another source's
// remaining rows. Emit only the known globally ordered prefix before refilling.
func TestFederationReviewPartiallyConsumedPagesNeverOvertakeUnfetchedRows(t *testing.T) {
	server := &Server{cfg: testConfig()}
	sourceTimes := [][]int64{{100, 98, 97, 95}, {99, 96, 94, 93}}
	sources := []cpampReadSource{{ID: pseudonym("review-source-a"), Name: "A"}, {ID: pseudonym("review-source-b"), Name: "B"}}
	state := federationEventsCursor{Version: 1, Query: "review-query", FromMS: federationTestMS - 1000, ToMS: federationTestMS + 1000, Expires: time.Now().Add(time.Minute).Unix(), Positions: []federationPagePosition{{ID: sources[0].ID}, {ID: sources[1].ID}}}
	const limit = 2
	seen := map[string]bool{}
	previous := int64(1 << 62)
	shortPages := 0
	for pageNumber := 0; pageNumber < 20; pageNumber++ {
		results := make([]federationAnalyticsResult, len(sources))
		for i, source := range sources {
			position := state.Positions[i]
			result := &results[i]
			result.source = source
			result.position = position
			if position.Done {
				continue
			}
			remaining := []int64{}
			for _, offset := range sourceTimes[i] {
				stamp := federationTestMS + offset
				if position.BeforeMS == 0 || stamp < position.BeforeMS {
					remaining = append(remaining, stamp)
				}
			}
			result.hasMore = len(remaining) > limit
			if len(remaining) > limit {
				remaining = remaining[:limit]
			}
			result.pageRows = []any{}
			for j, stamp := range remaining {
				result.pageRows = append(result.pageRows, map[string]any{"event_hash": pseudonym(fmt.Sprintf("source-%d-%d", i, stamp)), "timestamp_ms": stamp, "instance_id": source.ID})
				if j == len(remaining)-1 {
					result.nextMS = stamp
					result.nextID = int64(j + 1)
				}
			}
			result.fingerprint = federationPageFingerprint(result.pageRows)
			result.response = map[string]any{"events": map[string]any{"total_count": len(sourceTimes[i])}}
		}
		events, err := server.mergeFederationEvents(results, &state, limit)
		if err != nil {
			t.Fatal(err)
		}
		rows := events["items"].([]any)
		if len(rows) < limit && events["has_more"] == true {
			shortPages++
		}
		for _, item := range rows {
			row := item.(map[string]any)
			stamp := intValue(row["timestamp_ms"])
			if stamp > previous {
				t.Fatalf("page %d emitted %d after older %d: a source's unconsumed page hid a newer row", pageNumber, stamp, previous)
			}
			previous = stamp
			id := stringValue(row["event_hash"])
			if seen[id] {
				t.Fatalf("duplicate event %s", id)
			}
			seen[id] = true
		}
		if events["has_more"] != true {
			if len(seen) != 8 {
				t.Fatalf("missing rows: %d", len(seen))
			}
			if shortPages == 0 {
				t.Fatal("fixture should need a conservative short page")
			}
			return
		}
	}
	t.Fatal("pagination did not finish")
}

func TestFederationReviewMalformedHasMoreDoesNotClaimCompletion(t *testing.T) {
	for _, malformed := range []any{nil, "true", 1} {
		server, _, _ := federationTestServer(t)
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v0/management/api-key-aliases" {
				writeJSON(w, 200, map[string]any{"items": []any{}})
				return
			}
			page := map[string]any{"items": []any{}, "total_count": 10}
			if malformed != nil {
				page["has_more"] = malformed
			}
			writeJSON(w, 200, map[string]any{"events": page})
		}))
		server.cfg.QuotaSources[0].BaseURL = upstream.URL
		server.quotaSources = newAdditionalQuotaSources(server.cfg)
		code, response, raw := federationTestRequest(t, server, federationTestPayload(map[string]any{"events_page": map[string]any{"limit": 2}}))
		upstream.Close()
		if code != 200 || response["partial_failed"] != true {
			t.Fatalf("malformed has_more %v silently treated as complete: %d %s", malformed, code, raw)
		}
	}
}

func TestFederationReviewOversizedUpstreamPageCannotSkipHiddenTail(t *testing.T) {
	server, _, _ := federationTestServer(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/management/api-key-aliases" {
			writeJSON(w, 200, map[string]any{"items": []any{}})
			return
		}
		rows := make([]any, 201)
		for i := range rows {
			rows[i] = map[string]any{"event_hash": fmt.Sprintf("hidden-tail-event-%d", i), "timestamp_ms": federationTestMS}
		}
		writeJSON(w, 200, map[string]any{"events": map[string]any{"items": rows, "has_more": true, "total_count": 202, "next_before_ms": federationTestMS, "next_before_id": 1}})
	}))
	defer upstream.Close()
	server.cfg.QuotaSources[0].BaseURL = upstream.URL
	server.quotaSources = newAdditionalQuotaSources(server.cfg)
	code, response, raw := federationTestRequest(t, server, federationTestPayload(map[string]any{"events_page": map[string]any{"limit": 200}}))
	if code != 200 || response["partial_failed"] != true {
		t.Fatalf("truncated tail cursor accepted: %d %s", code, raw)
	}
	page := response["events"].(map[string]any)
	if len(page["items"].([]any)) != 5 || intValue(page["total_count"]) != 5 {
		t.Fatalf("oversized source silently contributed skipped rows: %s", raw)
	}
}
