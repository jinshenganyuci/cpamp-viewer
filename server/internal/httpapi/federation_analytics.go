package httpapi

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const federationCursorAAD = "cpamp-viewer/federated-events/v1"
const maxFederationCursorBytes = 12288

// Each checkpoint refers to an upstream page, not a globally shared database ID.
// Offset and fingerprint preserve unconsumed rows when several instances return
// rows at the same timestamp and the upstream does not expose row IDs.
type federationPagePosition struct {
	ID          string `json:"s"`
	BeforeMS    int64  `json:"m,omitempty"`
	BeforeID    int64  `json:"i,omitempty"`
	Offset      int    `json:"o,omitempty"`
	Fingerprint string `json:"h,omitempty"`
	Done        bool   `json:"d,omitempty"`
	Count       int64  `json:"c,omitempty"`
	Excluded    bool   `json:"x,omitempty"`
}
type federationEventsCursor struct {
	Version   int                      `json:"v"`
	Query     string                   `json:"q"`
	FromMS    int64                    `json:"f"`
	ToMS      int64                    `json:"t"`
	NowMS     int64                    `json:"n"`
	Expires   int64                    `json:"e"`
	Positions []federationPagePosition `json:"p"`
}
type federationAnalyticsResult struct {
	source      cpampReadSource
	aliases     apiKeyAliasEnvelope
	index       analyticsIdentityIndex
	payload     map[string]any
	response    map[string]any
	err         error
	skip        bool
	position    federationPagePosition
	nextMS      int64
	nextID      int64
	hasMore     bool
	pageRows    []any
	fingerprint string
}

