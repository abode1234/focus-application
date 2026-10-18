package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/abode1234/focus-cli/internal/model"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

var t0 = time.Unix(1_700_000_000, 0)

func focus(goal string) model.Session {
	return model.Session{Kind: model.KindFocus, Goal: goal, PlannedSeconds: 1500, StartedAt: t0}
}

func TestOneRunningConstraint(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	first, err := s.Insert(ctx, focus("a"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Insert(ctx, focus("b")); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("second insert: got %v, want ErrConflict", err)
	}
	if _, err := s.Complete(ctx, first.ID, t0.Add(time.Minute), "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Insert(ctx, focus("b")); err != nil {
		t.Fatalf("insert after completion: %v", err)
	}
}

func TestPlannedSecondsCheck(t *testing.T) {
	s := open(t)
	bad := focus("x")
	bad.PlannedSeconds = 0
	if _, err := s.Insert(context.Background(), bad); err == nil {
		t.Fatal("insert with zero duration succeeded")
	}
}

func TestTransitions(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	sess, _ := s.Insert(ctx, focus("a"))

	done, err := s.Complete(ctx, sess.ID, t0.Add(25*time.Minute), "wrote it")
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != model.StatusCompleted || done.Reflection != "wrote it" || done.EndedAt.IsZero() {
		t.Fatalf("unexpected completed session: %+v", done)
	}
	if _, err := s.Complete(ctx, sess.ID, t0, ""); !errors.Is(err, model.ErrNotRunning) {
		t.Fatalf("double complete: got %v", err)
	}
	if _, err := s.Abandon(ctx, sess.ID, t0); !errors.Is(err, model.ErrNotRunning) {
		t.Fatalf("abandon completed: got %v", err)
	}
	if _, err := s.Abandon(ctx, 999, t0); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("abandon missing: got %v", err)
	}
	if r, _ := s.Running(ctx); r != nil {
		t.Fatalf("running = %+v, want nil", r)
	}
}

func TestListRange(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for i := range 3 {
		sess := focus("s")
		sess.StartedAt = t0.Add(time.Duration(i) * time.Hour)
		got, err := s.Insert(ctx, sess)
		if err != nil {
			t.Fatal(err)
		}
		s.Complete(ctx, got.ID, sess.EndsAt(), "")
	}
	list, err := s.List(ctx, t0.Add(30*time.Minute), t0.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d sessions, want 2", len(list))
	}
}

func TestReopenFileKeepsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "focus.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Insert(context.Background(), focus("persist")); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, err := s.Running(context.Background())
	if err != nil || r == nil || r.Goal != "persist" {
		t.Fatalf("after reopen: %+v, %v", r, err)
	}
}
