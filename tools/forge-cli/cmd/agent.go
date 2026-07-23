package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	sharedclient "forge.local/tools/forge-cli/internal/client"
	"forge.local/tools/forge-cli/internal/config"

	"github.com/spf13/cobra"
)

// AwaitingApprovalError is returned when a waited run pauses for human approval.
type AwaitingApprovalError struct {
	RunID      string
	ApprovalID string
	Tool       string
	RequestID  string
}

func (e *AwaitingApprovalError) Error() string {
	msg := fmt.Sprintf("run %s is awaiting_approval", e.RunID)
	if e.ApprovalID != "" {
		msg += fmt.Sprintf(" (approval_id=%s", e.ApprovalID)
		if e.Tool != "" {
			msg += fmt.Sprintf(" tool=%s", e.Tool)
		}
		msg += "); use forge agent approve|deny"
	}
	if e.RequestID != "" {
		msg += fmt.Sprintf(" (requestId: %s)", e.RequestID)
	}
	return msg
}

// RunFailedError is returned when a waited run ends in a non-success status.
type RunFailedError struct {
	RunID  string
	Status string
	Detail string
}

func (e *RunFailedError) Error() string {
	msg := fmt.Sprintf("run %s ended with status %s", e.RunID, e.Status)
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return msg
}

func newAgentCommand(state *State) *cobra.Command {
	command := &cobra.Command{
		Use:   "agent",
		Short: "Call forge-agents (list, run, status, approve, deny)",
		Long: `Thin client for the forge-agents service.

Environment:
  FORGE_AGENTS_URL   Agents base URL (default http://127.0.0.1:4301)
  FORGE_PROJECT      Default project id for run/status/approve/deny`,
	}
	command.AddCommand(
		newAgentListCommand(state),
		newAgentRunCommand(state),
		newAgentStatusCommand(state),
		newAgentApproveCommand(state),
		newAgentDenyCommand(state),
	)
	return command
}

func newAgentListCommand(state *State) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "list",
		Short: "List registered agents",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if asJSON {
				state.Output = "json"
			}
			client, err := state.agentsClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			result, err := client.ListAgents(ctx)
			if err != nil {
				return err
			}
			if state.Output == "json" {
				return state.render(cmd, result)
			}
			for _, agent := range result.Agents {
				tools := strings.Join(agent.Tools, ",")
				fmt.Fprintf(
					cmd.OutOrStdout(),
					"%s\t%s\tsteps=%d\ttimeout=%ds\t%s\n",
					agent.Name,
					agent.Model,
					agent.Limits.MaxSteps,
					agent.Limits.TimeoutSeconds,
					tools,
				)
			}
			return nil
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	return command
}

