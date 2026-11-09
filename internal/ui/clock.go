package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// Clock tells the time and schedules timer messages. Tests substitute a
// manual clock.
type Clock interface {
	Now() time.Time
	// After returns a command that delivers msg after d.
	After(d time.Duration, msg tea.Msg) tea.Cmd
}

// SystemClock is the real Clock.
type SystemClock struct{}

// Now returns the current time.
func (SystemClock) Now() time.Time { return time.Now() }

// After returns a command that delivers msg after d.
func (SystemClock) After(d time.Duration, msg tea.Msg) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return msg })
}