func (s *Server) handleAnalytics(w http.ResponseWriter, r *http.Request) {
	sources := s.cpampReadSources()
	if len(sources) <= 1 {
		s.handleSingleAnalytics(w, r)
		return
	}
	select {
	case s.analyticsSem <- struct{}{}:
		defer func() { <-s.analyticsSem }()
	default:
		writeError(w, http.StatusTooManyRequests, "too many analytics requests")
		return
	}
	var payload map[string]any
	if err := decodeJSON(r, &payload, 512<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid analytics request")
		return
	}
	// Only this handler accepts federated cursors. The established single-source
	// cursor validation remains unchanged and never accepts federation state.
	token := ""
	if include, ok := payload["include"].(map[string]any); ok {
		if page, ok := include["events_page"].(map[string]any); ok {
			if value, exists := page["cursor"]; exists {
				var valid bool
				token, valid = value.(string)
				if !valid || token == "" || len(token) > maxFederationCursorBytes {
					writeError(w, 400, "invalid events page cursor")
					return
				}
				delete(page, "cursor")
			}
		}
	}
	if err := s.validateAnalytics(payload); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	query := federationAnalyticsQueryHash(payload, sources)
	state := federationEventsCursor{Version: 1, Query: query, FromMS: intValue(payload["from_ms"]), ToMS: intValue(payload["to_ms"]), NowMS: intValue(payload["now_ms"]), Expires: time.Now().Add(20 * time.Minute).Unix()}
	if token != "" {
		var err error
		state, err = s.decodeFederationCursor(token)
		if err != nil || state.Query != query || len(state.Positions) != len(sources) {
			writeError(w, 400, "invalid events page cursor")
			return
		}
		for i, source := range sources {
			if state.Positions[i].ID != source.ID {
				writeError(w, 400, "invalid events page cursor")
				return
			}
		}
		// A cursor freezes the original range. UI load-more requests may have a new
		// current time; they must not introduce newly arrived head rows mid-page.
		payload["from_ms"], payload["to_ms"], payload["now_ms"] = state.FromMS, state.ToMS, state.NowMS
	} else {
		for _, source := range sources {
			state.Positions = append(state.Positions, federationPagePosition{ID: source.ID})
		}
	}
	results := make([]federationAnalyticsResult, len(sources))
	federationParallel(r.Context(), len(sources), func(i int) {
		result := &results[i]
		result.source = sources[i]
		result.position = state.Positions[i]
		aliases, aliasErr := sources[i].Reader.loadAllAliases(r.Context())
		if aliasErr == nil {
			result.aliases = aliases
		}
		result.index, result.err = sources[i].Reader.loadAnalyticsIdentityIndex(r.Context(), payload, result.aliases)
	})
	// Convert global opaque IDs into only this instance's authenticated upstream
	// identities. A nonmatching nonempty filter excludes the entire source; it
	// must never become an unfiltered upstream request.
	if err := resolveFederationAnalyticsFilters(payload, results); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	limit := federationRequestedPageLimit(payload)
	federationParallel(r.Context(), len(results), func(i int) {
		result := &results[i]
		if result.err != nil || result.skip {
			return
		}
		request := result.payload
		if limit > 0 {
			include := request["include"].(map[string]any)
			if result.position.Done {
				delete(include, "events_page")
			} else {
				page := include["events_page"].(map[string]any)
				if result.position.BeforeMS > 0 {
					page["before_ms"] = result.position.BeforeMS
					page["before_id"] = result.position.BeforeID
				}
			}
		}
		var response map[string]any
		if err := result.source.Reader.cpamp.PostJSON(r.Context(), "/v0/management/monitoring/analytics", request, &response); err != nil {
			result.err = err
			return
		}
		if response == nil {
			result.err = errors.New("invalid upstream analytics response")
			return
		}
		if limit > 0 && !result.position.Done {
			page, ok := response["events"].(map[string]any)
			if !ok {
				result.err = errors.New("missing upstream event page")
				return
			}
			var hasMoreOK bool
			result.hasMore, hasMoreOK = page["has_more"].(bool)
			if items, itemsOK := page["items"].([]any); !itemsOK || !hasMoreOK || len(items) > limit {
				result.err = errors.New("invalid upstream event page")
				return
			}
			if _, countOK := publicUsageInteger(page["total_count"]); !countOK {
				result.err = errors.New("invalid upstream event count")
				return
			}
			if result.hasMore {
				var msOK, idOK bool
				result.nextMS, msOK = strictInt64(page["next_before_ms"])
				result.nextID, idOK = strictInt64(page["next_before_id"])
				if !msOK || !idOK {
					result.err = errors.New("invalid upstream event cursor")
					return
				}
			}
		}
		// Never carry an upstream-provided opaque cursor across the public boundary.
		if page, ok := response["events"].(map[string]any); ok {
			delete(page, "next_cursor")
		}
		if page, ok := response["drilldown_preview"].(map[string]any); ok {
			delete(page, "next_cursor")
		}
		attachAnalyticsAliases(response, result.aliases)
		projectAnalyticsWithLimit(response, s.analyticsEventsPageLimit())
		namespaceFederationAnalytics(response, result.source)
		result.response = response
		if page, ok := response["events"].(map[string]any); ok {
			result.pageRows, _ = page["items"].([]any)
			result.fingerprint = federationPageFingerprint(result.pageRows)
			previousStamp := int64(1<<63 - 1)
			for _, item := range result.pageRows {
				row, ok := item.(map[string]any)
				stamp, stampOK := strictInt64(row["timestamp_ms"])
				if !ok || !apiKeyViewIDPattern.MatchString(stringValue(row["event_hash"])) || !stampOK || stamp <= 0 || stamp > previousStamp {
					result.err = errors.New("invalid upstream event order")
					return
				}
				previousStamp = stamp
			}
			if len(result.pageRows) > limit && limit > 0 {
				result.err = errors.New("invalid upstream event page")
				return
			}
			if result.position.Offset > len(result.pageRows) || result.position.Fingerprint != "" && result.position.Fingerprint != result.fingerprint {
				result.err = errFederationPageChanged
				return
			}
			if result.hasMore && (result.nextMS <= 0 || result.nextID <= 0 || len(result.pageRows) == 0) {
				result.err = errors.New("invalid upstream event cursor")
			}
			if result.hasMore && (result.nextMS != previousStamp || result.position.BeforeMS > 0 && (result.nextMS > result.position.BeforeMS || result.nextMS == result.position.BeforeMS && result.nextID >= result.position.BeforeID)) {
				result.err = errors.New("upstream event cursor did not advance")
			}
		} else if limit > 0 && !result.position.Done {
			result.err = errors.New("missing upstream event page")
		}
	})
	safeResponses := []map[string]any{}
	warnings := []string{}
	failed := false
	pageUnavailable := false
	for i := range results {
		result := &results[i]
		if errors.Is(result.err, errFederationPageChanged) {
			writeError(w, http.StatusConflict, "event history changed; refresh before loading more")
			return
		}
		if result.err != nil {
			failed = true
			warnings = append(warnings, result.source.Name+" 数据暂不可用")
			if limit > 0 && token != "" && !result.position.Done {
				pageUnavailable = true
			}
			if token == "" {
				state.Positions[i].Done = true
				state.Positions[i].Excluded = true
			}
			continue
		}
		if result.skip {
			state.Positions[i].Done = true
			continue
		}
		safeResponses = append(safeResponses, result.response)
	}
	if len(safeResponses) == 0 && failed {
		writeError(w, http.StatusBadGateway, "CPAMP is unavailable")
		return
	}
	response := mergeFederatedAnalytics(safeResponses)
	if limit > 0 {
		if pageUnavailable {
			// Keep summary data from healthy instances, but never advance a globally
			// ordered page past a temporarily unavailable source.
			total := int64(0)
			for _, position := range state.Positions {
				total += position.Count
			}
			response["events"] = map[string]any{"items": []any{}, "has_more": true, "next_cursor": token, "total_count": total}
		} else {
			events, err := s.mergeFederationEvents(results, &state, limit)
			if err != nil {
				writeError(w, http.StatusBadGateway, "CPAMP event pagination is unavailable")
				return
			}
			response["events"] = events
		}
	}
	mergeFederationPreview(response, safeResponses, payload)
	if include, ok := payload["include"].(map[string]any); ok {
		if max, exists := include["recent_failures"]; exists {
			if rows, ok := response["recent_failures"].([]any); ok && len(rows) > int(intValue(max)) {
				response["recent_failures"] = rows[:int(intValue(max))]
			}
		}
	}
	for _, position := range state.Positions {
		if position.Excluded && token != "" {
			warnings = append(warnings, "部分实例未参与本轮事件分页，请刷新后重试")
			failed = true
			break
		}
	}
	if existing, ok := response["warnings"].([]string); ok {
		warnings = append(existing, warnings...)
	}
	response["warnings"] = warnings
	response["partial_failed"] = failed
	response["instances"] = federationAnalyticsInstances(results)
	writeJSON(w, http.StatusOK, response)
}

