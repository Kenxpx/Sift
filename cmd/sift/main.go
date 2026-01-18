// Command sift indexes a local corpus and answers queries over the
// published index.
//
// The whole command line lives in internal/cli, which returns an exit code
// instead of ending the process, so every command is testable. This file is
// the only place that touches the process itself.
//
// Usage:
//
//	sift <command> [flags] [arguments]
//
// Run "sift help" for the list of commands.
package main

import (
	"os"

	"sift/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
