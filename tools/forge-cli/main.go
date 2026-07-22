package main

import (
	"fmt"
	"os"

	"forge.local/tools/forge-cli/cmd"
	"forge.local/tools/forge-cli/internal/errmap"
)

var version = "dev"

func main() {
	root := cmd.NewRootCommand(version)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(root.ErrOrStderr(), "forge:", err)
		os.Exit(errmap.ExitCode(err))
	}
}
