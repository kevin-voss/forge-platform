package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"forge.local/services/forge-network/internal/httperr"
	"forge.local/services/forge-network/internal/policy"
)

// NetworkPoliciesHandler serves NetworkPolicy CRUD.
type NetworkPoliciesHandler struct {
	Store *policy.Store
	Log   *slog.Logger
}

// Register mounts policy routes.
func (h *NetworkPoliciesHandler) Register(mux *http.ServeMux) {
	base := "/v1/projects/{project}/environments/{environment}/network-policies"
	mux.HandleFunc("POST "+base, h.create)
	mux.HandleFunc("GET "+base, h.list)
	mux.HandleFunc("GET "+base+"/{name}", h.get)
	mux.HandleFunc("PUT "+base+"/{name}", h.put)
	mux.HandleFunc("PATCH "+base+"/{name}", h.put)
	mux.HandleFunc("DELETE "+base+"/{name}", h.delete)
}

type createPolicyRequest struct {
	Name         string            `json:"name"`
	Organization string            `json:"organization"`
	Spec         policy.PolicySpec `json:"spec"`
}

func (h *NetworkPoliciesHandler) create(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	env := r.PathValue("environment")
	var req createPolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	org := strings.TrimSpace(req.Organization)
	if org == "" {
		org = "default"
	}
	if strings.TrimSpace(req.Name) == "" {
		httperr.Write(w, http.StatusBadRequest, "invalid_request", "name is required")
		return
	}
	row, err := h.Store.CreatePolicy(r.Context(), org, project, env, req.Name, req.Spec)
	if err != nil {
		if errors.Is(err, policy.ErrPolicyAlreadyExists) {
			httperr.Write(w, http.StatusConflict, "already_exists", err.Error())
			return
		}
		httperr.Write(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, row.ToEnvelope())
}

func (h *NetworkPoliciesHandler) list(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	env := r.PathValue("environment")
	org := orgFromQuery(r)
	rows, err := h.Store.ListPolicies(r.Context(), org, project, env)
	if err != nil {
		httperr.Write(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	items := make([]policy.NetworkPolicyEnvelope, 0, len(rows))
	for _, row := range rows {
		items = append(items, row.ToEnvelope())
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *NetworkPoliciesHandler) get(w http.ResponseWriter, r *http.Request) {
	row, err := h.Store.GetPolicy(r.Context(), orgFromQuery(r), r.PathValue("project"),
		r.PathValue("environment"), r.PathValue("name"))
	if err != nil {
		if errors.Is(err, policy.ErrPolicyNotFound) {
			httperr.Write(w, http.StatusNotFound, "not_found", "network policy not found")
			return
		}
		httperr.Write(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, row.ToEnvelope())
}

func (h *NetworkPoliciesHandler) put(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Spec policy.PolicySpec `json:"spec"`
	}
	if err := decodeJSON(r, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	row, err := h.Store.UpdatePolicy(r.Context(), orgFromQuery(r), r.PathValue("project"),
		r.PathValue("environment"), r.PathValue("name"), req.Spec)
	if err != nil {
		if errors.Is(err, policy.ErrPolicyNotFound) {
			httperr.Write(w, http.StatusNotFound, "not_found", "network policy not found")
			return
		}
		httperr.Write(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, row.ToEnvelope())
}

func (h *NetworkPoliciesHandler) delete(w http.ResponseWriter, r *http.Request) {
	err := h.Store.DeletePolicy(r.Context(), orgFromQuery(r), r.PathValue("project"),
		r.PathValue("environment"), r.PathValue("name"))
	if err != nil {
		if errors.Is(err, policy.ErrPolicyNotFound) {
			httperr.Write(w, http.StatusNotFound, "not_found", "network policy not found")
			return
		}
		httperr.Write(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func orgFromQuery(r *http.Request) string {
	org := strings.TrimSpace(r.URL.Query().Get("organization"))
	if org == "" {
		org = "default"
	}
	return org
}
