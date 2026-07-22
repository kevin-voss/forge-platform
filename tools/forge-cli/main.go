package main

import (
	"errors"
	"fmt"
	"os"

	"forge.local/tools/forge-cli/cmd"
	"forge.local/tools/forge-cli/internal/config"
)

var version = "dev"

func main() {
	root := cmd.NewRootCommand(version)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(root.ErrOrStderr(), "forge:", err)
		var usageError *config.UsageError
		if errors.As(err, &usageError) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
