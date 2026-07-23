package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"forge.local/tools/forge-cli/internal/config"
)

const defaultObserveURL = "http://127.0.0.1:4106"
const defaultRuntimeURL = "http://127.0.0.1:4102"

// LogEntry is a normalized Observe log line (12.04 / 12.05).
type LogEntry struct {
	Time       string `json:"time"`
	Service    string `json:"service"`
	TraceID    string `json:"trace_id,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
	Level      string `json:"level,omitempty"`
	Message    string `json:"message"`
	Deployment string `json:"deployment,omitempty"`
	Project    string `json:"project,omitempty"`
	SpanID     string `json:"span_id,omitempty"`
	Node       string `json:"node,omitempty"`
}

// LogQueryResult is the point-in-time query response.
type LogQueryResult struct {
	Entries    []LogEntry `json:"entries"`
	NextCursor string     `json:"next_cursor,omitempty"`
	Warnings   []string   `json:"warnings,omitempty"`
	Capped     bool       `json:"capped,omitempty"`
}

// LogFilters maps CLI flags onto Observe query/stream parameters.
type LogFilters struct {
	Project    string
	Deployment string
	Service    string
	RequestID  string
	TraceID    string
	Since      string
	Until      string
	Q          string
	Limit      int
	Direction  string
}

// QueryValues returns URL values matching the Observe /v1/logs contract.
func (f LogFilters) QueryValues() url.Values {
	q := url.Values{}
	if f.Project != "" {
		q.Set("project", f.Project)
	}
	if f.Deployment != "" {
		q.Set("deployment", f.Deployment)
	}
	if f.Service != "" {
		q.Set("service", f.Service)
	}
	if f.RequestID != "" {
		q.Set("request_id", f.RequestID)
	}
	if f.TraceID != "" {
		q.Set("trace_id", f.TraceID)
	}
	if f.Since != "" {
		q.Set("since", f.Since)
	}
	if f.Until != "" {
		q.Set("until", f.Until)
	}
	if f.Q != "" {
		q.Set("q", f.Q)
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	if f.Direction != "" {
		q.Set("direction", f.Direction)
	}
	return q
}

// ObserveAPIError is an error returned by Forge Observe.
type ObserveAPIError struct {
	Status    int
	Code      string
	Message   string
	RequestID string
}

func (e *ObserveAPIError) Error() string {
	message := e.Message
	if message == "" {
		message = http.StatusText(e.Status)
	}
	switch e.Status {
	case http.StatusUnauthorized:
		message = "not logged in or session expired; run forge login"
	case http.StatusForbidden:
		if message == "" || message == "forbidden" {
			message = "forbidden: project log access denied"
		}
	case http.StatusServiceUnavailable:
		if e.Code == "loki_unavailable" {
			message = "observe log backend unavailable (Loki down)"
		}
	}
	if e.RequestID != "" {
		return fmt.Sprintf("%s (requestId: %s)", message, e.RequestID)
	}
	return message
}

// IsLokiUnavailable reports whether the error is a Loki-down signal for fallback.
func IsLokiUnavailable(err error) bool {
	var apiErr *ObserveAPIError
	if errors.As(err, &apiErr) {
		return apiErr.Status == http.StatusServiceUnavailable || apiErr.Code == "loki_unavailable"
	}
	return false
}

// ObserveClient talks to forge-observe.
type ObserveClient struct {
	http           *http.Client
	streamHTTP     *http.Client
	baseURL        *url.URL
	token          string
	verbose        func(method, path string, status int, requestID string, duration time.Duration)
	OnReconnect    func() // optional; tests assert resume/reconnect
}

// NewObserveClient creates an Observe API client.
func NewObserveClient(endpoint string, timeout time.Duration, verbose func(method, path string, status int, requestID string, duration time.Duration)) (*ObserveClient, error) {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultObserveURL()
	}
	if err := config.ValidateEndpoint(endpoint); err != nil {
		return nil, err
	}
	baseURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse observe endpoint: %w", err)
	}
	return &ObserveClient{
		http:       &http.Client{Timeout: timeout},
		streamHTTP: &http.Client{}, // no overall timeout; stream is long-lived
		baseURL:    baseURL,
		verbose:    verbose,
	}, nil
}

// DefaultObserveURL returns FORGE_OBSERVE_URL or the local Compose default.
func DefaultObserveURL() string {
	if u := strings.TrimSpace(os.Getenv("FORGE_OBSERVE_URL")); u != "" {
		return u
	}
	return defaultObserveURL
}

// DefaultRuntimeURL returns FORGE_RUNTIME_URL or the local Compose default.
func DefaultRuntimeURL() string {
	if u := strings.TrimSpace(os.Getenv("FORGE_RUNTIME_URL")); u != "" {
		return u
	}
	return defaultRuntimeURL
}

// SetBearerToken attaches the Identity token used for project-scoped log access.
func (c *ObserveClient) SetBearerToken(token string) {
	c.token = strings.TrimSpace(token)
}

// QueryLogs performs GET /v1/logs.
func (c *ObserveClient) QueryLogs(ctx context.Context, filters LogFilters) (LogQueryResult, error) {
	var out LogQueryResult
	path := "/v1/logs?" + filters.QueryValues().Encode()
	if err := c.doJSON(ctx, http.MethodGet, path, &out); err != nil {
		return LogQueryResult{}, err
	}
	return out, nil
}

// StreamHandler is invoked for each SSE log entry.
type StreamHandler func(LogEntry) error

// StreamLogsFollow consumes GET /v1/logs/stream with reconnect + resume-from-timestamp.
func (c *ObserveClient) StreamLogsFollow(ctx context.Context, filters LogFilters, reconnect time.Duration, onEntry StreamHandler, onStatus func(string)) error {
	if reconnect <= 0 {
		reconnect = time.Second
	}
	resume := filters.Since
	first := true
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		f := filters
		f.Since = resume
		last, err := c.streamOnce(ctx, f, onEntry)
		if last != "" {
			resume = last
		}
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if first && IsLokiUnavailable(err) {
			return err
		}
		var apiErr *ObserveAPIError
		if errors.As(err, &apiErr) {
			switch apiErr.Status {
			case http.StatusUnauthorized, http.StatusForbidden, http.StatusBadRequest:
				return err
			}
		}
		first = false
		if c.OnReconnect != nil {
			c.OnReconnect()
		}
		if onStatus != nil {
			onStatus(fmt.Sprintf("stream reconnecting in %s (resume=%s)", reconnect, resume))
		}
		timer := time.NewTimer(reconnect)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *ObserveClient) streamOnce(ctx context.Context, filters LogFilters, onEntry StreamHandler) (lastTime string, err error) {
	u := c.baseURL.ResolveReference(&url.URL{Path: "/v1/logs/stream", RawQuery: filters.QueryValues().Encode()})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/event-stream")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	started := time.Now()
	resp, err := c.streamHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if c.verbose != nil {
		c.verbose(http.MethodGet, u.Path, resp.StatusCode, resp.Header.Get("X-Request-Id"), time.Since(started))
	}

	if resp.StatusCode != http.StatusOK {
		return "", decodeObserveError(resp)
	}

	return readSSELogEntries(resp.Body, onEntry)
}

func readSSELogEntries(r io.Reader, onEntry StreamHandler) (string, error) {
	sc := bufio.NewScanner(r)
	// Allow large log lines.
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)

	var event string
	var data strings.Builder
	last := ""

	flush := func() error {
		defer func() {
			event = ""
			data.Reset()
		}()
		if data.Len() == 0 {
			return nil
		}
		payload := data.String()
		if event == "error" {
			return fmt.Errorf("stream error: %s", strings.Trim(payload, `"`))
		}
		if event != "" && event != "log" && event != "message" {
			return nil
		}
		var entry LogEntry
		if err := json.Unmarshal([]byte(payload), &entry); err != nil {
			// Runtime fallback may send plain text lines — ignore malformed JSON here.
			return nil
		}
		if entry.Time != "" {
			last = entry.Time
		}
		if onEntry != nil {
			return onEntry(entry)
		}
		return nil
	}

	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if err := flush(); err != nil {
				return last, err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			continue
		}
	}
	if err := flush(); err != nil {
		return last, err
	}
	if err := sc.Err(); err != nil {
		return last, err
	}
	return last, io.EOF
}

