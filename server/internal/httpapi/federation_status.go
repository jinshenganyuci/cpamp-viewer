package httpapi

import (
	"context"
	"io"
	"net/http"
	"sort"
	"time"
)

// Validate once before fan-out. A caller error must remain 400/405 rather than
// being transformed into an upstream failure, and no shared request body is read.
func validateFederationStatusRead(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return false
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		writeError(w, http.StatusBadRequest, "status requests do not accept parameters or a body")
		return false
	}
	if r.Body != nil {
		var probe [1]byte
		if n, err := r.Body.Read(probe[:]); n != 0 || (err != nil && err != io.EOF) {
			writeError(w, http.StatusBadRequest, "status requests do not accept a body")
			return false
		}
	}
	return true
}

type federatedUsageStatusInstance struct {
	InstanceID   string `json:"instance_id"`
	InstanceName string `json:"instance_name"`
	usageStatusResponse
}

type federatedUsageStorage struct {
	DatabaseBytes    *int64 `json:"database_bytes"`
	WALBytes         *int64 `json:"wal_bytes"`
	SHMBytes         *int64 `json:"shm_bytes"`
	TotalBytes       *int64 `json:"total_bytes"`
	ReclaimableBytes *int64 `json:"reclaimable_bytes"`
}

type federatedUsageTotals struct {
	RawEventCount         *int64                `json:"raw_event_count"`
	RawArchivedEventCount *int64                `json:"raw_archived_event_count"`
	RawDeletedEventCount  *int64                `json:"raw_deleted_event_count"`
	RawMinTimestampMS     *int64                `json:"raw_min_timestamp_ms"`
	RawMaxTimestampMS     *int64                `json:"raw_max_timestamp_ms"`
	MigrationReady        *bool                 `json:"migration_ready"`
	HourlyAggregateReady  *bool                 `json:"hourly_aggregate_ready"`
	Storage               federatedUsageStorage `json:"storage"`
	ActiveOperation       string                `json:"active_operation,omitempty"`
	ActiveStatus          string                `json:"active_status,omitempty"`
}

type federatedUsageStatusResponse struct {
	Available     bool                           `json:"available"`
	Stale         bool                           `json:"stale"`
	CheckedAtMS   int64                          `json:"checked_at_ms,omitempty"`
	Status        *federatedUsageTotals          `json:"status,omitempty"`
	Instances     []federatedUsageStatusInstance `json:"instances"`
	PartialFailed bool                           `json:"partial_failed"`
	Warnings      []string                       `json:"warnings,omitempty"`
}

