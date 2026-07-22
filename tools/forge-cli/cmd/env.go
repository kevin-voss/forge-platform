package cmd

import (
	"forge.local/tools/forge-cli/internal/config"

	"github.com/spf13/cobra"
)

func newEnvironmentCommand(state *State) *cobra.Command {
	command := &cobra.Command{Use: "env", Short: "Manage project environments"}
	command.AddCommand(newEnvironmentCreateCommand(state), newEnvironmentListCommand(state))
	return command
}

func newEnvironmentCreateCommand(state *State) *cobra.Command {
	var projectID, name string
	command := &cobra.Command{
		Use:   "create",
		Short: "Create an environment",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if projectID == "" {
				return &config.UsageError{Message: "--project is required"}
			}
			if name == "" {
				return &config.UsageError{Message: "--name is required"}
			}
			client, err := state.controlClient(cmd)
			if err != nil {
				return err
			}
			environment, err := client.CreateEnvironment(commandContext(cmd), projectID, name)
			if err != nil {
				return err
			}
			return state.render(cmd, environment)
		},
	}
	command.Flags().StringVar(&projectID, "project", "", "parent project ID")
	command.Flags().StringVar(&name, "name", "", "environment name")
	return command
}

func newEnvironmentListCommand(state *State) *cobra.Command {
	var projectID string
	command := &cobra.Command{
		Use:   "list",
		Short: "List project environments",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if projectID == "" {
				return &config.UsageError{Message: "--project is required"}
			}
			client, err := state.controlClient(cmd)
			if err != nil {
				return err
			}
			environments, err := client.ListEnvironments(commandContext(cmd), projectID)
			if err != nil {
				return err
			}
			return state.render(cmd, environments)
		},
	}
	command.Flags().StringVar(&projectID, "project", "", "parent project ID")
	return command
}
