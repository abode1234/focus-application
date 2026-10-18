// Package model holds the types shared by the engine, the store and the
// clients. It contains no behaviour.
package model

import (
	"errors"
	"time"
)

// Status is the lifecycle state of a session.
type Status string

const (
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusAbandoned Status = "abandoned"
)

// Kind distinguishes focus sessions from breaks.
type Kind string

const (
	KindFocus Kind = "focus"
	KindBreak Kind = "break"
)

// Session is one focus session or break.
type Session struct {
	ID             int64     `json:"id"`
	Kind           Kind      `json:"kind"`
	Goal           string    `json:"goal"`
	PlannedSeconds int64     `json:"planned_seconds"`
	StartedAt      time.Time `json:"started_at"`
	EndedAt        time.Time `json:"ended_at,omitzero"`
	Status         Status    `json:"status"`
	Reflection     string    `json:"reflection,omitempty"`
}

// EndsAt is when the planned duration elapses.
func (s Session) EndsAt() time.Time {
	return s.StartedAt.Add(time.Duration(s.PlannedSeconds) * time.Second)
}

// Remaining is the time left at now, never negative.
func (s Session) Remaining(now time.Time) time.Duration {
	return max(s.EndsAt().Sub(now), 0)
}

// Errors returned by the engine and the store.
var (
	ErrConflict   = errors.New("a session is already running")
	ErrNotRunning = errors.New("session is not running")
	ErrNotFound   = errors.New("session not found")
	ErrInvalid    = errors.New("invalid input")
)