func newAgentRunCommand(state *State) *cobra.Command {
	var (
		input      string
		project    string
		deployment string
		tool       string
		dryRun     bool
		wait       bool
		asJSON     bool
		pollEvery  time.Duration
	)
	command := &cobra.Command{
		Use:   "run NAME",
		Short: "Start an agent run (optionally wait for completion)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if asJSON {
				state.Output = "json"
			}
			agentName := args[0]
			projectID, err := resolveAgentProject(state, project)
			if err != nil {
				return err
			}
			runContext := map[string]any{}
			if dryRun {
				runContext["dry_run"] = true
			}
			if strings.TrimSpace(deployment) != "" {
				runContext["deployment_id"] = strings.TrimSpace(deployment)
			}
			if strings.TrimSpace(tool) != "" {
				runContext["tool"] = strings.TrimSpace(tool)
			}
			client, err := state.agentsClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			started, err := client.StartRun(ctx, projectID, agentName, input, runContext)
			if err != nil {
				return err
			}
			if !wait {
				if state.Output == "json" {
					return state.render(cmd, started)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "run_id=%s status=%s\n", started.RunID, started.Status)
				return nil
			}

			run, err := waitForAgentRun(cmd, state, client, projectID, started.RunID, pollEvery)
			if err != nil {
				return err
			}
			if state.Output == "json" {
				if err := state.render(cmd, run); err != nil {
					return err
				}
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "run_id=%s status=%s", run.ID, run.Status)
				if run.PendingApproval != nil {
					fmt.Fprintf(cmd.OutOrStdout(), " approval_id=%s tool=%s", run.PendingApproval.ID, run.PendingApproval.Tool)
				}
				if run.Result != "" {
					fmt.Fprintf(cmd.OutOrStdout(), " result=%s", truncateForTable(run.Result, 120))
				}
				if run.Error != "" {
					fmt.Fprintf(cmd.OutOrStdout(), " error=%s", run.Error)
				}
				fmt.Fprintln(cmd.OutOrStdout())
			}
			return exitForRunStatus(run)
		},
	}
	command.Flags().StringVar(&input, "input", "", "run input text")
	command.Flags().StringVar(&project, "project", "", "project id (or FORGE_PROJECT / --project global)")
	command.Flags().StringVar(&deployment, "deployment", "", "deployment id passed in run context")
	command.Flags().StringVar(&tool, "tool", "", "preferred tool name for dry-run / fake planner")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "use deterministic fake model (context.dry_run=true)")
	command.Flags().BoolVar(&wait, "wait", true, "poll until terminal or awaiting_approval")
	command.Flags().DurationVar(&pollEvery, "poll-interval", 200*time.Millisecond, "status poll interval when --wait")
	command.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	return command
}

func newAgentStatusCommand(state *State) *cobra.Command {
	var (
		project string
		asJSON  bool
	)
	command := &cobra.Command{
		Use:   "status RUN_ID",
		Short: "Get agent run status and history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if asJSON {
				state.Output = "json"
			}
			projectID, err := resolveAgentProject(state, project)
			if err != nil {
				return err
			}
			client, err := state.agentsClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			run, err := client.GetRun(ctx, projectID, args[0])
			if err != nil {
				return err
			}
			if state.Output == "json" {
				return state.render(cmd, run)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "run_id=%s agent=%s status=%s", run.ID, run.Agent, run.Status)
			if run.PendingApproval != nil {
				fmt.Fprintf(cmd.OutOrStdout(), " approval_id=%s tool=%s", run.PendingApproval.ID, run.PendingApproval.Tool)
			}
			if run.Result != "" {
				fmt.Fprintf(cmd.OutOrStdout(), " result=%s", truncateForTable(run.Result, 120))
			}
			if run.Error != "" {
				fmt.Fprintf(cmd.OutOrStdout(), " error=%s", run.Error)
			}
			fmt.Fprintln(cmd.OutOrStdout())
			return nil
		},
	}
	command.Flags().StringVar(&project, "project", "", "project id (or FORGE_PROJECT / --project global)")
	command.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	return command
}

func newAgentApproveCommand(state *State) *cobra.Command {
	var (
		project string
		actor   string
		asJSON  bool
	)
	command := &cobra.Command{
		Use:   "approve APPROVAL_ID",
		Short: "Approve a pending destructive tool call",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if asJSON {
				state.Output = "json"
			}
			projectID, err := resolveAgentProject(state, project)
			if err != nil {
				return err
			}
			client, err := state.agentsClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			result, err := client.ApproveApproval(ctx, projectID, args[0], resolveActor(actor))
			if err != nil {
				return err
			}
			if state.Output == "json" {
				return state.render(cmd, result)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "approval %s status=%s\n", args[0], result.Status)
			return nil
		},
	}
	command.Flags().StringVar(&project, "project", "", "project id (or FORGE_PROJECT / --project global)")
	command.Flags().StringVar(&actor, "actor", "", "X-Forge-Actor value (default: anonymous or FORGE_ACTOR)")
	command.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	return command
}

