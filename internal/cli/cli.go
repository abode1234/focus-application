// Package cli implements the `focus` command.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/abode1234/focus-cli/internal/client"
	"github.com/abode1234/focus-cli/internal/config"
	"github.com/abode1234/focus-cli/internal/durfmt"
	"github.com/abode1234/focus-cli/internal/engine"
	"github.com/abode1234/focus-cli/internal/wire"
)

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	if err := NewRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "focus:", err)
		return 1
	}
	return 0
}

type app struct {
	socket string
	out    io.Writer
}

func (a *app) client() *client.Client { return client.New(a.socket) }

// NewRoot builds the command tree.
func NewRoot() *cobra.Command {
	a := &app{out: os.Stdout}
	root := &cobra.Command{
		Use:           "focus",
		Short:         "Focus sessions with a goal, a timer and a reflection",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&a.socket, "socket", config.ResolvePaths().Socket, "path to the focusd socket")
	root.AddCommand(
		a.startCmd(), a.breakCmd(), a.statusCmd(), a.finishCmd(), a.abandonCmd(),
		a.logCmd(), a.statsCmd(), tuiCmd(a), guiCmd(a), clockCmd(a), daemonCmd(),
	)
	return root
}

func (a *app) startCmd() *cobra.Command {
	return &cobra.Command{
		Use:   `start "goal" [duration]`,
		Short: "Start a focus session (duration like 25m, 1h30m, or minutes; default from config)",
		Example: `  focus start "Write the design" 25m
  focus start "Review PRs" 50
  focus start Inbox zero`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := a.client()
			goal, d, err := splitGoal(args)
			if err != nil {
				return err
			}
			if d == 0 {
				cfg, err := c.Config(cmd.Context())
				if err != nil {
					return err
				}
				d = time.Duration(cfg.DefaultSeconds) * time.Second
			}
			sess, err := c.Start(cmd.Context(), goal, d)
			if err != nil {
				return explain(err)
			}
			fmt.Fprintf(a.out, "Started %q for %s. Ends at %s.\n", sess.Goal, durfmt.Human(sess.Planned()), sess.Ends().Format("15:04"))
			return nil
		},
	}
}

// splitGoal treats a trailing duration argument as the duration and the rest
// as the goal, so quoting the goal is optional.
func splitGoal(args []string) (string, time.Duration, error) {
	var d time.Duration
	if len(args) > 1 {
		if parsed, err := durfmt.Parse(args[len(args)-1]); err == nil {
			d = parsed
			args = args[:len(args)-1]
		}
	}
	goal := strings.TrimSpace(strings.Join(args, " "))
	if goal == "" {
		return "", 0, errors.New("goal is empty")
	}
	if _, err := durfmt.Parse(goal); err == nil && d == 0 {
		return "", 0, fmt.Errorf(`%q looks like a duration; give a goal too: focus start "goal" %s`, goal, goal)
	}
	return goal, d, nil
}

func (a *app) breakCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "break [duration]",
		Short: "Start a break (default length from config)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var d time.Duration
			if len(args) == 1 {
				var err error
				if d, err = durfmt.Parse(args[0]); err != nil {
					return err
				}
			}
			sess, err := a.client().StartBreak(cmd.Context(), d)
			if err != nil {
				return explain(err)
			}
			fmt.Fprintf(a.out, "Break for %s. Back at %s.\n", durfmt.Human(sess.Planned()), sess.Ends().Format("15:04"))
			return nil
		},
	}
}

func (a *app) statusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the current session and remaining time",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := a.client().Status(cmd.Context())
			if err != nil {
				return explain(err)
			}
			if asJSON {
				return writeJSON(a.out, st)
			}
			printStatus(a.out, st)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

func printStatus(w io.Writer, st wire.Status) {
	s := st.Session
	remaining := time.Duration(st.RemainingSeconds) * time.Second
	switch {
	case s == nil:
		fmt.Fprintln(w, `No session running. Start one with: focus start "goal" 25m`)
	case s.IsBreak():
		fmt.Fprintf(w, "Break: %s remaining (back at %s)\n", durfmt.Clock(remaining), s.Ends().Format("15:04"))
	case st.TimeUp:
		fmt.Fprintf(w, "Time's up: %s\nWhat did you accomplish?  focus finish \"…\"\n", s.Goal)
	default:
		fmt.Fprintf(w, "Focus: %s\n%s remaining of %s (ends %s)\n", s.Goal, durfmt.Clock(remaining), durfmt.Human(s.Planned()), s.Ends().Format("15:04"))
	}
}

