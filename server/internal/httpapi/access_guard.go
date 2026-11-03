package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"cpamp-viewer/server/internal/accessguard"
	"cpamp-viewer/server/internal/config"
)

const accessGuardCacheTTL = 15 * time.Second

// The refresh itself owns a slot so canceled visitors cannot create more
// upstream reads while an earlier shared refresh is still running.
var accessGuardRefreshSlots = make(chan struct{}, 4)

type accessGuardQuotaItem struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	State            string   `json:"state"`
	WeeklyLimitUSD   *float64 `json:"weekly_limit_usd,omitempty"`
	RemainingUSD     *float64 `json:"remaining_usd,omitempty"`
	RemainingPercent *float64 `json:"remaining_percent,omitempty"`
	WindowStarted    *bool    `json:"window_started,omitempty"`
	ResetAt          string   `json:"reset_at,omitempty"`
	InstanceID       string   `json:"instance_id,omitempty"`
	InstanceName     string   `json:"instance_name,omitempty"`
}

type accessGuardQuotaResponse struct {
	Configured    bool                   `json:"configured"`
	UpdatedAt     string                 `json:"updated_at,omitempty"`
	Stale         bool                   `json:"stale"`
	Items         []accessGuardQuotaItem `json:"items"`
	PartialFailed bool                   `json:"partial_failed"`
	Warnings      []string               `json:"warnings,omitempty"`
}

type accessGuardQuotaCache struct {
	client     *accessguard.Client
	publicKeys []config.AccessGuardPublicKey
	publicAll  bool
	timeout    time.Duration
	now        func() time.Time

	mu          sync.Mutex
	pending     chan struct{}
	nextAttempt time.Time
	failed      bool
	// Only the public projection is retained between requests.
	current *accessGuardQuotaResponse
}

func newAccessGuardQuotaCache(cfg config.Config) *accessGuardQuotaCache {
	if cfg.AccessGuardBaseURL == "" && !cfg.AccessGuardViaCPAMP {
		return nil
	}
	timeout := cfg.RequestTimeout
	if timeout <= 0 || timeout > 10*time.Second {
		timeout = 10 * time.Second
	}
	maxBytes := cfg.MaxUpstreamBodyBytes
	if maxBytes <= 0 {
		maxBytes = 12 << 20
	}
	baseURL, credential := cfg.AccessGuardBaseURL, cfg.AccessGuardManagementKey
	if cfg.AccessGuardViaCPAMP {
		// Authenticate to CPAMP. Its plugin proxy uses its own saved CPA
		// connection; never assume a CPAMP Admin Key is a CPA Management Key.
		baseURL, credential = cfg.CPAMPBaseURL, cfg.CPAMPAdminKey
	}
	return &accessGuardQuotaCache{
		client:     accessguard.New(baseURL, credential, timeout, maxBytes),
		publicKeys: append([]config.AccessGuardPublicKey(nil), cfg.AccessGuardPublicKeys...),
		publicAll:  cfg.AccessGuardPublicAll,
		timeout:    timeout,
		now:        time.Now,
	}
}

