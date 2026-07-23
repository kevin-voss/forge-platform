package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"forge.local/tools/forge-cli/internal/config"
	"forge.local/tools/forge-cli/internal/control"

	"github.com/spf13/cobra"
)

var (
	dbNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
	uuidPattern   = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

func newDatabaseCommand(state *State) *cobra.Command {
	command := &cobra.Command{
		Use:   "database",
		Short: "Manage Forge managed PostgreSQL databases",
		Long: `Thin client for Control managed-database APIs.

Creates an isolated Postgres instance + database, attaches connection URLs
via Secrets for Runtime injection, and supports backup/restore/rotate/delete.

Requires --project (or FORGE_PROJECT).`,
	}
	command.AddCommand(
		newDatabaseCreateCommand(state),
		newDatabaseAttachCommand(state),
		newDatabaseDetachCommand(state),
		newDatabaseListCommand(state),
		newDatabaseBackupCommand(state),
		newDatabaseRestoreCommand(state),
		newDatabaseRotateCommand(state),
		newDatabaseDeleteCommand(state),
	)
	return command
}

func newDatabaseCreateCommand(state *State) *cobra.Command {
	var databaseName string
	command := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a managed Postgres instance and database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == "" {
				return &config.UsageError{Message: "name is required"}
			}
			dbName := strings.TrimSpace(databaseName)
			if dbName == "" {
				dbName = name
			}
			if !dbNamePattern.MatchString(dbName) {
				return &config.UsageError{Message: "database name must match [a-z_][a-z0-9_]{0,62}"}
			}
			projectID, err := state.requireProjectID()
			if err != nil {
				return err
			}
			client, err := state.controlClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()

			instance, err := client.CreateDbInstance(ctx, projectID, name)
			if err != nil {
				return err
			}
			if instance.Status != "available" {
				return fmt.Errorf(
					"instance %s status=%s (want available)%s",
					instance.ID,
					instance.Status,
					statusReasonSuffix(instance.StatusReason),
				)
			}
			database, err := client.CreateDbDatabase(ctx, instance.ID, dbName)
			if err != nil {
				return err
			}
			// Never echo the one-time password (table or JSON).
			database.Password = ""
			if state.Output == "json" {
				return state.render(cmd, map[string]any{
					"instance": instance,
					"database": database,
				})
			}
			fmt.Fprintf(
				cmd.OutOrStdout(),
				"created database %s (id=%s) on instance %s (id=%s) status=%s\n",
				database.Name,
				database.ID,
				instance.Name,
				instance.ID,
				database.Status,
			)
			if database.SecretRef != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "secretRef=%s\n", database.SecretRef)
			}
			return nil
		},
	}
	command.Flags().StringVar(&databaseName, "database", "", "logical database name (default: same as instance name)")
	return command
}

func newDatabaseAttachCommand(state *State) *cobra.Command {
	var appRef, envVar string
	command := &cobra.Command{
		Use:   "attach <name-or-id>",
		Short: "Attach a managed database to an application",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(appRef) == "" {
				return &config.UsageError{Message: "--app is required"}
			}
			projectID, err := state.requireProjectID()
			if err != nil {
				return err
			}
			client, err := state.controlClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()

			database, err := resolveDatabase(ctx, client, projectID, args[0])
			if err != nil {
				return err
			}
			applicationID, err := resolveApplicationID(ctx, client, projectID, appRef)
			if err != nil {
				return err
			}
			attachment, err := client.AttachDbDatabase(ctx, database.ID, applicationID, envVar)
			if err != nil {
				return err
			}
			if state.Output == "json" {
				return state.render(cmd, attachment)
			}
			fmt.Fprintf(
				cmd.OutOrStdout(),
				"attached database %s to app %s as %s (attachment=%s)\n",
				database.Name,
				applicationID,
				attachment.EnvVar,
				attachment.ID,
			)
			if attachment.SecretRef != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "secretRef=%s\n", attachment.SecretRef)
			}
			return nil
		},
	}
	command.Flags().StringVar(&appRef, "app", "", "application id or name")
	command.Flags().StringVar(&envVar, "env-var", "DATABASE_URL", "workload env var for the connection URL")
	command.Flags().StringVar(&envVar, "env", "DATABASE_URL", "alias for --env-var")
	return command
}

