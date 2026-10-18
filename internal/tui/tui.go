// Package tui is the interactive terminal interface.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/abode1234/focus-cli/internal/durfmt"
	"github.com/abode1234/focus-cli/internal/wire"
)

// API is the subset of the daemon client the TUI uses.
type API interface {
	Status(ctx context.Context) (wire.Status, error)
	Config(ctx context.Context) (wire.Config, error)
	Start(ctx context.Context, goal string, d time.Duration) (wire.Session, error)
	StartBreak(ctx context.Context, d time.Duration) (wire.Session, error)
	Finish(ctx context.Context, id int64, reflection string, startBreak *bool) (wire.FinishResponse, error)
	Abandon(ctx context.Context, id int64) (wire.Session, error)
	History(ctx context.Context, from, to time.Time) ([]wire.Session, error)
	Stats(ctx context.Context, rng string) (wire.Stats, error)
	Watch(ctx context.Context, fn func(wire.Event))
}

// Run opens the TUI until the user quits.
func Run(api API) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newModel(api, time.Now)
	p := tea.NewProgram(m, tea.WithAltScreen())
	go api.Watch(ctx, func(ev wire.Event) { p.Send(eventMsg(ev)) })
	_, err := p.Run()
	return err
}

type screen int

const (
	screenMain screen = iota
	screenGoal
	screenDuration
	screenFinish
	screenConfirmAbandon
	screenHistory
)

type (
	statusMsg  wire.Status
	configMsg  wire.Config
	eventMsg   wire.Event
	historyMsg struct {
		sessions []wire.Session
		stats    wire.Stats
	}
	errMsg  struct{ err error }
	tickMsg time.Time
)

type model struct {
	api API
	now func() time.Time

	screen    screen
	status    wire.Status
	loaded    bool
	cfg       wire.Config
	connected bool
	err       error
	notice    string

	goal       textinput.Model
	custom     textinput.Model
	reflection textinput.Model
	choice     int // index into durations(); len means "custom"
	takeBreak  bool
	prompted   int64 // session whose time-up already opened the finish screen
	target     int64 // session the finish or abandon screen acts on

	history []wire.Session
	stats   wire.Stats

	bar   progress.Model
	width int
}

func newModel(api API, now func() time.Time) model {
	goal := textinput.New()
	goal.Placeholder = "What will you work on?"
	goal.CharLimit = 200
	custom := textinput.New()
	custom.Placeholder = "e.g. 40m or 1h15m"
	custom.CharLimit = 16
	reflection := textinput.New()
	reflection.Placeholder = "What did you accomplish?"
	reflection.CharLimit = 1000
	return model{
		api: api, now: now,
		goal: goal, custom: custom, reflection: reflection,
		cfg: wire.Config{PresetSeconds: []int64{1500, 3000}, BreakSeconds: 300, AutoBreak: true},
		bar: progress.New(progress.WithDefaultGradient(), progress.WithoutPercentage()),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.fetchStatus(), m.fetchConfig(), tick())
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) fetchStatus() tea.Cmd {
	return func() tea.Msg {
		st, err := m.api.Status(context.Background())
		if err != nil {
			return errMsg{err}
		}
		return statusMsg(st)
	}
}

func (m model) fetchConfig() tea.Cmd {
	return func() tea.Msg {
		cfg, err := m.api.Config(context.Background())
		if err != nil {
			return errMsg{err}
		}
		return configMsg(cfg)
	}
}

func (m model) fetchHistory() tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		now := m.now()
		from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -6)
		list, err := m.api.History(ctx, from, now.Add(time.Hour))
		if err != nil {
			return errMsg{err}
		}
		st, err := m.api.Stats(ctx, "week")
		if err != nil {
			return errMsg{err}
		}
		return historyMsg{sessions: list, stats: st}
	}
}

// do runs an action and then refreshes status.
func (m model) do(notice string, f func(ctx context.Context) error) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		if err := f(ctx); err != nil {
			return errMsg{err}
		}
		st, err := m.api.Status(ctx)
		if err != nil {
			return errMsg{err}
		}
		return actionDone{status: st, notice: notice}
	}
}

type actionDone struct {
	status wire.Status
	notice string
}

func (m model) durations() []time.Duration {
	out := make([]time.Duration, 0, len(m.cfg.PresetSeconds))
	for _, s := range m.cfg.PresetSeconds {
		out = append(out, time.Duration(s)*time.Second)
	}
	return out
}

func (m model) session() *wire.Session { return m.status.Session }

