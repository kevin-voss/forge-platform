package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"forge.local/services/forge-build/internal/jobs"
)

// StatusService is the job manager surface used by status/cancel handlers.
type StatusService interface {
	Get(id string) (*jobs.Record, bool)
	List(filter jobs.ListFilter) []*jobs.Record
	Cancel(id string) (jobs.CancelResult, error)
}

// StatusHandler serves build status list/get/cancel endpoints.
type StatusHandler struct {
	svc StatusService
}

// NewStatusHandler returns routes for build status APIs.
func NewStatusHandler(svc StatusService) *StatusHandler {
	return &StatusHandler{svc: svc}
}

// Register mounts status routes on mux.
func (h *StatusHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/builds", h.handleList)
	mux.HandleFunc("GET /v1/builds/{buildId}", h.handleGet)
	mux.HandleFunc("POST /v1/builds/{buildId}/cancel", h.handleCancel)
}

func (h *StatusHandler) handleGet(w http.ResponseWriter, r *http.Request) {
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

func (h *StatusHandler) handleList(w http.ResponseWriter, r *http.Request) {
	filter := jobs.ListFilter{
		Status:  jobs.Status(strings.TrimSpace(r.URL.Query().Get("status"))),
		Service: strings.TrimSpace(r.URL.Query().Get("service")),
	}
	recs := h.svc.List(filter)
	sort.Slice(recs, func(i, j int) bool {
		return recs[i].StartedAt.After(recs[j].StartedAt)
	})
	out := make([]BuildRecord, 0, len(recs))
	for _, rec := range recs {
		out = append(out, recordToAPI(rec))
	}
	ensureRequestID(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(out)
}

func (h *StatusHandler) handleCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("buildId")
	result, err := h.svc.Cancel(id)
	if err != nil {
		switch {
		case errors.Is(err, jobs.ErrNotFound):
			WriteError(w, http.StatusNotFound, "not_found", "build not found", map[string]string{"buildId": id})
		case errors.Is(err, jobs.ErrConflict):
			WriteError(w, http.StatusConflict, "conflict", "build is already terminal", map[string]string{"buildId": id})
		default:
			WriteError(w, http.StatusInternalServerError, "internal_error", "unable to cancel build", map[string]string{"buildId": id})
		}
		return
	}
	ensureRequestID(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(CancelAccepted{Status: result.Status})
}

func recordToAPI(rec *jobs.Record) BuildRecord {
	out := BuildRecord{
		BuildID:   rec.ID,
		Status:    BuildStatus(rec.Status),
		Phase:     BuildPhase(rec.Phase),
		Commit:    rec.Commit,
		StartedAt: rec.StartedAt.UTC(),
	}
	if rec.ServiceID != "" {
		out.ServiceID = rec.ServiceID
	}
	if rec.Status == jobs.StatusSucceeded {
		out.Image = rec.Image
		out.Digest = rec.Digest
		if rec.ServiceID != "" {
			recorded := rec.ImageRecorded
			out.ImageRecorded = &recorded
			out.RecordedImage = rec.RecordedImage
			out.LinkedDeploymentID = rec.LinkedDeploymentID
			out.ControlError = rec.ControlError
		}
	}
	if rec.FinishedAt != nil {
		t := rec.FinishedAt.UTC()
		out.FinishedAt = &t
	}
	if rec.Error != nil {
		out.Error = &BuildError{Code: rec.Error.Code, Message: rec.Error.Message}
	}
	return out
}

// EnforceImageInvariant reports whether the record satisfies image ⟺ succeeded.
func EnforceImageInvariant(rec BuildRecord) bool {
	hasImage := rec.Image != ""
	if rec.Status == BuildStatusSucceeded {
		return hasImage
	}
	return !hasImage && rec.Digest == ""
}
