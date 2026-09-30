package httpapi

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"cpamp-viewer/server/internal/auth"
	"cpamp-viewer/server/internal/config"
	"cpamp-viewer/server/internal/cpamp"
	"cpamp-viewer/server/internal/sub2api"
)

var (
	apiKeyHashPattern           = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
	apiKeyViewIDPattern         = regexp.MustCompile(`^view_[a-f0-9]{12}$`)
	eventsCursorPattern         = regexp.MustCompile(`^[A-Za-z0-9_-]{40,512}$`)
	hex64TokenPattern           = regexp.MustCompile(`(?i)(^|[^a-f0-9])[a-f0-9]{64}([^a-f0-9]|$)`)
	highEntropyCandidatePattern = regexp.MustCompile(`[A-Za-z0-9_+/=-]{32,}`)
	stackPathPattern            = regexp.MustCompile(`(?i)([a-z]:\\[^\s]+\.(go|py|js|ts|tsx|java|rb|rs|cpp|c|h):?\d*|/(?:[^\s/]+/)+[^\s]+\.(go|py|js|ts|tsx|java|rb|rs|cpp|c|h):?\d*)`)
	allowedFilters              = map[string]bool{
		"models": true, "providers": true, "api_key_ids": true, "api_key_hashes": true,
		"accounts": true, "credential_ids": true, "auth_files": true, "auth_indices": true,
		"source_hashes": true, "project_ids": true, "request_types": true,
		"header_error_kinds": true, "header_error_codes": true, "header_quota_plans": true,
		"include_failed": true, "failed_only": true, "min_latency_ms": true,
		"cache_status": true,
	}
	viewerIdentityFilters = map[string]bool{
		"api_key_ids": true, "api_key_hashes": true, "accounts": true, "credential_ids": true,
		"auth_files": true, "auth_indices": true, "source_hashes": true,
		"project_ids": true,
	}
	allowedInclude = map[string]bool{
		"summary": true, "summary_profile": true, "summary_percentiles": true,
		"summary_comparison": true, "timeline": true,
		"hourly_distribution": true, "model_share": true, "channel_share": true,
		"model_stats": true, "failure_sources": true, "account_stats": true,
		"credential_stats": true, "credential_timeline": true, "api_key_timeline": true,
		"api_key_stats":  true,
		"filter_options": true, "filter_selectors": true, "heatmap": true,
		"anomaly_points": true, "task_buckets": true, "recent_failures": true,
		"events_page": true, "drilldown_preview": true, "granularity": true,
	}
)

const (
	viewerVersion             = "2.5.0"
	maxPublicEventsPage       = 500
	maxDrilldownPreviewEvents = 100
	eventsCursorAAD           = "cpamp-viewer/events-cursor/v1"
)

type analyticsIdentityFilterGroup struct {
	Target  string
	Sources []string
}

var analyticsIdentityFilterGroups = []analyticsIdentityFilterGroup{
	{Target: "api_key_hashes", Sources: []string{"api_key_ids", "api_key_hashes"}},
	{Target: "accounts", Sources: []string{"accounts"}},
	{Target: "credential_ids", Sources: []string{"credential_ids"}},
	{Target: "auth_files", Sources: []string{"auth_files"}},
	{Target: "auth_indices", Sources: []string{"auth_indices"}},
	{Target: "source_hashes", Sources: []string{"source_hashes"}},
	{Target: "project_ids", Sources: []string{"project_ids"}},
}

type Server struct {
	cfg                  config.Config
	auth                 *auth.Manager
	cpamp                *cpamp.Client
	sub2api              *sub2api.Client
	web                  fs.FS
	logger               *slog.Logger
	analyticsSem         chan struct{}
	accessGuard          *accessGuardQuotaCache
	codexUsage           *codexUsageCache
	modelPriceStatus     *modelPriceStatusCache
	cachedQuotaSnapshots *quotaSnapshotCache
	usageStatus          *usageStatusCache
}

type authFileEnvelope struct {
	Files []map[string]any `json:"files"`
}

type headerSnapshotEnvelope struct {
	GeneratedAt int64            `json:"generated_at_ms"`
	Items       []map[string]any `json:"items"`
}

type quotaWindow struct {
	ID               string  `json:"id"`
	Pool             string  `json:"pool,omitempty"`
	Label            string  `json:"label"`
	Remaining        float64 `json:"remaining_percent"`
	Used             float64 `json:"used_percent"`
	ResetAtMS        int64   `json:"reset_at_ms,omitempty"`
	WindowMins       float64 `json:"window_minutes,omitempty"`
	ObservedAt       int64   `json:"observed_at_ms,omitempty"`
	Stale            bool    `json:"stale,omitempty"`
	WindowKind       string  `json:"window_kind,omitempty"`
	ModelScope       string  `json:"model_scope,omitempty"`
	UnknownUsed      bool    `json:"-"`
	UnknownRemaining bool    `json:"-"`
}

type quotaAccount struct {
	ID            string        `json:"id"`
	Source        string        `json:"source,omitempty"`
	Provider      string        `json:"provider"`
	DisplayName   string        `json:"display_name"`
	Plan          string        `json:"plan,omitempty"`
	Status        string        `json:"status"`
	StatusMessage string        `json:"status_message,omitempty"`
	Disabled      bool          `json:"disabled"`
	Windows       []quotaWindow `json:"windows"`
	UpdatedAtMS   int64         `json:"updated_at_ms,omitempty"`
	Success       int64         `json:"success,omitempty"`
	Failed        int64         `json:"failed,omitempty"`
}

type quotaResponse struct {
	GeneratedAt int64          `json:"generated_at_ms"`
	Accounts    []quotaAccount `json:"accounts"`
	Source      string         `json:"source"`
	Warnings    []string       `json:"warnings,omitempty"`
}

type databaseMaintenanceStatus struct {
	Required            bool `json:"required"`
	PerformanceDegraded bool `json:"performanceDegraded"`
	DeferredIndexes     int  `json:"deferredIndexes"`
	OfflineJobs         int  `json:"offlineJobs"`
}

type databaseMaintenanceEnvelope struct {
	DatabaseMaintenance databaseMaintenanceStatus `json:"databaseMaintenance"`
}

type apiKeyAlias struct {
	APIKeyHash string `json:"apiKeyHash"`
	Alias      string `json:"alias"`
	UpdatedAt  int64  `json:"updatedAtMs,omitempty"`
}

type apiKeyAliasEnvelope struct {
	Items []apiKeyAlias `json:"items"`
}

type activeAPIKeyEnvelope struct {
	APIKeys []string `json:"api-keys"`
}

type safeAlias struct {
	ID    string `json:"id"`
	Alias string `json:"alias"`
}

type modelPrice struct {
	Prompt                  float64 `json:"prompt"`
	Completion              float64 `json:"completion"`
	Cache                   float64 `json:"cache"`
	CacheRead               float64 `json:"cacheRead,omitempty"`
	CacheCreation           float64 `json:"cacheCreation,omitempty"`
	PromptConfigured        bool    `json:"promptConfigured,omitempty"`
	CompletionConfigured    bool    `json:"completionConfigured,omitempty"`
	CacheReadConfigured     bool    `json:"cacheReadConfigured,omitempty"`
	CacheCreationConfigured bool    `json:"cacheCreationConfigured,omitempty"`
}

type modelPriceEnvelope struct {
	Prices map[string]modelPrice `json:"prices"`
}

type eventsCursor struct {
	BeforeMS int64 `json:"m"`
	BeforeID int64 `json:"i"`
}

type dashboardMetrics struct {
	TotalCalls         int64    `json:"total_calls"`
	SuccessCalls       int64    `json:"success_calls"`
	FailureCalls       int64    `json:"failure_calls"`
	SuccessRate        float64  `json:"success_rate"`
	InputTokens        int64    `json:"input_tokens"`
	OutputTokens       int64    `json:"output_tokens"`
	CachedTokens       int64    `json:"cached_tokens"`
	ReasoningTokens    int64    `json:"reasoning_tokens"`
	TotalTokens        int64    `json:"total_tokens"`
	TotalCost          float64  `json:"total_cost"`
	AverageLatencyMS   *float64 `json:"average_latency_ms"`
	P95LatencyMS       *float64 `json:"p95_latency_ms,omitempty"`
	RPM30m             float64  `json:"rpm_30m"`
	TPM30m             float64  `json:"tpm_30m"`
	ZeroTokenCalls     int64    `json:"zero_token_calls,omitempty"`
	CacheReadTokens    int64    `json:"cache_read_tokens,omitempty"`
	CacheCreationToken int64    `json:"cache_creation_tokens,omitempty"`
}

type dashboardRolling struct {
	RPM         float64 `json:"rpm"`
	TPM         float64 `json:"tpm"`
	TotalCalls  int64   `json:"total_calls"`
	TotalTokens int64   `json:"total_tokens"`
}

type dashboardWindow struct {
	TodayStartMS      int64 `json:"today_start_ms"`
	NowMS             int64 `json:"now_ms"`
	Rolling30MStartMS int64 `json:"rolling_30m_start_ms"`
}

type dashboardTimelinePoint struct {
	BucketMS         int64   `json:"bucket_ms"`
	Label            string  `json:"label,omitempty"`
	Calls            int64   `json:"calls"`
	Tokens           int64   `json:"tokens"`
	Success          int64   `json:"success"`
	Failure          int64   `json:"failure"`
	CallsShare       float64 `json:"calls_share,omitempty"`
	TokensShare      float64 `json:"tokens_share,omitempty"`
	FailureRate      float64 `json:"failure_rate,omitempty"`
	Cost             float64 `json:"cost,omitempty"`
	AverageLatencyMS float64 `json:"average_latency_ms,omitempty"`
	SuccessRate      float64 `json:"success_rate,omitempty"`
}

type dashboardHealthPoint struct {
	BucketMS    int64   `json:"bucket_ms"`
	Calls       int64   `json:"calls"`
	Tokens      int64   `json:"tokens"`
	Success     int64   `json:"success"`
	Failure     int64   `json:"failure"`
	SuccessRate float64 `json:"success_rate"`
	Tone        string  `json:"tone,omitempty"`
	Intensity   float64 `json:"intensity,omitempty"`
	Future      bool    `json:"future,omitempty"`
}

type dashboardHealthTimeline struct {
	FromMS       int64                  `json:"from_ms,omitempty"`
	ToMS         int64                  `json:"to_ms,omitempty"`
	BucketMS     int64                  `json:"bucket_ms,omitempty"`
	SuccessCalls int64                  `json:"success_calls"`
	FailureCalls int64                  `json:"failure_calls"`
	TotalCalls   int64                  `json:"total_calls"`
	SuccessRate  float64                `json:"success_rate"`
	Points       []dashboardHealthPoint `json:"points"`
}

type dashboardTokenMix struct {
	Key    string  `json:"key"`
	Tokens int64   `json:"tokens"`
	Share  float64 `json:"share"`
}

type dashboardModel struct {
	Model        string  `json:"model"`
	Calls        int64   `json:"calls"`
	Tokens       int64   `json:"tokens"`
	SuccessCalls int64   `json:"success_calls"`
	FailureCalls int64   `json:"failure_calls"`
	SuccessRate  float64 `json:"success_rate"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CachedTokens int64   `json:"cached_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	Cost         float64 `json:"cost"`
	CostShare    float64 `json:"cost_share,omitempty"`
}

type dashboardChannelUpstream struct {
	AuthIndex            string  `json:"auth_index"`
	Source               string  `json:"source"`
	AccountSnapshot      string  `json:"account_snapshot"`
	AuthLabelSnapshot    string  `json:"auth_label_snapshot"`
	AuthProviderSnapshot string  `json:"auth_provider_snapshot"`
	Calls                int64   `json:"calls"`
	Failures             int64   `json:"failures"`
	FailureRate          float64 `json:"failure_rate"`
	SuccessRate          float64 `json:"success_rate"`
	Tokens               int64   `json:"tokens"`
	Cost                 float64 `json:"cost"`
	AverageLatencyMS     float64 `json:"average_latency_ms"`
	Tone                 string  `json:"tone"`
}

type dashboardChannel struct {
	ID               string  `json:"id"`
	AuthID           string  `json:"auth_id,omitempty"`
	SourceID         string  `json:"source_id,omitempty"`
	AccountID        string  `json:"account_id,omitempty"`
	AuthLabelID      string  `json:"auth_label_id,omitempty"`
	Channel          string  `json:"channel"`
	SourceDisplay    string  `json:"source_display,omitempty"`
	AccountDisplay   string  `json:"account_display,omitempty"`
	AuthLabelDisplay string  `json:"auth_label_display,omitempty"`
	Provider         string  `json:"provider,omitempty"`
	Calls            int64   `json:"calls"`
	Failures         int64   `json:"failures"`
	FailureRate      float64 `json:"failure_rate"`
	SuccessRate      float64 `json:"success_rate"`
	Tokens           int64   `json:"tokens"`
	Cost             float64 `json:"cost"`
	AverageLatencyMS float64 `json:"average_latency_ms"`
	Tone             string  `json:"tone,omitempty"`
}

