// Package engine owns session state: the state machine, the time-up timer
// and the business rules. It knows nothing about HTTP or SQL.
package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/abode1234/focus-cli/internal/clock"
	"github.com/abode1234/focus-cli/internal/model"
)

// Limits on session input.
const (
	MaxDuration   = 24 * time.Hour
	MaxGoal       = 200
	MaxReflection = 2000
)

// Repository is the persistence the engine needs.
type Repository interface {
	Insert(ctx context.Context, s model.Session) (model.Session, error)
	Get(ctx context.Context, id int64) (model.Session, error)
	Running(ctx context.Context) (*model.Session, error)
	Complete(ctx context.Context, id int64, endedAt time.Time, reflection string) (model.Session, error)
	Abandon(ctx context.Context, id int64, endedAt time.Time) (model.Session, error)
	List(ctx context.Context, from, to time.Time) ([]model.Session, error)
}

// Event types broadcast to subscribers.
const (
	EventStarted   = "session.started"
	EventTimeUp    = "session.time_up"
	EventFinished  = "session.finished"
	EventAbandoned = "session.abandoned"
)

// Event is a state change.
type Event struct {
	Type    string        `json:"type"`
	Session model.Session `json:"session"`
}

// Options configures the engine.
type Options struct {
	// AutoBreak starts a break after a focus session is finished, unless the
	// caller says otherwise.
	AutoBreak bool
	// BreakDuration is the length of automatic breaks.
	BreakDuration time.Duration
	// Location is used to compute day and week boundaries for stats.
	Location *time.Location
	// OnTimeUp is called (in its own goroutine) when a session's time elapses.
	OnTimeUp func(model.Session)
	Logger   *slog.Logger
}

// Engine is safe for concurrent use.
type Engine struct {
	repo  Repository
	clock clock.Clock
	opts  Options

	mu     sync.Mutex // serialises transitions and guards timer
	timer  clock.Timer
	timeID int64 // session the timer belongs to

	subMu sync.Mutex
	subs  map[chan Event]struct{}
}

// New creates an engine. Call Recover once before serving requests.
func New(repo Repository, clk clock.Clock, opts Options) *Engine {
	if opts.Location == nil {
		opts.Location = time.Local
	}
	if opts.BreakDuration <= 0 {
		opts.BreakDuration = 5 * time.Minute
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Engine{repo: repo, clock: clk, opts: opts, subs: map[chan Event]struct{}{}}
}

// Recover rebuilds timer state from the database after a (re)start. A break
// whose time already elapsed is completed; a focus session whose time elapsed
// stays running and fires time-up immediately so the user is asked to reflect.
func (e *Engine) Recover(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	running, err := e.repo.Running(ctx)
	if err != nil || running == nil {
		return err
	}
	e.opts.Logger.Info("recovered running session", "id", running.ID, "kind", running.Kind,
		"remaining", running.Remaining(e.clock.Now()))
	e.schedule(*running)
	return nil
}

// Status is a snapshot of the current state.
type Status struct {
	Now       time.Time      `json:"now"`
	Session   *model.Session `json:"session"`
	Remaining time.Duration  `json:"-"`
	TimeUp    bool           `json:"time_up"`
}

// Status returns the running session, if any, and its remaining time.
func (e *Engine) Status(ctx context.Context) (Status, error) {
	now := e.clock.Now()
	running, err := e.repo.Running(ctx)
	if err != nil {
		return Status{}, err
	}
	st := Status{Now: now, Session: running}
	if running != nil {
		st.Remaining = running.Remaining(now)
		st.TimeUp = st.Remaining == 0
	}
	return st, nil
}

// Start begins a focus session.
func (e *Engine) Start(ctx context.Context, goal string, planned time.Duration) (model.Session, error) {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return model.Session{}, fmt.Errorf("%w: goal is empty", model.ErrInvalid)
	}
	if len([]rune(goal)) > MaxGoal {
		return model.Session{}, fmt.Errorf("%w: goal is longer than %d characters", model.ErrInvalid, MaxGoal)
	}
	return e.start(ctx, model.KindFocus, goal, planned)
}

// StartBreak begins a break. A zero duration uses the configured default.
func (e *Engine) StartBreak(ctx context.Context, planned time.Duration) (model.Session, error) {
	if planned == 0 {
		planned = e.opts.BreakDuration
	}
	return e.start(ctx, model.KindBreak, "Break", planned)
}

