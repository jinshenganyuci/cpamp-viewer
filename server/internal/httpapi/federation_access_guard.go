package httpapi

import (
	"context"
	"strings"
	"sync"
	"time"

	"cpamp-viewer/server/internal/accessguard"
	"cpamp-viewer/server/internal/config"
)

// Only an explicitly public-all main configuration is inherited. Binding IDs
// in a main-instance allowlist do not authorize any secondary instance's IDs.
func federatedAccessGuardConfig(base config.Config, source config.QuotaSource) config.Config {
	cfg := base
	cfg.CPAMPBaseURL, cfg.CPAMPAdminKey = source.BaseURL, source.AdminKey
	cfg.AccessGuardBaseURL, cfg.AccessGuardManagementKey = "", ""
	cfg.AccessGuardViaCPAMP, cfg.AccessGuardPublicAll = false, false
	cfg.AccessGuardPublicKeys = nil
	if source.Type != "cpamp" && source.Type != "cpa" {
		return cfg
	}
	switch {
	case source.AccessGuardPublicKeys != nil:
		cfg.AccessGuardViaCPAMP = true
		cfg.AccessGuardPublicKeys = append([]config.AccessGuardPublicKey{}, source.AccessGuardPublicKeys...)
	case source.AccessGuardPublicAll != nil:
		cfg.AccessGuardPublicAll = *source.AccessGuardPublicAll
		cfg.AccessGuardViaCPAMP = *source.AccessGuardPublicAll
	default:
		cfg.AccessGuardPublicAll = base.AccessGuardPublicAll
		cfg.AccessGuardViaCPAMP = base.AccessGuardPublicAll
	}
	return cfg
}

func federatedAccessGuardSnapshot(ctx context.Context, sources []cpampReadSource) (accessGuardQuotaResponse, error) {
	type outcome struct {
		response accessGuardQuotaResponse
		err      error
	}
	results := make([]outcome, len(sources))
	var workers sync.WaitGroup
	for index, source := range sources {
		if source.Reader == nil || source.Reader.accessGuard == nil {
			continue
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			results[index].response, results[index].err = source.Reader.accessGuard.snapshot(ctx)
		}()
	}
	workers.Wait()
	response := accessGuardQuotaResponse{Items: []accessGuardQuotaItem{}}
	successful := false
	var oldest time.Time
	for index, result := range results {
		source := sources[index]
		if source.Reader == nil || source.Reader.accessGuard == nil {
			continue
		}
		response.Configured = true
		name := cleanText(strings.TrimSpace(source.Name), 80)
		if name == "" || name == "敏感错误详情已隐藏" || name == "内部错误详情已隐藏" {
			name = "CPA Manager Plus"
		}
		if result.err != nil {
			response.PartialFailed = true
			response.Warnings = append(response.Warnings, name+" Key 额度暂不可用")
			continue
		}
		successful = true
		if result.response.Stale {
			response.Stale, response.PartialFailed = true, true
			response.Warnings = append(response.Warnings, name+" Key 额度更新失败，显示上次记录")
		}
		if observed, err := time.Parse(time.RFC3339, result.response.UpdatedAt); err == nil && (oldest.IsZero() || observed.Before(oldest)) {
			oldest = observed
			response.UpdatedAt = result.response.UpdatedAt
		}
		for _, item := range result.response.Items {
			item.ID = federationViewID(source.ID, item.ID)
			item.InstanceID, item.InstanceName = source.ID, name
			response.Items = append(response.Items, item)
		}
	}
	if response.Configured && !successful {
		return accessGuardQuotaResponse{}, accessguard.ErrUnavailable
	}
	return response, nil
}
