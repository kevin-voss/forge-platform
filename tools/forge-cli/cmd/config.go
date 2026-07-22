package cmd

import (
	"fmt"
	"os"
	"sort"

	forgeconfig "forge.local/tools/forge-cli/internal/config"

	"github.com/spf13/cobra"
)

func newConfigCommand(state *State) *cobra.Command {
	configCommand := &cobra.Command{
		Use:   "config",
		Short: "Manage Forge CLI profiles",
	}
	configCommand.AddCommand(
		newConfigSetCommand(state),
		newConfigGetCommand(state),
		newConfigListCommand(state),
		newConfigUseCommand(state),
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
	return &cobra.Command{
		Use:   "set endpoint <url>",
		Short: "Set a profile endpoint",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != "endpoint" {
				return &forgeconfig.UsageError{Message: fmt.Sprintf("unknown config key %q: only endpoint is supported", args[0])}
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
