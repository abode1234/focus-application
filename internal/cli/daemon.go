package cli

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/abode1234/focus-cli/internal/config"
	"github.com/abode1234/focus-cli/internal/daemon"
)

func daemonCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "daemon",
		Short: "Run the focusd daemon in the foreground (for development)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return RunDaemon()
		},
	}
}

// RunDaemon runs focusd until SIGINT/SIGTERM. Shared by `focus daemon` and
// the focusd binary.
func RunDaemon() error {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	paths := config.ResolvePaths()
	cfg, err := config.Load(paths.Config)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	return daemon.Run(ctx, paths, cfg, log)
}