var errFederationPageChanged = errors.New("federated event page changed")

func federationParallel(ctx context.Context, n int, work func(int)) {
	var group sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i := 0; i < n; i++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
				work(index)
			case <-ctx.Done():
				work(index)
			}
		}(i)
	}
	group.Wait()
}

func cloneFederationPayload(value map[string]any) map[string]any {
	data, _ := json.Marshal(value)
	var copy map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	_ = decoder.Decode(&copy)
	return copy
}

func resolveFederationAnalyticsFilters(payload map[string]any, results []federationAnalyticsResult) error {
	filters, _ := payload["filters"].(map[string]any)
	matched := map[string]bool{}
	requested := map[string]bool{}
	anyFailed := false
	for i := range results {
		result := &results[i]
		result.payload = cloneFederationPayload(payload)
		if result.err != nil {
			anyFailed = true
			continue
		}
		destination, _ := result.payload["filters"].(map[string]any)
		for _, group := range analyticsIdentityFilterGroups {
			var values []any
			for _, field := range group.Sources {
				if value, ok := filters[field].([]any); ok && values == nil {
					values = value
				}
				delete(destination, field)
			}
			if len(values) == 0 {
				continue
			}
			resolved := []any{}
			for _, value := range values {
				candidate := strings.TrimSpace(stringValue(value))
				requested[group.Target+":"+candidate] = true
				for localID, raw := range result.index[group.Target] {
					if federationViewID(result.source.ID, localID) == candidate {
						resolved = append(resolved, raw)
						matched[group.Target+":"+candidate] = true
						break
					}
				}
			}
			if len(resolved) == 0 {
				result.skip = true
			} else {
				destination[group.Target] = resolved
			}
		}
	}
	if !anyFailed {
		for value := range requested {
			if !matched[value] {
				return errors.New("unknown Viewer identity for analytics filter")
			}
		}
	}
	return nil
}