func (s *Server) handleUsageStatus(w http.ResponseWriter, r *http.Request) {
	if !validateFederationStatusRead(w, r) {
		return
	}
	sources := s.cpampReadSources()
	if len(sources) <= 1 {
		s.handleSingleUsageStatus(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 28*time.Second)
	defer cancel()
	req := r.Clone(ctx)
	req.Body = http.NoBody
	instances := make([]federatedUsageStatusInstance, len(sources))
	parallelCPAMPReads(ctx, sources, func(ctx context.Context, i int, source cpampReadSource) {
		instance := federatedUsageStatusInstance{InstanceID: source.ID, InstanceName: dashboardInstanceName(source)}
		if source.Reader != nil && ctx.Err() == nil {
			if err := captureFederationRead(ctx, req, source.Reader.handleSingleUsageStatus, &instance.usageStatusResponse); err != nil {
				instance.usageStatusResponse = usageStatusResponse{}
			}
		}
		if !instance.Available {
			instance.Status = nil
		}
		instances[i] = instance
	})
	if ctx.Err() != nil {
		writeError(w, http.StatusGatewayTimeout, "usage status is unavailable")
		return
	}
	response := federatedUsageStatusResponse{Instances: instances}
	for _, instance := range instances {
		if !instance.Available || instance.Status == nil {
			response.PartialFailed = true
			response.Warnings = append(response.Warnings, instance.InstanceName+" 用量状态暂不可用")
			continue
		}
		response.Available = true
		response.CheckedAtMS = oldestFederatedCheck(response.CheckedAtMS, instance.CheckedAtMS)
		if instance.Stale {
			response.Stale, response.PartialFailed = true, true
			response.Warnings = append(response.Warnings, instance.InstanceName+" 用量状态更新失败，显示上次记录")
		}
	}
	if response.Available {
		status := aggregateFederatedUsageStatus(instances)
		response.Status = &status
	}
	writeJSON(w, http.StatusOK, response)
}

func oldestFederatedCheck(current, candidate int64) int64 {
	if candidate > 0 && (current == 0 || candidate < current) {
		return candidate
	}
	return current
}

func federatedUsageSum(instances []federatedUsageStatusInstance, field func(*publicUsageStatus) *int64) *int64 {
	var sum int64
	for _, instance := range instances {
		if !instance.Available || instance.Status == nil {
			return nil
		}
		value := field(instance.Status)
		if value == nil || *value < 0 || *value > 9007199254740991-sum {
			return nil
		}
		sum += *value
	}
	return &sum
}

func federatedUsageReady(instances []federatedUsageStatusInstance, field func(*publicUsageStatus) bool) *bool {
	known := true
	value := true
	for _, instance := range instances {
		if !instance.Available || instance.Status == nil {
			known = false
			continue
		}
		if !field(instance.Status) {
			value = false
		}
	}
	if !known && value {
		return nil
	}
	return &value
}

func federatedUsageTimeRange(instances []federatedUsageStatusInstance, minimum bool) *int64 {
	var result *int64
	for _, instance := range instances {
		if !instance.Available || instance.Status == nil {
			return nil
		}
		if instance.Status.RawEventCount == 0 {
			continue
		}
		candidate := instance.Status.RawMaxTimestampMS
		if minimum {
			candidate = instance.Status.RawMinTimestampMS
		}
		if candidate == nil {
			return nil
		}
		if result == nil || (minimum && *candidate < *result) || (!minimum && *candidate > *result) {
			value := *candidate
			result = &value
		}
	}
	return result
}

func aggregateFederatedUsageStatus(instances []federatedUsageStatusInstance) federatedUsageTotals {
	status := federatedUsageTotals{
		RawEventCount:         federatedUsageSum(instances, func(s *publicUsageStatus) *int64 { return &s.RawEventCount }),
		RawArchivedEventCount: federatedUsageSum(instances, func(s *publicUsageStatus) *int64 { return s.RawArchivedEventCount }),
		RawDeletedEventCount:  federatedUsageSum(instances, func(s *publicUsageStatus) *int64 { return &s.RawDeletedEventCount }),
		RawMinTimestampMS:     federatedUsageTimeRange(instances, true), RawMaxTimestampMS: federatedUsageTimeRange(instances, false),
		MigrationReady:       federatedUsageReady(instances, func(s *publicUsageStatus) bool { return s.MigrationReady }),
		HourlyAggregateReady: federatedUsageReady(instances, func(s *publicUsageStatus) bool { return s.HourlyAggregateReady }),
		Storage: federatedUsageStorage{
			DatabaseBytes:    federatedUsageSum(instances, func(s *publicUsageStatus) *int64 { return &s.Storage.DatabaseBytes }),
			WALBytes:         federatedUsageSum(instances, func(s *publicUsageStatus) *int64 { return &s.Storage.WALBytes }),
			SHMBytes:         federatedUsageSum(instances, func(s *publicUsageStatus) *int64 { return &s.Storage.SHMBytes }),
			TotalBytes:       federatedUsageSum(instances, func(s *publicUsageStatus) *int64 { return &s.Storage.TotalBytes }),
			ReclaimableBytes: federatedUsageSum(instances, func(s *publicUsageStatus) *int64 { return &s.Storage.ReclaimableBytes }),
		},
	}
	for _, instance := range instances {
		if !instance.Available || instance.Status == nil {
			continue
		}
		if operation := instance.Status.ActiveOperation; operation != "" {
			if status.ActiveOperation == "" {
				status.ActiveOperation, status.ActiveStatus = operation, instance.Status.ActiveStatus
			} else if status.ActiveOperation != operation || status.ActiveStatus != instance.Status.ActiveStatus {
				status.ActiveOperation, status.ActiveStatus = "maintenance", ""
			}
		}
	}
	return status
}

type federatedModelPriceStatusInstance struct {
	InstanceID   string `json:"instance_id"`
	InstanceName string `json:"instance_name"`
	modelPriceStatusResponse
}

type federatedModelPriceStatusResponse struct {
	Available      bool                                `json:"available"`
	Stale          bool                                `json:"stale"`
	CheckedAtMS    int64                               `json:"checked_at_ms,omitempty"`
	UnpricedModels []string                            `json:"unpriced_models"`
	UnpricedCount  *int64                              `json:"unpriced_count"`
	ModelCount     *int64                              `json:"model_count"`
	Instances      []federatedModelPriceStatusInstance `json:"instances"`
	PartialFailed  bool                                `json:"partial_failed"`
	Warnings       []string                            `json:"warnings,omitempty"`
}

func (s *Server) handleModelPriceStatus(w http.ResponseWriter, r *http.Request) {
	if !validateFederationStatusRead(w, r) {
		return
	}
	sources := s.cpampReadSources()
	if len(sources) <= 1 {
		s.handleSingleModelPriceStatus(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 28*time.Second)
	defer cancel()
	req := r.Clone(ctx)
	req.Body = http.NoBody
	instances := make([]federatedModelPriceStatusInstance, len(sources))
	parallelCPAMPReads(ctx, sources, func(ctx context.Context, i int, source cpampReadSource) {
		instance := federatedModelPriceStatusInstance{InstanceID: source.ID, InstanceName: dashboardInstanceName(source)}
		instance.UnpricedModels = []string{}
		if source.Reader != nil && ctx.Err() == nil {
			if err := captureFederationRead(ctx, req, source.Reader.handleSingleModelPriceStatus, &instance.modelPriceStatusResponse); err != nil {
				instance.modelPriceStatusResponse = modelPriceStatusResponse{UnpricedModels: []string{}}
			}
		}
		instances[i] = instance
	})
	if ctx.Err() != nil {
		writeError(w, http.StatusGatewayTimeout, "model price status is unavailable")
		return
	}
	response := federatedModelPriceStatusResponse{Instances: instances, UnpricedModels: []string{}}
	var models, unpriced int64
	complete := true
	for _, instance := range instances {
		if !instance.Available {
			complete, response.PartialFailed = false, true
			response.Warnings = append(response.Warnings, instance.InstanceName+" 模型价格状态暂不可用")
			continue
		}
		response.Available = true
		response.CheckedAtMS = oldestFederatedCheck(response.CheckedAtMS, instance.CheckedAtMS)
		models += int64(instance.ModelCount)
		unpriced += int64(instance.UnpricedCount)
		if instance.Stale {
			response.Stale, response.PartialFailed = true, true
			response.Warnings = append(response.Warnings, instance.InstanceName+" 模型价格状态更新失败，显示上次记录")
		}
		for _, model := range instance.UnpricedModels {
			if len(response.UnpricedModels) < maxPublicUnpricedModels {
				response.UnpricedModels = append(response.UnpricedModels, instance.InstanceName+" · "+model)
			}
		}
	}
	if complete && models <= 9007199254740991 && unpriced <= 9007199254740991 {
		response.ModelCount, response.UnpricedCount = &models, &unpriced
	}
	writeJSON(w, http.StatusOK, response)
}

type federatedModelPricesInstance struct {
	InstanceID   string                `json:"instance_id"`
	InstanceName string                `json:"instance_name"`
	Available    bool                  `json:"available"`
	Prices       map[string]modelPrice `json:"prices"`
}

type federatedModelPricesResponse struct {
	Prices            map[string]modelPrice          `json:"prices"`
	Instances         []federatedModelPricesInstance `json:"instances"`
	ConflictingModels []string                       `json:"conflicting_models"`
	PartialFailed     bool                           `json:"partial_failed"`
	Warnings          []string                       `json:"warnings,omitempty"`
}

func (s *Server) handleModelPrices(w http.ResponseWriter, r *http.Request) {
	if !validateFederationStatusRead(w, r) {
		return
	}
	sources := s.cpampReadSources()
	if len(sources) <= 1 {
		s.handleSingleModelPrices(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 28*time.Second)
	defer cancel()
	req := r.Clone(ctx)
	req.Body = http.NoBody
	instances := make([]federatedModelPricesInstance, len(sources))
	parallelCPAMPReads(ctx, sources, func(ctx context.Context, i int, source cpampReadSource) {
		instance := federatedModelPricesInstance{InstanceID: source.ID, InstanceName: dashboardInstanceName(source), Prices: map[string]modelPrice{}}
		if source.Reader != nil && ctx.Err() == nil {
			var prices modelPriceEnvelope
			if err := captureFederationRead(ctx, req, source.Reader.handleSingleModelPrices, &prices); err == nil {
				instance.Available = true
				instance.Prices = prices.Prices
			}
		}
		instances[i] = instance
	})
	if ctx.Err() != nil {
		writeError(w, http.StatusGatewayTimeout, "model prices are unavailable")
		return
	}
	response := federatedModelPricesResponse{Prices: map[string]modelPrice{}, Instances: instances, ConflictingModels: []string{}}
	seen, counts, conflicts := map[string]modelPrice{}, map[string]int{}, map[string]bool{}
	successful := 0
	for _, instance := range instances {
		if !instance.Available {
			response.PartialFailed = true
			response.Warnings = append(response.Warnings, instance.InstanceName+" 模型价格暂不可用")
			continue
		}
		successful++
		for model, price := range instance.Prices {
			if previous, exists := seen[model]; exists && previous != price {
				conflicts[model] = true
			}
			seen[model] = price
			counts[model]++
		}
	}
	if successful == 0 {
		writeError(w, http.StatusBadGateway, "model prices are unavailable")
		return
	}
	for model, price := range seen {
		if conflicts[model] {
			response.ConflictingModels = append(response.ConflictingModels, model)
		} else if counts[model] == len(instances) {
			response.Prices[model] = price
		}
	}
	sort.Strings(response.ConflictingModels)
	if len(response.ConflictingModels) > 0 {
		response.Warnings = append(response.Warnings, "不同实例的模型价格存在差异，请按来源查看")
	}
	writeJSON(w, http.StatusOK, response)
}
