package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Capture only responses from fixed internal read handlers, never caller supplied URLs.
type federationReadCapture struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func (w *federationReadCapture) Header() http.Header { return w.header }
func (w *federationReadCapture) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *federationReadCapture) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.body.Len()+len(b) > 24<<20 {
		w.overflow = true
		return 0, errors.New("read response too large")
	}
	return w.body.Write(b)
}
func captureFederationRead(ctx context.Context, r *http.Request, handler http.HandlerFunc, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	req := r.Clone(ctx)
	w := &federationReadCapture{header: make(http.Header)}
	handler(w, req)
	if w.overflow || w.status != http.StatusOK {
		return errors.New("instance read unavailable")
	}
	d := json.NewDecoder(&w.body)
	d.UseNumber()
	return d.Decode(out)
}
func parallelCPAMPReads(ctx context.Context, sources []cpampReadSource, read func(context.Context, int, cpampReadSource)) {
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for i, source := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
				read(ctx, i, source)
			case <-ctx.Done():
				read(ctx, i, source)
			}
		}()
	}
	wg.Wait()
}

type federatedAlias struct {
	ID           string `json:"id"`
	Alias        string `json:"alias"`
	InstanceID   string `json:"instance_id,omitempty"`
	InstanceName string `json:"instance_name,omitempty"`
}

func (s *Server) handleAliases(w http.ResponseWriter, r *http.Request) {
	sources := s.cpampReadSources()
	if len(sources) == 1 {
		s.handleSingleAliases(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 28*time.Second)
	defer cancel()
	type result struct {
		items []safeAlias
		err   error
	}
	results := make([]result, len(sources))
	parallelCPAMPReads(ctx, sources, func(ctx context.Context, i int, source cpampReadSource) {
		var data struct {
			Items []safeAlias `json:"items"`
		}
		results[i].err = captureFederationRead(ctx, r, source.Reader.handleSingleAliases, &data)
		results[i].items = data.Items
	})
	response := struct {
		Items         []federatedAlias `json:"items"`
		Warnings      []string         `json:"warnings,omitempty"`
		PartialFailed bool             `json:"partial_failed,omitempty"`
	}{Items: []federatedAlias{}}
	success := false
	for i, result := range results {
		source := sources[i]
		if result.err != nil {
			response.PartialFailed = true
			response.Warnings = append(response.Warnings, source.Name+" 调用密钥暂不可用")
			continue
		}
		success = true
		for _, item := range result.items {
			response.Items = append(response.Items, federatedAlias{ID: federationViewID(source.ID, item.ID), Alias: item.Alias, InstanceID: source.ID, InstanceName: source.Name})
		}
	}
	if !success {
		writeError(w, http.StatusBadGateway, "CPAMP aliases are unavailable")
		return
	}
	sort.Slice(response.Items, func(i, j int) bool {
		a, b := response.Items[i], response.Items[j]
		if a.InstanceName != b.InstanceName {
			return a.InstanceName < b.InstanceName
		}
		if a.Alias != b.Alias {
			return a.Alias < b.Alias
		}
		return a.ID < b.ID
	})
	writeJSON(w, http.StatusOK, response)
}
