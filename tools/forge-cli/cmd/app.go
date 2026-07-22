package cmd

import (
	"forge.local/tools/forge-cli/internal/config"

	"github.com/spf13/cobra"
)

func newApplicationCommand(state *State) *cobra.Command {
	command := &cobra.Command{Use: "app", Short: "Manage project applications"}
	command.AddCommand(newApplicationCreateCommand(state), newApplicationListCommand(state))
	return command
}

func newApplicationCreateCommand(state *State) *cobra.Command {
	var projectID, name string
	command := &cobra.Command{
		Use:   "create",
		Short: "Create an application",
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
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			application, err := client.CreateApplication(ctx, projectID, name)
			if err != nil {
				return err
			}
			return state.render(cmd, application)
		},
	}
	command.Flags().StringVar(&projectID, "project", "", "parent project ID")
	command.Flags().StringVar(&name, "name", "", "application name")
	return command
}

func newApplicationListCommand(state *State) *cobra.Command {
	var projectID string
	command := &cobra.Command{
		Use:   "list",
		Short: "List project applications",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if projectID == "" {
				return &config.UsageError{Message: "--project is required"}
			}
			client, err := state.controlClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			applications, err := client.ListApplications(ctx, projectID)
			if err != nil {
				return err
			}
			return state.render(cmd, applications)
		},
	}
	command.Flags().StringVar(&projectID, "project", "", "parent project ID")
	return command
}
