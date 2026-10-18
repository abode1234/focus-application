package tui

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/abode1234/focus-cli/internal/wire"
)

type started struct {
	goal string
	d    time.Duration
}

type finished struct {
	id         int64
	reflection string
	brk        *bool
}

// fakeAPI records what the TUI asked the daemon to do. Commands run on
// Bubble Tea's goroutines, hence the lock.
type fakeAPI struct {
	mu       sync.Mutex
	status   wire.Status
	starts   []started
	finishes []finished
	abandons []int64
}

func (f *fakeAPI) Status(context.Context) (wire.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, nil
}
func (f *fakeAPI) Config(context.Context) (wire.Config, error) {
	return wire.Config{PresetSeconds: []int64{1500, 3000}, BreakSeconds: 300, AutoBreak: true}, nil
}
func (f *fakeAPI) Start(_ context.Context, goal string, d time.Duration) (wire.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, started{goal, d})
	return wire.Session{}, nil
}
func (f *fakeAPI) StartBreak(context.Context, time.Duration) (wire.Session, error) {
	return wire.Session{}, nil
}
func (f *fakeAPI) Finish(_ context.Context, id int64, r string, b *bool) (wire.FinishResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finishes = append(f.finishes, finished{id, r, b})
	return wire.FinishResponse{}, nil
}
func (f *fakeAPI) Abandon(_ context.Context, id int64) (wire.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.abandons = append(f.abandons, id)
	return wire.Session{}, nil
}
func (f *fakeAPI) History(context.Context, time.Time, time.Time) ([]wire.Session, error) {
	return nil, nil
}
func (f *fakeAPI) Stats(context.Context, string) (wire.Stats, error) { return wire.Stats{}, nil }
func (f *fakeAPI) Watch(context.Context, func(wire.Event))           {}

func (f *fakeAPI) eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		done := ok()
		f.mu.Unlock()
		if done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(what)
}

var now = time.Unix(1_800_000_000, 0)

func open(t *testing.T, api *fakeAPI) *teatest.TestModel {
	t.Helper()
	m := newModel(api, func() time.Time { return now })
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	t.Cleanup(func() { tm.Quit() })
	return tm
}

func waitFor(t *testing.T, tm *teatest.TestModel, text string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool { return bytes.Contains(b, []byte(text)) },
		teatest.WithDuration(3*time.Second))
}

func press(tm *teatest.TestModel, k tea.KeyType) { tm.Send(tea.KeyMsg{Type: k}) }

func TestStartWithPreset(t *testing.T) {
	api := &fakeAPI{}
	tm := open(t, api)
	waitFor(t, tm, "No session running")
	tm.Type("s")
	waitFor(t, tm, "Goal")
	tm.Type("Write the design")
	press(tm, tea.KeyEnter)
	waitFor(t, tm, "Duration")
	tm.Type("2")
	api.eventually(t, "Start not called with the 50m preset", func() bool {
		return len(api.starts) == 1 && api.starts[0] == started{"Write the design", 50 * time.Minute}
	})
}

func TestStartWithCustomDuration(t *testing.T) {
	api := &fakeAPI{}
	tm := open(t, api)
	waitFor(t, tm, "No session running")
	tm.Type("s")
	tm.Type("Read")
	press(tm, tea.KeyEnter)
	waitFor(t, tm, "custom")
	tm.Type("3")
	tm.Type("40m")
	press(tm, tea.KeyEnter)
	api.eventually(t, "Start not called with 40m", func() bool {
		return len(api.starts) == 1 && api.starts[0].d == 40*time.Minute
	})
}

func TestTimeUpPromptsForReflection(t *testing.T) {
	sess := &wire.Session{ID: 7, Kind: "focus", Goal: "Write", PlannedSeconds: 1500,
		StartedAt: now.Unix() - 1500, EndsAt: now.Unix()}
	api := &fakeAPI{status: wire.Status{Session: sess, TimeUp: true}}
	tm := open(t, api)
	waitFor(t, tm, "What did you accomplish?")
	tm.Type("wrote the intro")
	press(tm, tea.KeyTab)
	press(tm, tea.KeyEnter)
	api.eventually(t, "Finish not called with the reflection and break off", func() bool {
		if len(api.finishes) != 1 {
			return false
		}
		f := api.finishes[0]
		return f.id == 7 && f.reflection == "wrote the intro" && f.brk != nil && !*f.brk
	})
}

func TestAbandonNeedsConfirmation(t *testing.T) {
	sess := &wire.Session{ID: 3, Kind: "focus", Goal: "x", PlannedSeconds: 1500, StartedAt: now.Unix(), EndsAt: now.Unix() + 1500}
	api := &fakeAPI{status: wire.Status{Session: sess}}
	tm := open(t, api)
	waitFor(t, tm, "f finish")
	tm.Type("a")
	waitFor(t, tm, "Abandon this session?")
	tm.Type("n")
	tm.Type("a")
	tm.Type("y")
	api.eventually(t, "Abandon not called once for session 3", func() bool {
		return len(api.abandons) == 1 && api.abandons[0] == 3
	})
}

func TestRunningViewShowsCountdown(t *testing.T) {
	sess := &wire.Session{ID: 1, Kind: "focus", Goal: "Ship it", PlannedSeconds: 1500, StartedAt: now.Unix() - 746, EndsAt: now.Unix() + 754}
	tm := open(t, &fakeAPI{status: wire.Status{Session: sess}})
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("12:34")) && bytes.Contains(b, []byte("Ship it"))
	}, teatest.WithDuration(3*time.Second))
}

func TestFinishScreenClosesWhenSessionChanges(t *testing.T) {
	sess := &wire.Session{ID: 5, Kind: "focus", Goal: "Write", PlannedSeconds: 1500,
		StartedAt: now.Unix() - 1500, EndsAt: now.Unix()}
	api := &fakeAPI{status: wire.Status{Session: sess, TimeUp: true}}
	tm := open(t, api)
	waitFor(t, tm, "What did you accomplish?")

	// Finished elsewhere; the automatic break is now running.
	brk := &wire.Session{ID: 6, Kind: "break", Goal: "Break", PlannedSeconds: 300, StartedAt: now.Unix(), EndsAt: now.Unix() + 300}
	api.mu.Lock()
	api.status = wire.Status{Session: brk}
	api.mu.Unlock()
	tm.Send(eventMsg{Type: "session.finished"})
	waitFor(t, tm, "end break")

	tm.Type("typed late")
	press(tm, tea.KeyEnter)
	time.Sleep(200 * time.Millisecond)
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.finishes) != 0 {
		t.Fatalf("finish sent for the wrong session: %+v", api.finishes)
	}
}