func (c *ObserveClient) doJSON(ctx context.Context, method, path string, out any) error {
	full := strings.TrimRight(c.baseURL.String(), "/") + path
	req, err := http.NewRequestWithContext(ctx, method, full, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	started := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if c.verbose != nil {
		c.verbose(method, path, resp.StatusCode, resp.Header.Get("X-Request-Id"), time.Since(started))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeObserveError(resp)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func decodeObserveError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	apiErr := &ObserveAPIError{
		Status:    resp.StatusCode,
		RequestID: resp.Header.Get("X-Request-Id"),
		Message:   strings.TrimSpace(string(body)),
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Error.Code != "" {
		apiErr.Code = envelope.Error.Code
		apiErr.Message = envelope.Error.Message
	}
	return apiErr
}

// RuntimeLogClient streams workload logs from forge-runtime (04.05 fallback).
type RuntimeLogClient struct {
	http    *http.Client
	baseURL *url.URL
	token   string
}

// NewRuntimeLogClient creates a Runtime logs client.
func NewRuntimeLogClient(endpoint string) (*RuntimeLogClient, error) {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultRuntimeURL()
	}
	if err := config.ValidateEndpoint(endpoint); err != nil {
		return nil, err
	}
	baseURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse runtime endpoint: %w", err)
	}
	return &RuntimeLogClient{
		http:    &http.Client{},
		baseURL: baseURL,
	}, nil
}

// SetBearerToken optionally attaches a bearer token.
func (c *RuntimeLogClient) SetBearerToken(token string) {
	c.token = strings.TrimSpace(token)
}

// FollowWorkloadLogs streams GET /v1/workloads/{id}/logs?follow=true as SSE/text.
func (c *RuntimeLogClient) FollowWorkloadLogs(ctx context.Context, workloadID string, onLine func(string) error) error {
	workloadID = strings.TrimSpace(workloadID)
	if workloadID == "" {
		return fmt.Errorf("workload id required for runtime log fallback")
	}
	u := c.baseURL.ResolveReference(&url.URL{
		Path:     "/v1/workloads/" + url.PathEscape(workloadID) + "/logs",
		RawQuery: "follow=true",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("runtime logs: status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/event-stream") {
		err := readSSEDataLines(resp.Body, onLine)
		if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}

	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if err := onLine(sc.Text()); err != nil {
			return err
		}
	}
	return sc.Err()
}

// readSSEDataLines yields raw SSE data payloads (Runtime text lines or JSON).
func readSSEDataLines(r io.Reader, onData func(string) error) error {
	sc := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	var data strings.Builder
	flush := func() error {
		defer data.Reset()
		if data.Len() == 0 {
			return nil
		}
		return onData(data.String())
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return io.EOF
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// SelectLogSource chooses observe vs runtime based on FORGE_LOGS_FALLBACK and filters.
// Returns "observe", "runtime", or an error when runtime is requested without a single service.
func SelectLogSource(mode, service string, observeUnavailable bool) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "auto"
	}
	singleService := strings.TrimSpace(service) != ""
	switch mode {
	case "observe":
		return "observe", nil
	case "runtime":
		if !singleService {
			return "", &config.UsageError{Message: "FORGE_LOGS_FALLBACK=runtime requires --service (single-service target)"}
		}
		return "runtime", nil
	case "auto":
		if observeUnavailable {
			if !singleService {
				return "", fmt.Errorf("observe/Loki unavailable and no single --service for runtime fallback")
			}
			return "runtime", nil
		}
		return "observe", nil
	default:
		return "", &config.UsageError{Message: "FORGE_LOGS_FALLBACK must be observe|runtime|auto"}
	}
}
