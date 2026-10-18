// Package durfmt parses and formats session durations for humans.
package durfmt

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Parse accepts Go durations ("25m", "1h30m") and bare numbers, which are
// read as minutes ("25").
func Parse(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if n, err := strconv.Atoi(s); err == nil {
		if n <= 0 {
			return 0, fmt.Errorf("duration must be positive: %q", s)
		}
		return time.Duration(n) * time.Minute, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q (try 25m or 1h30m)", s)
	}
	if d < time.Second {
		return 0, fmt.Errorf("duration must be at least 1s: %q", s)
	}
	return d, nil
}

// Clock formats d as MM:SS, or H:MM:SS when an hour or longer.
func Clock(d time.Duration) string {
	d = max(d, 0).Round(time.Second)
	h := int(d / time.Hour)
	m := int(d % time.Hour / time.Minute)
	s := int(d % time.Minute / time.Second)
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

// Human formats d compactly: "25m", "1h30m", "45s".
func Human(d time.Duration) string {
	d = max(d, 0).Round(time.Second)
	h := int(d / time.Hour)
	m := int(d % time.Hour / time.Minute)
	s := int(d % time.Minute / time.Second)
	var b strings.Builder
	if h > 0 {
		fmt.Fprintf(&b, "%dh", h)
	}
	if m > 0 {
		fmt.Fprintf(&b, "%dm", m)
	}
	if s > 0 || b.Len() == 0 {
		fmt.Fprintf(&b, "%ds", s)
	}
	return b.String()
}
