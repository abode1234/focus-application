package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/abode1234/focus-cli/internal/wire"
)

func TestSplitGoal(t *testing.T) {
	cases := []struct {
		args []string
		goal string
		d    time.Duration
	}{
		{[]string{"Write the design", "25m"}, "Write the design", 25 * time.Minute},
		{[]string{"Inbox", "zero"}, "Inbox zero", 0},
		{[]string{"Review", "PRs", "50"}, "Review PRs", 50 * time.Minute},
	}
	for _, c := range cases {
		goal, d, err := splitGoal(c.args)
		if err != nil || goal != c.goal || d != c.d {
			t.Errorf("splitGoal(%q) = %q, %v, %v", c.args, goal, d, err)
		}
	}
}

func TestPrintStatus(t *testing.T) {
	var b bytes.Buffer
	printStatus(&b, wire.Status{})
	if !strings.Contains(b.String(), "No session") {
		t.Errorf("idle: %q", b.String())
	}
	b.Reset()
	s := &wire.Session{Goal: "Write", Kind: "focus", PlannedSeconds: 1500, StartedAt: 0, EndsAt: 1500}
	printStatus(&b, wire.Status{Session: s, RemainingSeconds: 754})
	if !strings.Contains(b.String(), "12:34 remaining of 25m") {
		t.Errorf("running: %q", b.String())
	}
	b.Reset()
	printStatus(&b, wire.Status{Session: s, TimeUp: true})
	if !strings.Contains(b.String(), "Time's up: Write") {
		t.Errorf("time up: %q", b.String())
	}
}

func TestStartRejectsDurationAsOnlyArgument(t *testing.T) {
	if _, _, err := splitGoal([]string{"25m"}); err == nil {
		t.Fatal(`"focus start 25m" should ask for a goal, not use "25m" as one`)
	}
}