func newAgentDenyCommand(state *State) *cobra.Command {
	var (
		project string
		actor   string
		reason  string
		asJSON  bool
	)
	command := &cobra.Command{
		Use:   "deny APPROVAL_ID",
		Short: "Deny a pending destructive tool call",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if asJSON {
				state.Output = "json"
			}
			projectID, err := resolveAgentProject(state, project)
			if err != nil {
				return err
			}
			client, err := state.agentsClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			result, err := client.DenyApproval(ctx, projectID, args[0], resolveActor(actor), reason)
			if err != nil {
				return err
			}
			if state.Output == "json" {
				return state.render(cmd, result)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "approval %s status=%s\n", args[0], result.Status)
			return nil
		},
	}
	command.Flags().StringVar(&project, "project", "", "project id (or FORGE_PROJECT / --project global)")
	command.Flags().StringVar(&actor, "actor", "", "X-Forge-Actor value (default: anonymous or FORGE_ACTOR)")
	command.Flags().StringVar(&reason, "reason", "", "denial reason")
	command.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	return command
}

func (s *State) agentsClient(cmd *cobra.Command) (*sharedclient.AgentsClient, error) {
	client, err := sharedclient.NewAgentsClient(s.agentsURL(), s.TimeoutDuration(), func(method, path string, status int, requestID string, duration time.Duration) {
		if s.Verbose {
			fmt.Fprintf(cmd.ErrOrStderr(), "forge: %s %s status=%d duration=%s requestId=%s\n", method, path, status, duration.Round(time.Millisecond), requestID)
		}
	})
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (s *State) agentsURL() string {
	return sharedclient.DefaultAgentsURL()
}

func isAgentCommand(cmd *cobra.Command) bool {
	current := cmd
	for current != nil {
		if current.Name() == "agent" {
			return true
		}
		current = current.Parent()
	}
	return false
}

func resolveAgentProject(state *State, flag string) (string, error) {
	if v := strings.TrimSpace(flag); v != "" {
		return v, nil
	}
	if v := strings.TrimSpace(state.Project); v != "" {
		return v, nil
	}
	if v := strings.TrimSpace(os.Getenv("FORGE_PROJECT")); v != "" {
		return v, nil
	}
	return "", &config.UsageError{Message: "--project is required (or set FORGE_PROJECT)"}
}

func resolveActor(flag string) string {
	if v := strings.TrimSpace(flag); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("FORGE_ACTOR")); v != "" {
		return v
	}
	return "anonymous"
}

func waitForAgentRun(
	cmd *cobra.Command,
	state *State,
	client *sharedclient.AgentsClient,
	projectID, runID string,
	pollEvery time.Duration,
) (*sharedclient.RunDetail, error) {
	if pollEvery <= 0 {
		pollEvery = 200 * time.Millisecond
	}
	deadline := time.Now().Add(state.TimeoutDuration())
	for {
		ctx, cancel := state.requestContext(cmd)
		run, err := client.GetRun(ctx, projectID, runID)
		cancel()
		if err != nil {
			return nil, err
		}
		switch run.Status {
		case "running":
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("timed out waiting for run %s", runID)
			}
			time.Sleep(pollEvery)
			continue
		default:
			return run, nil
		}
	}
}

func exitForRunStatus(run *sharedclient.RunDetail) error {
	switch run.Status {
	case "succeeded":
		return nil
	case "awaiting_approval":
		err := &AwaitingApprovalError{RunID: run.ID}
		if run.PendingApproval != nil {
			err.ApprovalID = run.PendingApproval.ID
			err.Tool = run.PendingApproval.Tool
		}
		return err
	default:
		detail := run.Error
		if detail == "" {
			detail = run.Result
		}
		return &RunFailedError{RunID: run.ID, Status: run.Status, Detail: detail}
	}
}

func truncateForTable(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// IsAwaitingApproval reports whether err is an awaiting-approval run outcome.
func IsAwaitingApproval(err error) bool {
	var target *AwaitingApprovalError
	return errors.As(err, &target)
}