type dashboardFailure struct {
	TimestampMS            int64    `json:"timestamp_ms"`
	Model                  string   `json:"model"`
	APIKeyHash             string   `json:"api_key_hash,omitempty"`
	APIKeyID               string   `json:"api_key_id,omitempty"`
	APIKeyAlias            string   `json:"api_key_alias,omitempty"`
	Source                 string   `json:"source,omitempty"`
	SourceHash             string   `json:"source_hash,omitempty"`
	SourceID               string   `json:"source_id,omitempty"`
	SourceDisplay          string   `json:"source_display,omitempty"`
	AuthIndex              string   `json:"auth_index,omitempty"`
	AuthID                 string   `json:"auth_id,omitempty"`
	AccountSnapshot        string   `json:"account_snapshot,omitempty"`
	AccountID              string   `json:"account_id,omitempty"`
	AccountDisplay         string   `json:"account_display,omitempty"`
	AuthLabelSnapshot      string   `json:"auth_label_snapshot,omitempty"`
	AuthLabelID            string   `json:"auth_label_id,omitempty"`
	AuthLabelDisplay       string   `json:"auth_label_display,omitempty"`
	AuthProviderSnapshot   string   `json:"auth_provider_snapshot,omitempty"`
	AuthProjectIDSnapshot  string   `json:"auth_project_id_snapshot,omitempty"`
	ProjectID              string   `json:"project_id,omitempty"`
	ProjectDisplay         string   `json:"project_display,omitempty"`
	Endpoint               string   `json:"endpoint,omitempty"`
	DurationMS             *float64 `json:"duration_ms"`
	FailStatusCode         *int64   `json:"fail_status_code,omitempty"`
	FailSummary            string   `json:"fail_summary,omitempty"`
	HeaderQuotaRecoverAtMS *int64   `json:"header_quota_recover_at_ms,omitempty"`
	HeaderQuotaUsedPercent *float64 `json:"header_quota_used_percent,omitempty"`
	HeaderQuotaPlanType    string   `json:"header_quota_plan_type,omitempty"`
	HeaderErrorKind        string   `json:"header_error_kind,omitempty"`
	HeaderErrorCode        string   `json:"header_error_code,omitempty"`
}

type dashboardCPAConfig struct {
	APIKeys             []string          `json:"api-keys"`
	GeminiAPIKeys       []json.RawMessage `json:"gemini-api-key"`
	CodexAPIKeys        []json.RawMessage `json:"codex-api-key"`
	XAIAPIKeys          []json.RawMessage `json:"xai-api-key"`
	MetaAPIKeys         []json.RawMessage `json:"meta-api-key"`
	ClaudeAPIKeys       []json.RawMessage `json:"claude-api-key"`
	OpenAICompatibility []json.RawMessage `json:"openai-compatibility"`
	Debug               bool              `json:"debug"`
	LoggingToFile       bool              `json:"logging-to-file"`
	RequestRetry        int64             `json:"request-retry"`
	WSAuth              bool              `json:"ws-auth"`
	ProxyURL            string            `json:"proxy-url"`
	Routing             struct {
		Strategy string `json:"strategy"`
	} `json:"routing"`
}

type dashboardManagerConfig struct {
	Config struct {
		CPAConnection struct {
			CPABaseURL string `json:"cpaBaseUrl"`
		} `json:"cpaConnection"`
	} `json:"config"`
}

type dashboardCollectorStatus struct {
	Events      int64 `json:"events"`
	DeadLetters int64 `json:"deadLetters"`
	Collector   struct {
		Mode           string `json:"mode"`
		Collector      string `json:"collector"`
		Queue          string `json:"queue"`
		LastConsumedAt int64  `json:"lastConsumedAt"`
		LastInsertedAt int64  `json:"lastInsertedAt"`
		TotalInserted  int64  `json:"totalInserted"`
		TotalSkipped   int64  `json:"totalSkipped"`
		LastError      string `json:"lastError"`
	} `json:"collector"`
}

type dashboardErrorLogs struct {
	Files []json.RawMessage `json:"files"`
}

type dashboardModelList struct {
	Data   []map[string]any `json:"data"`
	Models []map[string]any `json:"models"`
}

type dashboardUpstream struct {
	GeneratedAtMS              int64                      `json:"generated_at_ms"`
	Window                     dashboardWindow            `json:"window"`
	Today                      dashboardMetrics           `json:"today"`
	Rolling30m                 dashboardRolling           `json:"rolling_30m"`
	TopModelsToday             []dashboardModel           `json:"top_models_today"`
	TrafficTimeline            []dashboardTimelinePoint   `json:"traffic_timeline"`
	TodayRequestHealthTimeline dashboardHealthTimeline    `json:"today_request_health_timeline"`
	TokenMix                   []dashboardTokenMix        `json:"token_mix"`
	ModelCostRank              []dashboardModel           `json:"model_cost_rank"`
	ChannelHealth              []dashboardChannelUpstream `json:"channel_health"`
	RecentFailures             []dashboardFailure         `json:"recent_failures"`
}

type usageServiceInfo struct {
	Service            string `json:"service"`
	Mode               string `json:"mode"`
	StartedAt          int64  `json:"startedAt"`
	Configured         bool   `json:"configured"`
	AdminReady         bool   `json:"adminReady"`
	ProjectInitialized bool   `json:"projectInitialized"`
	MigrationStatus    string `json:"migrationStatus"`
}

type dashboardResponse struct {
	GeneratedAtMS int64           `json:"generated_at_ms"`
	Window        dashboardWindow `json:"window"`
	Connection    struct {
		Connected bool `json:"connected"`
	} `json:"connection"`
	System struct {
		ManagementVersion string `json:"management_version,omitempty"`
		ServerVersion     string `json:"server_version,omitempty"`
		BuildTime         any    `json:"build_time,omitempty"`
		CPABase           string `json:"cpa_base,omitempty"`
	} `json:"system"`
	Health struct {
		UsageMonitor string `json:"usage_monitor"`
		RequestLog   string `json:"request_log"`
		DataSource   string `json:"data_source"`
		ErrorLogs    any    `json:"error_logs"`
	} `json:"health"`
	Stats struct {
		ManagementKeys  int  `json:"management_keys"`
		AuthFiles       int  `json:"auth_files"`
		AvailableModels *int `json:"available_models,omitempty"`
		Providers       struct {
			Gemini int `json:"gemini"`
			Codex  int `json:"codex"`
			XAI    int `json:"xai"`
			Meta   int `json:"meta"`
			Claude int `json:"claude"`
			OpenAI int `json:"openai"`
			Total  int `json:"total"`
		} `json:"providers"`
	} `json:"stats"`
	Config struct {
		Debug           bool   `json:"debug"`
		LoggingToFile   bool   `json:"logging_to_file"`
		RequestRetry    int64  `json:"request_retry"`
		WSAuth          bool   `json:"ws_auth"`
		RoutingStrategy string `json:"routing_strategy,omitempty"`
		ProxyURL        string `json:"proxy_url,omitempty"`
	} `json:"config"`
	Collector                  dashboardCollectorStatus `json:"collector"`
	Today                      dashboardMetrics         `json:"today"`
	Rolling30m                 dashboardRolling         `json:"rolling_30m"`
	TopModelsToday             []dashboardModel         `json:"top_models_today"`
	TrafficTimeline            []dashboardTimelinePoint `json:"traffic_timeline"`
	TodayRequestHealthTimeline dashboardHealthTimeline  `json:"today_request_health_timeline"`
	TokenMix                   []dashboardTokenMix      `json:"token_mix"`
	ModelCostRank              []dashboardModel         `json:"model_cost_rank"`
	ChannelHealth              []dashboardChannel       `json:"channel_health"`
	RecentFailures             []dashboardFailure       `json:"recent_failures"`
}

func New(cfg config.Config, webFS embed.FS, logger *slog.Logger) (*Server, error) {
	web, err := fs.Sub(webFS, "webdist")
	if err != nil {
		return nil, err
	}
	client := cpamp.New(cfg.CPAMPBaseURL, cfg.CPAMPAdminKey, cfg.RequestTimeout, cfg.MaxUpstreamBodyBytes)
	var sub2APIClient *sub2api.Client
	if cfg.Sub2APIBaseURL != "" {
		sub2APIClient = sub2api.New(cfg.Sub2APIBaseURL, cfg.Sub2APIAdminAPIKey, cfg.Sub2APIAdminJWT, cfg.RequestTimeout, cfg.MaxUpstreamBodyBytes)
	}
	return &Server{
		cfg:                  cfg,
		auth:                 auth.New(cfg.ViewerPassword, cfg.SessionSecret, cfg.SessionTTL, cfg.SecureCookies),
		cpamp:                client,
		sub2api:              sub2APIClient,
		web:                  web,
		logger:               logger,
		analyticsSem:         make(chan struct{}, 4),
		accessGuard:          newAccessGuardQuotaCache(cfg),
		codexUsage:           newCodexUsageCache(client, cfg.RequestTimeout),
		modelPriceStatus:     newModelPriceStatusCache(client, cfg.RequestTimeout),
		cachedQuotaSnapshots: newQuotaSnapshotCache(client, cfg.RequestTimeout),
		usageStatus:          newUsageStatusCache(client, cfg.RequestTimeout),
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /", s.handleRoot)
	mux.HandleFunc("GET /management.html", s.handleIndex)
	mux.HandleFunc("GET /viewer/", s.handleViewerAsset)
	mux.HandleFunc("POST /viewer/api/v1/login", s.handleLogin)
	mux.HandleFunc("POST /viewer/api/v1/logout", s.handleLogout)
	mux.HandleFunc("GET /viewer/api/v1/session", s.handleSession)
	mux.HandleFunc("GET /viewer/api/v1/dashboard", s.withSession(s.handleDashboard))
	mux.HandleFunc("GET /viewer/api/v1/maintenance", s.withSession(s.handleMaintenance))
	mux.HandleFunc("GET /viewer/api/v1/aliases", s.withSession(s.handleAliases))
	mux.HandleFunc("GET /viewer/api/v1/model-prices", s.withSession(s.handleModelPrices))
	mux.HandleFunc("GET /viewer/api/v1/model-price-status", s.withSession(s.handleModelPriceStatus))
	mux.HandleFunc("GET /viewer/api/v1/usage-status", s.withSession(s.handleUsageStatus))
	mux.HandleFunc("GET /viewer/api/v1/quota", s.withSession(s.handleQuota))
	mux.HandleFunc("GET /viewer/api/v1/access-guard/quotas", s.withSession(s.handleAccessGuardQuotas))
	mux.HandleFunc("POST /viewer/api/v1/analytics", s.withSession(s.handleAnalytics))
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; frame-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.cpamp.Health(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "degraded", "service": "cpamp-viewer", "upstream": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "cpamp-viewer", "upstream": "ok"})
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/management.html#/", http.StatusFound)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(s.web, "index.html")
	if err != nil {
		http.Error(w, "viewer frontend is not built", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

func (s *Server) handleViewerAsset(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/viewer/")
	if rel == "" || strings.Contains(rel, "..") {
		http.NotFound(w, r)
		return
	}
	cleaned := path.Clean(rel)
	data, err := fs.ReadFile(s.web, cleaned)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if strings.HasSuffix(cleaned, ".js") {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	} else if strings.HasSuffix(cleaned, ".css") {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	} else if strings.HasSuffix(cleaned, ".svg") {
		w.Header().Set("Content-Type", "image/svg+xml")
	}
	_, _ = w.Write(data)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.cfg.PublicAccess {
		s.writePublicSession(w)
		return
	}
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "origin check failed")
		return
	}
	var payload struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &payload, 4<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid login request")
		return
	}
	if !s.auth.VerifyPassword(payload.Password) {
		time.Sleep(750 * time.Millisecond)
		writeError(w, http.StatusUnauthorized, "密码错误")
		return
	}
	token, claims, err := s.auth.NewSession(time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create session")
		return
	}
	s.auth.SetCookie(w, token, claims.ExpiresAt)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "public_access": false, "csrf_token": claims.CSRF, "expires_at": claims.ExpiresAt})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s.cfg.PublicAccess {
		s.auth.ClearCookie(w)
		s.writePublicSession(w)
		return
	}
	claims, err := s.auth.ClaimsFromRequest(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "session required")
		return
	}
	if !validCSRF(r, claims) {
		writeError(w, http.StatusForbidden, "CSRF check failed")
		return
	}
	if cookie, err := r.Cookie(auth.CookieName); err == nil {
		s.auth.Revoke(cookie.Value)
	}
	s.auth.ClearCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": false})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	if s.cfg.PublicAccess {
		s.writePublicSession(w)
		return
	}
	claims, err := s.auth.ClaimsFromRequest(r)
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]bool{"authenticated": false, "public_access": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "public_access": false, "csrf_token": claims.CSRF, "expires_at": claims.ExpiresAt})
}