// Namespace only already-projected identity fields. Credential source_id is an
// identity within the CPAMP instance, never the instance's own ID.
func namespaceFederationAnalytics(value any, source cpampReadSource) {
	switch typed := value.(type) {
	case map[string]any:
		entity := false
		for key, item := range typed {
			switch key {
			case "channel_share", "failure_sources", "account_stats", "credential_stats", "credential_timeline", "api_key_timeline", "api_key_stats", "task_buckets", "recent_failures", "anomaly_points":
				if rows, ok := item.([]any); ok {
					for _, value := range rows {
						if row, ok := value.(map[string]any); ok {
							row["instance_id"] = source.ID
							row["instance_name"] = source.Name
						}
					}
				}
			}
			switch key {
			case "id", "api_key_id", "event_hash", "bucket_key", "auth_id", "source_id", "account_id", "auth_label_id", "auth_file_id", "project_id", "account_subject_id":
				if local, ok := item.(string); ok && apiKeyViewIDPattern.MatchString(local) {
					typed[key] = federationViewID(source.ID, local)
					entity = true
				}
			case "api_key_ids", "auth_ids", "source_ids", "account_ids", "auth_file_ids", "project_ids":
				if list, ok := item.([]any); ok {
					for i, v := range list {
						if local, ok := v.(string); ok && apiKeyViewIDPattern.MatchString(local) {
							list[i] = federationViewID(source.ID, local)
						}
					}
				}
			}
			namespaceFederationAnalytics(item, source)
		}
		if entity || typed["timestamp_ms"] != nil || typed["auth_provider_snapshot"] != nil {
			typed["instance_id"] = source.ID
			typed["instance_name"] = source.Name
		}
	case []any:
		for _, item := range typed {
			namespaceFederationAnalytics(item, source)
		}
	}
}

func federationRequestedPageLimit(payload map[string]any) int {
	include, _ := payload["include"].(map[string]any)
	page, _ := include["events_page"].(map[string]any)
	return int(intValue(page["limit"]))
}

func federationAnalyticsQueryHash(payload map[string]any, sources []cpampReadSource) string {
	copy := cloneFederationPayload(payload)
	// Range/time are carried inside the authenticated cursor; filters and include
	// must still match. Updating a UI clock does not reset a paginated snapshot.
	delete(copy, "from_ms")
	delete(copy, "to_ms")
	delete(copy, "now_ms")
	ids := make([]string, len(sources))
	for i, source := range sources {
		ids[i] = source.ID
	}
	encoded, _ := json.Marshal([]any{copy, ids})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func (s *Server) encodeFederationCursor(cursor federationEventsCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	aead, err := s.eventsCursorAEAD()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = cryptorand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nil, nonce, data, []byte(federationCursorAAD))
	token := base64.RawURLEncoding.EncodeToString(append(nonce, sealed...))
	if len(token) > maxFederationCursorBytes {
		return "", errors.New("cursor too large")
	}
	return token, nil
}

func (s *Server) decodeFederationCursor(token string) (federationEventsCursor, error) {
	var cursor federationEventsCursor
	invalid := errors.New("invalid federated cursor")
	if len(token) < 40 || len(token) > maxFederationCursorBytes {
		return cursor, invalid
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return cursor, invalid
	}
	aead, err := s.eventsCursorAEAD()
	if err != nil || len(data) <= aead.NonceSize()+aead.Overhead() {
		return cursor, invalid
	}
	decoded, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], []byte(federationCursorAAD))
	if err != nil {
		return cursor, invalid
	}
	if json.Unmarshal(decoded, &cursor) != nil || cursor.Version != 1 || cursor.Expires < time.Now().Unix() || cursor.FromMS <= 0 || cursor.ToMS <= cursor.FromMS || cursor.ToMS-cursor.FromMS > s.cfg.MaxAnalyticsRange.Milliseconds() || len(cursor.Positions) == 0 || len(cursor.Positions) > 16 {
		return cursor, invalid
	}
	for _, position := range cursor.Positions {
		if !apiKeyViewIDPattern.MatchString(position.ID) || position.Offset < 0 || position.Offset > maxPublicEventsPage || position.BeforeMS < 0 || position.BeforeID < 0 || (position.BeforeMS == 0) != (position.BeforeID == 0) || len(position.Fingerprint) > 64 {
			return cursor, invalid
		}
	}
	return cursor, nil
}

