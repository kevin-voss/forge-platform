package api

import (
	"errors"
	"log/slog"
	"net/http"

	"forge.local/services/forge-network/internal/httperr"
	"forge.local/services/forge-network/internal/network"
)

// PeersHandler serves peer distribution and registration.
type PeersHandler struct {
	Registry *network.PeerRegistry
	Computer *network.PeerSetComputer
	Log      *slog.Logger
}

// Register mounts peer routes.
func (h *PeersHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("PUT /v1/networks/{name}/nodes/{node_id}/wireguard", h.registerPeer)
	mux.HandleFunc("GET /v1/networks/{name}/nodes/{node_id}/peers", h.getPeers)
	mux.HandleFunc("POST /v1/networks/{name}/nodes/{node_id}/applied-version", h.appliedVersion)
}

type registerPeerRequest struct {
	PublicKey string `json:"public_key"`
	Endpoint  string `json:"endpoint"`
}

type appliedVersionRequest struct {
	AppliedPeerVersion int64 `json:"applied_peer_version"`
}

func (h *PeersHandler) registerPeer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	nodeID := r.PathValue("node_id")
	var req registerPeerRequest
	if err := decodeJSON(r, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	row, err := h.Registry.Register(r.Context(), name, nodeID, req.PublicKey, req.Endpoint)
	if err != nil {
		writePeerErr(w, err)
		return
	}
	if _, err := h.Computer.OnJoin(r.Context(), name, nodeID); err != nil {
		writePeerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"node_id":    row.NodeID,
		"public_key": row.PublicKey,
		"endpoint":   row.Endpoint,
		"status":     row.Status,
		"online":     row.Online,
	})
}

func (h *PeersHandler) getPeers(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	nodeID := r.PathValue("node_id")
	resp, err := h.Computer.ComputeForNode(r.Context(), name, nodeID)
	if err != nil {
		writePeerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *PeersHandler) appliedVersion(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	nodeID := r.PathValue("node_id")
	var req appliedVersionRequest
	if err := decodeJSON(r, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := h.Registry.ReportAppliedVersion(r.Context(), name, nodeID, req.AppliedPeerVersion); err != nil {
		writePeerErr(w, err)
		return
	}
	drift, _ := h.Registry.DriftCount(r.Context(), name)
	writeJSON(w, http.StatusOK, map[string]any{
		"node_id":              nodeID,
		"applied_peer_version": req.AppliedPeerVersion,
		"network_drift_total":  drift,
	})
}

func writePeerErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, network.ErrNetworkNotFound), errors.Is(err, network.ErrPeerNotFound),
		errors.Is(err, network.ErrNodeLeaseNotFound):
		httperr.Write(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, network.ErrInvalidPeerKey), errors.Is(err, network.ErrNotRotating):
		httperr.Write(w, http.StatusBadRequest, "invalid_request", err.Error())
	default:
		httperr.Write(w, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