func (s *Server) writePublicSession(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true, "public_access": true})
}

func (s *Server) withSession(next func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.PublicAccess {
			if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
				writeError(w, http.StatusForbidden, "origin check failed")
				return
			}
			w.Header().Set("Cache-Control", "no-store, private")
			next(w, r)
			return
		}
		claims, err := s.auth.ClaimsFromRequest(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "session required")
			return
		}
		if r.Method != http.MethodGet && !validCSRF(r, claims) {
			writeError(w, http.StatusForbidden, "CSRF check failed")
			return
		}
		w.Header().Set("Cache-Control", "no-store, private")
		next(w, r)
	}
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	query := url.Values{
		"today_start_ms":  {strconv.FormatInt(todayStart.UnixMilli(), 10)},
		"now_ms":          {strconv.FormatInt(now.UnixMilli(), 10)},
		"top_models":      {"8"},
		"recent_failures": {"8"},
	}
	var upstream dashboardUpstream
	if err := s.cpamp.GetJSON(r.Context(), "/v0/management/dashboard/summary", query, &upstream); err != nil {
		s.writeUpstreamError(w, err)
		return
	}

	var cpaConfig dashboardCPAConfig
	configHeaders, configErr := s.cpamp.GetJSONWithHeaders(r.Context(), "/v0/management/config", nil, &cpaConfig)
	if configErr != nil {
		s.logger.Warn("load CPA dashboard configuration", "error", configErr)
	}

	aliases, aliasErr := s.loadAllAliases(r.Context())
	if aliasErr != nil {
		s.logger.Warn("load CPAMP aliases for dashboard", "error", aliasErr)
	}
	sanitizeDashboardUpstream(&upstream, aliases)

	var info usageServiceInfo
	infoErr := s.cpamp.GetJSON(r.Context(), "/usage-service/info", nil, &info)
	if infoErr != nil {
		s.logger.Warn("load CPAMP usage service info", "error", infoErr)
	}

	var managerConfig dashboardManagerConfig
	managerConfigErr := s.cpamp.GetJSON(r.Context(), "/usage-service/config", nil, &managerConfig)
	if managerConfigErr != nil {
		s.logger.Warn("load CPAMP manager configuration for dashboard", "error", managerConfigErr)
	}

	var collector dashboardCollectorStatus
	collectorErr := s.cpamp.GetJSON(r.Context(), "/status", nil, &collector)
	if collectorErr != nil {
		s.logger.Warn("load CPAMP collector status for dashboard", "error", collectorErr)
	}

	var authFiles authFileEnvelope
	authFilesErr := s.cpamp.GetJSON(r.Context(), "/v0/management/auth-files", nil, &authFiles)
	if authFilesErr != nil {
		s.logger.Warn("load CPA auth file count for dashboard", "error", authFilesErr)
	}

	var errorLogs dashboardErrorLogs
	errorLogsErr := s.cpamp.GetJSON(r.Context(), "/v0/management/request-error-logs", nil, &errorLogs)
	if errorLogsErr != nil {
		s.logger.Warn("load CPA error log count for dashboard", "error", errorLogsErr)
	}

	var availableModels *int
	if len(cpaConfig.APIKeys) > 0 {
		var models dashboardModelList
		if err := s.cpamp.GetJSONWithBearer(r.Context(), "/v1/models", cpaConfig.APIKeys[0], nil, &models); err != nil {
			s.logger.Warn("load CPA available model count for dashboard", "error", err)
		} else {
			count := countDashboardModels(models)
			availableModels = &count
		}
	}

	response := dashboardResponse{
		GeneratedAtMS:              upstream.GeneratedAtMS,
		Window:                     upstream.Window,
		Today:                      upstream.Today,
		Rolling30m:                 upstream.Rolling30m,
		TopModelsToday:             nonNilDashboardModels(upstream.TopModelsToday),
		TrafficTimeline:            nonNilTimeline(upstream.TrafficTimeline),
		TodayRequestHealthTimeline: upstream.TodayRequestHealthTimeline,
		TokenMix:                   nonNilTokenMix(upstream.TokenMix),
		ModelCostRank:              nonNilDashboardModels(upstream.ModelCostRank),
		ChannelHealth:              projectDashboardChannels(upstream.ChannelHealth),
		RecentFailures:             nonNilDashboardFailures(upstream.RecentFailures),
	}
	response.Connection.Connected = true
	response.System.ManagementVersion = viewerVersion
	response.System.ServerVersion = cleanText(firstHeader(configHeaders, "X-CPA-Version", "X-Server-Version"), 120)
	if response.System.ServerVersion == "" && info.Service != "" {
		response.System.ServerVersion = cleanText(strings.TrimSpace(info.Service+" "+info.Mode), 120)
	}
	if buildTime := cleanText(firstHeader(configHeaders, "X-CPA-Build-Date", "X-Server-Build-Date"), 120); buildTime != "" {
		response.System.BuildTime = buildTime
	} else {
		response.System.BuildTime = info.StartedAt
	}
	response.System.CPABase = safeDisplayURL(managerConfig.Config.CPAConnection.CPABaseURL)
	response.Health.UsageMonitor = healthLabel(infoErr == nil && info.Configured && info.AdminReady)
	response.Health.RequestLog = healthLabel(infoErr == nil && info.ProjectInitialized && usageMigrationReady(info.MigrationStatus) && collectorErr == nil)
	response.Health.DataSource = cleanText(collector.Collector.Queue, 80)
	if response.Health.DataSource == "" {
		response.Health.DataSource = "usage"
	}
	if errorLogsErr == nil {
		response.Health.ErrorLogs = len(errorLogs.Files)
	} else {
		response.Health.ErrorLogs = "未检查"
	}
	response.Stats.ManagementKeys = len(cpaConfig.APIKeys)
	response.Stats.AuthFiles = len(authFiles.Files)
	response.Stats.AvailableModels = availableModels
	response.Stats.Providers.Gemini = len(cpaConfig.GeminiAPIKeys)
	response.Stats.Providers.Codex = len(cpaConfig.CodexAPIKeys)
	response.Stats.Providers.XAI = len(cpaConfig.XAIAPIKeys)
	response.Stats.Providers.Meta = len(cpaConfig.MetaAPIKeys)
	response.Stats.Providers.Claude = len(cpaConfig.ClaudeAPIKeys)
	response.Stats.Providers.OpenAI = len(cpaConfig.OpenAICompatibility)
	response.Stats.Providers.Total = response.Stats.Providers.Gemini + response.Stats.Providers.Codex + response.Stats.Providers.XAI + response.Stats.Providers.Meta + response.Stats.Providers.Claude + response.Stats.Providers.OpenAI
	response.Config.Debug = cpaConfig.Debug
	response.Config.LoggingToFile = cpaConfig.LoggingToFile
	response.Config.RequestRetry = cpaConfig.RequestRetry
	response.Config.WSAuth = cpaConfig.WSAuth
	response.Config.RoutingStrategy = cleanText(cpaConfig.Routing.Strategy, 80)
	response.Config.ProxyURL = safeDisplayURL(cpaConfig.ProxyURL)
	if collectorErr == nil {
		collector.Collector.Mode = cleanText(collector.Collector.Mode, 80)
		collector.Collector.Collector = cleanText(collector.Collector.Collector, 80)
		collector.Collector.Queue = cleanText(collector.Collector.Queue, 80)
		collector.Collector.LastError = cleanText(collector.Collector.LastError, 240)
		response.Collector = collector
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleAliases(w http.ResponseWriter, r *http.Request) {
	aliases, err := s.loadActiveAliases(r.Context())
	if err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	items := make([]safeAlias, 0, len(aliases.Items))
	for _, item := range aliases.Items {
		rawHash := strings.ToLower(strings.TrimSpace(item.APIKeyHash))
		alias := cleanText(item.Alias, 80)
		if !apiKeyHashPattern.MatchString(rawHash) || alias == "" {
			continue
		}
		items = append(items, safeAlias{ID: pseudonym(rawHash), Alias: alias})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Alias < items[j].Alias })
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleMaintenance(w http.ResponseWriter, r *http.Request) {
	var upstream databaseMaintenanceEnvelope
	query := url.Values{"scope": {"database-maintenance"}}
	if err := s.cpamp.GetJSON(r.Context(), "/status", query, &upstream); err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	upstream.DatabaseMaintenance.DeferredIndexes = boundedMaintenanceCount(upstream.DatabaseMaintenance.DeferredIndexes)
	upstream.DatabaseMaintenance.OfflineJobs = boundedMaintenanceCount(upstream.DatabaseMaintenance.OfflineJobs)
	writeJSON(w, http.StatusOK, upstream)
}

func boundedMaintenanceCount(value int) int {
	if value < 0 {
		return 0
	}
	if value > 1_000_000 {
		return 1_000_000
	}
	return value
}

func (s *Server) handleModelPrices(w http.ResponseWriter, r *http.Request) {
	var upstream modelPriceEnvelope
	if err := s.cpamp.GetJSON(r.Context(), "/v0/management/model-prices", nil, &upstream); err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	prices := make(map[string]modelPrice, len(upstream.Prices))
	for rawModel, price := range upstream.Prices {
		model := cleanText(rawModel, 180)
		if model == "" || model == "敏感错误详情已隐藏" || model == "内部错误详情已隐藏" {
			continue
		}
		price.Prompt = nonNegativeFinite(price.Prompt)
		price.Completion = nonNegativeFinite(price.Completion)
		price.Cache = nonNegativeFinite(price.Cache)
		price.CacheRead = nonNegativeFinite(price.CacheRead)
		price.CacheCreation = nonNegativeFinite(price.CacheCreation)
		prices[model] = price
	}
	writeJSON(w, http.StatusOK, modelPriceEnvelope{Prices: prices})
}

func (s *Server) handleQuota(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		writeError(w, http.StatusBadRequest, "quota requests do not accept parameters or a body")
		return
	}
	if r.Body != nil {
		var probe [1]byte
		if n, err := r.Body.Read(probe[:]); n != 0 || (err != nil && err != io.EOF) {
			writeError(w, http.StatusBadRequest, "quota requests do not accept a body")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 28*time.Second)
	defer cancel()
	if s.sub2api == nil {
		accounts, err := s.loadCPAMPQuota(ctx)
		if err != nil {
			s.writeUpstreamError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, quotaResponse{GeneratedAt: time.Now().UnixMilli(), Accounts: accounts, Source: "cpamp-quota"})
		return
	}
	type cpampQuotaResult struct {
		accounts []quotaAccount
		err      error
	}
	cpampDone := make(chan cpampQuotaResult, 1)
	go func() {
		accounts, err := s.loadCPAMPQuota(ctx)
		cpampDone <- cpampQuotaResult{accounts: accounts, err: err}
	}()
	sub2APIAccounts, sub2APIErr := s.loadSub2APIQuota(ctx)
	cpampResult := <-cpampDone
	if cpampResult.err != nil && sub2APIErr != nil {
		s.writeUpstreamError(w, cpampResult.err)
		return
	}
	accounts := make([]quotaAccount, 0, len(cpampResult.accounts)+len(sub2APIAccounts))
	accounts = append(accounts, cpampResult.accounts...)
	accounts = append(accounts, sub2APIAccounts...)
	sort.Slice(accounts, func(i, j int) bool {
		if accounts[i].Provider == accounts[j].Provider {
			if accounts[i].Source == accounts[j].Source {
				return accounts[i].DisplayName < accounts[j].DisplayName
			}
			return accounts[i].Source < accounts[j].Source
		}
		return accounts[i].Provider < accounts[j].Provider
	})
	response := quotaResponse{GeneratedAt: time.Now().UnixMilli(), Accounts: accounts, Source: "combined-quota"}
	if cpampResult.err != nil {
		response.Warnings = append(response.Warnings, "CPA Manager Plus 额度暂不可用")
	}
	if sub2APIErr != nil {
		response.Warnings = append(response.Warnings, "Sub2API 额度暂不可用")
		if s.logger != nil {
			s.logger.Warn("Sub2API quota unavailable", "error", sub2APIErr)
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) loadCPAMPQuota(ctx context.Context) ([]quotaAccount, error) {
	var files authFileEnvelope
	if err := s.cpamp.GetJSON(ctx, "/v0/management/auth-files", nil, &files); err != nil {
		return nil, err
	}
	var snapshots headerSnapshotEnvelope
	query := url.Values{"days": {"30"}, "limit": {"1000"}}
	// Legacy headers are a fallback. A missing header database must not hide
	// an otherwise available complete Codex quota inventory.
	headersDone := make(chan struct{})
	var headersError error
	go func() {
		headerCtx, cancelHeaders := context.WithTimeout(ctx, 3*time.Second)
		defer cancelHeaders()
		headersError = s.cpamp.GetJSON(headerCtx, "/v0/management/monitoring/header-snapshots", query, &snapshots)
		close(headersDone)
	}()
	storedDone := make(chan struct{})
	var stored map[string]quotaSnapshotResult
	go func() {
		stored = s.cachedQuotaSnapshots.snapshot(ctx, files.Files)
		close(storedDone)
	}()
	usage := s.codexUsage.snapshot(ctx, files.Files)
	<-headersDone
	<-storedDone

	latest := latestSnapshots(snapshots.Items)
	accounts := make([]quotaAccount, 0, len(files.Files))
	for _, file := range files.Files {
		account, ok := projectQuotaAccount(file, latest)
		if ok {
			account.Source = "cpamp"
			if account.Provider != "codex" {
				query, queryable := quotaSnapshotAccount(file)
				result := stored[query.RowKey]
				if queryable && result.Valid {
					account.Windows = result.Windows
					account.UpdatedAtMS = result.ObservedAt
					if result.Plan != "" {
						account.Plan = result.Plan
					}
					if result.Stale {
						account.StatusMessage = strings.TrimSpace(account.StatusMessage + " 配额快照更新失败，显示上次记录")
					}
				} else if s.cachedQuotaSnapshots != nil || headersError != nil {
					account.StatusMessage = strings.TrimSpace(account.StatusMessage + " 已保存配额快照暂不可用，以下为请求观测")
				}
			}
			if account.Provider == "codex" && s.codexUsage != nil {
				result := usage[codexUsageKey(file)]
				if result.Valid {
					// The complete response replaces ALL passive windows, including
					// an empty inventory. Never append old unknown/Spark mirrors.
					account.Windows = result.Windows
					account.UpdatedAtMS = result.ObservedAt
					if result.Plan != "" {
						account.Plan = result.Plan
					}
				}
				if !result.Valid || result.Stale {
					note := "完整额度查询暂不可用，以下为请求快照"
					if result.Valid {
						note = "完整额度更新失败，显示上次查询结果"
					}
					account.StatusMessage = strings.TrimSpace(account.StatusMessage + " " + note)
				}
			}
			accounts = append(accounts, account)
		}
	}
	sort.Slice(accounts, func(i, j int) bool {
		if accounts[i].Provider == accounts[j].Provider {
			return accounts[i].DisplayName < accounts[j].DisplayName
		}
		return accounts[i].Provider < accounts[j].Provider
	})
	return accounts, nil
}

func (s *Server) handleAnalytics(w http.ResponseWriter, r *http.Request) {
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
	if err := s.validateAnalytics(payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	aliases, aliasErr := s.loadAllAliases(r.Context())
	if aliasErr != nil {
		s.logger.Warn("load CPAMP aliases for analytics", "error", aliasErr)
	}
	identityIndex, err := s.loadAnalyticsIdentityIndex(r.Context(), payload, aliases)
	if err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	if err := resolveAnalyticsIdentityFilters(payload, identityIndex); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var response map[string]any
	if err := s.cpamp.PostJSON(r.Context(), "/v0/management/monitoring/analytics", payload, &response); err != nil {
		s.writeUpstreamError(w, err)
		return
	}
	s.attachAnalyticsCursor(response)
	attachAnalyticsAliases(response, aliases)
	projectAnalyticsWithLimit(response, s.analyticsEventsPageLimit())
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) validateAnalytics(payload map[string]any) error {
	for key := range payload {
		if key != "from_ms" && key != "to_ms" && key != "now_ms" && key != "time_zone" && key != "search_query" && key != "filters" && key != "include" {
			return fmt.Errorf("unsupported analytics field: %s", key)
		}
	}
	from := intValue(payload["from_ms"])
	to := intValue(payload["to_ms"])
	if from <= 0 || to <= from {
		return errors.New("invalid analytics time range")
	}
	rangeMS := to - from
	maxRangeMS := s.cfg.MaxAnalyticsRange.Milliseconds()
	if maxRangeMS <= 0 || rangeMS > maxRangeMS {
		return errors.New("invalid analytics time range")
	}
	if value, exists := payload["search_query"]; exists {
		query, ok := value.(string)
		if !ok {
			return errors.New("search_query must be a string")
		}
		if len(strings.TrimSpace(query)) > 160 {
			return errors.New("search_query is too long")
		}
	}
	if value, exists := payload["time_zone"]; exists {
		zone, ok := value.(string)
		if !ok || len(strings.TrimSpace(zone)) > 80 {
			return errors.New("invalid time_zone")
		}
	}
	if rawFilters, exists := payload["filters"]; exists {
		filters, ok := rawFilters.(map[string]any)
		if !ok {
			return errors.New("filters must be an object")
		}
		for key, value := range filters {
			if !allowedFilters[key] {
				return fmt.Errorf("unsupported analytics filter: %s", key)
			}
			if viewerIdentityFilters[key] {
				list, ok := value.([]any)
				if !ok {
					return fmt.Errorf("%s must be an array of Viewer IDs", key)
				}
				if len(list) > 50 {
					return fmt.Errorf("too many values for filter %s", key)
				}
				for _, item := range list {
					candidate, ok := item.(string)
					if !ok {
						return fmt.Errorf("%s must contain only Viewer IDs", key)
					}
					candidate = strings.TrimSpace(candidate)
					if !apiKeyViewIDPattern.MatchString(candidate) {
						return fmt.Errorf("%s must contain only Viewer IDs", key)
					}
				}
				continue
			}
			switch key {
			case "include_failed", "failed_only":
				if _, ok := value.(bool); !ok {
					return fmt.Errorf("filter %s must be a boolean", key)
				}
			case "min_latency_ms":
				latency, ok := strictInt64(value)
				if !ok || latency < 0 || latency > int64((24*time.Hour)/time.Millisecond) {
					return errors.New("invalid min_latency_ms")
				}
			case "cache_status":
				status, ok := value.(string)
				if !ok || (status != "" && status != "all" && status != "hit" && status != "miss") {
					return errors.New("invalid cache_status")
				}
			default:
				list, ok := value.([]any)
				if !ok {
					return fmt.Errorf("filter %s must be an array", key)
				}
				if len(list) > 50 {
					return fmt.Errorf("too many values for filter %s", key)
				}
				for _, item := range list {
					text, ok := item.(string)
					if !ok || len(strings.TrimSpace(text)) > 180 {
						return fmt.Errorf("invalid filter value: %s", key)
					}
				}
			}
		}
	}
	if rawInclude, exists := payload["include"]; exists {
		include, ok := rawInclude.(map[string]any)
		if !ok {
			return errors.New("include must be an object")
		}
		for key := range include {
			if !allowedInclude[key] {
				return fmt.Errorf("unsupported analytics include: %s", key)
			}
		}
		for key, value := range include {
			switch key {
			case "recent_failures", "events_page", "drilldown_preview", "granularity", "summary_profile":
				continue
			default:
				if _, ok := value.(bool); !ok {
					return fmt.Errorf("analytics include %s must be a boolean", key)
				}
			}
		}
		if value, exists := include["summary_profile"]; exists {
			profile, ok := value.(string)
			if !ok || (profile != "full" && profile != "compact") {
				return errors.New("summary_profile must be full or compact")
			}
		}
		if value, exists := include["granularity"]; exists {
			granularity, ok := value.(string)
			if !ok || (granularity != "hour" && granularity != "day") {
				return errors.New("granularity must be hour or day")
			}
		}
		if value, exists := include["recent_failures"]; exists {
			recent, ok := strictInt64(value)
			if !ok || recent < 0 || recent > 100 {
				return errors.New("recent_failures must be between 0 and 100")
			}
		}
		if value, exists := include["events_page"]; exists {
			page, ok := value.(map[string]any)
			if !ok {
				return errors.New("events_page must be an object")
			}
			if err := validateObjectKeys(page, "events_page", "limit", "cursor"); err != nil {
				return err
			}
			limit := int64(0)
			if value, exists := page["limit"]; exists {
				var ok bool
				limit, ok = strictInt64(value)
				if !ok || limit < 0 || limit > maxPublicEventsPage {
					return fmt.Errorf("events page limit must be between 0 and %d", maxPublicEventsPage)
				}
			}
			if limit == 0 || limit > int64(s.analyticsEventsPageLimit()) {
				page["limit"] = json.Number(strconv.Itoa(s.analyticsEventsPageLimit()))
			}
			if value, exists := page["cursor"]; exists {
				token, ok := value.(string)
				if !ok || len(token) == 0 || len(token) > 512 {
					return errors.New("invalid events page cursor")
				}
				cursor, err := s.decodeEventsCursor(token)
				if err != nil {
					return errors.New("invalid events page cursor")
				}
				page["before_ms"] = json.Number(strconv.FormatInt(cursor.BeforeMS, 10))
				page["before_id"] = json.Number(strconv.FormatInt(cursor.BeforeID, 10))
				delete(page, "cursor")
			}
		}
		if value, exists := include["drilldown_preview"]; exists {
			preview, ok := value.(map[string]any)
			if !ok {
				return errors.New("drilldown_preview must be an object")
			}
			if err := validateObjectKeys(preview, "drilldown_preview", "from_ms", "to_ms", "limit"); err != nil {
				return err
			}
			previewFrom, fromOK := strictInt64(preview["from_ms"])
			previewTo, toOK := strictInt64(preview["to_ms"])
			if !fromOK || !toOK || previewFrom <= 0 || previewTo <= previewFrom || previewTo-previewFrom > maxRangeMS {
				return errors.New("invalid drilldown preview time range")
			}
			limit := int64(0)
			if value, exists := preview["limit"]; exists {
				var ok bool
				limit, ok = strictInt64(value)
				if !ok || limit < 0 || limit > maxDrilldownPreviewEvents {
					return fmt.Errorf("drilldown preview limit must be between 0 and %d", maxDrilldownPreviewEvents)
				}
			}
			if limit == 0 {
				preview["limit"] = json.Number("12")
			}
		}
	}
	return nil
}

func (s *Server) encodeEventsCursor(cursor eventsCursor) string {
	if cursor.BeforeMS <= 0 || cursor.BeforeID <= 0 {
		return ""
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	aead, err := s.eventsCursorAEAD()
	if err != nil {
		return ""
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := cryptorand.Read(nonce); err != nil {
		return ""
	}
	ciphertext := aead.Seal(nil, nonce, payload, []byte(eventsCursorAAD))
	sealed := append(nonce, ciphertext...)
	return base64.RawURLEncoding.EncodeToString(sealed)
}

func (s *Server) decodeEventsCursor(token string) (eventsCursor, error) {
	if !eventsCursorPattern.MatchString(token) {
		return eventsCursor{}, errors.New("invalid cursor encoding")
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return eventsCursor{}, errors.New("invalid cursor encoding")
	}
	aead, err := s.eventsCursorAEAD()
	if err != nil || len(data) <= aead.NonceSize()+aead.Overhead() {
		return eventsCursor{}, errors.New("invalid cursor encoding")
	}
	nonce, ciphertext := data[:aead.NonceSize()], data[aead.NonceSize():]
	payload, err := aead.Open(nil, nonce, ciphertext, []byte(eventsCursorAAD))
	if err != nil {
		return eventsCursor{}, errors.New("invalid cursor authentication")
	}
	var cursor eventsCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.BeforeMS <= 0 || cursor.BeforeID <= 0 {
		return eventsCursor{}, errors.New("invalid cursor payload")
	}
	return cursor, nil
}

func (s *Server) eventsCursorAEAD() (cipher.AEAD, error) {
	material := make([]byte, 0, len(eventsCursorAAD)+1+len(s.cfg.SessionSecret))
	material = append(material, eventsCursorAAD...)
	material = append(material, 0)
	material = append(material, s.cfg.SessionSecret...)
	key := sha256.Sum256(material)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *Server) attachAnalyticsCursor(response map[string]any) {
	if preview, ok := response["drilldown_preview"].(map[string]any); ok {
		delete(preview, "next_cursor")
	}
	events, ok := response["events"].(map[string]any)
	if !ok {
		return
	}
	// Only Viewer-generated cursors may cross the browser boundary. Never trust
	// an upstream next_cursor value, even if it happens to match the token shape.
	delete(events, "next_cursor")
	beforeMS, msOK := strictInt64(events["next_before_ms"])
	beforeID, idOK := strictInt64(events["next_before_id"])
	if !msOK || !idOK {
		return
	}
	if token := s.encodeEventsCursor(eventsCursor{BeforeMS: beforeMS, BeforeID: beforeID}); token != "" {
		events["next_cursor"] = token
	}
}

func (s *Server) analyticsEventsPageLimit() int {
	limit := s.cfg.MaxEventsPage
	if limit <= 0 {
		limit = 200
	}
	if limit > maxPublicEventsPage {
		return maxPublicEventsPage
	}
	return limit
}

func strictInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		if typed != float64(int64(typed)) {
			return 0, false
		}
		return int64(typed), true
	default:
		return 0, false
	}
}

func nonNegativeFinite(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	return value
}

func firstHeader(headers http.Header, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(headers.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func safeDisplayURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "http", "https", "socks", "socks5", "socks5h":
	default:
		return ""
	}
	host := cleanText(parsed.Host, 200)
	pathValue := cleanText(parsed.Path, 240)
	if host == "" || host == "敏感错误详情已隐藏" || host == "内部错误详情已隐藏" || pathValue == "敏感错误详情已隐藏" || pathValue == "内部错误详情已隐藏" {
		return ""
	}
	return scheme + "://" + host + pathValue
}

func countDashboardModels(models dashboardModelList) int {
	rows := models.Data
	if len(rows) == 0 {
		rows = models.Models
	}
	seen := make(map[string]struct{}, len(rows))
	unnamed := 0
	for _, row := range rows {
		name := strings.TrimSpace(stringValue(row["id"], row["model"], row["name"]))
		if name == "" {
			unnamed++
			continue
		}
		seen[name] = struct{}{}
	}
	return len(seen) + unnamed
}

func validateObjectKeys(value map[string]any, name string, allowed ...string) error {
	allowedKeys := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		allowedKeys[key] = true
	}
	for key := range value {
		if !allowedKeys[key] {
			return fmt.Errorf("unsupported %s field: %s", name, key)
		}
	}
	return nil
}

func (s *Server) writeUpstreamError(w http.ResponseWriter, err error) {
	var upstream *cpamp.UpstreamError
	if errors.As(err, &upstream) {
		if upstream.Status == http.StatusUnauthorized {
			writeError(w, http.StatusBadGateway, "CPAMP rejected the configured admin key")
			return
		}
		writeError(w, http.StatusBadGateway, "CPAMP request failed")
		return
	}
	s.logger.Warn("CPAMP request failed", "error", err)
	writeError(w, http.StatusBadGateway, "CPAMP is unavailable")
}

// loadAllAliases returns CPAMP's complete alias history. CPAMP uses these
// persisted mappings to label historical dashboard, analytics, and monitoring
// rows even after the corresponding CPA API key has been removed.
func (s *Server) loadAllAliases(ctx context.Context) (apiKeyAliasEnvelope, error) {
	var aliases apiKeyAliasEnvelope
	if err := s.cpamp.GetJSON(ctx, "/v0/management/api-key-aliases", nil, &aliases); err != nil {
		return apiKeyAliasEnvelope{}, err
	}
	return aliases, nil
}

// loadActiveAliases is used only for the standalone Viewer alias list. Rows
// returned by analytics carry their historical alias inline, while this list
// stays limited to keys that are still present in the live CPA configuration.
func (s *Server) loadActiveAliases(ctx context.Context) (apiKeyAliasEnvelope, error) {
	aliases, err := s.loadAllAliases(ctx)
	if err != nil {
		return apiKeyAliasEnvelope{}, err
	}
	var active activeAPIKeyEnvelope
	if err := s.cpamp.GetJSON(ctx, "/v0/management/api-keys", nil, &active); err != nil {
		return apiKeyAliasEnvelope{}, err
	}
	aliases.Items = filterActiveAliases(aliases.Items, active.APIKeys)
	return aliases, nil
}

// filterActiveAliases mirrors CPAMP's own dropdown behavior: SQLite alias rows
// are historical mappings, while only hashes of currently configured API keys
// are eligible for display in the Viewer.
func filterActiveAliases(aliases []apiKeyAlias, activeKeys []string) []apiKeyAlias {
	activeHashes := make(map[string]struct{}, len(activeKeys))
	for _, key := range activeKeys {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		sum := sha256.Sum256([]byte(trimmed))
		activeHashes[hex.EncodeToString(sum[:])] = struct{}{}
	}
	filtered := make([]apiKeyAlias, 0, len(aliases))
	for _, alias := range aliases {
		hash := strings.ToLower(strings.TrimSpace(alias.APIKeyHash))
		if !apiKeyHashPattern.MatchString(hash) {
			continue
		}
		if _, active := activeHashes[hash]; active {
			filtered = append(filtered, alias)
		}
	}
	return filtered
}

func sanitizeDashboardUpstream(upstream *dashboardUpstream, aliases apiKeyAliasEnvelope) {
	aliasByHash, _ := aliasMaps(aliases)
	for index := range upstream.TrafficTimeline {
		upstream.TrafficTimeline[index].Label = cleanText(upstream.TrafficTimeline[index].Label, 80)
	}
	for index := range upstream.TodayRequestHealthTimeline.Points {
		upstream.TodayRequestHealthTimeline.Points[index].Tone = cleanText(upstream.TodayRequestHealthTimeline.Points[index].Tone, 40)
	}
	for index := range upstream.TokenMix {
		upstream.TokenMix[index].Key = cleanText(upstream.TokenMix[index].Key, 80)
	}
	for index := range upstream.ModelCostRank {
		upstream.ModelCostRank[index].Model = cleanText(upstream.ModelCostRank[index].Model, 120)
	}
	for index := range upstream.TopModelsToday {
		upstream.TopModelsToday[index].Model = cleanText(upstream.TopModelsToday[index].Model, 120)
	}
	for index := range upstream.RecentFailures {
		failure := &upstream.RecentFailures[index]
		rawHash := strings.ToLower(strings.TrimSpace(failure.APIKeyHash))
		if alias := aliasByHash[rawHash]; alias != "" {
			failure.APIKeyAlias = alias
		} else {
			failure.APIKeyAlias = cleanText(failure.APIKeyAlias, 80)
		}
		failure.APIKeyHash = ""
		if rawHash != "" {
			failure.APIKeyID = pseudonym(rawHash)
		} else if failure.APIKeyID != "" {
			failure.APIKeyID = pseudonym(failure.APIKeyID)
		}
		failure.Model = cleanText(failure.Model, 120)
		if raw := strings.TrimSpace(failure.SourceHash); raw != "" {
			failure.SourceID = pseudonym(raw)
		} else if raw := strings.TrimSpace(failure.Source); raw != "" {
			failure.SourceID = pseudonym(raw)
		}
		failure.SourceDisplay = cleanText(failure.Source, 160)
		failure.Source = ""
		failure.SourceHash = ""
		if raw := strings.TrimSpace(failure.AuthIndex); raw != "" {
			failure.AuthID = pseudonym(raw)
		}
		failure.AuthIndex = ""
		if raw := strings.TrimSpace(failure.AccountSnapshot); raw != "" {
			failure.AccountID = pseudonym(raw)
			failure.AccountDisplay = cleanText(raw, 160)
		}
		failure.AccountSnapshot = ""
		if raw := strings.TrimSpace(failure.AuthLabelSnapshot); raw != "" {
			failure.AuthLabelID = pseudonym(raw)
			failure.AuthLabelDisplay = cleanText(raw, 160)
		}
		failure.AuthLabelSnapshot = ""
		if raw := strings.TrimSpace(failure.AuthProjectIDSnapshot); raw != "" {
			failure.ProjectID = pseudonym(raw)
			failure.ProjectDisplay = cleanText(raw, 160)
		}
		failure.AuthProjectIDSnapshot = ""
		failure.AuthProviderSnapshot = cleanText(failure.AuthProviderSnapshot, 80)
		failure.Endpoint = cleanURLPath(failure.Endpoint)
		failure.FailSummary = cleanText(failure.FailSummary, 240)
		failure.HeaderQuotaPlanType = cleanText(failure.HeaderQuotaPlanType, 120)
		failure.HeaderErrorKind = cleanText(failure.HeaderErrorKind, 120)
		failure.HeaderErrorCode = cleanText(failure.HeaderErrorCode, 120)
	}
}

func aliasMaps(aliases apiKeyAliasEnvelope) (map[string]string, map[string]string) {
	aliasByHash := make(map[string]string, len(aliases.Items))
	hashByID := make(map[string]string, len(aliases.Items))
	for _, item := range aliases.Items {
		rawHash := strings.ToLower(strings.TrimSpace(item.APIKeyHash))
		alias := cleanText(item.Alias, 80)
		if !apiKeyHashPattern.MatchString(rawHash) || alias == "" {
			continue
		}
		aliasByHash[rawHash] = alias
		hashByID[pseudonym(rawHash)] = rawHash
	}
	return aliasByHash, hashByID
}

type analyticsIdentityIndex map[string]map[string]string

func newAnalyticsIdentityIndex(aliases apiKeyAliasEnvelope) analyticsIdentityIndex {
	index := make(analyticsIdentityIndex, len(analyticsIdentityFilterGroups))
	for _, group := range analyticsIdentityFilterGroups {
		index[group.Target] = make(map[string]string)
	}
	_, hashByID := aliasMaps(aliases)
	for id, rawHash := range hashByID {
		index["api_key_hashes"][id] = rawHash
	}
	return index
}

func (index analyticsIdentityIndex) add(target, raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 512 {
		return
	}
	if target == "api_key_hashes" {
		raw = strings.ToLower(raw)
		if !apiKeyHashPattern.MatchString(raw) {
			return
		}
	}
	values := index[target]
	if values == nil {
		values = make(map[string]string)
		index[target] = values
	}
	values[pseudonym(raw)] = raw
}

func hasAnalyticsIdentityFilters(payload map[string]any) bool {
	filters, ok := payload["filters"].(map[string]any)
	if !ok {
		return false
	}
	for key := range filters {
		if viewerIdentityFilters[key] {
			return true
		}
	}
	return false
}

func (s *Server) loadAnalyticsIdentityIndex(ctx context.Context, payload map[string]any, aliases apiKeyAliasEnvelope) (analyticsIdentityIndex, error) {
	index := newAnalyticsIdentityIndex(aliases)
	if !hasAnalyticsIdentityFilters(payload) {
		return index, nil
	}

	discoveryPayload := map[string]any{
		"from_ms": payload["from_ms"],
		"to_ms":   payload["to_ms"],
		"include": map[string]any{
			"filter_options":   true,
			"filter_selectors": true,
		},
	}
	if filters, ok := payload["filters"].(map[string]any); ok {
		if _, needsCredentials := filters["credential_ids"]; needsCredentials {
			discoveryPayload["include"].(map[string]any)["credential_stats"] = true
		}
	}
	for _, key := range []string{"now_ms", "time_zone", "search_query"} {
		if value, exists := payload[key]; exists {
			discoveryPayload[key] = value
		}
	}

	var discovery map[string]any
	if err := s.cpamp.PostJSON(ctx, "/v0/management/monitoring/analytics", discoveryPayload, &discovery); err != nil {
		return nil, err
	}
	collectAnalyticsIdentityIndex(index, discovery["filter_options"])
	collectAnalyticsIdentityIndex(index, discovery["filter_selectors"])
	collectAnalyticsCredentialIdentityIndex(index, discovery["credential_stats"])
	return index, nil
}

func collectAnalyticsCredentialIdentityIndex(index analyticsIdentityIndex, value any) {
	rows, ok := value.([]any)
	if !ok {
		return
	}
	for _, item := range rows {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if raw, ok := row["id"].(string); ok {
			index.add("credential_ids", raw)
		}
	}
}

func collectAnalyticsIdentityIndex(index analyticsIdentityIndex, value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			switch key {
			case "api_key_hash", "api_key_hashes":
				addAnalyticsIdentityValue(index, "api_key_hashes", item)
			case "account_snapshot", "auth_label_snapshot", "source":
				addAnalyticsIdentityValue(index, "accounts", item)
			case "auth_file_snapshot", "auth_files":
				addAnalyticsIdentityValue(index, "auth_files", item)
			case "auth_index", "auth_indices":
				addAnalyticsIdentityValue(index, "auth_indices", item)
			case "source_hash", "source_hashes":
				addAnalyticsIdentityValue(index, "source_hashes", item)
			case "auth_project_id_snapshot", "project_ids":
				addAnalyticsIdentityValue(index, "project_ids", item)
			}
			collectAnalyticsIdentityIndex(index, item)
		}
	case []any:
		for _, item := range typed {
			collectAnalyticsIdentityIndex(index, item)
		}
	}
}

func addAnalyticsIdentityValue(index analyticsIdentityIndex, target string, value any) {
	switch typed := value.(type) {
	case string:
		index.add(target, typed)
	case []any:
		for _, item := range typed {
			if raw, ok := item.(string); ok {
				index.add(target, raw)
			}
		}
	}
}

func resolveAnalyticsIdentityFilters(payload map[string]any, index analyticsIdentityIndex) error {
	filters, ok := payload["filters"].(map[string]any)
	if !ok {
		return nil
	}
	for _, group := range analyticsIdentityFilterGroups {
		var (
			rawValues any
			exists    bool
		)
		for _, source := range group.Sources {
			if value, found := filters[source]; found && !exists {
				rawValues, exists = value, true
			}
			delete(filters, source)
		}
		if !exists {
			continue
		}
		values, ok := rawValues.([]any)
		if !ok {
			return fmt.Errorf("%s must be an array of Viewer IDs", group.Target)
		}
		resolved := make([]any, 0, len(values))
		for _, value := range values {
			candidate := strings.TrimSpace(stringValue(value))
			if !apiKeyViewIDPattern.MatchString(candidate) {
				return fmt.Errorf("%s must contain only Viewer IDs", group.Target)
			}
			raw := index[group.Target][candidate]
			if raw == "" {
				return fmt.Errorf("unknown Viewer identity for filter %s", group.Target)
			}
			resolved = append(resolved, raw)
		}
		filters[group.Target] = resolved
	}
	return nil
}

func resolveAliasFilters(payload map[string]any, aliases apiKeyAliasEnvelope) error {
	return resolveAnalyticsIdentityFilters(payload, newAnalyticsIdentityIndex(aliases))
}

func attachAnalyticsAliases(response map[string]any, aliases apiKeyAliasEnvelope) {
	aliasByHash, _ := aliasMaps(aliases)
	var attach func(any)
	attach = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			rawHash := strings.ToLower(strings.TrimSpace(stringValue(typed["api_key_hash"])))
			if alias := aliasByHash[rawHash]; alias != "" {
				typed["api_key_alias"] = alias
			}
			for _, child := range typed {
				attach(child)
			}
		case []any:
			for _, child := range typed {
				attach(child)
			}
		}
	}
	attach(response)
	if heatmap, ok := response["heatmap"].([]any); ok {
		for _, item := range heatmap {
			point, ok := item.(map[string]any)
			if !ok {
				continue
			}
			contributors, ok := point["api_key_contributors"].([]any)
			if !ok {
				continue
			}
			for _, contributor := range contributors {
				row, ok := contributor.(map[string]any)
				if !ok {
					continue
				}
				rawHash := strings.ToLower(strings.TrimSpace(stringValue(row["key"])))
				if alias := aliasByHash[rawHash]; alias != "" {
					row["api_key_alias"] = alias
				}
			}
		}
	}
}

