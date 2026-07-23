package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"forge.local/services/forge-autoscaler/internal/audit"
	"forge.local/services/forge-autoscaler/internal/httperr"
	"forge.local/services/forge-autoscaler/internal/policy"
)

// Routes hosts ScalingPolicy CRUD/status/watch/override handlers.
type Routes struct {
	Store  *policy.Store
	Hub    *policy.Hub
	Events audit.Publisher
}

// Register mounts policy routes on mux.
func (r *Routes) Register(mux *http.ServeMux) {
	base := "/v1/projects/{project}/environments/{environment}/scalingpolicies"
	mux.HandleFunc("POST "+base, r.create)
	mux.HandleFunc("GET "+base, r.list)
	mux.HandleFunc("GET "+base+"/{name}", r.get)
	mux.HandleFunc("PUT "+base+"/{name}", r.put)
	mux.HandleFunc("PATCH "+base+"/{name}", r.patch)
	mux.HandleFunc("DELETE "+base+"/{name}", r.delete)
	mux.HandleFunc("PUT "+base+"/{name}/status", r.putStatus)
	mux.HandleFunc("PUT "+base+"/{name}/override", r.putOverride)
	mux.HandleFunc("GET "+base+"/{name}/override", r.getOverride)
	mux.HandleFunc("DELETE "+base+"/{name}/override", r.deleteOverride)
	mux.HandleFunc("GET /v1/watch/scalingpolicies", r.watch)
}

type createRequest struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec policy.ScalingPolicySpec `json:"spec"`
}

func (r *Routes) create(w http.ResponseWriter, req *http.Request) {
	project := req.PathValue("project")
	env := req.PathValue("environment")
	raw, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid_body", "unable to read body")
		return
	}
	var body createRequest
	if err := json.Unmarshal(raw, &body); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid_body", "JSON body is invalid")
		return
	}
	name := strings.TrimSpace(body.Metadata.Name)
	if name == "" {
		httperr.Write(w, http.StatusBadRequest, "validation_error", "metadata.name is required")
		return
	}
	idem := req.Header.Get("Idempotency-Key")
	envelope, status, err := r.Store.Create(req.Context(), project, env, name, body.Spec, idem, string(raw))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, status, envelope)
}

