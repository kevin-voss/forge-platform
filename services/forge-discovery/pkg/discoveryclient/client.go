// Package discoveryclient is the Go client for Forge Discovery ready-endpoint selection.
//
// Products that talk to peers directly (not through Gateway) can Resolve a service
// to the current Ready address set, and optionally Watch for changes. The scoped
// Discovery watch is a low-latency alternative to Control's generic
// GET /v1/watch/endpoints (which works even when Discovery's own watch is down,
// via the Control mirror from step 21.02).
package discoveryclient

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Address is a ready endpoint address.
type Address struct {
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

// Endpoint is one Ready (by default) discovery endpoint.
type Endpoint struct {
	ID              string  `json:"id"`
	Service         string  `json:"service"`
	Node            string  `json:"node"`
	Phase           string  `json:"phase"`
	Ready           bool    `json:"ready"`
	Revision        string  `json:"revision,omitempty"`
	ResourceVersion string  `json:"resourceVersion,omitempty"`
	Address         Address `json:"address"`
}

// EventType matches Discovery SSE event names.
type EventType string

const (
	EventAdded   EventType = "added"
	EventUpdated EventType = "updated"
	EventRemoved EventType = "removed"
)

// Event is a watch change notification.
type Event struct {
	Type            EventType
	ResourceVersion string
	Endpoint        Endpoint
}

// Config configures the Discovery HTTP client.
type Config struct {
	BaseURL     string
	Project     string
	Environment string
	HTTPClient  *http.Client
}

// Client talks to forge-discovery list + watch APIs with a small local cache.
type Client struct {
	base    string
	project string
	env     string
	http    *http.Client

	mu    sync.RWMutex
	cache map[string][]Endpoint // service → ready set
}

// New constructs a Client.
func New(cfg Config) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("BaseURL is required")
	}
	if strings.TrimSpace(cfg.Project) == "" || strings.TrimSpace(cfg.Environment) == "" {
		return nil, fmt.Errorf("Project and Environment are required")
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{
		base:    base,
		project: cfg.Project,
		env:     cfg.Environment,
		http:    hc,
		cache:   map[string][]Endpoint{},
	}, nil
}

// Resolve returns Ready endpoints for a service (defaults match GET .../endpoints).
func (c *Client) Resolve(ctx context.Context, service string) ([]Address, error) {
	eps, err := c.List(ctx, service, ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]Address, 0, len(eps))
	for _, ep := range eps {
		out = append(out, ep.Address)
	}
	return out, nil
}

// ListOptions controls list query parameters.
type ListOptions struct {
	ReadyOnly *bool // nil → server default (Ready-only)
	Revision  string
}

// List fetches endpoints and updates the local cache for the service.
func (c *Client) List(ctx context.Context, service string, opts ListOptions) ([]Endpoint, error) {
	u, err := url.Parse(fmt.Sprintf("%s/v1/projects/%s/environments/%s/services/%s/endpoints",
		c.base, url.PathEscape(c.project), url.PathEscape(c.env), url.PathEscape(service)))
	if err != nil {
		return nil, err
	}
	q := u.Query()
	if opts.ReadyOnly != nil && !*opts.ReadyOnly {
		q.Set("ready", "false")
	}
	if opts.Revision != "" {
		q.Set("revision", opts.Revision)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list endpoints: status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var eps []Endpoint
	if err := json.Unmarshal(body, &eps); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.cache[service] = append([]Endpoint{}, eps...)
	c.mu.Unlock()
	return eps, nil
}

// Cached returns the last Resolve/List/Watch snapshot for a service (may be nil).
func (c *Client) Cached(service string) []Endpoint {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Endpoint{}, c.cache[service]...)
}

// Watch streams SSE events for a service until ctx is cancelled.
// It performs an initial List to populate the cache, then follows the watch
// from the highest known resourceVersion (or 0).
func (c *Client) Watch(ctx context.Context, service string, fn func(Event)) error {
	eps, err := c.List(ctx, service, ListOptions{})
	if err != nil {
		return err
	}
	var since int64
	for _, ep := range eps {
		if rv := parseRV(ep.ResourceVersion); rv > since {
			since = rv
		}
	}
	// Discovery watch cursors are broker stream versions, not endpoint row versions.
	// Start from 0 so the server can resync/replay; cache is already seeded by List.
	since = 0

	hc := c.http
	// Streaming clients must not use a short overall timeout.
	streamClient := &http.Client{Timeout: 0, Transport: hc.Transport}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := c.watchOnce(ctx, streamClient, service, since, func(ev Event) {
			c.applyEvent(service, ev)
			if fn != nil {
				fn(ev)
			}
			if rv := parseRV(ev.ResourceVersion); rv > since {
				since = rv
			}
		})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && ctx.Err() == nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
}

func (c *Client) watchOnce(ctx context.Context, hc *http.Client, service string, since int64, fn func(Event)) error {
	u := fmt.Sprintf("%s/v1/projects/%s/environments/%s/services/%s/endpoints/watch?since=%d",
		c.base, url.PathEscape(c.project), url.PathEscape(c.env), url.PathEscape(service), since)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("watch status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	sc := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	var eventName string
	var dataLines []string
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := sc.Text()
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			continue
		}
		if line == "" && len(dataLines) > 0 {
			payload := strings.Join(dataLines, "\n")
			dataLines = nil
			var ep Endpoint
			if err := json.Unmarshal([]byte(payload), &ep); err != nil {
				eventName = ""
				continue
			}
			fn(Event{
				Type:            EventType(eventName),
				ResourceVersion: ep.ResourceVersion,
				Endpoint:        ep,
			})
			eventName = ""
		}
	}
	return sc.Err()
}

func (c *Client) applyEvent(service string, ev Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur := c.cache[service]
	switch ev.Type {
	case EventRemoved:
		next := cur[:0]
		for _, ep := range cur {
			if ep.ID != ev.Endpoint.ID {
				next = append(next, ep)
			}
		}
		c.cache[service] = append([]Endpoint{}, next...)
	case EventAdded, EventUpdated:
		if ev.Endpoint.Phase != "" && ev.Endpoint.Phase != "Ready" {
			// Drop non-ready from Resolve cache.
			next := make([]Endpoint, 0, len(cur))
			for _, ep := range cur {
				if ep.ID != ev.Endpoint.ID {
					next = append(next, ep)
				}
			}
			c.cache[service] = next
			return
		}
		replaced := false
		for i, ep := range cur {
			if ep.ID == ev.Endpoint.ID {
				cur[i] = ev.Endpoint
				replaced = true
				break
			}
		}
		if !replaced {
			cur = append(cur, ev.Endpoint)
		}
		c.cache[service] = cur
	}
}

func parseRV(s string) int64 {
	var n int64
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int64(ch-'0')
	}
	return n
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