func (e *Engine) start(ctx context.Context, kind model.Kind, goal string, planned time.Duration) (model.Session, error) {
	if err := validDuration(planned); err != nil {
		return model.Session{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.startLocked(ctx, kind, goal, planned)
}

func (e *Engine) startLocked(ctx context.Context, kind model.Kind, goal string, planned time.Duration) (model.Session, error) {
	if kind == model.KindFocus {
		// Starting to focus ends a break early; a running focus session still conflicts.
		running, err := e.repo.Running(ctx)
		if err != nil {
			return model.Session{}, err
		}
		if running != nil && running.Kind == model.KindBreak {
			done, err := e.repo.Complete(ctx, running.ID, e.clock.Now(), "")
			if err != nil {
				return model.Session{}, err
			}
			e.cancelTimer(running.ID)
			e.publish(Event{Type: EventFinished, Session: done})
		}
	}
	sess, err := e.repo.Insert(ctx, model.Session{
		Kind:           kind,
		Goal:           goal,
		PlannedSeconds: int64(planned / time.Second),
		StartedAt:      e.clock.Now().Truncate(time.Second),
	})
	if err != nil {
		return model.Session{}, err
	}
	e.schedule(sess)
	e.publish(Event{Type: EventStarted, Session: sess})
	return sess, nil
}

func validDuration(d time.Duration) error {
	if d < time.Second {
		return fmt.Errorf("%w: duration must be at least 1s", model.ErrInvalid)
	}
	if d > MaxDuration {
		return fmt.Errorf("%w: duration must be at most %s", model.ErrInvalid, MaxDuration)
	}
	return nil
}

// FinishResult is the outcome of Finish.
type FinishResult struct {
	Session model.Session  `json:"session"`
	Break   *model.Session `json:"break,omitempty"`
}

// Finish completes a running session with a reflection. For a focus session,
// startBreak (nil means "use the AutoBreak option") starts a break right after.
func (e *Engine) Finish(ctx context.Context, id int64, reflection string, startBreak *bool) (FinishResult, error) {
	reflection = strings.TrimSpace(reflection)
	if len([]rune(reflection)) > MaxReflection {
		return FinishResult{}, fmt.Errorf("%w: reflection is longer than %d characters", model.ErrInvalid, MaxReflection)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	sess, err := e.repo.Get(ctx, id)
	if err != nil {
		return FinishResult{}, err
	}
	if sess.Kind == model.KindBreak {
		reflection = ""
	}
	done, err := e.repo.Complete(ctx, id, e.clock.Now(), reflection)
	if err != nil {
		return FinishResult{}, err
	}
	e.cancelTimer(id)
	e.publish(Event{Type: EventFinished, Session: done})

	res := FinishResult{Session: done}
	wantBreak := e.opts.AutoBreak
	if startBreak != nil {
		wantBreak = *startBreak
	}
	if done.Kind == model.KindFocus && wantBreak {
		br, err := e.startLocked(ctx, model.KindBreak, "Break", e.opts.BreakDuration)
		if err != nil {
			e.opts.Logger.Error("start automatic break", "err", err)
		} else {
			res.Break = &br
		}
	}
	return res, nil
}

// Abandon ends a running session without counting it.
func (e *Engine) Abandon(ctx context.Context, id int64) (model.Session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	sess, err := e.repo.Abandon(ctx, id, e.clock.Now())
	if err != nil {
		return model.Session{}, err
	}
	e.cancelTimer(id)
	e.publish(Event{Type: EventAbandoned, Session: sess})
	return sess, nil
}

// History lists sessions started in [from, to).
func (e *Engine) History(ctx context.Context, from, to time.Time) ([]model.Session, error) {
	return e.repo.List(ctx, from, to)
}

// schedule arms the time-up timer for sess. Callers hold e.mu.
func (e *Engine) schedule(sess model.Session) {
	if e.timer != nil {
		e.timer.Stop()
	}
	id := sess.ID
	e.timeID = id
	e.timer = e.clock.AfterFunc(sess.Remaining(e.clock.Now()), func() { e.timeUp(id) })
}

// cancelTimer stops the timer if it belongs to id. Callers hold e.mu.
func (e *Engine) cancelTimer(id int64) {
	if e.timer != nil && e.timeID == id {
		e.timer.Stop()
		e.timer = nil
		e.timeID = 0
	}
}

// CheckTimeUp fires a pending time-up whose moment has passed on the wall
// clock. Go timers run on the monotonic clock, which stops during suspend,
// so the daemon calls this periodically to catch up after a wake.
func (e *Engine) CheckTimeUp() {
	e.mu.Lock()
	id := e.timeID
	if e.timer == nil {
		e.mu.Unlock()
		return
	}
	sess, err := e.repo.Get(context.Background(), id)
	if err != nil || sess.Remaining(e.clock.Now()) > 0 {
		e.mu.Unlock()
		return
	}
	e.timer.Stop()
	e.mu.Unlock()
	e.timeUp(id)
}

func (e *Engine) timeUp(id int64) {
	ctx := context.Background()
	e.mu.Lock()
	if e.timeID != id {
		e.mu.Unlock()
		return // already fired, or the session ended
	}
	e.timer = nil
	e.timeID = 0
	sess, err := e.repo.Get(ctx, id)
	if err != nil || sess.Status != model.StatusRunning {
		e.mu.Unlock()
		return
	}
	e.publish(Event{Type: EventTimeUp, Session: sess})
	if sess.Kind == model.KindBreak {
		// Breaks need no reflection; they end on their own.
		done, err := e.repo.Complete(ctx, id, sess.EndsAt(), "")
		if err != nil {
			e.opts.Logger.Error("complete break", "id", id, "err", err)
		} else {
			e.publish(Event{Type: EventFinished, Session: done})
		}
	}
	e.mu.Unlock()

	if e.opts.OnTimeUp != nil {
		go e.opts.OnTimeUp(sess)
	}
}

// Subscribe returns a channel of events and a function to stop receiving.
// Slow subscribers miss events rather than block the engine.
func (e *Engine) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 16)
	e.subMu.Lock()
	e.subs[ch] = struct{}{}
	e.subMu.Unlock()
	return ch, func() {
		e.subMu.Lock()
		if _, ok := e.subs[ch]; ok {
			delete(e.subs, ch)
			close(ch)
		}
		e.subMu.Unlock()
	}
}

func (e *Engine) publish(ev Event) {
	e.subMu.Lock()
	defer e.subMu.Unlock()
	for ch := range e.subs {
		select {
		case ch <- ev:
		default:
			e.opts.Logger.Warn("dropping event for slow subscriber", "type", ev.Type)
		}
	}
}
