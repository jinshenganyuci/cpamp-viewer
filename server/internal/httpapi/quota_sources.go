package httpapi

import (
	"context"
	"sort"
	"sync"
	"time"

	"cpamp-viewer/server/internal/config"
	"cpamp-viewer/server/internal/sub2api"
)

type quotaSource struct {
	id     string
	kind   string
	name   string
	load   func(context.Context) ([]quotaAccount, error)
	reader *Server
}

func newAdditionalQuotaSources(cfg config.Config) []quotaSource {
	sources := make([]quotaSource, 0, len(cfg.QuotaSources))
	for _, source := range cfg.QuotaSources {
		reader := &Server{}
		var load func(context.Context) ([]quotaAccount, error)
		if source.Type == "cpamp" {
			reader = newFederatedCPAMPReader(cfg, source)
			load = reader.loadCPAMPQuota
		} else {
			reader.sub2api = sub2api.New(source.BaseURL, source.AdminAPIKey, source.AdminJWT, cfg.RequestTimeout, cfg.MaxUpstreamBodyBytes)
			load = reader.loadSub2APIQuota
		}
		id := pseudonym("quota-source:" + source.Type + ":" + source.BaseURL)
		if source.Type == "cpamp" {
			id = federationSourceID(source.BaseURL)
		}
		sources = append(sources, quotaSource{
			id:   id,
			kind: source.Type, name: cleanText(source.Name, 80), load: load, reader: reader,
		})
	}
	return sources
}

func (s *Server) loadCombinedQuota(ctx context.Context) (quotaResponse, error) {
	multiCPAMP := len(s.cpampReadSources()) > 1
	primaryID := ""
	if multiCPAMP {
		primaryID = federationSourceID(s.cfg.CPAMPBaseURL)
	}
	sources := []quotaSource{{id: primaryID, kind: "cpamp", name: "CPA Manager Plus", load: s.loadCPAMPQuota}}
	if s.sub2api != nil {
		sources = append(sources, quotaSource{kind: "sub2api", name: "Sub2API", load: s.loadSub2APIQuota})
	}
	sources = append(sources, s.quotaSources...)
	type outcome struct {
		accounts []quotaAccount
		err      error
	}
	results := make([]outcome, len(sources))
	var workers sync.WaitGroup
	semaphore := make(chan struct{}, 4)
	for index, source := range sources {
		workers.Add(1)
		go func() {
			defer workers.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results[index].err = ctx.Err()
				return
			}
			results[index].accounts, results[index].err = source.load(ctx)
		}()
	}
	workers.Wait()
	response := quotaResponse{GeneratedAt: time.Now().UnixMilli(), Accounts: make([]quotaAccount, 0), Source: "cpamp-quota"}
	if len(sources) > 1 {
		response.Source = "combined-quota"
	}
	succeeded := false
	for index, result := range results {
		source := sources[index]
		if result.err != nil {
			response.Warnings = append(response.Warnings, source.name+" 额度暂不可用")
			if s.logger != nil {
				s.logger.Warn("quota source unavailable", "source", source.kind, "source_id", source.id)
			}
			continue
		}
		succeeded = true
		for _, account := range result.accounts {
			account.Source = source.kind
			if source.id != "" {
				account.ID = federationViewID(source.id, account.ID)
				account.SourceID = source.id
				account.SourceName = source.name
				if multiCPAMP || source.kind != "cpamp" {
					account.InstanceID, account.InstanceName = source.id, source.name
				}
			}
			response.Accounts = append(response.Accounts, account)
		}
	}
	if !succeeded {
		return quotaResponse{}, results[0].err
	}
	sort.Slice(response.Accounts, func(left, right int) bool {
		first, second := response.Accounts[left], response.Accounts[right]
		if first.Provider != second.Provider {
			return first.Provider < second.Provider
		}
		if first.Source != second.Source {
			return first.Source < second.Source
		}
		if first.SourceName != second.SourceName {
			return first.SourceName < second.SourceName
		}
		if first.DisplayName != second.DisplayName {
			return first.DisplayName < second.DisplayName
		}
		return first.ID < second.ID
	})
	return response, nil
}
