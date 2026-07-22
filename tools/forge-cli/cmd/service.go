package cmd

import (
	"forge.local/tools/forge-cli/internal/config"

	"github.com/spf13/cobra"
)

func newServiceCommand(state *State) *cobra.Command {
	command := &cobra.Command{Use: "service", Short: "Manage application services"}
	command.AddCommand(newServiceCreateCommand(state), newServiceListCommand(state))
	return command
}

func newServiceCreateCommand(state *State) *cobra.Command {
	var applicationID, name string
	var port int
	command := &cobra.Command{
		Use:   "create",
		Short: "Create a service",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if applicationID == "" {
				return &config.UsageError{Message: "--app is required"}
			}
			if name == "" {
				return &config.UsageError{Message: "--name is required"}
			}
			if !cmd.Flags().Changed("port") {
				return &config.UsageError{Message: "--port is required"}
			}
			if port < 1 || port > 65535 {
				return &config.UsageError{Message: "--port must be between 1 and 65535"}
			}
			client, err := state.controlClient(cmd)
			if err != nil {
				return err
			}
			service, err := client.CreateService(commandContext(cmd), applicationID, name, port)
			if err != nil {
				return err
			}
			return state.render(cmd, service)
		},
	}
	command.Flags().StringVar(&applicationID, "app", "", "parent application ID")
	command.Flags().StringVar(&name, "name", "", "service name")
	command.Flags().IntVar(&port, "port", 0, "service port")
	return command
}

func newServiceListCommand(state *State) *cobra.Command {
	var applicationID string
	command := &cobra.Command{
		Use:   "list",
		Short: "List application services",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if applicationID == "" {
				return &config.UsageError{Message: "--app is required"}
			}
			client, err := state.controlClient(cmd)
			if err != nil {
				return err
			}
			services, err := client.ListServices(commandContext(cmd), applicationID)
			if err != nil {
				return err
			}
			return state.render(cmd, services)
		},
	}
	command.Flags().StringVar(&applicationID, "app", "", "parent application ID")
	return command
}
