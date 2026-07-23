package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"forge.local/services/forge-discovery/internal/store"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// EndpointStore is the persistence surface used by registration handlers.
type EndpointStore interface {
	Register(ctx context.Context, in store.RegisterInput) (store.EndpointRow, error)
	Renew(ctx context.Context, in store.RenewInput) (store.EndpointRow, error)
	Deregister(ctx context.Context, project, environment, id string) error
	ListServiceEndpoints(ctx context.Context, project, environment, service string) ([]store.EndpointRow, error)
}

// MirrorNotifier is notified after accepted local writes (best-effort).
type MirrorNotifier interface {
	NotifyEndpointUpsert(row store.EndpointRow)
	NotifyEndpointDelete(project, environment, id string)
	NotifyServiceUpsert(project, environment, service string)
}

// EndpointsHandler serves register / renew / deregister / list.
type EndpointsHandler struct {
	Store          EndpointStore
	Log            *slog.Logger
	DefaultLease   int
	Mirror         MirrorNotifier
	TracerProvider trace.TracerProvider
}

// Register mounts endpoint routes.
func (h *EndpointsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/projects/{project}/environments/{environment}/services/{service}/endpoints", h.handleRegister)
	mux.HandleFunc("GET /v1/projects/{project}/environments/{environment}/services/{service}/endpoints", h.handleList)
	mux.HandleFunc("POST /v1/projects/{project}/environments/{environment}/endpoints/{id}/renew", h.handleRenew)
	mux.HandleFunc("DELETE /v1/projects/{project}/environments/{environment}/endpoints/{id}", h.handleDeregister)
}

type registerRequest struct {
	ID      string `json:"id"`
	Node    string `json:"node"`
	Address struct {
		IP   string `json:"ip"`
		Port int    `json:"port"`
	} `json:"address"`
	Protocol     string `json:"protocol"`
	Revision     string `json:"revision"`
	LeaseSeconds int    `json:"leaseSeconds"`
}

type registerResponse struct {
	ID        string `json:"id"`
	Service   string `json:"service"`
	Phase     string `json:"phase"`
	ExpiresAt string `json:"expiresAt"`
}

type renewRequest struct {
	Ready        bool `json:"ready"`
	LeaseSeconds int  `json:"leaseSeconds"`
}

type renewResponse struct {
	ID        string `json:"id"`
	Phase     string `json:"phase"`
	ExpiresAt string `json:"expiresAt"`
}

type endpointListItem struct {
	ID            string  `json:"id"`
	Service       string  `json:"service"`
	Node          string  `json:"node"`
	Phase         string  `json:"phase"`
	Ready         bool    `json:"ready"`
	ExpiresAt     string  `json:"expiresAt"`
	UnreadyReason *string `json:"unreadyReason,omitempty"`
	Address       struct {
		IP   string `json:"ip"`
		Port int    `json:"port"`
	} `json:"address"`
	Protocol string `json:"protocol"`
	Revision string `json:"revision,omitempty"`
}

