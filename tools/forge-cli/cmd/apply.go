package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"forge.local/tools/forge-cli/internal/config"
	"forge.local/tools/forge-cli/internal/control"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newApplyCommand(state *State) *cobra.Command {
	var file string
	var dryRun bool
	var target string
	command := &cobra.Command{
		Use:   "apply",
		Short: "Apply a portable resource manifest to Control",
		Long: `Apply one or more forge.dev/v1 resources from a YAML file (multi-document supported).

Portability rules: product manifests must not contain provider-specific fields
(provider names, machine types, regions, IPs, disk types, managed-service names).
Use --dry-run to preview creates/updates without mutating state.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(file) == "" {
				return &config.UsageError{Message: "-f / --filename is required"}
			}
			docs, err := loadManifestResources(file)
			if err != nil {
				return err
			}
			if len(docs) == 0 {
				return &config.UsageError{Message: "manifest contains no resources"}
			}
			if target != "" && state.Verbose {
				fmt.Fprintf(cmd.ErrOrStderr(), "forge: apply target=%s (install-time selector; not sent to Control)\n", target)
			}
			client, err := state.controlClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			result, err := client.Apply(ctx, control.ApplyRequest{
				DryRun:    dryRun,
				Resources: docs,
			})
			if err != nil {
				return mapApplyError(err)
			}
			return state.render(cmd, result)
		},
	}
	command.Flags().StringVarP(&file, "filename", "f", "", "path to forge.yaml manifest (multi-doc YAML)")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "report planned changes without mutating state")
	command.Flags().StringVar(&target, "target", "", "deployment target label (local|hetzner|aws|…); informational")
	return command
}

func loadManifestResources(path string) ([]map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	var docs []map[string]any
	for {
		var doc map[string]any
		err := decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse manifest: %w", err)
		}
		if doc == nil || len(doc) == 0 {
			continue
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

func mapApplyError(err error) error {
	apiErr, ok := err.(*control.APIError)
	if !ok {
		return err
	}
	if apiErr.Code == "portable_manifest_violation" {
		return fmt.Errorf("%s: remove provider-specific fields from the manifest", apiErr.Message)
	}
	if apiErr.Status == 409 {
		return fmt.Errorf("%s (stale resourceVersion — re-fetch and retry)", apiErr.Message)
	}
	return err
}
