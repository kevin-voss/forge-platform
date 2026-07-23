package cmd

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	forgeconfig "forge.local/tools/forge-cli/internal/config"

	"github.com/spf13/cobra"
)

var configNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func newConfigCommand(state *State) *cobra.Command {
	configCommand := &cobra.Command{
		Use:   "config",
		Short: "Manage CLI profiles and project configuration",
	}
	configCommand.AddCommand(
		newConfigSetCommand(state),
		newConfigGetCommand(state),
		newConfigListCommand(state),
		newConfigUseCommand(state),
		newConfigShowCommand(state),
	)
	return configCommand
}

func loadConfig() (string, forgeconfig.File, error) {
	path, err := forgeconfig.Path()
	if err != nil {
		return "", forgeconfig.File{}, err
	}
	file, err := forgeconfig.Load(path)
	return path, file, err
}

func selectedProfile(state *State, file forgeconfig.File) string {
	if state.Profile != "" {
		return state.Profile
	}
	if fromEnv := os.Getenv("FORGE_PROFILE"); fromEnv != "" {
		return fromEnv
	}
	if file.CurrentProfile != "" {
		return file.CurrentProfile
	}
	return "local"
}

func newConfigSetCommand(state *State) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "set <endpoint <url>|NAME=VALUE>",
		Short: "Set a CLI profile endpoint or a project config value",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && strings.Contains(args[0], "=") {
				if asJSON {
					state.Output = "json"
				}
				return setPlatformConfig(state, cmd, args[0])
			}
			if len(args) != 2 {
				return &forgeconfig.UsageError{Message: "usage: forge config set endpoint <url> or forge config set NAME=VALUE"}
			}
			if args[0] != "endpoint" {
				return &forgeconfig.UsageError{Message: fmt.Sprintf("unknown config key %q: only endpoint is supported for CLI profiles (use NAME=VALUE for project config)", args[0])}
			}
			if err := forgeconfig.ValidateEndpoint(args[1]); err != nil {
				return err
			}
			path, file, err := loadConfig()
			if err != nil {
				return err
			}
			profile := selectedProfile(state, file)
			file.Profiles[profile] = forgeconfig.Profile{Endpoint: args[1]}
			if file.CurrentProfile == "" {
				file.CurrentProfile = profile
			}
			if err := forgeconfig.Save(path, file); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "set endpoint for profile %q\n", profile)
			return nil
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "emit JSON output (project config only)")
	return command
}

func setPlatformConfig(state *State, cmd *cobra.Command, assignment string) error {
	name, value, ok := strings.Cut(assignment, "=")
	if !ok || name == "" {
		return &forgeconfig.UsageError{Message: "config assignment must be NAME=VALUE"}
	}
	if !configNamePattern.MatchString(name) {
		return &forgeconfig.UsageError{Message: "config name must match [A-Za-z_][A-Za-z0-9_]*"}
	}
	client, projectID, environment, err := state.secretsClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := state.requestContext(cmd)
	defer cancel()
	result, err := client.SetConfig(ctx, projectID, environment, name, value)
	if err != nil {
		return err
	}
	if state.Output == "json" {
		return state.render(cmd, result)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "set config %s=%s\n", result.Name, result.Value)
	return nil
}

func newConfigShowCommand(state *State) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "show",
		Short: "Show project/environment configuration values",
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
			items, err := client.ListConfig(ctx, projectID, environment)
			if err != nil {
				return err
			}
			return state.render(cmd, items)
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	return command
}

func newConfigGetCommand(state *State) *cobra.Command {
	return &cobra.Command{
		Use:   "get endpoint",
		Short: "Get a profile endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != "endpoint" {
				return &forgeconfig.UsageError{Message: fmt.Sprintf("unknown config key %q: only endpoint is supported", args[0])}
			}
			_, file, err := loadConfig()
			if err != nil {
				return err
			}
			profile := selectedProfile(state, file)
			value, exists := file.Profiles[profile]
			if !exists {
				return &forgeconfig.UsageError{Message: fmt.Sprintf("unknown profile %q", profile)}
			}
			fmt.Fprintln(cmd.OutOrStdout(), value.Endpoint)
			return nil
		},
	}
}

func newConfigListCommand(state *State) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List named profiles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, file, err := loadConfig()
			if err != nil {
				return err
			}
			names := make([]string, 0, len(file.Profiles))
			for name := range file.Profiles {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				marker := " "
				if name == file.CurrentProfile {
					marker = "*"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s\t%s\n", marker, name, file.Profiles[name].Endpoint)
			}
			return nil
		},
	}
}

func newConfigUseCommand(state *State) *cobra.Command {
	return &cobra.Command{
		Use:   "use <profile>",
		Short: "Select the current profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, file, err := loadConfig()
			if err != nil {
				return err
			}
			if _, exists := file.Profiles[args[0]]; !exists {
				return &forgeconfig.UsageError{Message: fmt.Sprintf("unknown profile %q", args[0])}
			}
			file.CurrentProfile = args[0]
			if err := forgeconfig.Save(path, file); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "using profile %q\n", args[0])
			return nil
		},
	}
}
