// Package render provides the basic table and JSON output used by CLI resources.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	sharedclient "forge.local/tools/forge-cli/internal/client"
	"forge.local/tools/forge-cli/internal/control"
)

// Write renders a resource or resource list in the requested output format.
func Write(writer io.Writer, format string, value any) error {
	if format == "json" {
		return writeJSON(writer, value)
	}

	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	switch value := value.(type) {
	case control.Project:
		writeProject(table, value)
	case []control.Project:
		fmt.Fprintln(table, "ID\tNAME\tSLUG")
		for _, project := range value {
			fmt.Fprintf(table, "%s\t%s\t%s\n", project.ID, project.Name, project.Slug)
		}
	case control.Environment:
		writeEnvironment(table, value)
	case []control.Environment:
		fmt.Fprintln(table, "ID\tPROJECT ID\tNAME")
		for _, environment := range value {
			fmt.Fprintf(table, "%s\t%s\t%s\n", environment.ID, environment.ProjectID, environment.Name)
		}
	case control.Application:
		writeApplication(table, value)
	case []control.Application:
		fmt.Fprintln(table, "ID\tPROJECT ID\tNAME")
		for _, application := range value {
			fmt.Fprintf(table, "%s\t%s\t%s\n", application.ID, application.ProjectID, application.Name)
		}
	case control.Service:
		writeService(table, value)
	case []control.Service:
		fmt.Fprintln(table, "ID\tAPPLICATION ID\tNAME\tPORT")
		for _, service := range value {
			fmt.Fprintf(table, "%s\t%s\t%s\t%d\n", service.ID, service.ApplicationID, service.Name, service.Port)
		}
	case control.Deployment:
		writeDeployment(table, value)
	case []control.Deployment:
		fmt.Fprintln(table, "ID\tSERVICE ID\tENVIRONMENT ID\tIMAGE\tREPLICAS\tSTATUS")
		for _, deployment := range value {
			fmt.Fprintf(
				table,
				"%s\t%s\t%s\t%s\t%d\t%s\n",
				deployment.ID,
				deployment.ServiceID,
				deployment.EnvironmentID,
				deployment.Image,
				deployment.DesiredReplicas,
				deployment.Status,
			)
		}
	case sharedclient.SetSecretResponse:
		fmt.Fprintln(table, "NAME\tVERSION")
		fmt.Fprintf(table, "%s\t%d\n", value.Name, value.Version)
	case []sharedclient.SecretListItem:
		fmt.Fprintln(table, "NAME\tVERSION\tUPDATED")
		for _, item := range value {
			fmt.Fprintf(table, "%s\t%d\t%s\n", item.Name, item.Version, item.UpdatedAt)
		}
	case sharedclient.ConfigListItem:
		fmt.Fprintln(table, "NAME\tVALUE\tUPDATED")
		fmt.Fprintf(table, "%s\t%s\t%s\n", value.Name, value.Value, value.UpdatedAt)
	case []sharedclient.ConfigListItem:
		fmt.Fprintln(table, "NAME\tVALUE\tUPDATED")
		for _, item := range value {
			fmt.Fprintf(table, "%s\t%s\t%s\n", item.Name, item.Value, item.UpdatedAt)
		}
	case control.DbInstance:
		fmt.Fprintln(table, "ID\tPROJECT ID\tNAME\tSTATUS\tENGINE")
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", value.ID, value.ProjectID, value.Name, value.Status, value.Engine)
	case []control.DbInstance:
		fmt.Fprintln(table, "ID\tPROJECT ID\tNAME\tSTATUS\tENGINE")
		for _, instance := range value {
			fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", instance.ID, instance.ProjectID, instance.Name, instance.Status, instance.Engine)
		}
	case control.DbDatabase:
		fmt.Fprintln(table, "ID\tINSTANCE ID\tNAME\tSTATUS\tSECRET REF")
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", value.ID, value.InstanceID, value.Name, value.Status, value.SecretRef)
	case []control.DbDatabase:
		fmt.Fprintln(table, "ID\tINSTANCE ID\tNAME\tSTATUS\tSECRET REF")
		for _, database := range value {
			fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", database.ID, database.InstanceID, database.Name, database.Status, database.SecretRef)
		}
	case control.DbAttachment:
		fmt.Fprintln(table, "ID\tDATABASE ID\tAPPLICATION ID\tENV VAR\tSECRET REF")
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", value.ID, value.DatabaseID, value.ApplicationID, value.EnvVar, value.SecretRef)
	case []control.DbAttachment:
		fmt.Fprintln(table, "ID\tDATABASE ID\tAPPLICATION ID\tENV VAR\tSECRET REF")
		for _, attachment := range value {
			fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", attachment.ID, attachment.DatabaseID, attachment.ApplicationID, attachment.EnvVar, attachment.SecretRef)
		}
	case control.DbBackup:
		fmt.Fprintln(table, "ID\tDATABASE ID\tSTATUS\tCHECKSUM\tSIZE")
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%d\n", value.ID, value.DatabaseID, value.Status, value.Checksum, value.SizeBytes)
	case []control.DbBackup:
		fmt.Fprintln(table, "ID\tDATABASE ID\tSTATUS\tCHECKSUM\tSIZE")
		for _, backup := range value {
			fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%d\n", backup.ID, backup.DatabaseID, backup.Status, backup.Checksum, backup.SizeBytes)
		}
	case []control.DatabaseListItem:
		fmt.Fprintln(table, "ID\tNAME\tINSTANCE\tSTATUS\tSECRET REF")
		for _, item := range value {
			fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", item.ID, item.Name, item.InstanceName, item.Status, item.SecretRef)
		}
	default:
		return fmt.Errorf("unsupported table output type %T", value)
	}
	return table.Flush()
}

// writeJSON emits resource structs and resource lists without a CLI envelope.
// Struct declaration order and encoding/json's sorted map keys make the output
// stable for scripts while preserving Control's resource shape.
func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func writeProject(table *tabwriter.Writer, project control.Project) {
	fmt.Fprintln(table, "ID\tNAME\tSLUG")
	fmt.Fprintf(table, "%s\t%s\t%s\n", project.ID, project.Name, project.Slug)
}

func writeEnvironment(table *tabwriter.Writer, environment control.Environment) {
	fmt.Fprintln(table, "ID\tPROJECT ID\tNAME")
	fmt.Fprintf(table, "%s\t%s\t%s\n", environment.ID, environment.ProjectID, environment.Name)
}

func writeApplication(table *tabwriter.Writer, application control.Application) {
	fmt.Fprintln(table, "ID\tPROJECT ID\tNAME")
	fmt.Fprintf(table, "%s\t%s\t%s\n", application.ID, application.ProjectID, application.Name)
}

func writeService(table *tabwriter.Writer, service control.Service) {
	fmt.Fprintln(table, "ID\tAPPLICATION ID\tNAME\tPORT")
	fmt.Fprintf(table, "%s\t%s\t%s\t%d\n", service.ID, service.ApplicationID, service.Name, service.Port)
}

func writeDeployment(table *tabwriter.Writer, deployment control.Deployment) {
	fmt.Fprintln(table, "ID\tSERVICE ID\tENVIRONMENT ID\tIMAGE\tREPLICAS\tSTATUS")
	fmt.Fprintf(
		table,
		"%s\t%s\t%s\t%s\t%d\t%s\n",
		deployment.ID,
		deployment.ServiceID,
		deployment.EnvironmentID,
		deployment.Image,
		deployment.DesiredReplicas,
		deployment.Status,
	)
}
