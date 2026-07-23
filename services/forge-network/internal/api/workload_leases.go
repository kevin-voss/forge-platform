package api

import (
	"errors"
	"log/slog"
	"net/http"

	"forge.local/services/forge-network/internal/httperr"
	"forge.local/services/forge-network/internal/network"
)

// WorkloadLeasesHandler serves workload address lease endpoints.
type WorkloadLeasesHandler struct {
	Alloc *network.Allocator
	Log   *slog.Logger
}

// Register mounts workload-lease routes.
func (h *WorkloadLeasesHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/networks/{name}/workload-leases", h.allocate)
	mux.HandleFunc("DELETE /v1/networks/{name}/workload-leases/{id}", h.release)
}

type workloadLeaseRequest struct {
	NodeID     string `json:"node_id"`
	WorkloadID string `json:"workload_id"`
}

func (h *WorkloadLeasesHandler) allocate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req workloadLeaseRequest
	if err := decodeJSON(r, &req); err != nil {
		httperr.Write(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	lease, err := h.Alloc.AllocateWorkloadLease(r.Context(), name, req.NodeID, req.WorkloadID)
	if err != nil {
		writeAllocErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"workload_id": lease.WorkloadID,
		"address":     lease.Address,
	})
}

func (h *WorkloadLeasesHandler) release(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	workloadID := r.PathValue("id")
	if err := h.Alloc.ReleaseWorkloadLease(r.Context(), name, workloadID); err != nil {
		if errors.Is(err, network.ErrNetworkNotFound) || errors.Is(err, network.ErrWorkloadLeaseMissing) {
			httperr.Write(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		httperr.Write(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
