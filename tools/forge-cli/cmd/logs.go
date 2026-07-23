package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"forge.local/tools/forge-cli/internal/auth"
	sharedclient "forge.local/tools/forge-cli/internal/client"
	"forge.local/tools/forge-cli/internal/config"

	"github.com/spf13/cobra"
)

func newLogsCommand(state *State) *cobra.Command {
	var (
		deployment string
		service    string
		requestID  string
		traceID    string
		since      string
		until      string
		q          string
		limit      int
		follow     bool
		asJSON     bool
	)

	command := &cobra.Command{
		Use:   "logs",
		Short: "Query or follow correlated platform logs",
		Long: `Query Forge Observe logs by project/deployment/service/request/trace ID.

Without --follow, prints a point-in-time query (GET /v1/logs).
With --follow, live-tails via Observe SSE (GET /v1/logs/stream), reconnecting
on transient drops. When Loki is unavailable and a single --service is set,
auto mode falls back to Runtime workload log streaming (04.05).

Environment:
  FORGE_OBSERVE_URL       Observe base URL (default http://127.0.0.1:4106)
  FORGE_RUNTIME_URL       Runtime base URL for fallback (default http://127.0.0.1:4102)
  FORGE_LOGS_RECONNECT_MS Stream reconnect backoff (default 1000)
  FORGE_LOGS_FALLBACK     observe|runtime|auto (default auto)`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if asJSON {
				state.Output = "json"
			}
			filters, err := buildLogFilters(state, deployment, service, requestID, traceID, since, until, q, limit)
			if err != nil {
				return err
			}
			if follow {
				return state.runLogsFollow(cmd, filters, service)
			}
			return state.runLogsQuery(cmd, filters)
		},
	}

	command.Flags().StringVar(&deployment, "deployment", "", "filter by deployment id")
	command.Flags().StringVar(&service, "service", "", "filter by service name (required for runtime fallback)")
	command.Flags().StringVar(&requestID, "request-id", "", "filter by request id")
	command.Flags().StringVar(&traceID, "trace-id", "", "filter by trace id")
	command.Flags().StringVar(&since, "since", "", "start of range (RFC3339, unix, or duration like 1h)")
	command.Flags().StringVar(&until, "until", "", "end of range for point-in-time query")
	command.Flags().StringVar(&q, "q", "", "free-text substring filter")
	command.Flags().IntVar(&limit, "limit", 100, "max entries for point-in-time query")
	command.Flags().BoolVar(&follow, "follow", false, "live-tail logs via Observe SSE")
	command.Flags().BoolVar(&asJSON, "json", false, "emit JSON (NDJSON lines when --follow)")
	return command
}

func buildLogFilters(state *State, deployment, service, requestID, traceID, since, until, q string, limit int) (sharedclient.LogFilters, error) {
	project := strings.TrimSpace(state.Project)
	if project == "" {
		project = strings.TrimSpace(os.Getenv("FORGE_PROJECT"))
	}
	f := sharedclient.LogFilters{
		Project:    project,
		Deployment: strings.TrimSpace(deployment),
		Service:    strings.TrimSpace(service),
		RequestID:  strings.TrimSpace(requestID),
		TraceID:    strings.TrimSpace(traceID),
		Since:      strings.TrimSpace(since),
		Until:      strings.TrimSpace(until),
		Q:          strings.TrimSpace(q),
		Limit:      limit,
	}
	if f.Project == "" && f.Deployment == "" && f.Service == "" && f.RequestID == "" && f.TraceID == "" {
		return sharedclient.LogFilters{}, &config.UsageError{
			Message: "at least one filter is required: --project, --deployment, --service, --request-id, or --trace-id",
		}
	}
	return f, nil
}

func (s *State) observeClient(cmd *cobra.Command) (*sharedclient.ObserveClient, error) {
	token, err := s.resolveBearerToken()
	if err != nil {
		return nil, err
	}
	client, err := sharedclient.NewObserveClient(sharedclient.DefaultObserveURL(), s.TimeoutDuration(), func(method, path string, status int, requestID string, duration time.Duration) {
		if s.Verbose {
			fmt.Fprintf(cmd.ErrOrStderr(), "forge: %s %s status=%d duration=%s requestId=%s\n", method, path, status, duration.Round(time.Millisecond), requestID)
		}
	})
	if err != nil {
		return nil, err
	}
	if token != "" {
		client.SetBearerToken(token)
	}
	return client, nil
}

