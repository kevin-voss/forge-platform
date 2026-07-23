package httpapi

import (
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel/trace"
)

// RouterDeps wires Discovery HTTP handlers.
type RouterDeps struct {
	Ready          *Readiness
	Endpoints      *EndpointsHandler
	Log            *slog.Logger
	TracerProvider trace.TracerProvider
}

// NewRouter mounts Discovery HTTP routes.
func NewRouter(ready *Readiness) *http.ServeMux {
	return NewRouterWith(RouterDeps{Ready: ready})
}

// NewRouterWith mounts health + endpoint registration routes.
func NewRouterWith(deps RouterDeps) *http.ServeMux {
	mux := http.NewServeMux()
	NewHealthHandler(deps.Ready).Register(mux)
	if deps.Endpoints != nil {
		if deps.Endpoints.TracerProvider == nil {
			deps.Endpoints.TracerProvider = deps.TracerProvider
		}
		if deps.Endpoints.Log == nil {
			deps.Endpoints.Log = deps.Log
		}
		deps.Endpoints.Register(mux)
	}
	return mux
}
