package httpapi

import (
	"math"
	"sort"
)

// Provider reset identifiers and grant metadata never leave the read operation.
type quotaResetCredit struct {
	ExpiresAtMS int64 `json:"expires_at_ms"`
}

type quotaResetCredits struct {
	AvailableCount *int64             `json:"available_count"`
	Credits        []quotaResetCredit `json:"credits"`
	ObservedAtMS   int64              `json:"observed_at_ms,omitempty"`
	Stale          bool               `json:"stale,omitempty"`
	Partial        bool               `json:"partial,omitempty"`
}

func resetCreditCount(value any) *int64 {
	n, ok := quotaNumber(value)
	if !ok || n < 0 || n >= float64(math.MaxInt64) || math.Trunc(n) != n {
		return nil
	}
	count := int64(n)
	return &count
}

func projectResetCredits(usage, details map[string]any, observedAt int64) *quotaResetCredits {
	result := &quotaResetCredits{Credits: []quotaResetCredit{}, Partial: true}
	if value, _ := codexUsageField(usage, "rate_limit_reset_credits", "rateLimitResetCredits"); value != nil {
		if summary, ok := value.(map[string]any); ok {
			count, _ := codexUsageField(summary, "available_count", "availableCount")
			result.AvailableCount = resetCreditCount(count)
		}
	}
	if result.AvailableCount != nil {
		result.ObservedAtMS = observedAt
	}
	if details == nil {
		return result
	}
	count, _ := codexUsageField(details, "available_count", "availableCount")
	if explicit := resetCreditCount(count); explicit != nil {
		result.AvailableCount = explicit
		result.ObservedAtMS = observedAt
	}
	entries, hasList := details["credits"].([]any)
	if !hasList {
		return result
	}
	invalid := false
	available, expired := int64(0), int64(0)
	for _, entry := range entries {
		credit, ok := entry.(map[string]any)
		if !ok {
			invalid = true
			continue
		}
		resetType, _ := codexUsageField(credit, "reset_type", "resetType")
		if stringValue(resetType) != "codex_rate_limits" || stringValue(credit["status"]) != "available" {
			continue
		}
		available++
		expires, _ := codexUsageField(credit, "expires_at", "expiresAt")
		expiresAt := quotaTimestampMS(expires)
		if expiresAt == 0 || expiresAt > 8_640_000_000_000_000 {
			invalid = true
			continue
		}
		if expiresAt <= observedAt {
			expired++
			continue
		}
		result.Credits = append(result.Credits, quotaResetCredit{ExpiresAtMS: expiresAt})
	}
	if result.AvailableCount == nil && !invalid {
		current := available - expired
		result.AvailableCount = &current
	} else if result.AvailableCount != nil && *result.AvailableCount == available && expired > 0 {
		current := available - expired
		result.AvailableCount = &current
	}
	if result.AvailableCount != nil && *result.AvailableCount == 0 {
		result.Credits = []quotaResetCredit{}
	}
	sort.Slice(result.Credits, func(i, j int) bool { return result.Credits[i].ExpiresAtMS < result.Credits[j].ExpiresAtMS })
	result.Partial = invalid || result.AvailableCount == nil || *result.AvailableCount != int64(len(result.Credits))
	result.ObservedAtMS = observedAt
	return result
}

func currentResetCredits(value *quotaResetCredits, now int64, stale bool) *quotaResetCredits {
	if value == nil {
		return nil
	}
	result := *value
	result.Stale = result.Stale || stale
	result.Credits = make([]quotaResetCredit, 0, len(value.Credits))
	expired := int64(0)
	for _, credit := range value.Credits {
		if credit.ExpiresAtMS <= now {
			expired++
		} else {
			result.Credits = append(result.Credits, credit)
		}
	}
	if value.AvailableCount != nil {
		count := max(0, *value.AvailableCount-expired)
		result.AvailableCount = &count
	}
	return &result
}

func mergeResetCredits(fresh, previous *quotaResetCredits, now int64) *quotaResetCredits {
	if fresh == nil {
		return currentResetCredits(previous, now, true)
	}
	if !fresh.Partial || previous == nil {
		return currentResetCredits(fresh, now, false)
	}
	if fresh.AvailableCount != nil && *fresh.AvailableCount == 0 {
		return currentResetCredits(fresh, now, false)
	}
	old := currentResetCredits(previous, now, true)
	if fresh.AvailableCount != nil && (old.AvailableCount == nil || *fresh.AvailableCount != *old.AvailableCount) {
		// A changed total does not identify which old credit was consumed or
		// newly granted. Do not attach an unrelated expiry to the new count.
		return currentResetCredits(fresh, now, false)
	}
	if fresh.AvailableCount != nil {
		old.AvailableCount = fresh.AvailableCount
	}
	// Retain earlier expiries only as a marked historical observation. A fresh
	// count can have changed, so do not assert that all old cards remain usable.
	if len(fresh.Credits) > 0 {
		old.Credits = append([]quotaResetCredit{}, fresh.Credits...)
		old.ObservedAtMS = fresh.ObservedAtMS
	}
	old.Partial = true
	return old
}
