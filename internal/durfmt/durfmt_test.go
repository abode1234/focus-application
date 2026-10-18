package durfmt

import (
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	cases := map[string]time.Duration{
		"25":    25 * time.Minute,
		"25m":   25 * time.Minute,
		"1h30m": 90 * time.Minute,
		"90s":   90 * time.Second,
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "0", "-5", "abc", "10ms"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", bad)
		}
	}
}

func TestFormat(t *testing.T) {
	if got := Clock(25 * time.Minute); got != "25:00" {
		t.Errorf("Clock = %q", got)
	}
	if got := Clock(time.Hour + 2*time.Second); got != "1:00:02" {
		t.Errorf("Clock = %q", got)
	}
	if got := Human(90 * time.Minute); got != "1h30m" {
		t.Errorf("Human = %q", got)
	}
	if got := Human(0); got != "0s" {
		t.Errorf("Human = %q", got)
	}
}
