// Command focus is the CLI, TUI and GUI client for focusd.
package main

import (
	"os"

	"github.com/abode1234/focus-cli/internal/cli"
)

func main() { os.Exit(cli.Execute()) }