func safeEndpoint(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func healthLabel(healthy bool) string {
	if healthy {
		return "正常"
	}
	return "不可用"
}

func usageMigrationReady(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ready", "migrated", "completed":
		return true
	default:
		return false
	}
}

func nonNilTimeline(items []dashboardTimelinePoint) []dashboardTimelinePoint {
	if items == nil {
		return []dashboardTimelinePoint{}
	}
	return items
}

func nonNilTokenMix(items []dashboardTokenMix) []dashboardTokenMix {
	if items == nil {
		return []dashboardTokenMix{}
	}
	return items
}

func nonNilDashboardModels(items []dashboardModel) []dashboardModel {
	if items == nil {
		return []dashboardModel{}
	}
	return items
}

func projectDashboardChannels(items []dashboardChannelUpstream) []dashboardChannel {
	projected := make([]dashboardChannel, 0, len(items))
	for _, item := range items {
		identity := strings.TrimSpace(strings.Join([]string{
			item.AuthIndex,
			item.AccountSnapshot,
			item.AuthLabelSnapshot,
			item.Source,
		}, "|"))
		if identity == "" {
			identity = item.AuthProviderSnapshot
		}
		id := pseudonym(identity)
		provider := cleanText(strings.ToLower(strings.TrimSpace(item.AuthProviderSnapshot)), 40)
		sourceDisplay := cleanText(item.Source, 160)
		accountDisplay := cleanText(item.AccountSnapshot, 160)
		authLabelDisplay := cleanText(item.AuthLabelSnapshot, 160)
		label := authLabelDisplay
		if label == "" {
			label = accountDisplay
		}
		if label == "" {
			label = sourceDisplay
		}
		if label == "" {
			label = provider
		}
		if label == "" {
			label = "渠道 " + id[len(id)-6:]
		}
		authID := pseudonymIfPresent(item.AuthIndex)
		sourceID := pseudonymIfPresent(item.Source)
		accountID := pseudonymIfPresent(item.AccountSnapshot)
		authLabelID := pseudonymIfPresent(item.AuthLabelSnapshot)
		projected = append(projected, dashboardChannel{
			ID:               id,
			AuthID:           authID,
			SourceID:         sourceID,
			AccountID:        accountID,
			AuthLabelID:      authLabelID,
			Channel:          label,
			SourceDisplay:    sourceDisplay,
			AccountDisplay:   accountDisplay,
			AuthLabelDisplay: authLabelDisplay,
			Provider:         provider,
			Calls:            item.Calls,
			Failures:         item.Failures,
			FailureRate:      item.FailureRate,
			SuccessRate:      item.SuccessRate,
			Tokens:           item.Tokens,
			Cost:             item.Cost,
			AverageLatencyMS: item.AverageLatencyMS,
			Tone:             cleanText(item.Tone, 20),
		})
	}
	return projected
}

