package api

import (
	"log/slog"
	"net/http"

	"forge.local/services/forge-network/internal/health"
	"forge.local/services/forge-network/internal/network"
)

// Deps wires HTTP handlers.
type Deps struct {
	Alloc *network.Allocator
	DB    health.ReadyChecker
	Log   *slog.Logger
}

// NewRouter builds the forge-network HTTP mux.
func NewRouter(d Deps) *http.ServeMux {
	mux := http.NewServeMux()
	(&health.Handler{DB: d.DB}).Register(mux)
	n := &NetworksHandler{Alloc: d.Alloc, Log: d.Log}
	n.Register(mux)
	(&NodeLeasesHandler{Alloc: d.Alloc, Log: d.Log}).Register(mux)
	(&WorkloadLeasesHandler{Alloc: d.Alloc, Log: d.Log}).Register(mux)
	return mux
}
