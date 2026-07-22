package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"forge.local/tools/forge-cli/internal/auth"
	"forge.local/tools/forge-cli/internal/identity"

	"github.com/spf13/cobra"
)

func newLogoutCommand(state *State) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Revoke the current session/token and clear the local profile",
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
			token := strings.TrimSpace(os.Getenv("FORGE_TOKEN"))
			if token == "" {
				token = creds.Token
			}
			identityURL := auth.DefaultIdentityURL()
			if creds.IdentityURL != "" && strings.TrimSpace(os.Getenv("FORGE_IDENTITY_URL")) == "" {
				identityURL = creds.IdentityURL
			}

			if token != "" {
				client, err := identity.New(identityURL, state.TimeoutDuration())
				if err != nil {
					return err
				}
				ctx, cancel := state.requestContext(cmd)
				defer cancel()
				if err := client.Logout(ctx, token); err != nil {
					var apiErr *identity.APIError
					if !(errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized) {
						// Still clear local state below for API tokens that are not sessions.
						fmt.Fprintf(cmd.ErrOrStderr(), "forge: warning: server revoke failed: %v\n", err)
					}
				}
			}

			if err := store.Delete(profile); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "logged out of profile %q\n", profile)
			return nil
		},
	}
}
