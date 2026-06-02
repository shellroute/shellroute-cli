package main

import (
	"os"

	"github.com/shellroute/shellroute-cli/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		// Error already printed by command handlers
		os.Exit(1)
	}
}
