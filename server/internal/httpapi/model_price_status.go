package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"cpamp-viewer/server/internal/cpamp"
)

const modelPriceStatusCacheTTL = time.Minute
const modelPriceStatusPath = "/v0/management/model-prices/runtime-models"
const maxPublicUnpricedModels = 200

type modelPriceStatusResponse struct {
	Available      bool     `json:"available"`
	Stale          bool     `json:"stale"`
	CheckedAtMS    int64    `json:"checked_at_ms,omitempty"`
	UnpricedModels []string `json:"unpriced_models"`
	UnpricedCount  int      `json:"unpriced_count"`
	ModelCount     int      `json:"model_count"`
}

type modelPriceStatusCache struct {
	client  *cpamp.Client
	timeout time.Duration
	now     func() time.Time

	mu          sync.Mutex
	pending     chan struct{}
	nextAttempt time.Time
	// Retain only the public projection, never the upstream payload or errors.
	current modelPriceStatusResponse
}

func newModelPriceStatusCache(client *cpamp.Client, timeout time.Duration) *modelPriceStatusCache {
	if timeout <= 0 || timeout > 5*time.Second {
		timeout = 5 * time.Second
	}
	return &modelPriceStatusCache{client: client, timeout: timeout, now: time.Now}
}

func (c *modelPriceStatusCache) snapshot(ctx context.Context) (modelPriceStatusResponse, error) {
	if c == nil || c.client == nil {
		return modelPriceStatusResponse{UnpricedModels: []string{}}, nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return modelPriceStatusResponse{UnpricedModels: []string{}}, err
		}
		c.mu.Lock()
		if c.now().Before(c.nextAttempt) {
			response := cloneModelPriceStatus(c.current)
			c.mu.Unlock()
			return response, nil
		}
		if c.pending == nil {
			c.pending = make(chan struct{})
			// Cancellation releases this visitor without aborting other visitors'
			// shared refresh. The independent request remains bounded to five seconds.
			refreshParent := context.WithoutCancel(ctx)
			go func() {
				refreshCtx, cancel := context.WithTimeout(refreshParent, c.timeout)
				response, err := c.fetch(refreshCtx)
				cancel()
				c.mu.Lock()
				c.nextAttempt = c.now().Add(modelPriceStatusCacheTTL)
				if err == nil {
					c.current = response
				} else if c.current.Available {
					c.current.Stale = true
				} else {
					c.current = modelPriceStatusResponse{UnpricedModels: []string{}}
				}
				close(c.pending)
				c.pending = nil
				c.mu.Unlock()
			}()
		}
		pending := c.pending
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return modelPriceStatusResponse{UnpricedModels: []string{}}, ctx.Err()
		case <-pending:
		}
	}
}

func cloneModelPriceStatus(response modelPriceStatusResponse) modelPriceStatusResponse {
	response.UnpricedModels = append([]string{}, response.UnpricedModels...)
	return response
}

func (c *modelPriceStatusCache) fetch(ctx context.Context) (modelPriceStatusResponse, error) {
	var upstream struct {
		Models         json.RawMessage `json:"models"`
		UnpricedModels json.RawMessage `json:"unpricedModels"`
	}
	if err := c.client.GetJSON(ctx, modelPriceStatusPath, nil, &upstream); err != nil {
		return modelPriceStatusResponse{}, err
	}
	models, ok := safeModelPriceStatusNames(upstream.Models)
	if !ok {
		return modelPriceStatusResponse{}, errors.New("model price status is unavailable")
	}
	unpriced, ok := safeModelPriceStatusNames(upstream.UnpricedModels)
	if !ok {
		return modelPriceStatusResponse{}, errors.New("model price status is unavailable")
	}
	// Derive counts from the actual safe inventory rather than trusting claimed
	// counts. A model outside that inventory cannot establish missing pricing.
	known := make(map[string]bool, len(models))
	for _, model := range models {
		known[model] = true
	}
	selected := make([]string, 0, len(unpriced))
	for _, model := range unpriced {
		if known[model] {
			selected = append(selected, model)
		}
	}
	count := len(selected)
	if len(selected) > maxPublicUnpricedModels {
		selected = selected[:maxPublicUnpricedModels]
	}
	return modelPriceStatusResponse{
		Available: true, CheckedAtMS: c.now().UnixMilli(),
		UnpricedModels: selected, UnpricedCount: count, ModelCount: len(models),
	}, nil
}

func safeModelPriceStatusNames(raw json.RawMessage) ([]string, bool) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return nil, false
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return nil, false
	}
	names := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		var name string
		if json.Unmarshal(value, &name) != nil {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" || len(name) > 256 || seen[name] {
			continue
		}
		valid := true
		for _, char := range name {
			if !unicode.IsLetter(char) && !unicode.IsDigit(char) && !strings.ContainsRune("._-/():+", char) {
				valid = false
				break
			}
		}
		if !valid || cleanText(name, 256) != name || name == "敏感错误详情已隐藏" || name == "内部错误详情已隐藏" {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names, true
}

func (s *Server) handleSingleModelPriceStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		writeError(w, http.StatusBadRequest, "model price status requests do not accept parameters or a body")
		return
	}
	if r.Body != nil {
		var probe [1]byte
		if n, err := r.Body.Read(probe[:]); n != 0 || (err != nil && err != io.EOF) {
			writeError(w, http.StatusBadRequest, "model price status requests do not accept a body")
			return
		}
	}
	response, err := s.modelPriceStatus.snapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusGatewayTimeout, "model price status is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, response)
}
