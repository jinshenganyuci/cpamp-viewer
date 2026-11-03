package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cpamp-viewer/server/internal/cpamp"
)

func TestFederatedMaintenanceAddsCountsPreservesFailureAndRedacts(t *testing.T) {
	sources := []cpampReadSource{}
	for index := 0; index < 3; index++ {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" || r.URL.Path != "/status" || r.URL.Query().Get("scope") != "database-maintenance" {
				t.Fatal("unexpected maintenance request")
			}
			if index == 2 {
				w.WriteHeader(500)
				_, _ = w.Write([]byte("Bearer private-secret"))
				return
			}
			writeJSON(w, 200, map[string]any{"databaseMaintenance": map[string]any{"required": index == 1, "performanceDegraded": index == 1, "deferredIndexes": index + 1, "offlineJobs": index + 2, "command": "private command", "reasons": []string{"private reason"}}, "dbPath": "/private/db"})
		}))
		t.Cleanup(upstream.Close)
		sources = append(sources, cpampReadSource{ID: []string{"one", "two", "three"}[index], Name: "Instance", Reader: &Server{cpamp: cpamp.New(upstream.URL, "synthetic-admin", time.Second, 1<<20)}})
	}
	result, err := federateMaintenance(httptest.NewRequest("GET", "/viewer/api/v1/maintenance", nil), sources)
	if err != nil {
		t.Fatal(err)
	}
	if !result.DatabaseMaintenance.Required || !result.DatabaseMaintenance.PerformanceDegraded || result.DatabaseMaintenance.DeferredIndexes != 3 || result.DatabaseMaintenance.OfflineJobs != 5 || !result.PartialFailed || len(result.Warnings) != 1 || len(result.Instances) != 3 || result.Instances[2].Available {
		t.Fatalf("incorrect aggregate: %+v", result)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "private") {
		t.Fatal("private maintenance metadata leaked")
	}
	if _, err := federateMaintenance(httptest.NewRequest("GET", "/viewer/api/v1/maintenance", nil), sources[2:]); err == nil {
		t.Fatal("failed source fabricated maintenance ready")
	}
}
