package httpapi

import "net/http"

// NewRouter mounts Discovery HTTP routes (health for 21.01; APIs in later steps).
func NewRouter(ready *Readiness) *http.ServeMux {
	mux := http.NewServeMux()
	NewHealthHandler(ready).Register(mux)
	return mux
}
