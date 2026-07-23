package cmd

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"forge.local/tools/forge-cli/internal/auth"
	sharedclient "forge.local/tools/forge-cli/internal/client"
	"forge.local/tools/forge-cli/internal/config"

	"github.com/spf13/cobra"
)

var secretNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func newSecretCommand(state *State) *cobra.Command {
	command := &cobra.Command{
		Use:   "secret",
		Short: "Manage project/environment secrets",
	}
	command.AddCommand(
		newSecretSetCommand(state),
		newSecretListCommand(state),
		newSecretRotateCommand(state),
	)
	return command
}

func newSecretSetCommand(state *State) *cobra.Command {
	var fromStdin bool
	var fromFile string
	var asJSON bool
	command := &cobra.Command{
		Use:   "set NAME",
		Short: "Set a secret value (creates a new version)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if asJSON {
				state.Output = "json"
			}
			name := args[0]
			if err := validateSecretName(name); err != nil {
				return err
			}
			value, err := readSecretValue(cmd, state, fromStdin, fromFile, "Secret value: ")
			if err != nil {
				return err
			}
			if strings.TrimSpace(value) == "" {
				return &config.UsageError{Message: "secret value must not be empty"}
			}
			client, projectID, environment, err := state.secretsClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			result, err := client.SetSecret(ctx, projectID, environment, name, value)
			if err != nil {
				return err
			}
			if state.Output == "json" {
				return state.render(cmd, result)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "set secret %s version %d\n", result.Name, result.Version)
			return nil
		},
	}
	command.Flags().BoolVar(&fromStdin, "from-stdin", false, "read secret value from stdin")
	command.Flags().StringVar(&fromFile, "from-file", "", "read secret value from a file")
	command.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	command.MarkFlagsMutuallyExclusive("from-stdin", "from-file")
	return command
}

func newSecretRotateCommand(state *State) *cobra.Command {
	var fromStdin bool
	var fromFile string
	var asJSON bool
	command := &cobra.Command{
		Use:   "rotate NAME",
		Short: "Rotate a secret by writing a new version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if asJSON {
				state.Output = "json"
			}
			name := args[0]
			if err := validateSecretName(name); err != nil {
				return err
			}
			value, err := readSecretValue(cmd, state, fromStdin, fromFile, "New secret value: ")
			if err != nil {
				return err
			}
			if strings.TrimSpace(value) == "" {
				return &config.UsageError{Message: "secret value must not be empty"}
			}
			client, projectID, environment, err := state.secretsClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			result, err := client.SetSecret(ctx, projectID, environment, name, value)
			if err != nil {
				return err
			}
			if state.Output == "json" {
				return state.render(cmd, result)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "rotated secret %s to version %d\n", result.Name, result.Version)
			return nil
		},
	}
	command.Flags().BoolVar(&fromStdin, "from-stdin", false, "read new secret value from stdin")
	command.Flags().StringVar(&fromFile, "from-file", "", "read new secret value from a file")
	command.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	command.MarkFlagsMutuallyExclusive("from-stdin", "from-file")
	return command
}

func newSecretListCommand(state *State) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "list",
		Short: "List secret names and versions (never values)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if asJSON {
				state.Output = "json"
			}
			client, projectID, environment, err := state.secretsClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			items, err := client.ListSecrets(ctx, projectID, environment)
			if err != nil {
				return err
			}
			return state.render(cmd, items)
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	return command
}

func validateSecretName(name string) error {
	if !secretNamePattern.MatchString(name) {
		return &config.UsageError{Message: "secret name must match [A-Za-z_][A-Za-z0-9_]*"}
	}
	return nil
}

func readSecretValue(cmd *cobra.Command, state *State, fromStdin bool, fromFile, prompt string) (string, error) {
	if fromFile != "" {
		return readSecretFromFile(cmd, fromFile)
	}
	if fromStdin {
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return strings.TrimRight(string(data), "\r\n"), nil
	}
	if state.Interaction.NonInteractive() {
		return "", &config.UsageError{Message: "secret prompt unavailable in non-interactive mode; use --from-stdin or --from-file"}
	}
	return readPassword(cmd, prompt)
}

func readSecretFromFile(cmd *cobra.Command, path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", &config.UsageError{Message: fmt.Sprintf("secret file %q not found", path)}
		}
		return "", fmt.Errorf("stat secret file: %w", err)
	}
	if mode := info.Mode().Perm(); mode&0o004 != 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "forge: warning: secret file %q is world-readable (%04o)\n", path, mode)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read secret file: %w", err)
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}

func (s *State) secretsClient(cmd *cobra.Command) (*sharedclient.SecretsClient, string, string, error) {
	projectID, environment, err := s.resolveProjectEnv()
	if err != nil {
		return nil, "", "", err
	}
	token, err := s.resolveBearerToken()
	if err != nil {
		return nil, "", "", err
	}
	if token == "" {
		return nil, "", "", &auth.Error{Message: "not logged in or session expired; run forge login"}
	}
	client, err := sharedclient.NewSecretsClient(s.secretsURL(), s.TimeoutDuration(), func(method, path string, status int, requestID string, duration time.Duration) {
		if s.Verbose {
			// Never log request bodies or secret values — path/status/requestId only.
			fmt.Fprintf(cmd.ErrOrStderr(), "forge: %s %s status=%d duration=%s requestId=%s\n", method, path, status, duration.Round(time.Millisecond), requestID)
		}
	})
	if err != nil {
		return nil, "", "", err
	}
	client.SetBearerToken(token)
	return client, projectID, environment, nil
}

func (s *State) secretsURL() string {
	return sharedclient.DefaultSecretsURL()
}

func (s *State) resolveProjectEnv() (string, string, error) {
	projectID := strings.TrimSpace(s.Project)
	if projectID == "" {
		projectID = strings.TrimSpace(os.Getenv("FORGE_PROJECT"))
	}
	if projectID == "" {
		return "", "", &config.UsageError{Message: "--project or FORGE_PROJECT is required"}
	}
	environment := strings.TrimSpace(s.Env)
	if environment == "" {
		environment = strings.TrimSpace(os.Getenv("FORGE_ENV"))
	}
	if environment == "" {
		environment = "production"
	}
	return projectID, environment, nil
}
