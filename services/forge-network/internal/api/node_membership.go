package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"forge.local/services/forge-network/internal/httperr"
	"forge.local/services/forge-network/internal/network"
)

// NodeMembershipHandler serves PATCH /v1/nodes/{id}/network-membership.
type NodeMembershipHandler struct {
	Store *network.MembershipStore
	Log   *slog.Logger
}

// Register mounts membership routes.
func (h *NodeMembershipHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("PATCH /v1/nodes/{id}/network-membership", h.patch)
	mux.HandleFunc("GET /v1/nodes/{id}/network-membership", h.get)
}

type membershipPatchRequest struct {
	Membership      *string `json:"membership"`
	DockerColocated *bool   `json:"docker_colocated"`
}

func (h *NodeMembershipHandler) patch(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		httperr.Write(w, http.StatusBadRequest, "invalid_request", "id is required")
		return
	}
	var req membershipPatchRequest
	if err := decodeJSON(r, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if req.Membership == nil && req.DockerColocated == nil {
		httperr.Write(w, http.StatusBadRequest, "invalid_request", "membership or docker_colocated is required")
		return
	}
	row, err := h.Store.UpsertMembership(r.Context(), id, req.Membership, req.DockerColocated)
	if err != nil {
		httperr.Write(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if h.Log != nil {
		mem := ""
		if row.Membership != nil {
			mem = *row.Membership
		}
		h.Log.Info("network membership updated",
			"event", "network.membership.updated",
			"node_id", row.NodeID,
			"membership", mem,
			"docker_colocated", row.DockerColocated,
		)
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *NodeMembershipHandler) get(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	row, err := h.Store.GetMembership(r.Context(), id)
	if errors.Is(err, network.ErrNodeMembershipNotFound) {
		writeJSON(w, http.StatusOK, network.NodeMembership{
			NodeID:          id,
			Membership:      nil,
			DockerColocated: false,
		})
		return
	}
	if err != nil {
		httperr.Write(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, row)
}
