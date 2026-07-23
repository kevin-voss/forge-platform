package api

import (
	"log/slog"
	"net/http"

	"forge.local/services/forge-network/internal/httperr"
	"forge.local/services/forge-network/internal/network"
)

// RotateKeyHandler serves key rotation and explicit retire.
type RotateKeyHandler struct {
	Registry *network.PeerRegistry
	Computer *network.PeerSetComputer
	Log      *slog.Logger
}

// Register mounts rotate/retire routes.
func (h *RotateKeyHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/networks/{name}/nodes/{node_id}/rotate-key", h.rotate)
	mux.HandleFunc("POST /v1/networks/{name}/nodes/{node_id}/retire-key", h.retire)
}

type rotateKeyRequest struct {
	NewPublicKey string `json:"new_public_key"`
}

func (h *RotateKeyHandler) rotate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	nodeID := r.PathValue("node_id")
	var req rotateKeyRequest
	if err := decodeJSON(r, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	result, err := h.Registry.RotateKey(r.Context(), name, nodeID, req.NewPublicKey)
	if err != nil {
		writePeerErr(w, err)
		return
	}
	if _, err := h.Computer.OnRotate(r.Context(), name, nodeID); err != nil {
		writePeerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *RotateKeyHandler) retire(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	nodeID := r.PathValue("node_id")
	if err := h.Registry.RetireOldKey(r.Context(), name, nodeID); err != nil {
		writePeerErr(w, err)
		return
	}
	if _, err := h.Computer.OnRetire(r.Context(), name, nodeID); err != nil {
		writePeerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": network.PeerStatusActive})
}
