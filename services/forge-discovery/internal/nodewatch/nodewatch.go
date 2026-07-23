package nodewatch

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
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

// Store applies node-level unready transitions.
type Store interface {
	MarkNodeUnready(ctx context.Context, nodeID string, now time.Time) ([]store.EndpointRow, error)
}

// WatchPublisher receives updated events after node-loss transitions.
type WatchPublisher interface {
	PublishUpdated(row store.EndpointRow)
}

// Config controls watch reconnect / resync.
type Config struct {
	ControlURL  string
	ResyncEvery time.Duration
	HTTPClient  *http.Client
}

// Subscriber watches Control's generic Node watch for Reachable=False.
type Subscriber struct {
	Store  Store
	Log    *slog.Logger
	Cfg    Config
	Watch  WatchPublisher
	Now    func() time.Time
	Tracer trace.Tracer
}

// Run reconnects forever until ctx is cancelled.
func (s *Subscriber) Run(ctx context.Context) {
	if s.Cfg.ResyncEvery <= 0 {
		s.Cfg.ResyncEvery = 30 * time.Second
	}
	if s.Cfg.HTTPClient == nil {
		s.Cfg.HTTPClient = &http.Client{Timeout: 0} // streaming
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	if s.Tracer == nil {
		s.Tracer = otel.Tracer("forge-discovery")
	}

	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		err := s.watchOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if s.Log != nil {
			s.Log.Warn("node watch disconnected; resyncing",
				"error", errString(err),
				"resync_after", s.Cfg.ResyncEvery.String(),
			)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(minDuration(backoff, s.Cfg.ResyncEvery)):
		}
		if backoff < s.Cfg.ResyncEvery {
			backoff *= 2
		}
	}
}

func (s *Subscriber) watchOnce(ctx context.Context) error {
	url := strings.TrimRight(s.Cfg.ControlURL, "/") + "/v1/watch/nodes?since=0"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := s.Cfg.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("watch status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	return s.consumeSSE(ctx, resp.Body)
}

func (s *Subscriber) consumeSSE(ctx context.Context, body io.Reader) error {
	sc := bufio.NewScanner(body)
	// Allow large SSE frames.
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)

	var dataLines []string
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := sc.Text()
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			continue
		}
		if line == "" && len(dataLines) > 0 {
			payload := strings.Join(dataLines, "\n")
			dataLines = nil
			if err := s.HandleWatchPayload(ctx, payload); err != nil {
				if s.Log != nil {
					s.Log.Warn("node watch event apply failed", "error", err.Error())
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return io.EOF
}

// HandleWatchPayload parses one SSE data payload and applies Reachable=False.
func (s *Subscriber) HandleWatchPayload(ctx context.Context, payload string) error {
	var frame watchFrame
	if err := json.Unmarshal([]byte(payload), &frame); err != nil {
		return fmt.Errorf("decode watch frame: %w", err)
	}
	if !strings.EqualFold(frame.Resource.Kind, "Node") && frame.Resource.Kind != "" {
		// Ignore non-Node frames if present.
		if frame.Resource.Kind != "" {
			return nil
		}
	}
	nodeID := frame.Resource.Metadata.Name
	if nodeID == "" {
		nodeID = frame.Resource.Metadata.ID
	}
	if nodeID == "" {
		return nil
	}
	if !reachableIsFalse(frame.Resource.Status) {
		return nil
	}
	return s.ApplyNodeUnreachable(ctx, nodeID)
}

// ApplyNodeUnreachable marks all endpoints on the node Unready in one transaction.
func (s *Subscriber) ApplyNodeUnreachable(ctx context.Context, nodeID string) error {
	tracer := s.Tracer
	if tracer == nil {
		tracer = otel.Tracer("forge-discovery")
	}
	nowFn := s.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	ctx, span := tracer.Start(ctx, "discovery.node.unready_batch",
		trace.WithAttributes(attribute.String("node_id", nodeID)),
	)
	defer span.End()

	rows, err := s.Store.MarkNodeUnready(ctx, nodeID, nowFn().UTC())
	if err != nil {
		return err
	}
	n := int64(len(rows))
	if s.Log != nil {
		s.Log.Info("node unreachable",
			"event", "discovery.node.unreachable",
			"node_id", nodeID,
			"endpoints_affected", n,
		)
		if n > 0 {
			s.Log.Info("endpoint unready",
				"event", "discovery.endpoint.unready",
				"node_id", nodeID,
				"reason", "NodeUnreachable",
				"count", n,
			)
		}
	}
	if s.Watch != nil {
		for _, row := range rows {
			s.Watch.PublishUpdated(row)
		}
	}
	span.SetAttributes(attribute.Int64("endpoints_affected", n))
	return nil
}

type watchFrame struct {
	Type            string        `json:"type"`
	ResourceVersion string        `json:"resourceVersion"`
	Resource        watchResource `json:"resource"`
}

type watchResource struct {
	Kind     string          `json:"kind"`
	Metadata watchMetadata   `json:"metadata"`
	Status   json.RawMessage `json:"status"`
}

type watchMetadata struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type condition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

func reachableIsFalse(statusRaw json.RawMessage) bool {
	if len(statusRaw) == 0 || string(statusRaw) == "null" {
		return false
	}
	var status struct {
		Conditions []condition `json:"conditions"`
		// Fleet fallback: status string online|offline|draining
		Phase string `json:"phase"`
	}
	if err := json.Unmarshal(statusRaw, &status); err != nil {
		// Try plain object with nested status field from fleet projections.
		var wrap map[string]any
		if err2 := json.Unmarshal(statusRaw, &wrap); err2 != nil {
			return false
		}
		if conds, ok := wrap["conditions"].([]any); ok {
			for _, c := range conds {
				m, _ := c.(map[string]any)
				if m == nil {
					continue
				}
				if m["type"] == "Reachable" && m["status"] == "False" {
					return true
				}
			}
		}
		return false
	}
	for _, c := range status.Conditions {
		if c.Type == "Reachable" && c.Status == "False" {
			return true
		}
	}
	return false
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
