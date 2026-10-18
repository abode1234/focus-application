package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abode1234/focus-cli/internal/clock"
	"github.com/abode1234/focus-cli/internal/model"
	"github.com/abode1234/focus-cli/internal/store"
)

var t0 = time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC) // a Wednesday

type harness struct {
	eng   *Engine
	clock *clock.Fake
	repo  *store.Store

	mu      sync.Mutex
	timeUps []model.Session
	done    chan struct{}
}

func newHarness(t *testing.T, opts Options) *harness {
	t.Helper()
	repo, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	h := &harness{clock: clock.NewFake(t0), repo: repo, done: make(chan struct{}, 8)}
	opts.Location = time.UTC
	opts.OnTimeUp = func(s model.Session) {
		h.mu.Lock()
		h.timeUps = append(h.timeUps, s)
		h.mu.Unlock()
		h.done <- struct{}{}
	}
	h.eng = New(repo, h.clock, opts)
	return h
}

func (h *harness) waitTimeUp(t *testing.T) model.Session {
	t.Helper()
	select {
	case <-h.done:
	case <-time.After(2 * time.Second):
		t.Fatal("OnTimeUp not called")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.timeUps[len(h.timeUps)-1]
}

var ctx = context.Background()

func TestStartValidation(t *testing.T) {
	h := newHarness(t, Options{})
	if _, err := h.eng.Start(ctx, "  ", 25*time.Minute); !errors.Is(err, model.ErrInvalid) {
		t.Errorf("empty goal: %v", err)
	}
	if _, err := h.eng.Start(ctx, "x", 0); !errors.Is(err, model.ErrInvalid) {
		t.Errorf("zero duration: %v", err)
	}
	if _, err := h.eng.Start(ctx, "x", 25*time.Hour); !errors.Is(err, model.ErrInvalid) {
		t.Errorf("too long: %v", err)
	}
}

func TestSingleSessionRule(t *testing.T) {
	h := newHarness(t, Options{})
	if _, err := h.eng.Start(ctx, "a", 25*time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := h.eng.Start(ctx, "b", 25*time.Minute); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("second start: %v", err)
	}
	if _, err := h.eng.StartBreak(ctx, 0); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("break during focus: %v", err)
	}
}

func TestFinishTransition(t *testing.T) {
	h := newHarness(t, Options{})
	sess, _ := h.eng.Start(ctx, "write", 25*time.Minute)
	h.clock.Advance(10 * time.Minute)

	res, err := h.eng.Finish(ctx, sess.ID, " wrote the intro ", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Session.Status != model.StatusCompleted || res.Session.Reflection != "wrote the intro" {
		t.Fatalf("finished: %+v", res.Session)
	}
	if res.Break != nil {
		t.Fatal("break started with AutoBreak off")
	}
	if h.clock.Pending() != 0 {
		t.Fatal("timer still armed after finish")
	}
	st, _ := h.eng.Status(ctx)
	if st.Session != nil {
		t.Fatalf("status after finish: %+v", st.Session)
	}
}

func TestDoubleFinish(t *testing.T) {
	h := newHarness(t, Options{})
	sess, _ := h.eng.Start(ctx, "a", time.Minute)
	if _, err := h.eng.Finish(ctx, sess.ID, "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.eng.Finish(ctx, sess.ID, "", nil); !errors.Is(err, model.ErrNotRunning) {
		t.Fatalf("double finish: %v", err)
	}
	if _, err := h.eng.Abandon(ctx, sess.ID); !errors.Is(err, model.ErrNotRunning) {
		t.Fatalf("abandon finished: %v", err)
	}
	if _, err := h.eng.Finish(ctx, 404, "", nil); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("finish missing: %v", err)
	}
}

