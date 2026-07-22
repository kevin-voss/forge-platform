// Package cmd defines the Forge CLI command tree.
package cmd

import (
	"fmt"
	"os"
	"time"

	"forge.local/tools/forge-cli/internal/config"

	"github.com/spf13/cobra"
)

// State holds global command-line options and their resolved values.
type State struct {
	Endpoint string
	Profile  string
	Output   string
	Timeout  string
	Verbose  bool

	Resolved config.Resolved
}

// NewRootCommand creates the forge command tree.
func NewRootCommand(version string) *cobra.Command {
	state := &State{Output: "table", Timeout: "30s"}
	root := &cobra.Command{
		Use:           "forge",
		Short:         "Forge platform command-line client",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := state.validateGlobals(cmd); err != nil {
				return err
			}
			if cmd.Parent() != nil && cmd.Parent().Name() == "config" {
				return nil
			}
			return state.resolve()
		},
	}
	root.PersistentFlags().StringVar(&state.Endpoint, "endpoint", "", "Control endpoint URL")
	root.PersistentFlags().StringVar(&state.Profile, "profile", "", "named configuration profile")
	root.PersistentFlags().StringVar(&state.Output, "output", "table", "output format: table or json")
	root.PersistentFlags().StringVar(&state.Timeout, "timeout", "30s", "HTTP request timeout")
	root.PersistentFlags().BoolVar(&state.Verbose, "verbose", false, "print diagnostics to stderr")

	root.AddCommand(newVersionCommand(version), newConfigCommand(state))
	return root
}

func (s *State) validateGlobals(cmd *cobra.Command) error {
	if !cmd.Flags().Changed("output") && os.Getenv("FORGE_OUTPUT") != "" {
		s.Output = os.Getenv("FORGE_OUTPUT")
	}
	if s.Output != "table" && s.Output != "json" {
		return &config.UsageError{Message: fmt.Sprintf("invalid output %q: expected table or json", s.Output)}
	}
	if !cmd.Flags().Changed("timeout") && os.Getenv("FORGE_TIMEOUT") != "" {
		s.Timeout = os.Getenv("FORGE_TIMEOUT")
	}
	if _, err := time.ParseDuration(s.Timeout); err != nil {
		return &config.UsageError{Message: fmt.Sprintf("invalid timeout %q: %v", s.Timeout, err)}
	}
	return nil
}

// Resolve loads configuration and applies the documented precedence rules.
func (s *State) Resolve() (config.Resolved, error) {
	path, err := config.Path()
	if err != nil {
		return config.Resolved{}, err
	}
	file, err := config.Load(path)
	if err != nil {
		return config.Resolved{}, err
	}
	return config.Resolve(file, s.Endpoint, s.Profile, os.Getenv("FORGE_ENDPOINT"), os.Getenv("FORGE_PROFILE"))
}

func (s *State) resolve() error {
	resolved, err := s.Resolve()
	if err != nil {
		return err
	}
	s.Resolved = resolved
	if s.Verbose {
		fmt.Fprintf(os.Stderr, "forge: endpoint=%s profile=%s timeout=%s\n", resolved.Endpoint, resolved.Profile, s.Timeout)
	}
	return nil
}

// TimeoutDuration parses the validated global timeout.
func (s *State) TimeoutDuration() time.Duration {
	timeout, _ := time.ParseDuration(s.Timeout)
	return timeout
}
