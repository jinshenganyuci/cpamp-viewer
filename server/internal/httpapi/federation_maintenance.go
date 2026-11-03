package httpapi

import (
	"errors"
	"net/http"
	"sync"
)

type federatedMaintenanceInstance struct {
	InstanceID          string                     `json:"instance_id"`
	InstanceName        string                     `json:"instance_name"`
	Available           bool                       `json:"available"`
	DatabaseMaintenance *databaseMaintenanceStatus `json:"databaseMaintenance,omitempty"`
}
type federatedMaintenanceResponse struct {
	DatabaseMaintenance databaseMaintenanceStatus      `json:"databaseMaintenance"`
	Instances           []federatedMaintenanceInstance `json:"instances"`
	PartialFailed       bool                           `json:"partial_failed"`
	Warnings            []string                       `json:"warnings"`
}

func (s *Server) handleMaintenance(w http.ResponseWriter, r *http.Request) {
	sources := s.cpampReadSources()
	if len(sources) <= 1 {
		s.handleSingleMaintenance(w, r)
		return
	}
	response, err := federateMaintenance(r, sources)
	if err != nil {
		writeError(w, http.StatusBadGateway, "maintenance instances are unavailable")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func federateMaintenance(r *http.Request, sources []cpampReadSource) (federatedMaintenanceResponse, error) {
	type outcome struct {
		value databaseMaintenanceEnvelope
		err   error
	}
	results := make([]outcome, len(sources))
	var workers sync.WaitGroup
	sem := make(chan struct{}, 4)
	for index, source := range sources {
		workers.Add(1)
		go func() {
			defer workers.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-r.Context().Done():
				results[index].err = r.Context().Err()
				return
			}
			results[index].value, results[index].err = federationProjectedJSON[databaseMaintenanceEnvelope](r, source.Reader, (*Server).handleSingleMaintenance)
		}()
	}
	workers.Wait()
	if err := r.Context().Err(); err != nil {
		return federatedMaintenanceResponse{}, err
	}
	response := federatedMaintenanceResponse{Instances: []federatedMaintenanceInstance{}, Warnings: []string{}}
	successes := 0
	for index, result := range results {
		source := sources[index]
		name := dashboardInstanceName(source)
		instance := federatedMaintenanceInstance{InstanceID: source.ID, InstanceName: name, Available: result.err == nil}
		if result.err != nil {
			response.PartialFailed = true
			response.Warnings = append(response.Warnings, name+" 维护状态暂不可用")
		} else {
			successes++
			value := result.value.DatabaseMaintenance
			instance.DatabaseMaintenance = &value
			response.DatabaseMaintenance.Required = response.DatabaseMaintenance.Required || value.Required
			response.DatabaseMaintenance.PerformanceDegraded = response.DatabaseMaintenance.PerformanceDegraded || value.PerformanceDegraded
			response.DatabaseMaintenance.DeferredIndexes += value.DeferredIndexes
			response.DatabaseMaintenance.OfflineJobs += value.OfflineJobs
		}
		response.Instances = append(response.Instances, instance)
	}
	if successes == 0 {
		return federatedMaintenanceResponse{}, errors.New("maintenance instances are unavailable")
	}
	return response, nil
}