func TestConcurrentFinishOnlyOneWins(t *testing.T) {
	h := newHarness(t, Options{})
	sess, _ := h.eng.Start(ctx, "a", time.Minute)
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for range 8 {
		wg.Go(func() {
			if _, err := h.eng.Finish(ctx, sess.ID, "", nil); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d finishes succeeded, want 1", wins)
	}
}

func TestAbandon(t *testing.T) {
	h := newHarness(t, Options{})
	sess, _ := h.eng.Start(ctx, "a", time.Minute)
	got, err := h.eng.Abandon(ctx, sess.ID)
	if err != nil || got.Status != model.StatusAbandoned {
		t.Fatalf("abandon: %+v, %v", got, err)
	}
	h.clock.Advance(time.Hour)
	if len(h.timeUps) != 0 {
		t.Fatal("time-up fired for abandoned session")
	}
}

func TestTimeUpKeepsSessionRunning(t *testing.T) {
	h := newHarness(t, Options{})
	events, cancel := h.eng.Subscribe()
	defer cancel()

	sess, _ := h.eng.Start(ctx, "a", 25*time.Minute)
	if ev := <-events; ev.Type != EventStarted {
		t.Fatalf("first event %q", ev.Type)
	}
	h.clock.Advance(24 * time.Minute)
	st, _ := h.eng.Status(ctx)
	if st.TimeUp || st.Remaining != time.Minute {
		t.Fatalf("at 24m: timeUp=%v remaining=%v", st.TimeUp, st.Remaining)
	}

	h.clock.Advance(time.Minute)
	if got := h.waitTimeUp(t); got.ID != sess.ID {
		t.Fatalf("time-up for %d, want %d", got.ID, sess.ID)
	}
	if ev := <-events; ev.Type != EventTimeUp {
		t.Fatalf("event %q, want time_up", ev.Type)
	}
	st, _ = h.eng.Status(ctx)
	if !st.TimeUp || st.Session == nil || st.Session.Status != model.StatusRunning {
		t.Fatalf("after time-up: %+v", st)
	}
}

func TestAutoBreakEndsByItself(t *testing.T) {
	h := newHarness(t, Options{AutoBreak: true, BreakDuration: 5 * time.Minute})
	sess, _ := h.eng.Start(ctx, "a", 25*time.Minute)
	h.clock.Advance(25 * time.Minute)
	h.waitTimeUp(t)

	res, err := h.eng.Finish(ctx, sess.ID, "done", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Break == nil || res.Break.Kind != model.KindBreak || res.Break.PlannedSeconds != 300 {
		t.Fatalf("break: %+v", res.Break)
	}

	h.clock.Advance(5 * time.Minute)
	h.waitTimeUp(t)
	st, _ := h.eng.Status(ctx)
	if st.Session != nil {
		t.Fatalf("break still running: %+v", st.Session)
	}
	br, _ := h.repo.Get(ctx, res.Break.ID)
	if br.Status != model.StatusCompleted || !br.EndedAt.Equal(br.EndsAt()) {
		t.Fatalf("break after time-up: %+v", br)
	}
}

func TestSkipBreak(t *testing.T) {
	h := newHarness(t, Options{AutoBreak: true})
	sess, _ := h.eng.Start(ctx, "a", time.Minute)
	no := false
	res, err := h.eng.Finish(ctx, sess.ID, "", &no)
	if err != nil || res.Break != nil {
		t.Fatalf("finish with break=false: %+v, %v", res, err)
	}
}

func TestRecoverAfterRestart(t *testing.T) {
	h := newHarness(t, Options{})
	sess, _ := h.eng.Start(ctx, "a", 25*time.Minute)

	// Simulate the daemon dying and coming back 30 minutes later.
	restarted := clock.NewFake(t0.Add(30 * time.Minute))
	done := make(chan model.Session, 1)
	eng := New(h.repo, restarted, Options{OnTimeUp: func(s model.Session) { done <- s }})
	if err := eng.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	restarted.Advance(0)
	select {
	case got := <-done:
		if got.ID != sess.ID {
			t.Fatalf("recovered %d, want %d", got.ID, sess.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no time-up after recovery")
	}
	st, _ := eng.Status(ctx)
	if !st.TimeUp {
		t.Fatal("status should report time up")
	}
}

func TestRecoverMidSession(t *testing.T) {
	h := newHarness(t, Options{})
	h.eng.Start(ctx, "a", 25*time.Minute)

	restarted := clock.NewFake(t0.Add(10 * time.Minute))
	fired := make(chan struct{}, 1)
	eng := New(h.repo, restarted, Options{OnTimeUp: func(model.Session) { fired <- struct{}{} }})
	eng.Recover(ctx)
	restarted.Advance(14 * time.Minute)
	select {
	case <-fired:
		t.Fatal("fired early")
	default:
	}
	restarted.Advance(time.Minute)
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("did not fire at 25m")
	}
}

func TestStats(t *testing.T) {
	h := newHarness(t, Options{})
	a, _ := h.eng.Start(ctx, "a", 25*time.Minute)
	h.clock.Advance(40 * time.Minute) // finished late; still counts 25m
	h.eng.Finish(ctx, a.ID, "", nil)
	b, _ := h.eng.Start(ctx, "b", 50*time.Minute)
	h.eng.Abandon(ctx, b.ID)
	br, _ := h.eng.StartBreak(ctx, time.Minute)
	h.eng.Finish(ctx, br.ID, "", nil)

	day, err := h.eng.Stats(ctx, RangeDay)
	if err != nil {
		t.Fatal(err)
	}
	if day.Completed != 1 || day.Abandoned != 1 || day.FocusSeconds != 1500 || len(day.Days) != 1 {
		t.Fatalf("day stats: %+v", day)
	}
	week, _ := h.eng.Stats(ctx, RangeWeek)
	if len(week.Days) != 7 || week.Days[0].Date != "2026-10-12" || week.Days[2].Completed != 1 {
		t.Fatalf("week stats: %+v", week)
	}
	if _, err := h.eng.Stats(ctx, "month"); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("bad range: %v", err)
	}
}

func TestStartFocusEndsBreak(t *testing.T) {
	h := newHarness(t, Options{})
	br, _ := h.eng.StartBreak(ctx, 5*time.Minute)
	h.clock.Advance(time.Minute)
	sess, err := h.eng.Start(ctx, "next", 25*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := h.repo.Get(ctx, br.ID)
	if got.Status != model.StatusCompleted {
		t.Fatalf("break status %q", got.Status)
	}
	// The break's timer must not fire against the new session.
	h.clock.Advance(4 * time.Minute)
	select {
	case <-h.done:
		t.Fatal("stale break timer fired")
	default:
	}
	st, _ := h.eng.Status(ctx)
	if st.Session == nil || st.Session.ID != sess.ID {
		t.Fatalf("status: %+v", st.Session)
	}
}

func TestEarlyFinishCountsElapsedTime(t *testing.T) {
	h := newHarness(t, Options{})
	s, _ := h.eng.Start(ctx, "a", 50*time.Minute)
	h.clock.Advance(10 * time.Minute)
	h.eng.Finish(ctx, s.ID, "", nil)
	st, _ := h.eng.Stats(ctx, RangeDay)
	if st.FocusSeconds != 600 {
		t.Fatalf("focus seconds = %d, want 600 for a 50m session finished after 10m", st.FocusSeconds)
	}
}

func TestTimeUpFiresAfterSuspend(t *testing.T) {
	h := newHarness(t, Options{})
	h.eng.Start(ctx, "a", 25*time.Minute)
	// Suspend: wall time moves on but the monotonic timer does not fire.
	h.clock.Jump(30 * time.Minute)
	h.eng.CheckTimeUp()
	h.waitTimeUp(t)
	h.eng.CheckTimeUp()
	select {
	case <-h.done:
		t.Fatal("time-up fired twice")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestTextLimits(t *testing.T) {
	h := newHarness(t, Options{})
	if _, err := h.eng.Start(ctx, strings.Repeat("g", MaxGoal+1), time.Minute); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("long goal: %v", err)
	}
	s, _ := h.eng.Start(ctx, "a", time.Minute)
	if _, err := h.eng.Finish(ctx, s.ID, strings.Repeat("r", MaxReflection+1), nil); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("long reflection: %v", err)
	}
}