func nonNilDashboardFailures(items []dashboardFailure) []dashboardFailure {
	if items == nil {
		return []dashboardFailure{}
	}
	return items
}

func latestSnapshots(items []map[string]any) []map[string]any {
	sort.Slice(items, func(i, j int) bool { return intValue(items[i]["timestamp_ms"]) > intValue(items[j]["timestamp_ms"]) })
	return items
}

func findSnapshot(items []map[string]any, name, authIndex, display string) map[string]any {
	name, authIndex, display = strings.ToLower(name), strings.ToLower(authIndex), strings.ToLower(display)
	for _, item := range items {
		if name != "" && strings.ToLower(stringValue(item["auth_file_snapshot"])) == name {
			return item
		}
	}
	for _, item := range items {
		if authIndex != "" && strings.ToLower(stringValue(item["auth_index"])) == authIndex {
			return item
		}
	}
	for _, item := range items {
		if display != "" && strings.ToLower(stringValue(item["account_snapshot"])) == display {
			return item
		}
	}
	return nil
}

func projectQuotaAccount(file map[string]any, snapshots []map[string]any) (quotaAccount, bool) {
	provider := canonicalQuotaProvider(stringValue(file["provider"], file["type"]))
	if !isQuotaProvider(provider) {
		return quotaAccount{}, false
	}
	name := stringValue(file["name"])
	authIndex := stringValue(file["authIndex"], file["auth_index"])
	rawDisplay := stringValue(file["account_snapshot"], file["account"], file["email"], file["label"])
	display := cleanText(rawDisplay, 160)
	if display == "" {
		display = "未命名账号 " + pseudonym(name + "|" + authIndex)[5:11]
	}
	snapshot := findCredentialQuotaSnapshot(snapshots, provider, name, authIndex)
	windows, plan, observedAt := projectQuotaSnapshot(snapshot)
	if provider == "codex" {
		windows, plan, observedAt = projectCodexQuota(file, snapshots)
		idToken, _ := file["id_token"].(map[string]any)
		if currentPlan := stringValue(file["plan"], file["plan_type"], idToken["plan_type"]); currentPlan != "" {
			plan = currentPlan
		}
	}
	if plan == "" {
		idToken, _ := file["id_token"].(map[string]any)
		plan = stringValue(file["plan"], file["plan_type"], idToken["plan_type"])
	}
	status := cleanText(stringValue(file["status"]), 80)
	if status == "" {
		status = "unknown"
	}
	return quotaAccount{
		ID:            pseudonym(name + "|" + authIndex),
		Provider:      cleanText(provider, 40),
		DisplayName:   display,
		Plan:          cleanText(plan, 80),
		Status:        status,
		StatusMessage: cleanText(stringValue(file["statusMessage"], file["status_message"]), 180),
		Disabled:      boolValue(file["disabled"]),
		Windows:       windows,
		UpdatedAtMS:   observedAt,
		Success:       intValue(file["success"]),
		Failed:        intValue(file["failed"]),
	}, true
}

