package controlmirror

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"forge.local/services/forge-discovery/internal/store"
)

// Job is a best-effort Control projection unit.
type Job struct {
	Kind        string // Endpoint | Service | EndpointDelete
	Project     string
	Environment string
	Name        string
	Endpoint    *store.EndpointRow
	Attempt     int
	OpID        string
}

// Worker projects accepted Discovery writes into Control's generic API.
type Worker struct {
	BaseURL    string
	HTTPClient *http.Client
	Log        *slog.Logger

	mu     sync.Mutex
	queue  []Job
	wake   chan struct{}
	closed bool
}

// NewWorker returns an async mirror worker.
func NewWorker(baseURL string, log *slog.Logger) *Worker {
	return &Worker{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		Log:  log,
		wake: make(chan struct{}, 1),
	}
}

// NotifyServiceUpsert enqueues a Service ensure.
func (w *Worker) NotifyServiceUpsert(project, environment, service string) {
	w.enqueue(Job{
		Kind:        "Service",
		Project:     project,
		Environment: environment,
		Name:        service,
		OpID:        fmt.Sprintf("mirror-svc-%s-%s-%s", project, environment, service),
	})
}

// NotifyEndpointUpsert enqueues an Endpoint upsert.
func (w *Worker) NotifyEndpointUpsert(row store.EndpointRow) {
	cp := row
	w.enqueue(Job{
		Kind:        "Endpoint",
		Project:     row.Project,
		Environment: row.Environment,
		Name:        row.ID,
		Endpoint:    &cp,
		OpID:        fmt.Sprintf("mirror-ep-%s-%s", row.ID, row.ResourceVersion),
	})
}

// NotifyEndpointDelete enqueues an Endpoint delete.
func (w *Worker) NotifyEndpointDelete(project, environment, id string) {
	w.enqueue(Job{
		Kind:        "EndpointDelete",
		Project:     project,
		Environment: environment,
		Name:        id,
		OpID:        fmt.Sprintf("mirror-ep-del-%s", id),
	})
}

// NotifyEndpointUnready is a no-op hook for sweeper (status already mirrored via upserts).
func (w *Worker) NotifyEndpointUnready(id, reason string) {
	if w.Log != nil {
		w.Log.Debug("mirror noted unready", "id", id, "reason", reason)
	}
}

func (w *Worker) enqueue(job Job) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.queue = append(w.queue, job)
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Run processes the queue until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	backoff := 500 * time.Millisecond
	for {
		select {
		case <-ctx.Done():
			w.mu.Lock()
			w.closed = true
			w.mu.Unlock()
			return
		case <-w.wake:
		case <-time.After(2 * time.Second):
		}

		for {
			job, ok := w.dequeue()
			if !ok {
				break
			}
			if err := w.apply(ctx, job); err != nil {
				job.Attempt++
				if w.Log != nil {
					w.Log.Warn("mirror apply failed; will retry",
						"kind", job.Kind,
						"name", job.Name,
						"attempt", job.Attempt,
						"error", err.Error(),
						"op_id", job.OpID,
					)
				}
				// Re-queue with delay.
				time.Sleep(minDur(backoff*time.Duration(job.Attempt), 30*time.Second))
				w.enqueue(job)
				break
			}
		}
	}
}

func (w *Worker) dequeue() (Job, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.queue) == 0 {
		return Job{}, false
	}
	job := w.queue[0]
	w.queue = w.queue[1:]
	return job, true
}

func (w *Worker) apply(ctx context.Context, job Job) error {
	switch job.Kind {
	case "Service":
		return w.ensureService(ctx, job)
	case "Endpoint":
		if job.Endpoint == nil {
			return fmt.Errorf("missing endpoint payload")
		}
		if err := w.ensureService(ctx, Job{
			Project: job.Project, Environment: job.Environment, Name: job.Endpoint.Service, OpID: job.OpID + "-svc",
		}); err != nil {
			return err
		}
		return w.upsertEndpoint(ctx, job)
	case "EndpointDelete":
		return w.deleteEndpoint(ctx, job)
	default:
		return fmt.Errorf("unknown job kind %q", job.Kind)
	}
}

