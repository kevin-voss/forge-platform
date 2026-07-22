package cmd

import (
	"forge.local/tools/forge-cli/internal/config"

	"github.com/spf13/cobra"
)

func newProjectCommand(state *State) *cobra.Command {
	command := &cobra.Command{
		Use:   "project",
		Short: "Manage projects",
	}
	command.AddCommand(newProjectCreateCommand(state), newProjectListCommand(state), newProjectGetCommand(state))
	return command
}

func newProjectCreateCommand(state *State) *cobra.Command {
	var name, slug string
	command := &cobra.Command{
		Use:   "create",
		Short: "Create a project",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" {
				return &config.UsageError{Message: "--name is required"}
			}
			client, err := state.controlClient(cmd)
			if err != nil {
				return err
			}
			project, err := client.CreateProject(commandContext(cmd), name, slug)
			if err != nil {
				return err
			}
			return state.render(cmd, project)
		},
	}
	command.Flags().StringVar(&name, "name", "", "project name")
	command.Flags().StringVar(&slug, "slug", "", "project slug")
	return command
}

func newProjectListCommand(state *State) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List projects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := state.controlClient(cmd)
			if err != nil {
				return err
			}
			projects, err := client.ListProjects(commandContext(cmd))
			if err != nil {
				return err
			}
			return state.render(cmd, projects)
		},
	}
}

func newProjectGetCommand(state *State) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Get a project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := state.controlClient(cmd)
			if err != nil {
				return err
			}
			project, err := client.GetProject(commandContext(cmd), args[0])
			if err != nil {
				return err
			}
			return state.render(cmd, project)
		},
	}
}