func projectQuotaSnapshot(snapshot map[string]any) ([]quotaWindow, string, int64) {
	if snapshot == nil {
		return []quotaWindow{}, "", 0
	}
	metadata, _ := snapshot["response_metadata"].(map[string]any)
	quota, _ := metadata["quota"].(map[string]any)
	plan := stringValue(snapshot["header_quota_plan_type"], quota["plan_type"])
	observedAt := intValue(snapshot["timestamp_ms"])
	windows := make([]quotaWindow, 0, 2)
	for _, spec := range []struct{ Key, ID, Label string }{{"primary", "primary", "主要窗口"}, {"secondary", "secondary", "次要窗口"}} {
		window, _ := quota[spec.Key].(map[string]any)
		used, validUsed := quotaNumber(window["used_percent"])
		if !validUsed || used < 0 || used > 100 {
			continue
		}
		resetAt := intValue(window["reset_at_ms"])
		windowMins, validMinutes := quotaNumber(window["window_minutes"])
		if !validMinutes || windowMins < 0 {
			windowMins = 0
		}
		if resetAt == 0 {
			resetAt = quotaRelativeReset(observedAt, window["reset_after_seconds"])
		}
		label := quotaWindowLabel(windowMins, spec.Label)
		windows = append(windows, quotaWindow{ID: spec.ID, Label: label, Used: clamp(used), Remaining: clamp(100 - used), ResetAtMS: resetAt, WindowMins: windowMins, ObservedAt: observedAt})
	}
	if len(windows) == 0 {
		used, validUsed := quotaNumber(snapshot["header_quota_used_percent"])
		if !validUsed {
			used, validUsed = quotaNumber(quota["used_percent"])
		}
		if validUsed && used >= 0 && used <= 100 {
			windows = append(windows, quotaWindow{ID: "observed", Label: "已观测额度", Used: clamp(used), Remaining: clamp(100 - used), ResetAtMS: intValue(snapshot["header_quota_recover_at_ms"], quota["recover_at_ms"]), ObservedAt: observedAt})
		}
	}
	return windows, plan, observedAt
}

func quotaWindowLabel(minutes float64, fallback string) string {
	switch {
	case minutes >= 295 && minutes <= 305:
		return "5 小时额度"
	case minutes >= 10000 && minutes <= 10200:
		return "周额度"
	case minutes >= 40000 && minutes <= 45000:
		return "月额度"
	default:
		return fallback
	}
}

