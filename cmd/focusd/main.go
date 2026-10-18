// Command focusd is the background daemon that owns focus-session state.
package main

import (
	"fmt"
	"os"

	"github.com/abode1234/focus-cli/internal/cli"
)

func main() {
	if err := cli.RunDaemon(); err != nil {
		fmt.Fprintln(os.Stderr, "focusd:", err)
		os.Exit(1)
	}
}