func newDatabaseDetachCommand(state *State) *cobra.Command {
	return &cobra.Command{
		Use:   "detach <attachment-id>",
		Short: "Detach a managed database from an application",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := state.controlClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			if err := client.DetachDbAttachment(ctx, args[0]); err != nil {
				return err
			}
			if state.Output == "json" {
				return state.render(cmd, map[string]string{"id": args[0], "status": "detached"})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "detached attachment %s\n", args[0])
			return nil
		},
	}
}

func newDatabaseListCommand(state *State) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List managed database instances and databases for a project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			projectID, err := state.requireProjectID()
			if err != nil {
				return err
			}
			client, err := state.controlClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			instances, err := client.ListDbInstances(ctx, projectID)
			if err != nil {
				return err
			}
			rows := make([]control.DatabaseListItem, 0)
			for _, instance := range instances {
				databases, err := client.ListDbDatabases(ctx, instance.ID)
				if err != nil {
					return err
				}
				if len(databases) == 0 {
					rows = append(rows, control.DatabaseListItem{
						InstanceID:         instance.ID,
						InstanceName:       instance.Name,
						Status:             instance.Status,
						Host:               instance.Host,
						Port:               instance.Port,
						DeletionProtection: instance.DeletionProtection,
					})
					continue
				}
				for _, database := range databases {
					rows = append(rows, control.DatabaseListItem{
						ID:                 database.ID,
						Name:               database.Name,
						InstanceID:         instance.ID,
						InstanceName:       instance.Name,
						Status:             database.Status,
						Host:               database.Host,
						Port:               database.Port,
						SecretRef:          database.SecretRef,
						DeletionProtection: database.DeletionProtection,
					})
				}
			}
			return state.render(cmd, rows)
		},
	}
}

func newDatabaseBackupCommand(state *State) *cobra.Command {
	var wait bool
	command := &cobra.Command{
		Use:   "backup <name-or-id>",
		Short: "Create an on-demand backup of a managed database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := state.requireProjectID()
			if err != nil {
				return err
			}
			client, err := state.controlClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			database, err := resolveDatabase(ctx, client, projectID, args[0])
			if err != nil {
				return err
			}
			backup, err := client.CreateDbBackup(ctx, projectID, database.ID)
			if err != nil {
				return err
			}
			if wait {
				backup, err = pollBackupUntil(ctx, client, projectID, database.ID, backup.ID)
				if err != nil {
					return err
				}
				if backup.Status == "failed" {
					return fmt.Errorf("backup %s failed: %s", backup.ID, backup.StatusReason)
				}
			}
			if state.Output == "json" {
				return state.render(cmd, backup)
			}
			fmt.Fprintf(
				cmd.OutOrStdout(),
				"backup %s status=%s database=%s checksum=%s\n",
				backup.ID,
				backup.Status,
				database.ID,
				backup.Checksum,
			)
			return nil
		},
	}
	command.Flags().BoolVar(&wait, "wait", true, "wait for backup to succeed or fail")
	return command
}

