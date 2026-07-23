package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"forge.local/services/forge-network/internal/httperr"
	"forge.local/services/forge-network/internal/network"
)

// TransportHandler serves GET /v1/networks/{name}/transport.
type TransportHandler struct {
	Store *network.MembershipStore
	Log   *slog.Logger
}

// Register mounts transport routes.
func (h *TransportHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/networks/{name}/transport", h.get)
}

func (h *TransportHandler) get(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	from := strings.TrimSpace(r.URL.Query().Get("from"))
	to := strings.TrimSpace(r.URL.Query().Get("to"))
	pair, err := h.Store.GetTransport(r.Context(), name, from, to)
	if err != nil {
		switch {
		case errors.Is(err, network.ErrNetworkNotFound):
			httperr.Write(w, http.StatusNotFound, "not_found", err.Error())
		case strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "must differ"):
			httperr.Write(w, http.StatusBadRequest, "invalid_request", err.Error())
		default:
			httperr.Write(w, http.StatusInternalServerError, "internal_error", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, pair)
}