func (r *Routes) list(w http.ResponseWriter, req *http.Request) {
	rows, err := r.Store.List(req.Context(), req.PathValue("project"), req.PathValue("environment"))
	if err != nil {
		httperr.Write(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	items := make([]policy.Envelope, 0, len(rows))
	for _, row := range rows {
		items = append(items, row.ToEnvelope())
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (r *Routes) get(w http.ResponseWriter, req *http.Request) {
	row, err := r.Store.Get(req.Context(), req.PathValue("project"), req.PathValue("environment"), req.PathValue("name"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row.ToEnvelope())
}

type replaceRequest struct {
	Metadata struct {
		ResourceVersion string `json:"resourceVersion"`
	} `json:"metadata"`
	Spec policy.ScalingPolicySpec `json:"spec"`
}

func (r *Routes) put(w http.ResponseWriter, req *http.Request) {
	var body replaceRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid_body", "JSON body is invalid")
		return
	}
	rv, err := policy.ParseRV(body.Metadata.ResourceVersion)
	if err != nil || body.Metadata.ResourceVersion == "" {
		httperr.Write(w, http.StatusBadRequest, "validation_error", "metadata.resourceVersion is required")
		return
	}
	envelope, err := r.Store.ReplaceSpec(req.Context(),
		req.PathValue("project"), req.PathValue("environment"), req.PathValue("name"),
		rv, body.Spec,
	)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, envelope)
}

func (r *Routes) patch(w http.ResponseWriter, req *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
	if err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid_body", "unable to read body")
		return
	}
	var envelope struct {
		Metadata struct {
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
		Spec json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid_body", "JSON body is invalid")
		return
	}
	rv, err := policy.ParseRV(envelope.Metadata.ResourceVersion)
	if err != nil || envelope.Metadata.ResourceVersion == "" {
		httperr.Write(w, http.StatusBadRequest, "validation_error", "metadata.resourceVersion is required")
		return
	}
	var patch policy.ScalingPolicySpec
	var patchMap map[string]json.RawMessage
	if len(envelope.Spec) > 0 {
		if err := json.Unmarshal(envelope.Spec, &patch); err != nil {
			httperr.Write(w, http.StatusBadRequest, "invalid_body", "spec is invalid")
			return
		}
		_ = json.Unmarshal(envelope.Spec, &patchMap)
	}
	out, err := r.Store.PatchSpec(req.Context(),
		req.PathValue("project"), req.PathValue("environment"), req.PathValue("name"),
		rv, patch, patchMap,
	)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type statusRequest struct {
	Metadata struct {
		ResourceVersion string `json:"resourceVersion"`
	} `json:"metadata"`
	Status policy.ScalingPolicyStatus `json:"status"`
}

func (r *Routes) putStatus(w http.ResponseWriter, req *http.Request) {
	var body statusRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid_body", "JSON body is invalid")
		return
	}
	rv, err := policy.ParseRV(body.Metadata.ResourceVersion)
	if err != nil || body.Metadata.ResourceVersion == "" {
		httperr.Write(w, http.StatusBadRequest, "validation_error", "metadata.resourceVersion is required")
		return
	}
	envelope, err := r.Store.ReplaceStatus(req.Context(),
		req.PathValue("project"), req.PathValue("environment"), req.PathValue("name"),
		rv, body.Status,
	)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, envelope)
}

func (r *Routes) delete(w http.ResponseWriter, req *http.Request) {
	if err := r.Store.Delete(req.Context(), req.PathValue("project"), req.PathValue("environment"), req.PathValue("name")); err != nil {
		writeStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *Routes) watch(w http.ResponseWriter, req *http.Request) {
	if r.Hub == nil {
		httperr.Write(w, http.StatusServiceUnavailable, "unavailable", "watch hub not configured")
		return
	}
	sinceRaw := req.URL.Query().Get("since")
	if sinceRaw == "" {
		httperr.Write(w, http.StatusBadRequest, "validation_error", "since is required")
		return
	}
	since, err := strconv.ParseInt(sinceRaw, 10, 64)
	if err != nil || since < 0 {
		httperr.Write(w, http.StatusBadRequest, "validation_error", "since must be a non-negative integer")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httperr.Write(w, http.StatusInternalServerError, "internal_error", "streaming unsupported")
		return
	}
	if !r.Hub.TryAcquireConnection() {
		httperr.Write(w, http.StatusServiceUnavailable, "unavailable", "watch connection limit reached")
		return
	}
	defer r.Hub.ReleaseConnection()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	replay, err := r.Hub.Replay(req.Context(), since)
	if err != nil {
		return
	}
	cursor := since
	for _, ev := range replay {
		if err := writeWatchEvent(w, flusher, ev); err != nil {
			return
		}
		rv, _ := policy.ParseRV(ev.ResourceVersion)
		if rv > cursor {
			cursor = rv
		}
	}

	sub := r.Hub.Subscribe()
	defer r.Hub.Unsubscribe(sub)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-req.Context().Done():
			return
		case ev, ok := <-sub:
			if !ok {
				return
			}
			rv, _ := policy.ParseRV(ev.ResourceVersion)
			if rv <= cursor {
				continue
			}
			if err := writeWatchEvent(w, flusher, ev); err != nil {
				return
			}
			cursor = rv
		case <-ticker.C:
			if _, err := fmt.Fprintf(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeWatchEvent(w http.ResponseWriter, flusher http.Flusher, ev policy.WatchEvent) error {
	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\nid: %s\ndata: %s\n\n", ev.Type, ev.ResourceVersion, string(raw)); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func writeStoreErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, policy.ErrNotFound):
		httperr.Write(w, http.StatusNotFound, "not_found", "ScalingPolicy not found")
	case errors.Is(err, policy.ErrAlreadyExists):
		httperr.Write(w, http.StatusConflict, "already_exists", "ScalingPolicy already exists")
	case errors.Is(err, policy.ErrConflict):
		details := map[string]string{}
		if cur := policy.ConflictCurrentRV(err); cur != "" {
			details["currentResourceVersion"] = cur
		}
		httperr.WriteDetails(w, http.StatusConflict, "resource_version_conflict", "resourceVersion is stale", details)
	case errors.Is(err, policy.ErrIdempotencyConflict):
		httperr.Write(w, http.StatusConflict, "idempotency_key_conflict", "Idempotency-Key reused with a different body")
	default:
		msg := err.Error()
		if strings.Contains(msg, "required") || strings.Contains(msg, "invalid") {
			httperr.Write(w, http.StatusBadRequest, "validation_error", msg)
			return
		}
		httperr.Write(w, http.StatusInternalServerError, "internal_error", msg)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

type overrideRequest struct {
	Metadata struct {
		ResourceVersion string `json:"resourceVersion"`
	} `json:"metadata"`
	Replicas   int    `json:"replicas"`
	Reason     string `json:"reason"`
	TTLSeconds int    `json:"ttlSeconds"`
	ExpiresAt  string `json:"expiresAt,omitempty"`
	CreatedBy  string `json:"createdBy,omitempty"`
}

func (r *Routes) putOverride(w http.ResponseWriter, req *http.Request) {
	var body overrideRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid_body", "JSON body is invalid")
		return
	}
	rv, err := policy.ParseRV(body.Metadata.ResourceVersion)
	if err != nil || body.Metadata.ResourceVersion == "" {
		httperr.Write(w, http.StatusBadRequest, "validation_error", "metadata.resourceVersion is required")
		return
	}
	if body.Replicas < 0 {
		httperr.Write(w, http.StatusBadRequest, "validation_error", "replicas must be >= 0")
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		httperr.Write(w, http.StatusBadRequest, "validation_error", "reason is required")
		return
	}
	now := time.Now().UTC()
	expiresAt := strings.TrimSpace(body.ExpiresAt)
	if expiresAt == "" {
		ttl := body.TTLSeconds
		if ttl <= 0 {
			httperr.Write(w, http.StatusBadRequest, "validation_error", "ttlSeconds or expiresAt is required")
			return
		}
		expiresAt = now.Add(time.Duration(ttl) * time.Second).Format(time.RFC3339)
	} else if _, err := time.Parse(time.RFC3339, expiresAt); err != nil {
		httperr.Write(w, http.StatusBadRequest, "validation_error", "expiresAt must be RFC3339")
		return
	}
	createdBy := strings.TrimSpace(body.CreatedBy)
	if createdBy == "" {
		createdBy = "operator"
	}
	override := policy.ManualOverride{
		Replicas:  body.Replicas,
		Reason:    reason,
		ExpiresAt: expiresAt,
		CreatedAt: now.Format(time.RFC3339),
		CreatedBy: createdBy,
	}
	project := req.PathValue("project")
	env := req.PathValue("environment")
	name := req.PathValue("name")
	envelope, err := r.Store.SetManualOverride(req.Context(), project, env, name, rv, override)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	r.publishOverride(req.Context(), audit.OverrideCreated, project, env, name, map[string]any{
		"replicas":   override.Replicas,
		"reason":     override.Reason,
		"expires_at": override.ExpiresAt,
		"created_by": override.CreatedBy,
	})
	writeJSON(w, http.StatusOK, envelope)
}

func (r *Routes) getOverride(w http.ResponseWriter, req *http.Request) {
	row, err := r.Store.Get(req.Context(), req.PathValue("project"), req.PathValue("environment"), req.PathValue("name"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if row.Status.ManualOverride == nil {
		httperr.Write(w, http.StatusNotFound, "not_found", "no active manual override")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"manualOverride":  row.Status.ManualOverride,
		"resourceVersion": policy.FormatRV(row.ResourceVersion),
	})
}

type clearOverrideRequest struct {
	Metadata struct {
		ResourceVersion string `json:"resourceVersion"`
	} `json:"metadata"`
	Reason string `json:"reason,omitempty"`
}

func (r *Routes) deleteOverride(w http.ResponseWriter, req *http.Request) {
	var body clearOverrideRequest
	if req.Body != nil {
		_ = json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&body)
	}
	rv, err := policy.ParseRV(body.Metadata.ResourceVersion)
	if err != nil || body.Metadata.ResourceVersion == "" {
		httperr.Write(w, http.StatusBadRequest, "validation_error", "metadata.resourceVersion is required")
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		reason = "cleared by operator"
	}
	project := req.PathValue("project")
	env := req.PathValue("environment")
	name := req.PathValue("name")
	envelope, err := r.Store.ClearManualOverride(req.Context(), project, env, name, rv, reason)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	r.publishOverride(req.Context(), audit.OverrideCleared, project, env, name, map[string]any{
		"reason": reason,
	})
	writeJSON(w, http.StatusOK, envelope)
}

func (r *Routes) publishOverride(ctx context.Context, eventType, project, env, name string, payload map[string]any) {
	if r.Events == nil {
		return
	}
	ev := audit.NewEvent(eventType, project, env, name, payload, time.Now().UTC())
	pubCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = r.Events.Publish(pubCtx, ev)
}
