// Package clock abstracts time so the engine can be tested without waiting.
package clock

import "time"

// Clock is the source of time for the engine.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer is a pending AfterFunc call.
type Timer interface {
	Stop() bool
}

// Real is the wall clock.
type Real struct{}

func (Real) Now() time.Time { return time.Now() }

func (Real) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }
