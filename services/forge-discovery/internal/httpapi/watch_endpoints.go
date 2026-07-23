package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"forge.local/services/forge-discovery/internal/store"
	"forge.local/services/forge-discovery/internal/watchhub"
	"go.opentelemetry.io/otel/attribute"
)

func (h *EndpointsHandler) handleWatch(w http.ResponseWriter, r *http.Request) {
	ctx, span := h.tracer().Start(r.Context(), "discovery.endpoints.watch")
	defer span.End()

	project := r.PathValue("project")
	environment := r.PathValue("environment")
	service := r.PathValue("service")
	if project == "" || environment == "" || service == "" {
		writeErr(w, http.StatusBadRequest, "project, environment, and service are required")
		return
	}
	if h.Watch == nil {
		writeErr(w, http.StatusServiceUnavailable, "watch broker not configured")
		return
	}

	sinceRaw := r.URL.Query().Get("since")
	if sinceRaw == "" {
		writeErr(w, http.StatusBadRequest, "since is required")
		return
	}
	since, err := strconv.ParseInt(sinceRaw, 10, 64)
	if err != nil || since < 0 {
		writeErr(w, http.StatusBadRequest, "since must be a non-negative integer resourceVersion")
		return
	}
	span.SetAttributes(
		attribute.String("service", service),
		attribute.Int64("since", since),
	)

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	if !h.Watch.TryAcquireConnection() {
		writeErr(w, http.StatusServiceUnavailable, "watch connection limit reached")
		return
	}
	defer h.Watch.ReleaseConnection()

	if h.Log != nil {
		h.Log.Info("watch connected",
			"event", "discovery.watch.connected",
			"service", service,
			"since", since,
			"project", project,
			"environment", environment,
		)
	}
	defer func() {
		if h.Log != nil {
			h.Log.Info("watch disconnected",
				"event", "discovery.watch.disconnected",
				"service", service,
				"since", since,
			)
		}
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	heartbeat := h.WatchHeartbeat
	if heartbeat <= 0 {
		heartbeat = 15 * time.Second
	}

	replay := h.Watch.Replay(project, environment, service, since)
	var cursor int64 = since
	if replay.Miss {
		if err := h.writeResync(ctx, w, flusher, project, environment, service, &cursor); err != nil {
			return
		}
	} else {
		for _, ev := range replay.Events {
			if err := writeWatchEvent(w, flusher, ev); err != nil {
				return
			}
			rv, _ := strconv.ParseInt(ev.ResourceVersion, 10, 64)
			if rv > cursor {
				cursor = rv
			}
		}
	}

	sub := h.Watch.Subscribe(project, environment, service)
	defer h.Watch.Unsubscribe(project, environment, service, sub)

	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.Context().Done():
			return
		case ev, ok := <-sub:
			if !ok {
				return
			}
			rv, _ := strconv.ParseInt(ev.ResourceVersion, 10, 64)
			if rv <= cursor {
				continue
			}
			if err := writeWatchEvent(w, flusher, ev); err != nil {
				return
			}
			cursor = rv
		case <-ticker.C:
			if _, err := fmt.Fprintf(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (h *EndpointsHandler) writeResync(
	ctx context.Context,
	w http.ResponseWriter,
	flusher http.Flusher,
	project, environment, service string,
	cursor *int64,
) error {
	var rows []store.EndpointRow
	var err error
	if listStore, ok := h.Store.(ListEndpointStore); ok {
		rows, err = listStore.ListEndpoints(ctx, store.ListFilter{
			Project: project, Environment: environment, Service: service, ReadyOnly: true,
		})
	} else {
		all, listErr := h.Store.ListServiceEndpoints(ctx, project, environment, service)
		err = listErr
		for _, row := range all {
			if row.Phase == "Ready" {
				rows = append(rows, row)
			}
		}
	}
	if err != nil {
		_, _ = fmt.Fprintf(w, "event: error\ndata: %q\n\n", err.Error())
		flusher.Flush()
		return err
	}
	for _, row := range rows {
		ev := h.Watch.Publish(watchhub.Event{
			Type:        watchhub.EventAdded,
			Project:     project,
			Environment: environment,
			Service:     service,
			Payload:     endpointPayloadFromRow(row),
		})
		if h.Metrics != nil {
			h.Metrics.IncWatchEvent(string(ev.Type))
		}
		if err := writeWatchEvent(w, flusher, ev); err != nil {
			return err
		}
		rv, _ := strconv.ParseInt(ev.ResourceVersion, 10, 64)
		if rv > *cursor {
			*cursor = rv
		}
	}
	return nil
}

func writeWatchEvent(w http.ResponseWriter, flusher http.Flusher, ev watchhub.Event) error {
	data, err := json.Marshal(ev.Payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\nid: %s\ndata: %s\n\n", ev.Type, ev.ResourceVersion, data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func endpointPayloadFromRow(row store.EndpointRow) watchhub.EndpointPayload {
	p := watchhub.EndpointPayload{
		ID:            row.ID,
		Service:       row.Service,
		Node:          row.NodeID,
		Phase:         row.Phase,
		Ready:         row.Ready,
		Revision:      row.Revision,
		Protocol:      row.Protocol,
		UnreadyReason: row.UnreadyReason,
	}
	p.Address = &struct {
		IP   string `json:"ip"`
		Port int    `json:"port"`
	}{IP: row.AddressIP, Port: row.AddressPort}
	return p
}

// PublishAdded publishes an added watch event for a registered endpoint.
func (h *EndpointsHandler) PublishAdded(row store.EndpointRow) {
	h.publish(watchhub.EventAdded, row)
}

// PublishUpdated publishes an updated watch event.
func (h *EndpointsHandler) PublishUpdated(row store.EndpointRow) {
	h.publish(watchhub.EventUpdated, row)
}

// PublishRemoved publishes a removed watch event.
func (h *EndpointsHandler) PublishRemoved(row store.EndpointRow) {
	if h.Watch == nil {
		return
	}
	h.Watch.Publish(watchhub.Event{
		Type:        watchhub.EventRemoved,
		Project:     row.Project,
		Environment: row.Environment,
		Service:     row.Service,
		Payload: watchhub.EndpointPayload{
			ID: row.ID,
		},
	})
	if h.Metrics != nil {
		h.Metrics.IncWatchEvent(string(watchhub.EventRemoved))
	}
}

func (h *EndpointsHandler) publish(typ watchhub.EventType, row store.EndpointRow) {
	if h.Watch == nil {
		return
	}
	h.Watch.Publish(watchhub.Event{
		Type:        typ,
		Project:     row.Project,
		Environment: row.Environment,
		Service:     row.Service,
		Payload:     endpointPayloadFromRow(row),
	})
	if h.Metrics != nil {
		h.Metrics.IncWatchEvent(string(typ))
	}
}
