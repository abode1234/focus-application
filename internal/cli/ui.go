package cli

import (
	"github.com/spf13/cobra"

	"github.com/abode1234/focus-cli/internal/config"
	"github.com/abode1234/focus-cli/internal/gui"
	"github.com/abode1234/focus-cli/internal/tui"
)

func tuiCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Open the terminal UI",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return tui.Run(a.client())
		},
	}
}

func guiCmd(a *app) *cobra.Command {
	var opts gui.Options
	cmd := &cobra.Command{
		Use:   "gui",
		Short: "Open the popup window",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			paths := config.ResolvePaths()
			cfg, err := config.Load(paths.Config)
			if err != nil {
				return err
			}
			g := cfg.GUI
			flags := cmd.Flags()
			if !flags.Changed("x") {
				opts.X = g.X
			}
			if !flags.Changed("y") {
				opts.Y = g.Y
			}
			if !flags.Changed("w") {
				opts.Width = g.Width
			}
			if !flags.Changed("h") {
				opts.Height = g.Height
			}
			if !flags.Changed("on-top") {
				opts.AlwaysOnTop = g.AlwaysOnTop
			}
			opts.LockPath = paths.GUILock
			return gui.Run(a.client(), opts)
		},
	}
	f := cmd.Flags()
	f.IntVar(&opts.X, "x", -1, "window x position (-1: let the window manager decide)")
	f.IntVar(&opts.Y, "y", -1, "window y position")
	f.IntVar(&opts.Width, "w", 420, "window width")
	f.IntVar(&opts.Height, "h", 520, "window height")
	f.BoolVar(&opts.AlwaysOnTop, "on-top", true, "keep the window above others")
	f.BoolVar(&opts.Popup, "popup", false, "opened by the daemon on time-up; closes itself after finishing")
	// -h is taken by --h; keep --help only.
	cmd.Flags().BoolP("help", "", false, "help for gui")
	return cmd
}