func federationPageFingerprint(rows []any) string {
	identities := make([]any, 0, len(rows))
	for _, item := range rows {
		row, _ := item.(map[string]any)
		identities = append(identities, []any{row["event_hash"], row["timestamp_ms"]})
	}
	data, _ := json.Marshal(identities)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

type federationEventCandidate struct {
	row    map[string]any
	source int
	order  int
}

func (s *Server) mergeFederationEvents(results []federationAnalyticsResult, state *federationEventsCursor, limit int) (map[string]any, error) {
	candidates := []federationEventCandidate{}
	total := int64(0)
	for i, result := range results {
		if result.err != nil || result.skip || result.position.Done {
			continue
		}
		if container, ok := result.response["events"].(map[string]any); ok {
			state.Positions[i].Count = intValue(container["total_count"])
		}
		for j := result.position.Offset; j < len(result.pageRows); j++ {
			row, ok := result.pageRows[j].(map[string]any)
			if ok {
				candidates = append(candidates, federationEventCandidate{row: row, source: i, order: j})
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		am, bm := intValue(a.row["timestamp_ms"]), intValue(b.row["timestamp_ms"])
		if am != bm {
			return am > bm
		}
		if results[a.source].source.ID != results[b.source].source.ID {
			return results[a.source].source.ID < results[b.source].source.ID
		}
		return a.order < b.order
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	items := make([]any, 0, len(candidates))
	consumed := make([]int, len(results))
	for _, candidate := range candidates {
		items = append(items, candidate.row)
		consumed[candidate.source]++
		source := results[candidate.source]
		if source.hasMore && candidate.order == len(source.pageRows)-1 {
			// The next upstream page may precede any other source's remaining
			// rows. Stop at this boundary; returning a short global page keeps
			// ordering exact without guessing the next page's first timestamp.
			break
		}
	}
	more := false
	for i, result := range results {
		if result.err != nil || result.skip || state.Positions[i].Done {
			continue
		}
		position := &state.Positions[i]
		position.Offset += consumed[i]
		if position.Offset == len(result.pageRows) {
			position.Offset = 0
			position.Fingerprint = ""
			if result.hasMore {
				position.BeforeMS = result.nextMS
				position.BeforeID = result.nextID
			} else {
				position.Done = true
			}
		} else {
			position.Fingerprint = result.fingerprint
		}
		if !position.Done {
			more = true
		}
	}
	for _, position := range state.Positions {
		total += position.Count
	}
	events := map[string]any{"items": items, "has_more": more, "total_count": total}
	if more {
		token, err := s.encodeFederationCursor(*state)
		if err != nil {
			return nil, err
		}
		events["next_cursor"] = token
	}
	return events, nil
}

func mergeFederationPreview(response map[string]any, responses []map[string]any, payload map[string]any) {
	include, _ := payload["include"].(map[string]any)
	options, exists := include["drilldown_preview"].(map[string]any)
	if !exists {
		return
	}
	limit := int(intValue(options["limit"]))
	if limit <= 0 || limit > maxDrilldownPreviewEvents {
		limit = 12
	}
	items := []any{}
	total := int64(0)
	more := false
	for _, part := range responses {
		if page, ok := part["drilldown_preview"].(map[string]any); ok {
			rows, _ := page["items"].([]any)
			items = append(items, rows...)
			total += intValue(page["total_count"])
			hasMore, _ := page["has_more"].(bool)
			more = more || hasMore
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, _ := items[i].(map[string]any)
		b, _ := items[j].(map[string]any)
		return intValue(a["timestamp_ms"]) > intValue(b["timestamp_ms"])
	})
	if len(items) > limit {
		items = items[:limit]
		more = true
	}
	response["drilldown_preview"] = map[string]any{"items": items, "has_more": more, "total_count": total}
}

func federationAnalyticsInstances(results []federationAnalyticsResult) []any {
	instances := make([]any, 0, len(results))
	for _, result := range results {
		instances = append(instances, map[string]any{"instance_id": result.source.ID, "instance_name": result.source.Name, "available": result.err == nil})
	}
	return instances
}