func (w *Worker) ensureService(ctx context.Context, job Job) error {
	url := fmt.Sprintf("%s/v1/projects/%s/environments/%s/services", w.BaseURL, job.Project, job.Environment)
	body := map[string]any{
		"apiVersion": "forge.dev/v1",
		"kind":       "Service",
		"metadata": map[string]any{
			"name":        job.Name,
			"project":     job.Project,
			"environment": job.Environment,
		},
		"spec": map[string]any{
			"ports":   []any{},
			"aliases": []any{},
		},
	}
	status, _, err := w.doJSON(ctx, http.MethodPost, url, body, job.OpID)
	if err != nil {
		return err
	}
	switch status {
	case http.StatusOK, http.StatusCreated, http.StatusConflict:
		return nil
	default:
		// GET to see if it already exists.
		getURL := fmt.Sprintf("%s/v1/projects/%s/environments/%s/services/%s", w.BaseURL, job.Project, job.Environment, job.Name)
		st, _, err := w.doJSON(ctx, http.MethodGet, getURL, nil, job.OpID)
		if err != nil {
			return err
		}
		if st == http.StatusOK {
			return nil
		}
		return fmt.Errorf("ensure service status %d", status)
	}
}

func (w *Worker) upsertEndpoint(ctx context.Context, job Job) error {
	ep := job.Endpoint
	getURL := fmt.Sprintf("%s/v1/projects/%s/environments/%s/endpoints/%s", w.BaseURL, job.Project, job.Environment, job.Name)
	st, raw, err := w.doJSON(ctx, http.MethodGet, getURL, nil, job.OpID)
	if err != nil {
		return err
	}

	spec := map[string]any{
		"service":  ep.Service,
		"nodeId":   ep.NodeID,
		"address":  map[string]any{"ip": ep.AddressIP, "port": ep.AddressPort},
		"protocol": ep.Protocol,
	}
	if ep.Revision != "" {
		spec["revision"] = ep.Revision
	}
	statusBody := map[string]any{
		"phase": ep.Phase,
	}
	if ep.UnreadyReason != nil {
		statusBody["unreadyReason"] = *ep.UnreadyReason
	}

	if st == http.StatusNotFound {
		createURL := fmt.Sprintf("%s/v1/projects/%s/environments/%s/endpoints", w.BaseURL, job.Project, job.Environment)
		body := map[string]any{
			"apiVersion": "forge.dev/v1",
			"kind":       "Endpoint",
			"metadata": map[string]any{
				"name":        ep.ID,
				"project":     ep.Project,
				"environment": ep.Environment,
			},
			"spec": spec,
		}
		cst, _, err := w.doJSON(ctx, http.MethodPost, createURL, body, job.OpID)
		if err != nil {
			return err
		}
		if cst != http.StatusOK && cst != http.StatusCreated && cst != http.StatusConflict {
			return fmt.Errorf("create endpoint status %d", cst)
		}
		return w.patchEndpointStatus(ctx, job, statusBody)
	}
	if st != http.StatusOK {
		return fmt.Errorf("get endpoint status %d", st)
	}

	var existing struct {
		Metadata struct {
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
	}
	_ = json.Unmarshal(raw, &existing)
	putBody := map[string]any{
		"apiVersion": "forge.dev/v1",
		"kind":       "Endpoint",
		"metadata": map[string]any{
			"name":            ep.ID,
			"project":         ep.Project,
			"environment":     ep.Environment,
			"resourceVersion": existing.Metadata.ResourceVersion,
		},
		"spec": spec,
	}
	pst, _, err := w.doJSON(ctx, http.MethodPut, getURL, putBody, job.OpID)
	if err != nil {
		return err
	}
	if pst != http.StatusOK && pst != http.StatusConflict {
		return fmt.Errorf("put endpoint status %d", pst)
	}
	return w.patchEndpointStatus(ctx, job, statusBody)
}

func (w *Worker) patchEndpointStatus(ctx context.Context, job Job, statusBody map[string]any) error {
	url := fmt.Sprintf("%s/v1/projects/%s/environments/%s/endpoints/%s/status", w.BaseURL, job.Project, job.Environment, job.Name)
	body := map[string]any{"status": statusBody}
	st, _, err := w.doJSON(ctx, http.MethodPut, url, body, job.OpID+"-status")
	if err != nil {
		return err
	}
	if st == http.StatusOK || st == http.StatusNotFound || st == http.StatusConflict {
		return nil
	}
	return fmt.Errorf("status put %d", st)
}

func (w *Worker) deleteEndpoint(ctx context.Context, job Job) error {
	url := fmt.Sprintf("%s/v1/projects/%s/environments/%s/endpoints/%s", w.BaseURL, job.Project, job.Environment, job.Name)
	st, _, err := w.doJSON(ctx, http.MethodDelete, url, nil, job.OpID)
	if err != nil {
		return err
	}
	if st == http.StatusOK || st == http.StatusNoContent || st == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("delete endpoint status %d", st)
}

func (w *Worker) doJSON(ctx context.Context, method, url string, body any, opID string) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if opID != "" {
		req.Header.Set("Idempotency-Key", opID)
		req.Header.Set("X-Forge-Operation-Id", opID)
	}
	resp, err := w.HTTPClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, data, nil
}

func minDur(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
