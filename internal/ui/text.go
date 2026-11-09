package ui

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const ellipsis = "…"

// clean replaces control characters and line separators, which would break
// the layout, with spaces.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return ' '
		}
		return r
	}, s)
}

// width returns the display width of plain text.
func width(s string) int { return ansi.StringWidth(s) }

// truncate cuts plain text to at most w columns, ending with an ellipsis when
// cut.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, ellipsis)
}

// pad truncates plain text and pads it with spaces to exactly w columns.
func pad(s string, w int) string {
	s = truncate(s, w)
	return s + strings.Repeat(" ", max(0, w-width(s)))
}

// formatDuration formats d as mm:ss, or as h:mm:ss from an hour up.
func formatDuration(d time.Duration) string {
	s := max(0, int(d/time.Second))
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// span is a piece of a line in one style.
type span struct {
	text  string
	style lipgloss.Style
}

// line lays out spans in exactly w columns on the base style: left from the
// left edge, cut with an ellipsis when too long, and right against the right
// edge, dropped when there is no room for it.
func line(w int, base lipgloss.Style, left, right []span) string {
	if w <= 0 {
		return ""
	}
	rightWidth := 0
	for _, s := range right {
		rightWidth += width(clean(s.text))
	}
	room := w - rightWidth - 2 // two columns between left and right
	if rightWidth == 0 || room < 1 {
		right, rightWidth, room = nil, 0, w
	}

	var b strings.Builder
	used := 0
	for _, s := range left {
		text := clean(s.text)
		if used+width(text) > room {
			text = truncate(text, room-used)
			b.WriteString(s.style.Inherit(base).Render(text))
			used += width(text)
			break
		}
		b.WriteString(s.style.Inherit(base).Render(text))
		used += width(text)
	}
	if gap := w - used - rightWidth; gap > 0 {
		b.WriteString(base.Render(strings.Repeat(" ", gap)))
	}
	for _, s := range right {
		b.WriteString(s.style.Inherit(base).Render(clean(s.text)))
	}
	return b.String()
}
