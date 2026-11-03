package httpapi

import (
	"crypto/sha256"
	"log/slog"
	"net/url"
	"strings"

	"cpamp-viewer/server/internal/config"
	"cpamp-viewer/server/internal/cpamp"
)

type cpampReadSource struct {
	ID     string
	Name   string
	Reader *Server
}

func federationSourceID(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err == nil {
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		parsed.Host = strings.ToLower(parsed.Host)
		if (parsed.Scheme == "http" && parsed.Port() == "80") || (parsed.Scheme == "https" && parsed.Port() == "443") {
			parsed.Host = strings.TrimSuffix(parsed.Host, ":"+parsed.Port())
		}
		parsed.Path = strings.TrimRight(parsed.Path, "/")
		baseURL = parsed.String()
	}
	return pseudonym("quota-source:cpamp:" + baseURL)
}

func federationViewID(instanceID, localID string) string {
	if localID == "" {
		return ""
	}
	return pseudonym("cpamp-federation:" + instanceID + ":" + localID)
}

func (s *Server) cpampReadSources() []cpampReadSource {
	sources := []cpampReadSource{{ID: federationSourceID(s.cfg.CPAMPBaseURL), Name: "CPA Manager Plus", Reader: s}}
	for _, source := range s.quotaSources {
		if source.kind == "cpamp" && source.reader != nil {
			sources = append(sources, cpampReadSource{ID: source.id, Name: dashboardInstanceName(cpampReadSource{Name: source.name}), Reader: source.reader})
		}
	}
	return sources
}

func newFederatedCPAMPReader(base config.Config, source config.QuotaSource) *Server {
	cfg := federatedAccessGuardConfig(base, source)
	cfg.QuotaSources = nil
	cfg.Sub2APIBaseURL, cfg.Sub2APIAdminAPIKey, cfg.Sub2APIAdminJWT = "", "", ""
	// Per-instance cursor keys prevent a local cursor being replayed to a different server.
	material := append(append([]byte{}, base.SessionSecret...), []byte("\x00"+federationSourceID(source.BaseURL))...)
	secret := sha256.Sum256(material)
	cfg.SessionSecret = secret[:]
	client := cpamp.New(source.BaseURL, source.AdminKey, cfg.RequestTimeout, cfg.MaxUpstreamBodyBytes)
	return &Server{
		cfg: cfg, cpamp: client, logger: slog.Default(), analyticsSem: make(chan struct{}, 4),
		accessGuard: newAccessGuardQuotaCache(cfg), codexUsage: newCodexUsageCache(client, cfg.RequestTimeout),
		modelPriceStatus:     newModelPriceStatusCache(client, cfg.RequestTimeout),
		cachedQuotaSnapshots: newQuotaSnapshotCache(client, cfg.RequestTimeout),
		usageStatus:          newUsageStatusCache(client, cfg.RequestTimeout),
	}
}
