package api

import (
	"log/slog"
	"net/http"

	"forge.local/services/forge-network/internal/health"
	"forge.local/services/forge-network/internal/network"
)

// Deps wires HTTP handlers.
type Deps struct {
	Alloc      *network.Allocator
	Registry   *network.PeerRegistry
	Computer   *network.PeerSetComputer
	Membership *network.MembershipStore
	DB         health.ReadyChecker
	Log        *slog.Logger
}

// NewRouter builds the forge-network HTTP mux.
func NewRouter(d Deps) *http.ServeMux {
	mux := http.NewServeMux()
	(&health.Handler{DB: d.DB}).Register(mux)
	n := &NetworksHandler{Alloc: d.Alloc, Log: d.Log}
	n.Register(mux)
	(&NodeLeasesHandler{Alloc: d.Alloc, Computer: d.Computer, Membership: d.Membership, Log: d.Log}).Register(mux)
	(&WorkloadLeasesHandler{Alloc: d.Alloc, Log: d.Log}).Register(mux)
	(&PeersHandler{Registry: d.Registry, Computer: d.Computer, Log: d.Log}).Register(mux)
	(&RotateKeyHandler{Registry: d.Registry, Computer: d.Computer, Log: d.Log}).Register(mux)
	(&NodeMembershipHandler{Store: d.Membership, Log: d.Log}).Register(mux)
	(&TransportHandler{Store: d.Membership, Log: d.Log}).Register(mux)
	return mux
}
