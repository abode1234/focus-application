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

func clockCmd(a *app) *cobra.Command {
	var x, y, w, h int
	cmd := &cobra.Command{
		Use:   "clock",
		Short: "Show only the countdown of the running session in a small window",
		Long: `Show only the countdown digits of the running focus session.

The daemon opens this window when a session starts (clock_on_start in the
config). Move or resize it and it reopens where you left it. The flags place
it explicitly; on Hyprland they also update the remembered place.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			paths := config.ResolvePaths()
			cfg, err := config.Load(paths.Config)
			if err != nil {
				return err
			}
			g := gui.Geometry{X: cfg.Clock.X, Y: cfg.Clock.Y, Width: cfg.Clock.Width, Height: cfg.Clock.Height}
			if saved, ok := gui.LoadGeometry(paths.ClockState); ok {
				g = saved
			}
			flags := cmd.Flags()
			if flags.Changed("x") {
				g.X = x
			}
			if flags.Changed("y") {
				g.Y = y
			}
			if flags.Changed("w") {
				g.Width = w
			}
			if flags.Changed("h") {
				g.Height = h
			}
			return gui.Run(a.client(), gui.Options{
				X: g.X, Y: g.Y, Width: g.Width, Height: g.Height,
				AlwaysOnTop: cfg.Clock.AlwaysOnTop,
				Clock:       true,
				LockPath:    paths.ClockLock,
				StatePath:   paths.ClockState,
			})
		},
	}
	f := cmd.Flags()
	f.IntVar(&x, "x", -1, "window x position")
	f.IntVar(&y, "y", -1, "window y position")
	f.IntVar(&w, "w", 260, "window width")
	f.IntVar(&h, "h", 110, "window height")
	cmd.Flags().BoolP("help", "", false, "help for clock")
	return cmd
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
