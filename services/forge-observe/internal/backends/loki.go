package backends

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"forge.local/services/forge-observe/internal/config"
	"forge.local/services/forge-observe/internal/logs"
)

// Loki is a read-only Loki client (health + query_range).
type Loki struct {
	*HTTPClient
}

// NewLoki returns a Loki client (health: GET /ready).
func NewLoki(baseURL string, opts Options) *Loki {
	return &Loki{HTTPClient: newHTTPClient(config.BackendLoki, baseURL, "/ready", opts)}
}

type lokiQueryRangeResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

// QueryRange executes Loki GET /loki/api/v1/query_range and flattens streams.
func (l *Loki) QueryRange(ctx context.Context, logql string, start, end time.Time, limit int, direction string) ([]logs.StreamValue, error) {
	if l == nil || l.HTTPClient == nil {
		return nil, fmt.Errorf("loki client not configured")
	}
	if limit < 1 {
		limit = 100
	}
	dir := strings.ToLower(strings.TrimSpace(direction))
	if dir == "" {
		dir = "backward"
	}

	ctx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()

	q := url.Values{}
	q.Set("query", logql)
	q.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	q.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	q.Set("limit", strconv.Itoa(limit))
	q.Set("direction", dir)

	endpoint := l.baseURL + "/loki/api/v1/query_range?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		l.record(false, err)
		return nil, fmt.Errorf("loki query_range build: %w", err)
	}

	resp, err := l.http.Do(req)
	if err != nil {
		l.record(false, err)
		return nil, fmt.Errorf("loki query_range: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		l.record(false, err)
		return nil, fmt.Errorf("loki query_range read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("loki query_range status %d: %s", resp.StatusCode, truncate(string(raw), 200))
		l.record(false, err)
		return nil, err
	}
	l.record(true, nil)

	var parsed lokiQueryRangeResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("loki query_range decode: %w", err)
	}

	out := make([]logs.StreamValue, 0, limit)
	for _, stream := range parsed.Data.Result {
		for _, pair := range stream.Values {
			if len(pair) < 2 {
				continue
			}
			ns, err := strconv.ParseInt(pair[0], 10, 64)
			if err != nil {
				continue
			}
			out = append(out, logs.StreamValue{
				Timestamp: time.Unix(0, ns).UTC(),
				Line:      pair[1],
				Labels:    stream.Stream,
			})
		}
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
