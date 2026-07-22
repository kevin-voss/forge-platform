// Package render provides the basic table and JSON output used by CLI resources.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

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
