package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"forge.local/services/forge-build/internal/jobs"
)

// BuildService is the job manager surface used by HTTP handlers.
type BuildService interface {
	Enqueue(req jobs.Request) (jobs.Accepted, error)
	Get(id string) (*jobs.Record, bool)
}

// BuildHandler serves build create/status/logs endpoints.
type BuildHandler struct {
	svc              BuildService
	defaultForgeYAML string
}

// NewBuildHandler returns routes for the build API.
func NewBuildHandler(svc BuildService, defaultForgeYAML string) *BuildHandler {
	if strings.TrimSpace(defaultForgeYAML) == "" {
		defaultForgeYAML = "forge.yaml"
	}
	return &BuildHandler{svc: svc, defaultForgeYAML: defaultForgeYAML}
}

// Register mounts build routes on mux.
func (h *BuildHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/builds", h.handleCreate)
	mux.HandleFunc("GET /v1/builds/{buildId}", h.handleGet)
	mux.HandleFunc("GET /v1/builds/{buildId}/logs", h.handleLogs)
}

func (h *BuildHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "validation_error", "unable to read request body", nil)
		return
	}
	req, err := DecodeBuildRequest(body, h.defaultForgeYAML)
	if err != nil {
		WriteManifestValidation(w, err)
		return
	}
	accepted, err := h.svc.Enqueue(jobs.Request{
		Repo:      req.Repo,
		Ref:       req.Ref,
		ForgeYAML: req.EffectiveForgeYAMLPath(h.defaultForgeYAML),
		Project:   req.Project,
	})
	if err != nil {
		WriteManifestValidation(w, err)
		return
	}
	ensureRequestID(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(BuildAccepted{
		BuildID: accepted.BuildID,
		Status:  BuildStatus(accepted.Status),
	})
}

func (h *BuildHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("buildId")
	rec, ok := h.svc.Get(id)
	if !ok {
		WriteError(w, http.StatusNotFound, "not_found", "build not found", map[string]string{"buildId": id})
		return
	}
	ensureRequestID(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(recordToAPI(rec))
}

func (h *BuildHandler) handleLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("buildId")
	rec, ok := h.svc.Get(id)
	if !ok {
		WriteError(w, http.StatusNotFound, "not_found", "build not found", map[string]string{"buildId": id})
		return
	}
	follow := parseBoolQuery(r.URL.Query().Get("follow"))
	ensureRequestID(w)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)
	if !follow {
		for _, line := range rec.Logs.Snapshot() {
			_, _ = io.WriteString(w, line+"\n")
		}
		if canFlush {
			flusher.Flush()
		}
		return
	}

	ch, unsub := rec.Logs.Subscribe()
	defer unsub()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if canFlush {
				flusher.Flush()
			}
		case line, ok := <-ch:
			if !ok {
				return
			}
			if _, err := io.WriteString(w, line+"\n"); err != nil {
				return
			}
			if canFlush {
				flusher.Flush()
			}
		}
	}
}

func recordToAPI(rec *jobs.Record) BuildRecord {
	out := BuildRecord{
		BuildID:   rec.ID,
		Status:    BuildStatus(rec.Status),
		Commit:    rec.Commit,
		StartedAt: rec.StartedAt.UTC(),
		Error:     rec.Error,
	}
	if rec.Status == jobs.StatusSucceeded {
		out.Image = rec.Image
		out.Digest = rec.Digest
	}
	if rec.FinishedAt != nil {
		t := rec.FinishedAt.UTC()
		out.FinishedAt = &t
	}
	return out
}

func parseBoolQuery(raw string) bool {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return false
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false
	}
	return v
}