var analyticsFields = map[string]map[string]bool{
	"root": {
		"coverage":        true,
		"generated_at_ms": true, "granularity": true, "summary": true, "timeline": true,
		"summary_comparison": true, "hourly_distribution": true, "model_share": true,
		"model_stats": true, "channel_share": true, "failure_sources": true,
		"account_stats": true, "credential_stats": true, "credential_timeline": true,
		"api_key_timeline": true,
		"api_key_stats":    true, "filter_options": true, "filter_selectors": true,
		"task_buckets": true, "recent_failures": true, "events": true,
		"drilldown_preview": true, "heatmap": true, "anomaly_points": true,
	},
	"summary": {
		"total_calls": true, "success_calls": true, "failure_calls": true, "success_rate": true,
		"input_tokens": true, "output_tokens": true, "cached_tokens": true, "reasoning_tokens": true,
		"total_tokens": true, "total_cost": true, "average_latency_ms": true, "p95_latency_ms": true,
		"rpm_30m": true, "tpm_30m": true, "cache_read_tokens": true, "cache_creation_tokens": true,
		"cache_hit_rate": true, "average_cost_per_call": true, "p95_ttft_ms": true, "zero_token_calls": true,
		"avg_daily_requests": true, "avg_daily_tokens": true, "approx_tasks": true,
		"approx_task_failures": true, "approx_task_success_rate": true, "zero_token_models": true,
	},
	"summary_comparison": {
		"from_ms": true, "to_ms": true, "total_calls": true, "success_calls": true,
		"failure_calls": true, "success_rate": true, "total_tokens": true, "total_cost": true,
	},
	"timeline": {
		"bucket_ms": true, "bucket_end_ms": true, "label": true, "calls": true, "tokens": true,
		"success": true, "failure": true, "input_tokens": true, "output_tokens": true,
		"cached_tokens": true, "cache_read_tokens": true, "cache_creation_tokens": true,
		"cache_hit_rate": true, "reasoning_tokens": true, "total_tokens": true, "cost": true,
		"average_latency_ms": true, "p95_latency_ms": true, "p95_ttft_ms": true,
		"success_rate": true, "failure_rate": true,
	},
	"hourly_distribution": {"hour": true, "calls": true, "tokens": true},
	"model_share":         {"model": true, "calls": true, "tokens": true, "cost": true},
	"account_model_stats": {
		"model": true, "calls": true, "success_calls": true, "failure_calls": true,
		"success_rate": true, "input_tokens": true, "output_tokens": true,
		"cached_tokens": true, "cache_read_tokens": true, "cache_creation_tokens": true,
		"cache_hit_tokens": true, "cache_hit_input_tokens": true, "cache_hit_rate": true,
		"total_tokens": true, "cost": true, "last_seen_ms": true,
	},
	"model_stats": {
		"model": true, "calls": true, "success_calls": true, "failure_calls": true,
		"success_rate": true, "input_tokens": true, "output_tokens": true, "cached_tokens": true,
		"cache_read_tokens": true, "cache_creation_tokens": true, "cache_hit_tokens": true,
		"cache_hit_input_tokens": true, "cache_hit_rate": true, "total_tokens": true, "cost": true,
	},
	"channel_share": {
		"auth_index": true, "source": true, "account_snapshot": true,
		"auth_label_snapshot": true, "auth_provider_snapshot": true, "auth_account_id_snapshot": true, "calls": true,
		"success": true, "failure": true, "tokens": true, "cost": true,
		"average_latency_ms": true,
	},
	"failure_sources": {
		"source": true, "source_hash": true, "auth_index": true, "account_snapshot": true,
		"auth_label_snapshot": true, "auth_provider_snapshot": true, "auth_account_id_snapshot": true, "calls": true,
		"failure": true, "last_seen_ms": true, "average_latency_ms": true,
	},
	"account_stats": {
		"id": true, "account_snapshot": true, "auth_label_snapshot": true, "auth_provider_snapshot": true,
		"auth_account_id_snapshot": true,
		"auth_indices":             true, "sources": true, "source_hashes": true,
		"calls": true, "success_calls": true, "failure_calls": true, "success_rate": true,
		"input_tokens": true, "output_tokens": true, "cached_tokens": true,
		"cache_read_tokens": true, "cache_creation_tokens": true, "total_tokens": true,
		"cost": true, "average_latency_ms": true, "last_seen_ms": true, "models": true,
	},
	"credential_stats": {
		"id": true, "auth_file_snapshot": true, "auth_index": true,
		"source": true, "source_hash": true, "account_snapshot": true,
		"auth_label_snapshot": true, "auth_provider_snapshot": true,
		"auth_account_id_snapshot": true, "auth_project_id_snapshot": true, "calls": true, "success_calls": true,
		"failure_calls": true, "success_rate": true, "input_tokens": true,
		"output_tokens": true, "cached_tokens": true, "cache_read_tokens": true,
		"cache_creation_tokens": true, "total_tokens": true, "cost": true,
		"average_latency_ms": true, "last_seen_ms": true, "models": true,
	},
	"credential_timeline": {
		"id": true, "label": true, "auth_file_snapshot": true, "auth_index": true,
		"source": true, "source_hash": true, "account_snapshot": true,
		"auth_label_snapshot": true, "auth_provider_snapshot": true,
		"auth_account_id_snapshot": true, "auth_project_id_snapshot": true, "bucket_ms": true, "bucket_label": true,
		"calls": true, "tokens": true, "success": true, "failure": true,
		"input_tokens": true, "output_tokens": true, "cached_tokens": true,
		"cache_read_tokens": true, "cache_creation_tokens": true, "reasoning_tokens": true,
		"total_tokens": true, "cost": true, "average_latency_ms": true,
		"success_rate": true, "failure_rate": true,
	},
	"api_key_timeline": {
		"api_key_hash": true, "api_key_id": true, "api_key_alias": true,
		"bucket_ms": true, "bucket_label": true, "calls": true, "tokens": true,
		"success": true, "failure": true, "input_tokens": true, "output_tokens": true,
		"cached_tokens": true, "cache_read_tokens": true, "cache_creation_tokens": true,
		"reasoning_tokens": true, "total_tokens": true, "cost": true,
		"average_latency_ms": true, "success_rate": true, "failure_rate": true,
	},
	"api_key_stats": {
		"id": true, "api_key_hash": true, "api_key_id": true, "api_key_alias": true,
		"account_snapshot": true, "auth_label_snapshot": true, "auth_provider_snapshot": true,
		"auth_account_id_snapshot": true,
		"auth_indices":             true, "sources": true, "source_hashes": true, "calls": true,
		"success_calls": true, "failure_calls": true, "success_rate": true,
		"input_tokens": true, "output_tokens": true, "cached_tokens": true,
		"cache_read_tokens": true, "cache_creation_tokens": true, "total_tokens": true,
		"cost": true, "average_latency_ms": true, "last_seen_ms": true,
		"models": true, "contexts": true,
	},
	"api_key_contexts": {
		"id": true, "account_snapshot": true, "auth_label_snapshot": true,
		"auth_provider_snapshot": true, "auth_account_id_snapshot": true, "auth_index": true, "source": true,
		"source_hash": true, "calls": true, "success_calls": true,
		"failure_calls": true, "success_rate": true, "failure_rate": true,
		"total_tokens": true, "cost": true, "average_latency_ms": true, "last_seen_ms": true,
	},
	"recent_failures": {
		"timestamp_ms": true, "model": true, "api_key_hash": true, "api_key_id": true, "api_key_alias": true,
		"source": true, "source_hash": true, "auth_index": true,
		"account_snapshot": true, "auth_label_snapshot": true, "auth_provider_snapshot": true,
		"auth_account_id_snapshot": true, "auth_project_id_snapshot": true,
		"endpoint": true, "duration_ms": true, "fail_status_code": true, "fail_summary": true,
		"header_quota_recover_at_ms": true, "header_quota_used_percent": true,
		"header_quota_plan_type": true, "header_error_kind": true, "header_error_code": true,
	},
	"events": {
		"items": true, "has_more": true, "next_before_ms": true, "next_cursor": true, "total_count": true,
	},
	"event_items": {
		"event_hash": true, "timestamp_ms": true, "model": true,
		"analytics_model": true, "requested_model": true, "resolved_model": true, "response_model": true,
		"endpoint": true, "method": true, "path": true, "auth_index": true, "source": true,
		"source_hash": true, "api_key_hash": true, "api_key_id": true, "api_key_alias": true,
		"account_snapshot": true, "auth_label_snapshot": true, "auth_file_snapshot": true,
		"auth_provider_snapshot": true, "auth_account_id_snapshot": true, "auth_project_id_snapshot": true,
		"reasoning_effort": true, "service_tier": true, "executor_type": true,
		"request_service_tier": true, "response_service_tier": true,
		"generate": true, "stream": true,
		"input_tokens": true, "output_tokens": true, "cached_tokens": true,
		"cache_read_tokens": true, "cache_creation_tokens": true, "reasoning_tokens": true,
		"total_tokens": true, "latency_ms": true, "ttft_ms": true, "failed": true,
		"fail_status_code": true, "fail_summary": true, "header_quota_recover_at_ms": true,
		"header_quota_used_percent": true, "header_quota_plan_type": true,
		"header_error_kind": true, "header_error_code": true,
	},
	"filter_options": {
		"account_stats": true, "api_key_stats": true, "channel_share": true,
		"model_stats": true, "models": true, "api_key_hashes": true, "providers": true,
		"accounts": true, "account_count": true, "api_key_count": true,
		"auth_files": true, "project_ids": true, "request_types": true,
		"header_error_kinds": true, "header_error_codes": true, "header_quota_plans": true,
	},
	"task_buckets": {
		"bucket_key": true, "total": true, "success": true, "failure": true,
		"first_ms": true, "last_ms": true, "source": true, "source_hash": true,
		"auth_index": true, "models": true, "endpoints": true, "input_tokens": true,
		"output_tokens": true, "cached_tokens": true, "cache_read_tokens": true,
		"cache_creation_tokens": true, "total_tokens": true, "average_latency_ms": true,
		"max_latency_ms": true,
	},
	"heatmap": {
		"weekday": true, "hour": true, "calls": true, "success": true, "failure": true,
		"tokens": true, "cost": true, "failure_rate": true, "model_contributors": true,
		"api_key_contributors": true, "provider_contributors": true,
	},
	"heatmap_contributors": {
		"key": true, "label": true, "calls": true, "success": true, "failure": true,
		"tokens": true, "cost": true, "failure_rate": true, "share": true,
	},
	"heatmap_api_key_contributors": {
		"key": true, "label": true, "api_key_alias": true, "calls": true,
		"success": true, "failure": true, "tokens": true, "cost": true,
		"failure_rate": true, "share": true,
	},
	"anomaly_points": {
		"bucket_ms": true, "bucket_end_ms": true, "timestamp_ms": true, "label": true,
		"severity": true, "metric_keys": true, "metric": true, "reason": true, "score": true,
		"calls": true, "total_tokens": true, "cost": true, "failure_rate": true,
		"request_change": true, "cost_change": true, "tokens_per_request_change": true,
		"cache_hit_rate_change": true, "failure_rate_change": true, "latency_p95_change": true,
	},
}

var analyticsChildren = map[string]map[string]string{
	"root": {
		"summary": "summary", "summary_comparison": "summary_comparison", "timeline": "timeline",
		"hourly_distribution": "hourly_distribution", "model_share": "model_share",
		"model_stats": "model_stats", "channel_share": "channel_share",
		"failure_sources": "failure_sources", "account_stats": "account_stats",
		"credential_stats": "credential_stats", "credential_timeline": "credential_timeline",
		"api_key_timeline": "api_key_timeline",
		"api_key_stats":    "api_key_stats", "filter_options": "filter_options",
		"filter_selectors": "filter_options", "task_buckets": "task_buckets",
		"recent_failures": "recent_failures", "events": "events",
		"drilldown_preview": "events", "heatmap": "heatmap", "anomaly_points": "anomaly_points",
	},
	"account_stats":    {"models": "account_model_stats"},
	"credential_stats": {"models": "account_model_stats"},
	"api_key_stats": {
		"models": "account_model_stats", "contexts": "api_key_contexts",
	},
	"filter_options": {
		"account_stats": "account_stats", "api_key_stats": "api_key_stats",
		"channel_share": "channel_share", "model_stats": "model_stats",
	},
	"events": {"items": "event_items"},
	"heatmap": {
		"model_contributors":    "heatmap_contributors",
		"api_key_contributors":  "heatmap_api_key_contributors",
		"provider_contributors": "heatmap_contributors",
	},
}

func projectAnalytics(response map[string]any) {
	projectAnalyticsWithLimit(response, maxPublicEventsPage)
}

func projectAnalyticsWithLimit(response map[string]any, eventsLimit int) {
	projected, _ := projectAnalyticsValue(response, "root").(map[string]any)
	if eventsLimit <= 0 || eventsLimit > maxPublicEventsPage {
		eventsLimit = maxPublicEventsPage
	}
	clampAnalyticsEventContainer(projected, "events", eventsLimit)
	clampAnalyticsEventContainer(projected, "drilldown_preview", min(eventsLimit, maxDrilldownPreviewEvents))
	if failures, ok := projected["recent_failures"].([]any); ok && len(failures) > 100 {
		projected["recent_failures"] = failures[:100]
	}
	clear(response)
	for key, value := range projected {
		response[key] = value
	}
}

func clampAnalyticsEventContainer(response map[string]any, key string, limit int) {
	container, ok := response[key].(map[string]any)
	if !ok {
		return
	}
	items, ok := container["items"].([]any)
	if !ok || len(items) <= limit {
		return
	}
	container["items"] = items[:limit]
	container["has_more"] = true
	// A cursor generated for the upstream page tail is no longer valid after
	// local truncation; returning it could skip rows on the next request.
	delete(container, "next_cursor")
	delete(container, "next_before_id")
	if last, ok := items[limit-1].(map[string]any); ok {
		if timestamp, ok := last["timestamp_ms"]; ok {
			container["next_before_ms"] = timestamp
		}
	}
}