func newDatabaseRestoreCommand(state *State) *cobra.Command {
	var targetRef string
	var wait bool
	command := &cobra.Command{
		Use:   "restore <backup-id>",
		Short: "Restore a backup into a target managed database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(targetRef) == "" {
				return &config.UsageError{Message: "--target is required"}
			}
			projectID, err := state.requireProjectID()
			if err != nil {
				return err
			}
			client, err := state.controlClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			target, err := resolveDatabase(ctx, client, projectID, targetRef)
			if err != nil {
				return err
			}
			sourceBackup, err := findBackupInProject(ctx, client, projectID, args[0])
			if err != nil {
				return err
			}
			result, err := client.RestoreDbBackup(ctx, projectID, args[0], target.ID)
			if err != nil {
				return err
			}
			if wait {
				backup, err := pollRestoreUntil(ctx, client, projectID, sourceBackup.DatabaseID, args[0])
				if err != nil {
					return err
				}
				if backup.RestoreStatus == "failed" {
					return fmt.Errorf("restore failed: %s", backup.RestoreStatusReason)
				}
				result.Status = backup.RestoreStatus
			}
			if state.Output == "json" {
				return state.render(cmd, result)
			}
			fmt.Fprintf(
				cmd.OutOrStdout(),
				"restore backup=%s target=%s status=%s\n",
				result.BackupID,
				result.TargetDatabaseID,
				result.Status,
			)
			return nil
		},
	}
	command.Flags().StringVar(&targetRef, "target", "", "target database name or id")
	command.Flags().BoolVar(&wait, "wait", true, "wait for restore to succeed or fail")
	return command
}

func newDatabaseRotateCommand(state *State) *cobra.Command {
	return &cobra.Command{
		Use:   "rotate <name-or-id>",
		Short: "Rotate credentials for a managed database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := state.requireProjectID()
			if err != nil {
				return err
			}
			client, err := state.controlClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			database, err := resolveDatabase(ctx, client, projectID, args[0])
			if err != nil {
				return err
			}
			result, err := client.RotateDbCredentials(ctx, database.ID)
			if err != nil {
				return err
			}
			result.Credential.Password = ""
			if state.Output == "json" {
				return state.render(cmd, result)
			}
			fmt.Fprintf(
				cmd.OutOrStdout(),
				"rotated credentials for %s username=%s secretRef=%s\n",
				database.Name,
				result.Credential.Username,
				result.SecretRef,
			)
			return nil
		},
	}
}

func newDatabaseDeleteCommand(state *State) *cobra.Command {
	var force bool
	command := &cobra.Command{
		Use:   "delete <name-or-id>",
		Short: "Delete a managed database (disable protection + --force)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectID, err := state.requireProjectID()
			if err != nil {
				return err
			}
			client, err := state.controlClient(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := state.requestContext(cmd)
			defer cancel()
			database, err := resolveDatabase(ctx, client, projectID, args[0])
			if err != nil {
				return err
			}
			if force {
				if _, err := client.PatchDbDatabaseDeletionProtection(ctx, database.ID, false); err != nil {
					return err
				}
			}
			if err := client.DeleteDbDatabase(ctx, database.ID, force); err != nil {
				return err
			}
			if state.Output == "json" {
				return state.render(cmd, map[string]string{"id": database.ID, "status": "deleted"})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted database %s (%s)\n", database.Name, database.ID)
			return nil
		},
	}
	command.Flags().BoolVar(&force, "force", false, "disable deletion protection and force delete")
	return command
}

func (s *State) requireProjectID() (string, error) {
	projectID := strings.TrimSpace(s.Project)
	if projectID == "" {
		projectID = strings.TrimSpace(os.Getenv("FORGE_PROJECT"))
	}
	if projectID == "" {
		return "", &config.UsageError{Message: "--project or FORGE_PROJECT is required"}
	}
	return projectID, nil
}

func statusReasonSuffix(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ""
	}
	return ": " + reason
}

func resolveApplicationID(ctx context.Context, client *control.Client, projectID, appRef string) (string, error) {
	appRef = strings.TrimSpace(appRef)
	if uuidPattern.MatchString(appRef) {
		return appRef, nil
	}
	apps, err := client.ListApplications(ctx, projectID)
	if err != nil {
		return "", err
	}
	var matches []control.Application
	for _, app := range apps {
		if strings.EqualFold(app.Name, appRef) {
			matches = append(matches, app)
		}
	}
	switch len(matches) {
	case 0:
		return "", &config.UsageError{Message: fmt.Sprintf("application %q not found in project", appRef)}
	case 1:
		return matches[0].ID, nil
	default:
		return "", &config.UsageError{Message: fmt.Sprintf("ambiguous application name %q", appRef)}
	}
}

