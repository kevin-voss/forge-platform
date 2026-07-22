package cmd

import (
	"fmt"
	"os"
	"strings"

	"forge.local/tools/forge-cli/internal/auth"
	"forge.local/tools/forge-cli/internal/identity"

	"github.com/spf13/cobra"
)

func newWhoamiCommand(state *State) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the authenticated principal, project, and role",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := auth.OpenStore()
			if err != nil {
				return err
			}
			profile := state.Resolved.Profile
			creds, err := store.Get(profile)
			if err != nil {
				return err
			}
			token, err := auth.ResolveToken(store, profile)
			if err != nil {
				return err
			}
			if token == "" {
				return &auth.Error{Message: "not logged in; run forge login"}
			}

			identityURL := auth.DefaultIdentityURL()
			if creds.IdentityURL != "" && strings.TrimSpace(os.Getenv("FORGE_IDENTITY_URL")) == "" {
				identityURL = creds.IdentityURL
			}
			client, err := identity.New(identityURL, state.TimeoutDuration())
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			result, err := client.Introspect(ctx, token)
			if err != nil {
				return err
			}
			if !result.Active {
				return &auth.Error{Message: "session expired, run forge login"}
			}

			out := cmd.OutOrStdout()
			principalID := result.PrincipalID
			if principalID == "" {
				principalID = result.UserID
			}
			fmt.Fprintf(out, "profile\t%s\n", profile)
			fmt.Fprintf(out, "principal_type\t%s\n", result.PrincipalType)
			if principalID != "" {
				fmt.Fprintf(out, "principal_id\t%s\n", principalID)
			}
			if result.UserID != "" && result.UserID != principalID {
				fmt.Fprintf(out, "user_id\t%s\n", result.UserID)
			}
			if result.ProjectID != "" {
				fmt.Fprintf(out, "project_id\t%s\n", result.ProjectID)
			}
			if result.Role != "" {
				fmt.Fprintf(out, "role\t%s\n", result.Role)
			}
			if result.Memberships != nil {
				for _, org := range result.Memberships.Orgs {
					fmt.Fprintf(out, "org\t%s\t%s\t%s\n", org.OrgID, org.OrgName, org.Role)
				}
				for _, project := range result.Memberships.Projects {
					fmt.Fprintf(out, "project\t%s\t%s\t%s\n", project.ProjectID, project.ProjectName, project.Role)
				}
			}
			return nil
		},
	}
}
