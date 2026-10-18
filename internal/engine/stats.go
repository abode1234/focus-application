package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/abode1234/focus-cli/internal/model"
)

// Range is a stats window.
type Range string

const (
	RangeDay  Range = "day"
	RangeWeek Range = "week"
)

// DayStats summarises one calendar day.
type DayStats struct {
	Date         string `json:"date"` // YYYY-MM-DD
	Completed    int    `json:"completed"`
	Abandoned    int    `json:"abandoned"`
	FocusSeconds int64  `json:"focus_seconds"`
}

// Stats summarises focus sessions over a range. Breaks are not counted. A
// completed session counts its planned duration, or the time actually spent
// if it was finished early; time after the alarm while writing the
// reflection is not counted.
type Stats struct {
	Range        Range      `json:"range"`
	From         time.Time  `json:"from"`
	To           time.Time  `json:"to"`
	Completed    int        `json:"completed"`
	Abandoned    int        `json:"abandoned"`
	FocusSeconds int64      `json:"focus_seconds"`
	Days         []DayStats `json:"days"`
}

// Bounds returns the [from, to) window for r containing now: today, or the
// week starting on Monday.
func Bounds(r Range, now time.Time, loc *time.Location) (time.Time, time.Time, error) {
	now = now.In(loc)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	switch r {
	case RangeDay:
		return day, day.AddDate(0, 0, 1), nil
	case RangeWeek:
		offset := (int(day.Weekday()) + 6) % 7 // Monday = 0
		start := day.AddDate(0, 0, -offset)
		return start, start.AddDate(0, 0, 7), nil
	}
	return time.Time{}, time.Time{}, fmt.Errorf("%w: range must be day or week", model.ErrInvalid)
}

// Stats computes stats for the range containing the current time.
func (e *Engine) Stats(ctx context.Context, r Range) (Stats, error) {
	loc := e.opts.Location
	from, to, err := Bounds(r, e.clock.Now(), loc)
	if err != nil {
		return Stats{}, err
	}
	sessions, err := e.repo.List(ctx, from, to)
	if err != nil {
		return Stats{}, err
	}
	st := Stats{Range: r, From: from, To: to}
	index := map[string]int{}
	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		key := d.Format(time.DateOnly)
		index[key] = len(st.Days)
		st.Days = append(st.Days, DayStats{Date: key})
	}
	for _, s := range sessions {
		if s.Kind != model.KindFocus {
			continue
		}
		day := &st.Days[index[s.StartedAt.In(loc).Format(time.DateOnly)]]
		switch s.Status {
		case model.StatusCompleted:
			spent := min(s.PlannedSeconds, max(int64(s.EndedAt.Sub(s.StartedAt)/time.Second), 0))
			st.Completed++
			st.FocusSeconds += spent
			day.Completed++
			day.FocusSeconds += spent
		case model.StatusAbandoned:
			st.Abandoned++
			day.Abandoned++
		}
	}
	return st, nil
}