func (h *EndpointsHandler) handleRegister(w http.ResponseWriter, r *http.Request) {
	ctx, span := h.tracer().Start(r.Context(), "discovery.endpoint.register")
	defer span.End()

	project := r.PathValue("project")
	environment := r.PathValue("environment")
	service := r.PathValue("service")
	if project == "" || environment == "" || service == "" {
		writeErr(w, http.StatusBadRequest, "project, environment, and service are required")
		return
	}

	var req registerRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		writeErr(w, http.StatusBadRequest, "id is required")
		return
	}
	if strings.TrimSpace(req.Node) == "" {
		writeErr(w, http.StatusBadRequest, "node is required")
		return
	}
	if strings.TrimSpace(req.Address.IP) == "" || req.Address.Port < 1 || req.Address.Port > 65535 {
		writeErr(w, http.StatusBadRequest, "address.ip and address.port are required")
		return
	}
	lease := req.LeaseSeconds
	if lease <= 0 {
		lease = h.DefaultLease
	}
	if lease <= 0 {
		lease = 20
	}

	span.SetAttributes(
		attribute.String("endpoint.id", req.ID),
		attribute.String("service", service),
		attribute.String("node", req.Node),
	)

	row, err := h.Store.Register(ctx, store.RegisterInput{
		ID:           req.ID,
		Project:      project,
		Environment:  environment,
		Service:      service,
		NodeID:       req.Node,
		AddressIP:    req.Address.IP,
		AddressPort:  req.Address.Port,
		Protocol:     req.Protocol,
		Revision:     req.Revision,
		LeaseSeconds: lease,
	})
	if err != nil {
		if h.Log != nil {
			h.Log.Error("endpoint register failed", "error", err.Error())
		}
		writeErr(w, http.StatusInternalServerError, "register failed")
		return
	}

	if h.Log != nil {
		h.Log.Info("endpoint registered",
			"event", "discovery.endpoint.registered",
			"id", row.ID,
			"service", row.Service,
			"node", row.NodeID,
			"address", row.AddressIP+":"+itoa(row.AddressPort),
		)
	}
	if h.Mirror != nil {
		h.Mirror.NotifyServiceUpsert(project, environment, service)
		h.Mirror.NotifyEndpointUpsert(row)
	}

	writeJSON(w, http.StatusOK, registerResponse{
		ID:        row.ID,
		Service:   row.Service,
		Phase:     row.Phase,
		ExpiresAt: row.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

func (h *EndpointsHandler) handleRenew(w http.ResponseWriter, r *http.Request) {
	ctx, span := h.tracer().Start(r.Context(), "discovery.endpoint.renew")
	defer span.End()

	project := r.PathValue("project")
	environment := r.PathValue("environment")
	id := r.PathValue("id")
	if project == "" || environment == "" || id == "" {
		writeErr(w, http.StatusBadRequest, "project, environment, and id are required")
		return
	}

	var req renewRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	lease := req.LeaseSeconds
	if lease <= 0 {
		lease = h.DefaultLease
	}
	if lease <= 0 {
		lease = 20
	}

	span.SetAttributes(attribute.String("endpoint.id", id), attribute.Bool("ready", req.Ready))

	row, err := h.Store.Renew(ctx, store.RenewInput{
		Project:      project,
		Environment:  environment,
		ID:           id,
		Ready:        req.Ready,
		LeaseSeconds: lease,
	})
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "endpoint not found")
		return
	}
	if err != nil {
		if h.Log != nil {
			h.Log.Error("endpoint renew failed", "error", err.Error())
		}
		writeErr(w, http.StatusInternalServerError, "renew failed")
		return
	}
	if h.Mirror != nil {
		h.Mirror.NotifyEndpointUpsert(row)
	}
	writeJSON(w, http.StatusOK, renewResponse{
		ID:        row.ID,
		Phase:     row.Phase,
		ExpiresAt: row.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

func (h *EndpointsHandler) handleDeregister(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project := r.PathValue("project")
	environment := r.PathValue("environment")
	id := r.PathValue("id")
	if project == "" || environment == "" || id == "" {
		writeErr(w, http.StatusBadRequest, "project, environment, and id are required")
		return
	}
	err := h.Store.Deregister(ctx, project, environment, id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "endpoint not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "deregister failed")
		return
	}
	if h.Mirror != nil {
		h.Mirror.NotifyEndpointDelete(project, environment, id)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *EndpointsHandler) handleList(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	environment := r.PathValue("environment")
	service := r.PathValue("service")
	rows, err := h.Store.ListServiceEndpoints(r.Context(), project, environment, service)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list failed")
		return
	}
	out := make([]endpointListItem, 0, len(rows))
	for _, row := range rows {
		item := endpointListItem{
			ID:            row.ID,
			Service:       row.Service,
			Node:          row.NodeID,
			Phase:         row.Phase,
			Ready:         row.Ready,
			ExpiresAt:     row.ExpiresAt.UTC().Format(time.RFC3339),
			UnreadyReason: row.UnreadyReason,
			Protocol:      row.Protocol,
			Revision:      row.Revision,
		}
		item.Address.IP = row.AddressIP
		item.Address.Port = row.AddressPort
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *EndpointsHandler) tracer() trace.Tracer {
	if h.TracerProvider != nil {
		return h.TracerProvider.Tracer("forge-discovery")
	}
	return otel.Tracer("forge-discovery")
}

func decodeJSON(r *http.Request, dest any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(dest); err != nil {
		return err
	}
	return nil
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