func (a *app) finishCmd() *cobra.Command {
	var noBreak, withBreak bool
	cmd := &cobra.Command{
		Use:   `finish ["what I did"]`,
		Short: "Finish the running session with a reflection (prompts if omitted)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := a.client()
			sess, err := running(cmd.Context(), c)
			if err != nil {
				return err
			}
			reflection := strings.Join(args, " ")
			if reflection == "" && !sess.IsBreak() && isTerminal(os.Stdin) {
				fmt.Fprint(a.out, "What did you accomplish? ")
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				reflection = strings.TrimSpace(line)
			}
			var startBreak *bool
			if noBreak {
				startBreak = new(false)
			} else if withBreak {
				startBreak = new(true)
			}
			res, err := c.Finish(cmd.Context(), sess.ID, reflection, startBreak)
			if err != nil {
				return explain(err)
			}
			if res.Session.IsBreak() {
				if reflection != "" {
					fmt.Fprintln(a.out, "A break was running, so there was nothing to reflect on; the text was not saved.")
				}
				fmt.Fprintln(a.out, "Break ended.")
				return nil
			}
			fmt.Fprintf(a.out, "Finished %q.\n", res.Session.Goal)
			if res.Break != nil {
				fmt.Fprintf(a.out, "Break for %s. Back at %s.\n", durfmt.Human(res.Break.Planned()), res.Break.Ends().Format("15:04"))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&noBreak, "no-break", false, "don't start a break afterwards")
	cmd.Flags().BoolVar(&withBreak, "break", false, "start a break afterwards even if auto_break is off")
	cmd.MarkFlagsMutuallyExclusive("no-break", "break")
	return cmd
}

func (a *app) abandonCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "abandon",
		Short: "Abandon the running session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c := a.client()
			sess, err := running(cmd.Context(), c)
			if err != nil {
				return err
			}
			if _, err := c.Abandon(cmd.Context(), sess.ID); err != nil {
				return explain(err)
			}
			fmt.Fprintf(a.out, "Abandoned %q.\n", sess.Goal)
			return nil
		},
	}
}

func running(ctx context.Context, c *client.Client) (*wire.Session, error) {
	st, err := c.Status(ctx)
	if err != nil {
		return nil, explain(err)
	}
	if st.Session == nil {
		return nil, errors.New("no session is running")
	}
	return st.Session, nil
}

func (a *app) logCmd() *cobra.Command {
	var week, all, asJSON bool
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Show today's sessions (--week for this week)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rng := engine.RangeDay
			if week {
				rng = engine.RangeWeek
			}
			from, to, _ := engine.Bounds(rng, time.Now(), time.Local)
			if all {
				from, to = time.Unix(0, 0), time.Now().Add(24*time.Hour)
			}
			list, err := a.client().History(cmd.Context(), from, to)
			if err != nil {
				return explain(err)
			}
			if asJSON {
				return writeJSON(a.out, wire.Sessions{Sessions: list})
			}
			printLog(a.out, list, week || all)
			return nil
		},
	}
	cmd.Flags().BoolVar(&week, "week", false, "this week (from Monday)")
	cmd.Flags().BoolVar(&all, "all", false, "all history")
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

func printLog(w io.Writer, list []wire.Session, showDate bool) {
	var focus []wire.Session
	for _, s := range list {
		if !s.IsBreak() {
			focus = append(focus, s)
		}
	}
	if len(focus) == 0 {
		fmt.Fprintln(w, "No sessions yet.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	layout := "15:04"
	if showDate {
		layout = "Mon 02 Jan 15:04"
	}
	fmt.Fprintln(tw, "STARTED\tLENGTH\tSTATUS\tGOAL\tREFLECTION")
	for _, s := range focus {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", s.Started().Format(layout), durfmt.Human(s.Planned()), s.Status, s.Goal, s.Reflection)
	}
	tw.Flush()
}

func (a *app) statsCmd() *cobra.Command {
	var week, asJSON bool
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show today's stats (--week for this week)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rng := "day"
			if week {
				rng = "week"
			}
			st, err := a.client().Stats(cmd.Context(), rng)
			if err != nil {
				return explain(err)
			}
			if asJSON {
				return writeJSON(a.out, st)
			}
			printStats(a.out, st)
			return nil
		},
	}
	cmd.Flags().BoolVar(&week, "week", false, "this week (from Monday)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output")
	return cmd
}

func printStats(w io.Writer, st wire.Stats) {
	title := "Today"
	if st.Range == "week" {
		title = "This week"
	}
	focus := time.Duration(st.FocusSeconds) * time.Second
	fmt.Fprintf(w, "%s: %s focused, %d completed, %d abandoned\n", title, durfmt.Human(focus), st.Completed, st.Abandoned)
	if st.Range != "week" {
		return
	}
	var peak int64 = 1
	for _, d := range st.Days {
		peak = max(peak, d.FocusSeconds)
	}
	for _, d := range st.Days {
		day, _ := time.ParseInLocation(time.DateOnly, d.Date, time.Local)
		bar := strings.Repeat("█", int(d.FocusSeconds*20/peak))
		fmt.Fprintf(w, "  %s  %-20s %s\n", day.Format("Mon"), bar, durfmt.Human(time.Duration(d.FocusSeconds)*time.Second))
	}
}

// explain turns API errors into friendlier messages.
func explain(err error) error {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.Status == 409 && strings.Contains(apiErr.Message, "already running") {
		return errors.New("a session is already running (see: focus status)")
	}
	return err
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// signalContext is cancelled on SIGINT or SIGTERM.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
