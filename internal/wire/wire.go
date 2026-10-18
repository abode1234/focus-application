// Package wire defines the JSON exchanged between focusd and its clients.
// Times are Unix seconds so shell tools and status bars can use them directly.
package wire

import (
	"time"

	"github.com/abode1234/focus-cli/internal/model"
)

// Session as sent over the API.
type Session struct {
	ID             int64        `json:"id"`
	Kind           model.Kind   `json:"kind"`
	Goal           string       `json:"goal"`
	PlannedSeconds int64        `json:"planned_seconds"`
	StartedAt      int64        `json:"started_at"`
	EndsAt         int64        `json:"ends_at"`
	EndedAt        *int64       `json:"ended_at,omitempty"`
	Status         model.Status `json:"status"`
	Reflection     string       `json:"reflection,omitempty"`
}

// FromModel converts a stored session.
func FromModel(s model.Session) Session {
	out := Session{
		ID:             s.ID,
		Kind:           s.Kind,
		Goal:           s.Goal,
		PlannedSeconds: s.PlannedSeconds,
		StartedAt:      s.StartedAt.Unix(),
		EndsAt:         s.EndsAt().Unix(),
		Status:         s.Status,
		Reflection:     s.Reflection,
	}
	if !s.EndedAt.IsZero() {
		e := s.EndedAt.Unix()
		out.EndedAt = &e
	}
	return out
}

// Ptr converts an optional session.
func Ptr(s *model.Session) *Session {
	if s == nil {
		return nil
	}
	w := FromModel(*s)
	return &w
}

// Ends returns EndsAt as a time.
func (s Session) Ends() time.Time { return time.Unix(s.EndsAt, 0) }

// Started returns StartedAt as a time.
func (s Session) Started() time.Time { return time.Unix(s.StartedAt, 0) }

// Planned returns the planned duration.
func (s Session) Planned() time.Duration { return time.Duration(s.PlannedSeconds) * time.Second }

// Remaining is the time left at now, never negative. Clients count down
// locally from EndsAt instead of polling.
func (s Session) Remaining(now time.Time) time.Duration { return max(s.Ends().Sub(now), 0) }

// IsBreak reports whether this is a break.
func (s Session) IsBreak() bool { return s.Kind == model.KindBreak }

// Status is GET /v1/status.
type Status struct {
	Now              int64    `json:"now"`
	Session          *Session `json:"session"`
	RemainingSeconds int64    `json:"remaining_seconds"`
	TimeUp           bool     `json:"time_up"`
}

// StartRequest is POST /v1/sessions.
type StartRequest struct {
	Goal           string `json:"goal"`
	PlannedSeconds int64  `json:"planned_seconds"`
}

// BreakRequest is POST /v1/breaks. Zero uses the configured break length.
type BreakRequest struct {
	PlannedSeconds int64 `json:"planned_seconds"`
}

// FinishRequest is POST /v1/sessions/{id}/finish. StartBreak nil follows
// the daemon's auto_break setting.
type FinishRequest struct {
	Reflection string `json:"reflection"`
	StartBreak *bool  `json:"start_break,omitempty"`
}

// FinishResponse is the result of finishing.
type FinishResponse struct {
	Session Session  `json:"session"`
	Break   *Session `json:"break,omitempty"`
}

// Sessions is GET /v1/sessions.
type Sessions struct {
	Sessions []Session `json:"sessions"`
}

// Day is one day in Stats.
type Day struct {
	Date         string `json:"date"`
	Completed    int    `json:"completed"`
	Abandoned    int    `json:"abandoned"`
	FocusSeconds int64  `json:"focus_seconds"`
}

// Stats is GET /v1/stats.
type Stats struct {
	Range        string `json:"range"`
	From         int64  `json:"from"`
	To           int64  `json:"to"`
	Completed    int    `json:"completed"`
	Abandoned    int    `json:"abandoned"`
	FocusSeconds int64  `json:"focus_seconds"`
	Days         []Day  `json:"days"`
}

// Config is GET /v1/config: the settings clients need for their UI.
type Config struct {
	DefaultSeconds int64   `json:"default_seconds"`
	PresetSeconds  []int64 `json:"preset_seconds"`
	BreakSeconds   int64   `json:"break_seconds"`
	AutoBreak      bool    `json:"auto_break"`
}

// Event is one SSE message from GET /v1/events.
type Event struct {
	Type    string   `json:"type"`
	Session *Session `json:"session,omitempty"`
}

// Error is the body of every non-2xx response.
type Error struct {
	Error string `json:"error"`
}
