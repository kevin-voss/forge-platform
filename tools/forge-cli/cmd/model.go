package cmd

import (
	"fmt"
	"strings"
	"time"

	sharedclient "forge.local/tools/forge-cli/internal/client"
	"forge.local/tools/forge-cli/internal/config"

	"github.com/spf13/cobra"
)

func newModelCommand(state *State) *cobra.Command {
	command := &cobra.Command{
		Use:   "model",
		Short: "Call forge-models (list, embed, generate)",
		Long: `Thin client for the forge-models service.

Environment:
  FORGE_MODELS_URL   Models base URL (default http://127.0.0.1:4300)`,
	}
	command.AddCommand(
		newModelListCommand(state),
		newModelEmbedCommand(state),
		newModelGenerateCommand(state),
	)
	return command
}

func newModelListCommand(state *State) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "list",
		Short: "List registered models",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if asJSON {
				state.Output = "json"
			}
			client, err := state.modelsClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			result, err := client.ListModels(ctx)
			if err != nil {
				return err
			}
			if state.Output == "json" {
				return state.render(cmd, result)
			}
			for _, model := range result.Models {
				caps := strings.Join(model.Capabilities, ",")
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", model.ID, model.Backend, model.Status, caps)
			}
			return nil
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	return command
}

func newModelEmbedCommand(state *State) *cobra.Command {
	var (
		model  string
		text   string
		asJSON bool
	)
	command := &cobra.Command{
		Use:   "embed",
		Short: "Embed text with a model",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if asJSON {
				state.Output = "json"
			}
			if strings.TrimSpace(model) == "" {
				return &config.UsageError{Message: "--model is required"}
			}
			if strings.TrimSpace(text) == "" {
				return &config.UsageError{Message: "--text is required"}
			}
			client, err := state.modelsClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			result, err := client.Embed(ctx, model, text)
			if err != nil {
				return err
			}
			if state.Output == "json" {
				return state.render(cmd, result)
			}
			fmt.Fprintf(
				cmd.OutOrStdout(),
				"model=%s dim=%d input_count=%d vectors=%d\n",
				result.Model,
				result.Dim,
				result.Usage.InputCount,
				len(result.Embeddings),
			)
			return nil
		},
	}
	command.Flags().StringVar(&model, "model", "", "model id")
	command.Flags().StringVar(&text, "text", "", "text to embed")
	command.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	return command
}

func newModelGenerateCommand(state *State) *cobra.Command {
	var (
		model     string
		prompt    string
		maxTokens int
		asJSON    bool
	)
	command := &cobra.Command{
		Use:   "generate",
		Short: "Generate text with a model",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if asJSON {
				state.Output = "json"
			}
			if strings.TrimSpace(model) == "" {
				return &config.UsageError{Message: "--model is required"}
			}
			if strings.TrimSpace(prompt) == "" {
				return &config.UsageError{Message: "--prompt is required"}
			}
			client, err := state.modelsClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			result, err := client.Generate(ctx, model, prompt, maxTokens)
			if err != nil {
				return err
			}
			if state.Output == "json" {
				return state.render(cmd, result)
			}
			fmt.Fprintln(cmd.OutOrStdout(), result.Text)
			return nil
		},
	}
	command.Flags().StringVar(&model, "model", "", "model id")
	command.Flags().StringVar(&prompt, "prompt", "", "generation prompt")
	command.Flags().IntVar(&maxTokens, "max-tokens", 0, "max completion tokens")
	command.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	return command
}

func (s *State) modelsClient(cmd *cobra.Command) (*sharedclient.ModelsClient, error) {
	client, err := sharedclient.NewModelsClient(s.modelsURL(), s.TimeoutDuration(), func(method, path string, status int, requestID string, duration time.Duration) {
		if s.Verbose {
			fmt.Fprintf(cmd.ErrOrStderr(), "forge: %s %s status=%d duration=%s requestId=%s\n", method, path, status, duration.Round(time.Millisecond), requestID)
		}
	})
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (s *State) modelsURL() string {
	return sharedclient.DefaultModelsURL()
}

func isModelCommand(cmd *cobra.Command) bool {
	current := cmd
	for current != nil {
		if current.Name() == "model" {
			return true
		}
		current = current.Parent()
	}
	return false
}
