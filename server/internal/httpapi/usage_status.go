package httpapi

import (
	"context"
	"cpamp-viewer/server/internal/cpamp"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

const usageStatusPath = "/v0/management/usage/maintenance"
const usageStatusCacheTTL = 30 * time.Second

type usageStorage struct {
	DatabaseBytes    int64 `json:"database_bytes"`
	WALBytes         int64 `json:"wal_bytes"`
	SHMBytes         int64 `json:"shm_bytes"`
	TotalBytes       int64 `json:"total_bytes"`
	ReclaimableBytes int64 `json:"reclaimable_bytes"`
}
type publicUsageStatus struct {
	RawEventCount         int64        `json:"raw_event_count"`
	RawArchivedEventCount *int64       `json:"raw_archived_event_count,omitempty"`
	RawDeletedEventCount  int64        `json:"raw_deleted_event_count"`
	RawMinTimestampMS     *int64       `json:"raw_min_timestamp_ms,omitempty"`
	RawMaxTimestampMS     *int64       `json:"raw_max_timestamp_ms,omitempty"`
	MigrationReady        bool         `json:"migration_ready"`
	HourlyAggregateReady  bool         `json:"hourly_aggregate_ready"`
	Storage               usageStorage `json:"storage"`
	ActiveOperation       string       `json:"active_operation,omitempty"`
	ActiveStatus          string       `json:"active_status,omitempty"`
}
type usageStatusResponse struct {
	Available   bool               `json:"available"`
	Stale       bool               `json:"stale"`
	CheckedAtMS int64              `json:"checked_at_ms,omitempty"`
	Status      *publicUsageStatus `json:"status,omitempty"`
}
type usageStatusCache struct {
	client      *cpamp.Client
	timeout     time.Duration
	now         func() time.Time
	mu          sync.Mutex
	pending     chan struct{}
	nextAttempt time.Time
	current     usageStatusResponse
}

func newUsageStatusCache(client *cpamp.Client, timeout time.Duration) *usageStatusCache {
	if timeout <= 0 || timeout > 5*time.Second {
		timeout = 5 * time.Second
	}
	return &usageStatusCache{client: client, timeout: timeout, now: time.Now}
}
func (c *usageStatusCache) snapshot(ctx context.Context) (usageStatusResponse, error) {
	if c == nil || c.client == nil {
		return usageStatusResponse{}, nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return usageStatusResponse{}, err
		}
		c.mu.Lock()
		if c.now().Before(c.nextAttempt) {
			response := cloneUsageStatus(c.current)
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
				c.nextAttempt = c.now().Add(usageStatusCacheTTL)
				if err == nil {
					c.current = response
				} else if c.current.Available {
					c.current.Stale = true
				} else {
					c.current = usageStatusResponse{}
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
			return usageStatusResponse{}, ctx.Err()
		case <-pending:
		}
	}
}

func cloneUsageStatus(response usageStatusResponse) usageStatusResponse {
	if response.Status != nil {
		copy := *response.Status
		for _, field := range []**int64{&copy.RawArchivedEventCount, &copy.RawMinTimestampMS, &copy.RawMaxTimestampMS} {
			if *field != nil {
				value := **field
				*field = &value
			}
		}
		response.Status = &copy
	}
	return response
}

func (c *usageStatusCache) fetch(ctx context.Context) (usageStatusResponse, error) {
	var raw map[string]any
	// A validated GET avoids relying on HEAD status rewritten by reverse proxies.
	// Legacy endpoints returning HTTP 200 with another schema remain unavailable.
	if err := c.client.GetJSON(ctx, usageStatusPath, nil, &raw); err != nil {
		return usageStatusResponse{}, err
	}
	status, ok := projectUsageStatus(raw)
	if !ok {
		return usageStatusResponse{}, errors.New("usage status is unavailable")
	}
	return usageStatusResponse{Available: true, CheckedAtMS: c.now().UnixMilli(), Status: &status}, nil
}
func projectUsageStatus(raw map[string]any) (publicUsageStatus, bool) {
	var status publicUsageStatus
	count, ok := publicUsageInteger(raw["raw_event_count"])
	if !ok {
		return status, false
	}
	status.RawEventCount = count
	count, ok = publicUsageInteger(raw["raw_deleted_event_count"])
	if !ok {
		return status, false
	}
	status.RawDeletedEventCount = count
	readiness, ok := raw["readiness"].(map[string]any)
	if !ok {
		return status, false
	}
	status.MigrationReady, ok = readiness["migration_ready"].(bool)
	if !ok {
		return status, false
	}
	status.HourlyAggregateReady, ok = readiness["hourly_aggregate_ready"].(bool)
	if !ok {
		return status, false
	}
	storage, ok := raw["storage"].(map[string]any)
	if !ok {
		return status, false
	}
	for key, dst := range map[string]*int64{"database_bytes": &status.Storage.DatabaseBytes, "wal_bytes": &status.Storage.WALBytes, "shm_bytes": &status.Storage.SHMBytes, "total_bytes": &status.Storage.TotalBytes, "reclaimable_bytes": &status.Storage.ReclaimableBytes} {
		n, valid := publicUsageInteger(storage[key])
		if !valid {
			return status, false
		}
		*dst = n
	}
	if raw["compact_requires_stopped_server"] != true {
		return status, false
	}
	for key, dst := range map[string]**int64{"raw_archived_event_count": &status.RawArchivedEventCount, "raw_min_timestamp_ms": &status.RawMinTimestampMS, "raw_max_timestamp_ms": &status.RawMaxTimestampMS} {
		if n, valid := publicUsageInteger(raw[key]); valid {
			*dst = &n
		}
	}
	if run, ok := raw["active_run"].(map[string]any); ok {
		status.ActiveStatus = usageEnum(run["status"], "previewed", "archiving", "archived", "verifying", "verified", "deleting", "completed", "failed", "cancelled")
		switch status.ActiveStatus {
		case "archiving":
			status.ActiveOperation = "archive"
		case "verifying":
			status.ActiveOperation = "verify"
		case "deleting":
			status.ActiveOperation = "delete"
		}
	}
	if lock, ok := raw["active_lock"].(map[string]any); ok {
		operation := usageEnum(lock["operation"], "archive", "verify", "delete", "compact")
		if operation == "" {
			operation = "maintenance"
		}
		status.ActiveOperation = operation
	}
	return status, true
}
func (s *Server) handleSingleUsageStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		writeError(w, http.StatusBadRequest, "usage status requests do not accept parameters or a body")
		return
	}
	if r.Body != nil {
		var probe [1]byte
		if n, err := r.Body.Read(probe[:]); n != 0 || (err != nil && err != io.EOF) {
			writeError(w, http.StatusBadRequest, "usage status requests do not accept a body")
			return
		}
	}
	response, err := s.usageStatus.snapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusGatewayTimeout, "usage status is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, response)
}