func projectAnalyticsValue(value any, context string) any {
	switch typed := value.(type) {
	case map[string]any:
		allowed := analyticsFields[context]
		if allowed == nil {
			return nil
		}
		projected := make(map[string]any)
		rawAPIKeyHash, hasAPIKeyHash := typed["api_key_hash"].(string)
		syntheticAPIKey := isSyntheticAPIKey(rawAPIKeyHash)
		for key, item := range typed {
			if !allowed[key] {
				continue
			}
			if context == "root" && key == "coverage" {
				if safe := projectUsageCoverage(item); safe != nil {
					projected[key] = safe
				}
				continue
			}
			if child := analyticsChildren[context][key]; child != "" {
				if safe := projectAnalyticsValue(item, child); safe != nil {
					projected[key] = safe
				}
				continue
			}
			switch key {
			case "generate", "stream":
				// Explicit false is meaningful; absent/null/invalid values must
				// stay unknown rather than being coerced to a request setting.
				if value, ok := item.(bool); ok {
					projected[key] = value
				}
			case "requested_model", "resolved_model", "response_model":
				if raw, ok := item.(string); ok {
					raw = strings.TrimSpace(raw)
					// Model comparison requires the exact safe value. Truncating
					// or redacting one side could create a false mismatch.
					if raw != "" && cleanText(raw, 240) == raw && raw != "敏感错误详情已隐藏" && raw != "内部错误详情已隐藏" {
						projected[key] = raw
					}
				}
			case "request_service_tier", "response_service_tier":
				if raw, ok := item.(string); ok && strings.TrimSpace(raw) != "" {
					projected[key] = cleanText(raw, 180)
				}
			case "api_key_hash":
				if raw, ok := item.(string); ok && strings.TrimSpace(raw) != "" {
					projected["api_key_id"] = pseudonym(raw)
					if syntheticAPIKey {
						projected["api_key_selectable"] = false
					}
				}
			case "api_key_hashes":
				if values, ok := pseudonymSelectableAPIKeyList(item); ok {
					projected["api_key_ids"] = values
				}
			case "key":
				if context == "heatmap_api_key_contributors" {
					if raw, ok := item.(string); ok && strings.TrimSpace(raw) != "" {
						projected["api_key_id"] = pseudonym(raw)
						if isSyntheticAPIKey(raw) {
							projected["api_key_selectable"] = false
						}
					}
					continue
				}
				if safe, ok := safeAnalyticsScalar(item); ok {
					projected[key] = safe
				}
			case "api_key_id", "id", "event_hash", "bucket_key":
				if key == "api_key_id" && hasAPIKeyHash {
					continue
				}
				if raw, ok := item.(string); ok && strings.TrimSpace(raw) != "" {
					projected[key] = pseudonym(raw)
				}
			case "auth_index":
				if raw, ok := item.(string); ok && strings.TrimSpace(raw) != "" {
					projected["auth_id"] = pseudonym(raw)
				}
			case "source_hash":
				if raw, ok := item.(string); ok && strings.TrimSpace(raw) != "" {
					projected["source_id"] = pseudonym(raw)
				}
			case "source":
				if raw, ok := item.(string); ok && strings.TrimSpace(raw) != "" {
					display := cleanText(raw, 160)
					if display != "敏感错误详情已隐藏" && display != "内部错误详情已隐藏" {
						projected["source"] = display
						projected["source_display"] = display
					}
				}
			case "account_snapshot":
				if raw, ok := item.(string); ok && strings.TrimSpace(raw) != "" {
					projected["account_id"] = pseudonym(raw)
					projected["account_display"] = cleanText(raw, 160)
				}
			case "auth_label_snapshot":
				if raw, ok := item.(string); ok && strings.TrimSpace(raw) != "" {
					projected["auth_label_id"] = pseudonym(raw)
					projected["auth_label_display"] = cleanText(raw, 160)
				}
			case "auth_file_snapshot":
				if raw, ok := item.(string); ok && strings.TrimSpace(raw) != "" {
					projected["auth_file_id"] = pseudonym(raw)
					projected["auth_file_display"] = cleanText(raw, 160)
				}
			case "auth_project_id_snapshot":
				if raw, ok := item.(string); ok && strings.TrimSpace(raw) != "" {
					projected["project_id"] = pseudonym(raw)
					projected["project_display"] = cleanText(raw, 160)
				}
			case "auth_account_id_snapshot":
				if raw, ok := item.(string); ok && strings.TrimSpace(raw) != "" {
					projected["account_subject_id"] = pseudonym(raw)
				}
			case "auth_indices":
				if values, ok := pseudonymAnalyticsList(item); ok {
					projected["auth_ids"] = values
				}
			case "source_hashes":
				if values, ok := pseudonymAnalyticsList(item); ok {
					projected["source_ids"] = values
				}
			case "accounts":
				if values, ok := pseudonymAnalyticsList(item); ok {
					projected["account_ids"] = values
				}
			case "auth_files":
				if values, options, ok := pseudonymAnalyticsOptions(item); ok {
					projected["auth_file_ids"] = values
					projected["auth_file_options"] = options
				}
			case "project_ids":
				if values, options, ok := pseudonymAnalyticsOptions(item); ok {
					projected["project_ids"] = values
					projected["project_options"] = options
				}
			case "endpoint", "path":
				if raw, ok := item.(string); ok {
					projected[key] = cleanURLPath(raw)
				}
			case "endpoints":
				if values, ok := item.([]any); ok {
					endpoints := make([]any, 0, len(values))
					for _, value := range values {
						if raw, ok := value.(string); ok {
							if safe := cleanURLPath(raw); safe != "" {
								endpoints = append(endpoints, safe)
							}
						}
					}
					projected[key] = endpoints
				}
			case "fail_summary", "header_error_kind", "header_error_code":
				if raw, ok := item.(string); ok {
					projected[key] = cleanText(raw, 240)
				}
			case "api_key_alias":
				if raw, ok := item.(string); ok {
					projected[key] = cleanText(raw, 80)
				}
			case "next_cursor":
				if raw, ok := item.(string); ok && eventsCursorPattern.MatchString(raw) {
					projected[key] = raw
				}
			default:
				if list, ok := item.([]any); ok {
					items := make([]any, 0, len(list))
					for _, entry := range list {
						if safe, ok := safeAnalyticsScalar(entry); ok {
							items = append(items, safe)
						}
					}
					projected[key] = items
				} else if safe, ok := safeAnalyticsScalar(item); ok {
					projected[key] = safe
				}
			}
		}
		// Aggregate rows can survive raw cleanup with an upstream grouping ID
		// but no client API key hash. Preserve their totals without turning the
		// grouping ID into a filter the identity resolver cannot authenticate.
		if context == "api_key_stats" && !apiKeyHashPattern.MatchString(strings.ToLower(strings.TrimSpace(rawAPIKeyHash))) {
			projected["api_key_selectable"] = false
			if _, exists := projected["api_key_id"]; !exists {
				projected["api_key_id"] = pseudonym(stringValue(typed["id"], "unknown-client-api-key"))
			}
		}
		return projected
	case []any:
		projected := make([]any, 0, len(typed))
		for _, item := range typed {
			if safe := projectAnalyticsValue(item, context); safe != nil {
				projected = append(projected, safe)
			}
		}
		return projected
	default:
		if context == "filter_options" {
			if safe, ok := safeAnalyticsScalar(typed); ok {
				return safe
			}
		}
		return nil
	}
}

func pseudonymAnalyticsList(value any) ([]any, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	projected := make([]any, 0, len(items))
	for _, item := range items {
		raw, ok := item.(string)
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		projected = append(projected, pseudonym(raw))
	}
	return projected, true
}

func pseudonymSelectableAPIKeyList(value any) ([]any, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	projected := make([]any, 0, len(items))
	for _, item := range items {
		raw, ok := item.(string)
		if !ok || strings.TrimSpace(raw) == "" || isSyntheticAPIKey(raw) {
			continue
		}
		projected = append(projected, pseudonym(raw))
	}
	return projected, true
}

func isSyntheticAPIKey(value string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "unknown-client-api-key:")
}

func pseudonymAnalyticsOptions(value any) ([]any, []any, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, nil, false
	}
	ids := make([]any, 0, len(items))
	options := make([]any, 0, len(items))
	for _, item := range items {
		raw, ok := item.(string)
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		id := pseudonym(raw)
		ids = append(ids, id)
		options = append(options, map[string]any{
			"id":      id,
			"display": cleanText(raw, 160),
		})
	}
	return ids, options, true
}

func safeAnalyticsScalar(value any) (any, bool) {
	switch typed := value.(type) {
	case nil:
		return nil, true
	case bool, float64, json.Number, int, int64:
		return typed, true
	case string:
		return cleanText(typed, 240), true
	default:
		return nil, false
	}
}

func isQuotaProvider(provider string) bool {
	switch provider {
	case "codex", "claude", "antigravity", "kimi", "xai", "devin", "meta":
		return true
	default:
		return false
	}
}

func pseudonym(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "view_" + hex.EncodeToString(sum[:6])
}

func pseudonymIfPresent(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return pseudonym(value)
}

func cleanText(value string, max int) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	lower := strings.ToLower(value)
	secretMarkers := []string{
		"bearer ", "basic ", "sk-", "access_token", "refresh_token", "id_token", "dca:", "llm|",
		"authorization:", "password=", "password:", "passwd=", "passwd:",
		"credential=", "credential:", "credentials=", "credentials:",
		"api_key=", "api_key:", "api-key=", "api-key:", "x-api-key", "cookie:",
		"set-cookie:", "client_secret", "secret_key", "session=", "session:", "token=", "token:",
		"private_key", "private key", "begin rsa", "begin openssh",
	}
	for _, marker := range secretMarkers {
		if strings.Contains(lower, marker) {
			return "敏感错误详情已隐藏"
		}
	}
	if hex64TokenPattern.MatchString(value) || containsHighEntropyToken(value) {
		return "敏感错误详情已隐藏"
	}
	if stackPathPattern.MatchString(value) || strings.Contains(lower, "goroutine ") || strings.Contains(lower, "traceback (most recent call last)") || strings.Contains(lower, " at ") {
		return "内部错误详情已隐藏"
	}
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > max {
		return value[:max] + "…"
	}
	return value
}

func containsHighEntropyToken(value string) bool {
	for _, candidate := range highEntropyCandidatePattern.FindAllString(value, -1) {
		classes := 0
		lower, upper, digit, symbol := false, false, false, false
		unique := make(map[rune]struct{})
		for _, char := range candidate {
			unique[char] = struct{}{}
			switch {
			case char >= 'a' && char <= 'z':
				lower = true
			case char >= 'A' && char <= 'Z':
				upper = true
			case char >= '0' && char <= '9':
				digit = true
			default:
				symbol = true
			}
		}
		for _, present := range []bool{lower, upper, digit, symbol} {
			if present {
				classes++
			}
		}
		if classes >= 3 && len(unique) >= 12 {
			return true
		}
	}
	return false
}

func cleanURLPath(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	cleaned := parsed.Path
	if parsed.Path == "" && !parsed.IsAbs() {
		cleaned = value
		if index := strings.IndexAny(cleaned, "?#"); index >= 0 {
			cleaned = cleaned[:index]
		}
	}
	cleaned = cleanText(cleaned, 240)
	if cleaned == "敏感错误详情已隐藏" || cleaned == "内部错误详情已隐藏" {
		return ""
	}
	return cleaned
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && subtle.ConstantTimeCompare([]byte(parsed.Host), []byte(r.Host)) == 1
}

func validCSRF(r *http.Request, claims auth.Claims) bool {
	if !sameOrigin(r) {
		return false
	}
	candidate := r.Header.Get("X-CSRF-Token")
	return len(candidate) == len(claims.CSRF) && subtle.ConstantTimeCompare([]byte(candidate), []byte(claims.CSRF)) == 1
}

func decodeJSON(r *http.Request, target any, max int64) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > max {
		return errors.New("request body is too large")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("multiple JSON values")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func stringValue(values ...any) string {
	for _, value := range values {
		switch typed := value.(type) {
		case string:
			if trimmed := strings.TrimSpace(typed); trimmed != "" {
				return trimmed
			}
		case json.Number:
			return typed.String()
		case float64:
			return strconv.FormatFloat(typed, 'f', -1, 64)
		case int:
			return strconv.Itoa(typed)
		case int64:
			return strconv.FormatInt(typed, 10)
		}
	}
	return ""
}

func intValue(values ...any) int64 {
	for _, value := range values {
		switch typed := value.(type) {
		case json.Number:
			if parsed, err := typed.Int64(); err == nil {
				return parsed
			}
			if parsed, err := typed.Float64(); err == nil {
				return int64(parsed)
			}
		case float64:
			return int64(typed)
		case int:
			return int64(typed)
		case int64:
			return typed
		case string:
			if parsed, err := strconv.ParseInt(typed, 10, 64); err == nil {
				return parsed
			}
		}
	}
	return 0
}

func floatValue(values ...any) float64 {
	for _, value := range values {
		switch typed := value.(type) {
		case json.Number:
			if parsed, err := typed.Float64(); err == nil {
				return parsed
			}
		case float64:
			return typed
		case int:
			return float64(typed)
		case int64:
			return float64(typed)
		case string:
			if parsed, err := strconv.ParseFloat(typed, 64); err == nil {
				return parsed
			}
		}
	}
	return 0
}

func boolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, _ := strconv.ParseBool(typed)
		return parsed
	default:
		return false
	}
}

func clamp(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