func (s *State) runLogsQuery(cmd *cobra.Command, filters sharedclient.LogFilters) error {
	client, err := s.observeClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := s.requestContext(cmd)
	defer cancel()
	result, err := client.QueryLogs(ctx, filters)
	if err != nil {
		return err
	}
	if s.Output == "json" {
		return s.render(cmd, result)
	}
	return writeLogTable(cmd.OutOrStdout(), result.Entries)
}

func (s *State) runLogsFollow(cmd *cobra.Command, filters sharedclient.LogFilters, serviceFlag string) error {
	fallbackMode := strings.TrimSpace(os.Getenv("FORGE_LOGS_FALLBACK"))
	if fallbackMode == "" {
		fallbackMode = "auto"
	}
	reconnect := logsReconnectDuration()

	src, err := sharedclient.SelectLogSource(fallbackMode, serviceFlag, false)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if src == "runtime" {
		return s.followRuntime(ctx, cmd, serviceFlag)
	}

	client, err := s.observeClient(cmd)
	if err != nil {
		return err
	}
	// Streaming should not inherit the short request timeout as a hard deadline.
	// Use signal context only; reconnect uses FORGE_LOGS_RECONNECT_MS.

	err = client.StreamLogsFollow(ctx, filters, reconnect, func(e sharedclient.LogEntry) error {
		return writeLogEntry(cmd.OutOrStdout(), s.Output, e)
	}, func(msg string) {
		fmt.Fprintf(cmd.ErrOrStderr(), "forge: %s\n", msg)
	})
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
		return nil // Ctrl-C → exit 0
	}
	if sharedclient.IsLokiUnavailable(err) {
		runtimeSrc, selErr := sharedclient.SelectLogSource(fallbackMode, serviceFlag, true)
		if selErr != nil {
			return selErr
		}
		if runtimeSrc == "runtime" {
			fmt.Fprintf(cmd.ErrOrStderr(), "forge: observe/Loki unavailable; falling back to runtime logs for service %s\n", serviceFlag)
			return s.followRuntime(ctx, cmd, serviceFlag)
		}
	}
	// Auth errors must surface (non-zero via errmap).
	if tokenMissing(err) {
		return err
	}
	var authErr *auth.Error
	if errors.As(err, &authErr) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (s *State) followRuntime(ctx context.Context, cmd *cobra.Command, service string) error {
	client, err := sharedclient.NewRuntimeLogClient(sharedclient.DefaultRuntimeURL())
	if err != nil {
		return err
	}
	token, _ := s.resolveBearerToken()
	client.SetBearerToken(token)
	workload := strings.TrimSpace(service)
	err = client.FollowWorkloadLogs(ctx, workload, func(line string) error {
		if s.Output == "json" {
			entry := sharedclient.LogEntry{Time: time.Now().UTC().Format(time.RFC3339Nano), Service: workload, Message: line, Level: "info"}
			return writeLogEntry(cmd.OutOrStdout(), "json", entry)
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), line)
		return err
	})
	if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func logsReconnectDuration() time.Duration {
	raw := strings.TrimSpace(os.Getenv("FORGE_LOGS_RECONNECT_MS"))
	if raw == "" {
		return time.Second
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms < 1 {
		return time.Second
	}
	return time.Duration(ms) * time.Millisecond
}

func writeLogTable(w io.Writer, entries []sharedclient.LogEntry) error {
	if len(entries) == 0 {
		fmt.Fprintln(w, "No log entries.")
		return nil
	}
	fmt.Fprintf(w, "%-28s %-6s %-16s %s\n", "TIME", "LEVEL", "SERVICE", "MESSAGE")
	for _, e := range entries {
		fmt.Fprintf(w, "%-28s %-6s %-16s %s\n", truncateRunes(e.Time, 28), truncateRunes(e.Level, 6), truncateRunes(e.Service, 16), e.Message)
	}
	return nil
}

func writeLogEntry(w io.Writer, output string, e sharedclient.LogEntry) error {
	if output == "json" {
		enc := json.NewEncoder(w)
		return enc.Encode(e)
	}
	fmt.Fprintf(w, "%s %s %s %s\n", e.Time, e.Level, e.Service, e.Message)
	return nil
}

func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func tokenMissing(err error) bool {
	var apiErr *sharedclient.ObserveAPIError
	return errors.As(err, &apiErr) && (apiErr.Status == 401 || apiErr.Status == 403)
}
