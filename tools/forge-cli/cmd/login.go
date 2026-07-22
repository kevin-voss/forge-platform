package cmd

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"forge.local/tools/forge-cli/internal/auth"
	"forge.local/tools/forge-cli/internal/config"
	"forge.local/tools/forge-cli/internal/identity"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newLoginCommand(state *State) *cobra.Command {
	var email, tokenFlag string
	command := &cobra.Command{
		Use:   "login",
		Short: "Authenticate to Forge Identity and store a token profile",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := auth.OpenStore()
			if err != nil {
				return err
			}
			profile := state.Resolved.Profile
			identityURL := auth.DefaultIdentityURL()
			if existing, err := store.Get(profile); err == nil && existing.IdentityURL != "" {
				if strings.TrimSpace(os.Getenv("FORGE_IDENTITY_URL")) == "" {
					identityURL = existing.IdentityURL
				}
			}

			token := strings.TrimSpace(tokenFlag)
			if token == "" {
				token = strings.TrimSpace(os.Getenv("FORGE_TOKEN"))
			}

			if token != "" {
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
					return &auth.Error{Message: "token is inactive or unknown; provide a valid session or API token"}
				}
			} else {
				if email == "" {
					return &config.UsageError{Message: "--email is required unless --token or FORGE_TOKEN is set"}
				}
				if state.Interaction.NonInteractive() {
					return &config.UsageError{Message: "password prompt unavailable in non-interactive mode; use --token or FORGE_TOKEN"}
				}
				password, err := readPassword(cmd, "Password: ")
				if err != nil {
					return err
				}
				if password == "" {
					return &config.UsageError{Message: "password is required"}
				}
				client, err := identity.New(identityURL, state.TimeoutDuration())
				if err != nil {
					return err
				}
				ctx, cancel := state.requestContext(cmd)
				defer cancel()
				login, err := client.Login(ctx, email, password)
				if err != nil {
					return err
				}
				token = login.SessionToken
			}

			if err := store.Put(profile, auth.Credentials{
				IdentityURL: identityURL,
				Token:       token,
			}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "logged in to profile %q\n", profile)
			return nil
		},
	}
	command.Flags().StringVar(&email, "email", "", "account email for interactive login")
	command.Flags().StringVar(&tokenFlag, "token", "", "session or API token for non-interactive login")
	return command
}

func readPassword(cmd *cobra.Command, prompt string) (string, error) {
	fmt.Fprint(cmd.ErrOrStderr(), prompt)
	fd := int(syscall.Stdin)
	password, err := term.ReadPassword(fd)
	fmt.Fprintln(cmd.ErrOrStderr())
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return string(password), nil
}