func resolveDatabase(ctx context.Context, client *control.Client, projectID, ref string) (control.DbDatabase, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return control.DbDatabase{}, &config.UsageError{Message: "database name or id is required"}
	}
	if uuidPattern.MatchString(ref) {
		database, err := client.GetDbDatabase(ctx, ref)
		if err == nil {
			return database, nil
		}
		var apiErr *control.APIError
		if !errors.As(err, &apiErr) || apiErr.Status != 404 {
			return control.DbDatabase{}, err
		}
	}

	instances, err := client.ListDbInstances(ctx, projectID)
	if err != nil {
		return control.DbDatabase{}, err
	}
	var byName []control.DbDatabase
	for _, instance := range instances {
		databases, err := client.ListDbDatabases(ctx, instance.ID)
		if err != nil {
			return control.DbDatabase{}, err
		}
		for _, database := range databases {
			if database.ID == ref || strings.EqualFold(database.Name, ref) {
				byName = append(byName, database)
			}
		}
		if strings.EqualFold(instance.Name, ref) {
			switch len(databases) {
			case 0:
				return control.DbDatabase{}, &config.UsageError{
					Message: fmt.Sprintf("instance %q has no databases", ref),
				}
			case 1:
				return databases[0], nil
			default:
				for _, database := range databases {
					if strings.EqualFold(database.Name, ref) {
						return database, nil
					}
				}
				return control.DbDatabase{}, &config.UsageError{
					Message: fmt.Sprintf("instance %q has multiple databases; pass a database name or id", ref),
				}
			}
		}
	}
	switch len(byName) {
	case 0:
		return control.DbDatabase{}, &config.UsageError{Message: fmt.Sprintf("database %q not found in project", ref)}
	case 1:
		return byName[0], nil
	default:
		return control.DbDatabase{}, &config.UsageError{Message: fmt.Sprintf("ambiguous database name %q", ref)}
	}
}

func pollBackupUntil(
	ctx context.Context,
	client *control.Client,
	projectID, databaseID, backupID string,
) (control.DbBackup, error) {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		backup, err := client.GetDbBackup(ctx, projectID, databaseID, backupID)
		if err != nil {
			return control.DbBackup{}, err
		}
		if backup.Status == "succeeded" || backup.Status == "failed" {
			return backup, nil
		}
		if time.Now().After(deadline) {
			return backup, fmt.Errorf("timed out waiting for backup %s (status=%s)", backupID, backup.Status)
		}
		select {
		case <-ctx.Done():
			return backup, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func pollRestoreUntil(
	ctx context.Context,
	client *control.Client,
	projectID, databaseID, backupID string,
) (control.DbBackup, error) {
	deadline := time.Now().Add(2 * time.Minute)
	for {
		backup, err := client.GetDbBackup(ctx, projectID, databaseID, backupID)
		if err != nil {
			return control.DbBackup{}, err
		}
		if backup.RestoreStatus == "succeeded" || backup.RestoreStatus == "failed" {
			return backup, nil
		}
		if time.Now().After(deadline) {
			return backup, fmt.Errorf("timed out waiting for restore of backup %s", backupID)
		}
		select {
		case <-ctx.Done():
			return backup, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func findBackupInProject(ctx context.Context, client *control.Client, projectID, backupID string) (control.DbBackup, error) {
	instances, err := client.ListDbInstances(ctx, projectID)
	if err != nil {
		return control.DbBackup{}, err
	}
	for _, instance := range instances {
		databases, err := client.ListDbDatabases(ctx, instance.ID)
		if err != nil {
			return control.DbBackup{}, err
		}
		for _, database := range databases {
			backup, err := client.GetDbBackup(ctx, projectID, database.ID, backupID)
			if err == nil {
				return backup, nil
			}
		}
	}
	return control.DbBackup{}, &control.APIError{Status: 404, Message: fmt.Sprintf("backup %s not found", backupID)}
}
