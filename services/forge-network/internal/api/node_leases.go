package api

import (
	"errors"
	"log/slog"
	"net/http"

	"forge.local/services/forge-network/internal/httperr"
	"forge.local/services/forge-network/internal/network"
)

// NodeLeasesHandler serves node block lease endpoints.
type NodeLeasesHandler struct {
	Alloc    *network.Allocator
	Computer *network.PeerSetComputer
	Log      *slog.Logger
}

// Register mounts node-lease routes.
func (h *NodeLeasesHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/networks/{name}/node-leases", h.allocate)
	mux.HandleFunc("DELETE /v1/networks/{name}/node-leases/{id}", h.release)
}

type nodeLeaseRequest struct {
	NodeID string `json:"node_id"`
}

func (h *NodeLeasesHandler) allocate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req nodeLeaseRequest
	if err := decodeJSON(r, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	lease, err := h.Alloc.AllocateNodeLease(r.Context(), name, req.NodeID)
	if err != nil {
		writeAllocErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lease)
}

func (h *NodeLeasesHandler) release(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	nodeID := r.PathValue("id")
	if err := h.Alloc.ReleaseNodeLease(r.Context(), name, nodeID); err != nil {
		if errors.Is(err, network.ErrNetworkNotFound) || errors.Is(err, network.ErrNodeLeaseNotFound) {
			httperr.Write(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		httperr.Write(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if h.Computer != nil {
		if _, err := h.Computer.OnLeave(r.Context(), name, nodeID); err != nil && h.Log != nil {
			h.Log.Warn("peer leave after node lease release failed",
				"network", name, "node_id", nodeID, "error", err.Error())
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeAllocErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, network.ErrNetworkNotFound), errors.Is(err, network.ErrNodeLeaseNotFound):
		httperr.Write(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, network.ErrNoAddressSpace):
		httperr.Write(w, http.StatusConflict, "NoAddressSpaceAvailable", "cluster CIDR has no free node blocks")
	case errors.Is(err, network.ErrNodeBlockExhausted):
		httperr.Write(w, http.StatusConflict, "NodeBlockExhausted", "node block has no free addresses")
	case errors.Is(err, network.ErrNetworkNotReady), errors.Is(err, network.ErrCidrCollision):
		httperr.Write(w, http.StatusConflict, "NetworkNotReady", err.Error())
	default:
		httperr.Write(w, http.StatusBadRequest, "invalid_request", err.Error())
	}
}
