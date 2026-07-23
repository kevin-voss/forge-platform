package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"forge.local/services/forge-network/internal/httperr"
	"forge.local/services/forge-network/internal/network"
)

// NetworksHandler serves Network CRUD.
type NetworksHandler struct {
	Alloc *network.Allocator
	Log   *slog.Logger
}

// Register mounts network routes.
func (h *NetworksHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/networks", h.create)
	mux.HandleFunc("GET /v1/networks", h.list)
	mux.HandleFunc("GET /v1/networks/{name}", h.get)
}

type createNetworkRequest struct {
	Name string              `json:"name"`
	Spec network.NetworkSpec `json:"spec"`
}

func (h *NetworksHandler) create(w http.ResponseWriter, r *http.Request) {
	var req createNetworkRequest
	if err := decodeJSON(r, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		httperr.Write(w, http.StatusBadRequest, "invalid_request", "name is required")
		return
	}
	prefix := req.Spec.NodePrefixLength
	if prefix == 0 {
		prefix = 24
	}
	cidr := strings.TrimSpace(req.Spec.ClusterCIDR)
	if cidr == "" {
		httperr.Write(w, http.StatusBadRequest, "invalid_request", "spec.clusterCidr is required")
		return
	}

	row, err := h.Alloc.CreateNetwork(r.Context(), req.Name, cidr, prefix, req.Spec.IPv6CIDR)
	if err != nil {
		if errors.Is(err, network.ErrCidrCollision) {
			httperr.WriteDetails(w, http.StatusConflict, "CidrCollision", err.Error(), map[string]string{
				"name":  req.Name,
				"phase": "Failed",
			})
			return
		}
		if strings.Contains(err.Error(), "already exists") {
			httperr.Write(w, http.StatusConflict, "already_exists", err.Error())
			return
		}
		httperr.Write(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, row.ToEnvelope())
}

func (h *NetworksHandler) list(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Alloc.ListNetworks(r.Context())
	if err != nil {
		httperr.Write(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	items := make([]network.Network, 0, len(rows))
	for _, row := range rows {
		items = append(items, row.ToEnvelope())
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *NetworksHandler) get(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	row, err := h.Alloc.GetNetworkByName(r.Context(), name)
	if err != nil {
		if errors.Is(err, network.ErrNetworkNotFound) {
			httperr.Write(w, http.StatusNotFound, "not_found", "network not found")
			return
		}
		httperr.Write(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, row.ToEnvelope())
}

func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
