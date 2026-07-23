package api

import (
	"encoding/json"
	"net/http"

	"forge.local/services/forge-infrastructure/internal/operations"
)

// Handler serves infrastructure debug/health companion routes.
type Handler struct {
	Ledger *operations.Ledger
}

// Register mounts /v1/operations/{opId}.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/operations/{opId}", h.handleGetOperation)
}

func (h *Handler) handleGetOperation(w http.ResponseWriter, r *http.Request) {
	opID := r.PathValue("opId")
	if opID == "" {
		writeErr(w, http.StatusBadRequest, "opId is required")
		return
	}
	if h.Ledger == nil {
		writeErr(w, http.StatusServiceUnavailable, "ledger not available")
		return
	}
	op, err := h.Ledger.Get(r.Context(), opID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if op == nil {
		writeErr(w, http.StatusNotFound, "operation not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":           op.ID,
		"providerName": op.ProviderName,
		"kind":         op.Kind,
		"targetKind":   op.TargetKind,
		"targetId":     op.TargetID,
		"naturalKey":   op.NaturalKey,
		"request":      json.RawMessage(op.Request),
		"status":       op.Status,
		"result":       json.RawMessage(op.Result),
		"error":        op.Error,
		"createdAt":    op.CreatedAt,
		"completedAt":  op.CompletedAt,
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