func (s *Server) handleAccessGuardQuotas(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
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
	response, err := federatedAccessGuardSnapshot(ctx, s.cpampReadSources())
	if err != nil {
		writeError(w, http.StatusBadGateway, "Access Guard is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (c *accessGuardQuotaCache) snapshot(ctx context.Context) (accessGuardQuotaResponse, error) {
	if c == nil {
		return accessGuardQuotaResponse{Items: []accessGuardQuotaItem{}}, nil
	}
	for {
		if ctx.Err() != nil {
			return accessGuardQuotaResponse{}, accessguard.ErrUnavailable
		}
		c.mu.Lock()
		if c.now().Before(c.nextAttempt) {
			response, err := c.cachedLocked()
			c.mu.Unlock()
			return response, err
		}
		if c.pending == nil {
			c.pending = make(chan struct{})
			// Detach the bounded refresh, not the visitor's wait. All visitors
			// including the initiator can leave without canceling shared work.
			refreshParent := context.WithoutCancel(ctx)
			go c.refresh(refreshParent)
		}
		pending := c.pending
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return accessGuardQuotaResponse{}, accessguard.ErrUnavailable
		case <-pending:
		}
	}
}

func (c *accessGuardQuotaCache) refresh(parent context.Context) {
	// Up to sixteen instances share four slots. Give queued refreshes a
	// bounded batch budget without consuming their individual HTTP timeout.
	budget := 4 * c.timeout
	if budget > 28*time.Second {
		budget = 28 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()
	var bindings []json.RawMessage
	err := accessguard.ErrUnavailable
	select {
	case accessGuardRefreshSlots <- struct{}{}:
		requestCtx, requestCancel := context.WithTimeout(ctx, c.timeout)
		bindings, err = c.client.NativeKeyBindings(requestCtx)
		requestCancel()
		<-accessGuardRefreshSlots
	case <-ctx.Done():
	}
	now := c.now()
	var response accessGuardQuotaResponse
	if err == nil {
		publicKeys := c.publicKeys
		if c.publicAll {
			publicKeys = accessGuardAutomaticPublicKeys(bindings)
		}
		response = accessGuardQuotaResponse{
			Configured: true, UpdatedAt: now.UTC().Format(time.RFC3339),
			Items: projectAccessGuardQuotas(publicKeys, bindings, now),
		}
	}
	c.mu.Lock()
	c.nextAttempt = now.Add(accessGuardCacheTTL)
	c.failed = err != nil
	if err == nil {
		c.current = &response
	}
	close(c.pending)
	c.pending = nil
	c.mu.Unlock()
}

func (c *accessGuardQuotaCache) cachedLocked() (accessGuardQuotaResponse, error) {
	if c.current == nil {
		return accessGuardQuotaResponse{}, accessguard.ErrUnavailable
	}
	response := *c.current
	response.Items = append([]accessGuardQuotaItem{}, response.Items...)
	response.Stale = c.failed
	return response, nil
}

type accessGuardBinding struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Enabled *bool           `json:"enabled"`
	Usage   json.RawMessage `json:"usage"`
}

// Called only after an explicit server-side PUBLIC_ALL opt-in. Only the name
// becomes public; credential restrictions, key previews and IDs remain private.
func accessGuardAutomaticPublicKeys(rawBindings []json.RawMessage) []config.AccessGuardPublicKey {
	keys := make([]config.AccessGuardPublicKey, 0, len(rawBindings))
	seen := make(map[string]bool)
	for _, raw := range rawBindings {
		var binding accessGuardBinding
		if json.Unmarshal(raw, &binding) != nil || strings.TrimSpace(binding.ID) == "" || seen[binding.ID] {
			continue
		}
		seen[binding.ID] = true
		name := cleanText(binding.Name, 256)
		if name == "" || name == "敏感错误详情已隐藏" || name == "内部错误详情已隐藏" {
			name = "Key " + strconv.Itoa(len(keys)+1)
		}
		keys = append(keys, config.AccessGuardPublicKey{BindingID: binding.ID, Name: name})
	}
	return keys
}

type accessGuardWeeklyUsage struct {
	Limit   *float64 `json:"weekly_usd_limit"`
	Used    *float64 `json:"weekly_usd_used"`
	Calls   *int64   `json:"weekly_calls"`
	ResetAt string   `json:"weekly_reset_at"`
}

func projectAccessGuardQuotas(publicKeys []config.AccessGuardPublicKey, rawBindings []json.RawMessage, now time.Time) []accessGuardQuotaItem {
	bindings := make(map[string]accessGuardBinding, len(publicKeys))
	allowed := make(map[string]bool, len(publicKeys))
	for _, key := range publicKeys {
		allowed[key.BindingID] = true
	}
	duplicates := make(map[string]bool)
	for _, raw := range rawBindings {
		var binding accessGuardBinding
		if json.Unmarshal(raw, &binding) != nil || !allowed[binding.ID] {
			continue
		}
		if _, exists := bindings[binding.ID]; exists {
			duplicates[binding.ID] = true
		}
		bindings[binding.ID] = binding
	}
	items := make([]accessGuardQuotaItem, 0, len(publicKeys))
	for index, key := range publicKeys {
		item := accessGuardQuotaItem{
			// Public order is already visible. Do not hash predictable management IDs.
			ID:    pseudonym("access-guard-public-slot:" + strconv.Itoa(index+1)),
			Name:  key.Name,
			State: "unavailable",
		}
		binding, exists := bindings[key.BindingID]
		if exists && !duplicates[key.BindingID] && binding.Enabled != nil {
			if !*binding.Enabled {
				item.State = "inactive"
			} else {
				applyAccessGuardWeeklyUsage(&item, binding.Usage, now)
			}
		}
		items = append(items, item)
	}
	return items
}

func applyAccessGuardWeeklyUsage(item *accessGuardQuotaItem, raw json.RawMessage, now time.Time) {
	var usage accessGuardWeeklyUsage
	if json.Unmarshal(raw, &usage) != nil || !validAccessGuardAmount(usage.Limit) || !validAccessGuardAmount(usage.Used) || usage.Calls == nil || *usage.Calls < 0 {
		return
	}
	if *usage.Limit == 0 {
		item.State = "unlimited"
		return
	}
	started := *usage.Calls > 0
	if !started && *usage.Used != 0 {
		return
	}
	var resetAt string
	if started {
		reset, err := time.Parse(time.RFC3339, usage.ResetAt)
		if err != nil || !reset.After(now) {
			return
		}
		resetAt = reset.UTC().Format(time.RFC3339)
	}
	remaining := math.Max(0, *usage.Limit-*usage.Used)
	percent := remaining / *usage.Limit * 100
	item.State = "active"
	item.WeeklyLimitUSD = usage.Limit
	item.RemainingUSD = &remaining
	item.RemainingPercent = &percent
	item.WindowStarted = &started
	item.ResetAt = resetAt
}

func validAccessGuardAmount(value *float64) bool {
	return value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0
}
