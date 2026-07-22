package cmd

import (
	"crypto/rand"
	"fmt"

	"forge.local/tools/forge-cli/internal/config"

	"github.com/spf13/cobra"
)

func newDeploymentCommand(state *State) *cobra.Command {
	command := &cobra.Command{Use: "deployment", Short: "Manage service deployments"}
	command.AddCommand(
		newDeploymentCreateCommand(state),
		newDeploymentStatusCommand(state),
		newDeploymentListCommand(state),
	)
	return command
}

func newDeploymentCreateCommand(state *State) *cobra.Command {
	var serviceID, image, environmentID, idempotencyKey string
	var replicas int
	command := &cobra.Command{
		Use:   "create",
		Short: "Record a desired deployment",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if serviceID == "" {
				return &config.UsageError{Message: "--service is required"}
			}
			if image == "" {
				return &config.UsageError{Message: "--image is required"}
			}
			if environmentID == "" {
				return &config.UsageError{Message: "--env is required"}
			}
			if replicas < 0 {
				return &config.UsageError{Message: "--replicas must be greater than or equal to 0"}
			}
			if idempotencyKey == "" {
				var err error
				idempotencyKey, err = newIdempotencyKey()
				if err != nil {
					return fmt.Errorf("generate idempotency key: %w", err)
				}
			}
			if state.Verbose {
				fmt.Fprintf(cmd.ErrOrStderr(), "forge: idempotencyKey=%s\n", idempotencyKey)
			}
			client, err := state.controlClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			deployment, err := client.CreateDeployment(
				ctx,
				serviceID,
				image,
				replicas,
				environmentID,
				idempotencyKey,
			)
			if err != nil {
				return err
			}
			return state.render(cmd, deployment)
		},
	}
	command.Flags().StringVar(&serviceID, "service", "", "service ID")
	command.Flags().StringVar(&image, "image", "", "container image reference")
	command.Flags().IntVar(&replicas, "replicas", 1, "desired replica count")
	command.Flags().StringVar(&environmentID, "env", "", "target environment ID")
	command.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "key for safely retrying this create request")
	return command
}

func newDeploymentStatusCommand(state *State) *cobra.Command {
	return &cobra.Command{
		Use:   "status <deployment-id>",
		Short: "Show deployment status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := state.controlClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			deployment, err := client.GetDeployment(ctx, args[0])
			if err != nil {
				return err
			}
			return state.render(cmd, deployment)
		},
	}
}

func newDeploymentListCommand(state *State) *cobra.Command {
	var serviceID string
	command := &cobra.Command{
		Use:   "list",
		Short: "List service deployments",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if serviceID == "" {
				return &config.UsageError{Message: "--service is required"}
			}
			client, err := state.controlClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			deployments, err := client.ListDeployments(ctx, serviceID)
			if err != nil {
				return err
			}
			return state.render(cmd, deployments)
		},
	}
	command.Flags().StringVar(&serviceID, "service", "", "service ID")
	return command
}

func newIdempotencyKey() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