func (m model) timeUp() bool {
	s := m.session()
	return s != nil && !s.IsBreak() && s.Remaining(m.now()) == 0
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.bar.Width = min(max(msg.Width-8, 10), 60)
		return m, nil
	case tickMsg:
		return m.maybePrompt(), tick()
	case statusMsg:
		m.status, m.loaded, m.err = wire.Status(msg), true, nil
		m.connected = true
		return m.dropStaleScreen().maybePrompt(), nil
	case configMsg:
		m.cfg = wire.Config(msg)
		return m, nil
	case actionDone:
		m.status, m.notice, m.err = msg.status, msg.notice, nil
		m.screen = screenMain
		return m, nil
	case historyMsg:
		m.history, m.stats = msg.sessions, msg.stats
		return m, nil
	case eventMsg:
		switch msg.Type {
		case "disconnected":
			m.connected = false
			return m, nil
		case "connected":
			m.connected = true
		}
		cmds := []tea.Cmd{m.fetchStatus()}
		if m.screen == screenHistory {
			cmds = append(cmds, m.fetchHistory())
		}
		return m, tea.Batch(cmds...)
	case errMsg:
		m.err = msg.err
		return m, nil
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

// maybePrompt opens the reflection screen once when a focus session's time
// runs out, so the TUI asks the same question as the popup.
func (m model) maybePrompt() model {
	s := m.session()
	if m.screen == screenMain && m.timeUp() && m.prompted != s.ID {
		m.prompted = s.ID
		return m.openFinish()
	}
	return m
}

// dropStaleScreen leaves the finish or abandon screen when the session it
// was opened for is no longer the running one, e.g. after finishing it from
// another interface.
func (m model) dropStaleScreen() model {
	if m.screen != screenFinish && m.screen != screenConfirmAbandon {
		return m
	}
	if s := m.session(); s == nil || s.ID != m.target {
		m.screen = screenMain
		m.reflection.Blur()
	}
	return m
}

func (m model) openFinish() model {
	m.target = m.session().ID
	m.screen = screenFinish
	m.takeBreak = m.cfg.AutoBreak
	m.reflection.Reset()
	m.reflection.Focus()
	return m
}

func (m model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}
	m.err, m.notice = nil, ""
	switch m.screen {
	case screenMain:
		return m.keyMain(msg)
	case screenGoal:
		switch msg.Type {
		case tea.KeyEsc:
			m.screen = screenMain
			return m, nil
		case tea.KeyEnter:
			if strings.TrimSpace(m.goal.Value()) == "" {
				return m, nil
			}
			m.screen = screenDuration
			m.choice = 0
			m.custom.Reset()
			m.custom.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.goal, cmd = m.goal.Update(msg)
		return m, cmd
	case screenDuration:
		return m.keyDuration(msg)
	case screenFinish:
		switch msg.Type {
		case tea.KeyEsc:
			m.screen = screenMain
			return m, nil
		case tea.KeyTab:
			m.takeBreak = !m.takeBreak
			return m, nil
		case tea.KeyEnter:
			id, text, brk := m.target, m.reflection.Value(), m.takeBreak
			return m, m.do("Session saved.", func(ctx context.Context) error {
				_, err := m.api.Finish(ctx, id, text, &brk)
				return err
			})
		}
		var cmd tea.Cmd
		m.reflection, cmd = m.reflection.Update(msg)
		return m, cmd
	case screenConfirmAbandon:
		if msg.String() == "y" {
			id := m.target
			return m, m.do("Session abandoned.", func(ctx context.Context) error {
				_, err := m.api.Abandon(ctx, id)
				return err
			})
		}
		m.screen = screenMain
		return m, nil
	case screenHistory:
		switch msg.String() {
		case "esc", "h", "q":
			m.screen = screenMain
		}
		return m, nil
	}
	return m, nil
}

func (m model) keyMain(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.session()
	idle := s == nil || s.IsBreak()
	switch msg.String() {
	case "q", "esc":
		return m, tea.Quit
	case "s":
		if idle {
			m.screen = screenGoal
			m.goal.Reset()
			m.goal.Focus()
			return m, textinput.Blink
		}
	case "f":
		if s != nil && s.IsBreak() {
			id := s.ID
			return m, m.do("Break ended.", func(ctx context.Context) error {
				_, err := m.api.Finish(ctx, id, "", nil)
				return err
			})
		}
		if s != nil {
			return m.openFinish(), textinput.Blink
		}
	case "a":
		if s != nil && !s.IsBreak() {
			m.screen = screenConfirmAbandon
			m.target = s.ID
		}
	case "b":
		if s == nil {
			return m, m.do("Enjoy your break.", func(ctx context.Context) error {
				_, err := m.api.StartBreak(ctx, 0)
				return err
			})
		}
	case "h":
		m.screen = screenHistory
		return m, m.fetchHistory()
	case "r":
		return m, m.fetchStatus()
	}
	return m, nil
}

func (m model) keyDuration(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	options := m.durations()
	customIdx := len(options)
	if m.choice == customIdx && m.custom.Focused() {
		switch msg.Type {
		case tea.KeyEsc:
			m.custom.Blur()
			return m, nil
		case tea.KeyEnter:
			d, err := durfmt.Parse(m.custom.Value())
			if err != nil {
				m.err = err
				return m, nil
			}
			return m, m.start(d)
		}
		var cmd tea.Cmd
		m.custom, cmd = m.custom.Update(msg)
		return m, cmd
	}
	switch msg.String() {
	case "esc":
		m.screen = screenGoal
		m.goal.Focus()
	case "left", "h", "shift+tab", "up", "k":
		m.choice = (m.choice + customIdx) % (customIdx + 1)
	case "right", "l", "tab", "down", "j":
		m.choice = (m.choice + 1) % (customIdx + 1)
	case "enter":
		if m.choice == customIdx {
			m.custom.Focus()
			return m, textinput.Blink
		}
		return m, m.start(options[m.choice])
	default:
		if n := int(msg.String()[0] - '1'); len(msg.String()) == 1 && n >= 0 && n <= customIdx {
			m.choice = n
			if n == customIdx {
				m.custom.Focus()
				return m, textinput.Blink
			}
			return m, m.start(options[n])
		}
	}
	return m, nil
}

func (m model) start(d time.Duration) tea.Cmd {
	goal := strings.TrimSpace(m.goal.Value())
	return m.do(fmt.Sprintf("Focusing for %s.", durfmt.Human(d)), func(ctx context.Context) error {
		_, err := m.api.Start(ctx, goal, d)
		return err
	})
}

var (
	accent    = lipgloss.AdaptiveColor{Light: "#5A3FD9", Dark: "#A78BFA"}
	calm      = lipgloss.AdaptiveColor{Light: "#0F766E", Dark: "#5EEAD4"}
	alert     = lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FBBF24"}
	muted     = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#9CA3AF"}
	danger    = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#F87171"}
	frame     = lipgloss.NewStyle().Padding(1, 3)
	title     = lipgloss.NewStyle().Bold(true).Foreground(accent)
	label     = lipgloss.NewStyle().Foreground(muted)
	big       = lipgloss.NewStyle().Bold(true).Padding(0, 1)
	help      = lipgloss.NewStyle().Foreground(muted)
	selected  = lipgloss.NewStyle().Bold(true).Foreground(accent).Underline(true)
	errStyle  = lipgloss.NewStyle().Foreground(danger)
	noteStyle = lipgloss.NewStyle().Foreground(calm)
)

func (m model) View() string {
	var b strings.Builder
	b.WriteString(title.Render("◉ focus"))
	if !m.connected && m.loaded {
		b.WriteString(errStyle.Render("  reconnecting…"))
	}
	b.WriteString("\n\n")

	switch m.screen {
	case screenGoal:
		b.WriteString(label.Render("Goal") + "\n" + m.goal.View() + "\n\n")
		b.WriteString(help.Render("enter next · esc back"))
	case screenDuration:
		b.WriteString(label.Render("Goal  ") + m.goal.Value() + "\n\n")
		b.WriteString(label.Render("Duration") + "\n")
		options := m.durations()
		for i, d := range options {
			b.WriteString(m.option(i, fmt.Sprintf("%d %s", i+1, durfmt.Human(d))) + "   ")
		}
		b.WriteString(m.option(len(options), fmt.Sprintf("%d custom", len(options)+1)) + "\n")
		if m.choice == len(options) && m.custom.Focused() {
			b.WriteString("\n" + m.custom.View() + "\n")
		}
		b.WriteString("\n" + help.Render("←/→ choose · enter start · esc back"))
	case screenFinish:
		if s := m.session(); s != nil {
			if m.timeUp() {
				b.WriteString(big.Foreground(alert).Render("Time's up!") + "\n\n")
			}
			b.WriteString(label.Render("Goal  ") + s.Goal + "\n\n")
		}
		b.WriteString(label.Render("What did you accomplish?") + "\n" + m.reflection.View() + "\n\n")
		check := "[ ]"
		if m.takeBreak {
			check = "[x]"
		}
		fmt.Fprintf(&b, "%s take a %s break\n\n", check, durfmt.Human(time.Duration(m.cfg.BreakSeconds)*time.Second))
		b.WriteString(help.Render("enter save · tab toggle break · esc back"))
	case screenConfirmAbandon:
		b.WriteString("Abandon this session? It won't count toward your stats.\n\n")
		b.WriteString(help.Render("y abandon · any other key cancel"))
	case screenHistory:
		b.WriteString(m.viewHistory())
	default:
		b.WriteString(m.viewMain())
	}

	if m.err != nil {
		b.WriteString("\n\n" + errStyle.Render(m.err.Error()))
	} else if m.notice != "" {
		b.WriteString("\n\n" + noteStyle.Render(m.notice))
	}
	return frame.Render(b.String())
}

func (m model) option(i int, text string) string {
	if i == m.choice {
		return selected.Render(text)
	}
	return text
}

func (m model) viewMain() string {
	if !m.loaded {
		if m.err != nil {
			return "Can't reach focusd."
		}
		return label.Render("Connecting…")
	}
	s := m.session()
	if s == nil {
		return "No session running.\n\n" + help.Render("s start · b break · h history · q quit")
	}
	now := m.now()
	remaining := s.Remaining(now)
	elapsed := float64(s.Planned()-remaining) / float64(s.Planned())
	var b strings.Builder
	switch {
	case s.IsBreak():
		b.WriteString(label.Render("Break") + "\n\n")
		b.WriteString(big.Foreground(calm).Render(durfmt.Clock(remaining)) + "\n\n")
		b.WriteString(m.bar.ViewAs(elapsed) + "\n\n")
		b.WriteString(help.Render("s start focusing · f end break · h history · q quit"))
	case remaining == 0:
		b.WriteString(label.Render("Goal  ") + s.Goal + "\n\n")
		b.WriteString(big.Foreground(alert).Render("Time's up!") + "\n\n")
		b.WriteString(help.Render("f write reflection · a abandon · q quit"))
	default:
		b.WriteString(label.Render("Goal  ") + s.Goal + "\n\n")
		b.WriteString(big.Foreground(accent).Render(durfmt.Clock(remaining)) +
			label.Render(fmt.Sprintf(" of %s · ends %s", durfmt.Human(s.Planned()), s.Ends().Format("15:04"))) + "\n\n")
		b.WriteString(m.bar.ViewAs(elapsed) + "\n\n")
		b.WriteString(help.Render("f finish · a abandon · h history · q quit"))
	}
	return b.String()
}

func (m model) viewHistory() string {
	var b strings.Builder
	st := m.stats
	fmt.Fprintf(&b, "This week  %s focused · %d completed · %d abandoned\n\n",
		durfmt.Human(time.Duration(st.FocusSeconds)*time.Second), st.Completed, st.Abandoned)
	var peak int64 = 1
	for _, d := range st.Days {
		peak = max(peak, d.FocusSeconds)
	}
	for _, d := range st.Days {
		day, _ := time.ParseInLocation(time.DateOnly, d.Date, time.Local)
		bar := lipgloss.NewStyle().Foreground(accent).Render(strings.Repeat("█", int(d.FocusSeconds*24/peak)))
		fmt.Fprintf(&b, "%s %s %s\n", label.Render(day.Format("Mon")), bar,
			label.Render(durfmt.Human(time.Duration(d.FocusSeconds)*time.Second)))
	}
	b.WriteString("\n")
	shown := 0
	for i := len(m.history) - 1; i >= 0 && shown < 10; i-- {
		s := m.history[i]
		if s.IsBreak() {
			continue
		}
		shown++
		mark := map[string]string{"completed": noteStyle.Render("✓"), "abandoned": errStyle.Render("✗"), "running": "…"}[string(s.Status)]
		line := fmt.Sprintf("%s %s  %-6s %s", mark, label.Render(s.Started().Format("Mon 15:04")), durfmt.Human(s.Planned()), s.Goal)
		if s.Reflection != "" {
			line += label.Render("  — " + s.Reflection)
		}
		b.WriteString(line + "\n")
	}
	if shown == 0 {
		b.WriteString(label.Render("No sessions in the last 7 days.") + "\n")
	}
	b.WriteString("\n" + help.Render("esc back"))
	return b.String()
}
