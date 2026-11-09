package ui

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// View renders the screen: a header, the list, the status line and the
// footer, with a pop-up on top.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "kodipilot"
	return v
}

func (m *Model) render() string {
	w, h := m.width, m.height
	if w <= 0 || h <= 0 {
		return ""
	}
	lines := make([]string, 0, h)
	lines = append(lines, m.header(w))
	lines = append(lines, m.body(w, m.listHeight())...)
	lines = append(lines, m.statusLine(w), m.footer(w))
	screen := strings.Join(lines[:min(h, len(lines))], "\n")

	popup := m.popup(w, h)
	if popup == "" {
		return screen
	}
	x, y := (w-lipgloss.Width(popup))/2, (h-lipgloss.Height(popup))/2
	canvas := lipgloss.NewCanvas(w, h)
	canvas.Compose(lipgloss.NewCompositor(lipgloss.NewLayer(screen), lipgloss.NewLayer(popup).X(x).Y(y)))
	return canvas.Render()
}

func (m *Model) header(w int) string {
	t := m.theme
	var crumbs []string
	var right string
	switch {
	case m.view == playerView:
		crumbs = []string{"Player", "Queue"}
		right = m.queueSummary()
	case len(m.levels) == 0:
		crumbs = []string{"kodipilot"}
	default:
		crumbs = []string{"Library"}
		for _, l := range m.levels {
			crumbs = append(crumbs, l.title)
		}
		if l := m.current(); l.len() > 0 {
			right = fmt.Sprintf("%d/%d", l.list.index+1, l.len())
		}
	}
	left := []span{{" ", t.plain}}
	for i, crumb := range crumbs {
		if i == len(crumbs)-1 {
			left = append(left, span{crumb, t.strong})
		} else {
			left = append(left, span{crumb, t.dim}, span{" ▸ ", t.dim})
		}
	}
	if right == "" {
		return line(w, t.plain, left, nil)
	}
	return line(w, t.plain, left, []span{{right + " ", t.dim}})
}

// queueSummary is the header's right side in the player view: position,
// repeat and shuffle.
func (m *Model) queueSummary() string {
	s := m.status
	if s == nil {
		return ""
	}
	parts := []string{}
	if s.Position >= 0 && s.Count > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d", s.Position+1, s.Count))
	}
	parts = append(parts, "repeat "+cmp.Or(s.Repeat, "off"))
	shuffle := "shuffle off"
	if s.Shuffled {
		shuffle = "shuffle on"
	}
	return strings.Join(append(parts, shuffle), " · ")
}

// body renders the list area: exactly height lines.
func (m *Model) body(w, height int) []string {
	t := m.theme
	rows := make([]string, 0, height)
	note := func(text string, style lipgloss.Style) {
		if len(rows) < height {
			rows = append(rows, line(w, t.plain, []span{{"  " + text, style}}, nil))
		}
	}
	switch l := m.current(); {
	case m.view == playerView && len(m.queue) == 0:
		note("queue is empty — pick something in the library (Tab)", t.dim)
	case m.view == playerView:
		c := m.queueList
		for i := c.top; i < min(len(m.queue), c.top+height); i++ {
			rows = append(rows, m.queueRow(w, i, i == c.index))
		}
	case l == nil:
		note("waiting for Kodi…", t.dim)
		if m.waitErr != nil {
			note(errorText(m.waitErr), t.err)
		}
	case l.len() == 0:
		note("(empty)", t.dim)
	default:
		for i := l.list.top; i < min(l.len(), l.list.top+height); i++ {
			rows = append(rows, m.libraryRow(w, l, i, i == l.list.index))
		}
	}
	for len(rows) < height {
		rows = append(rows, "")
	}
	return rows
}

// rowBase returns the base style and the left gutter of a list row.
func (m *Model) rowBase(selected bool) (lipgloss.Style, span) {
	if selected {
		return m.theme.selected, span{"▌ ", m.theme.bar}
	}
	return m.theme.plain, span{"  ", m.theme.plain}
}

func (m *Model) libraryRow(w int, l *level, i int, selected bool) string {
	t := m.theme
	base, gutter := m.rowBase(selected)
	switch l.kind {
	case artistsLevel:
		artist := l.artists[i]
		count := strconv.Itoa(len(m.lib.Albums(artist.ID)))
		return line(w, base, []span{gutter, {artist.Name, t.plain}}, []span{{count + " ", t.dim}})
	case albumsLevel:
		album := l.albums[i]
		year := "    "
		if album.Year > 0 {
			year = fmt.Sprintf("%4d", album.Year)
		}
		return line(w, base, []span{gutter, {year + "  ", t.dim}, {album.Title, t.plain}}, nil)
	default:
		song := l.songs[i]
		number := "   "
		if song.Track > 0 {
			number = fmt.Sprintf("%02d ", song.Track)
		}
		left := []span{gutter, {number + " ", t.dim}, {song.Title, t.plain}}
		if song.DisplayArtist != "" && song.DisplayArtist != l.album.DisplayArtist {
			left = append(left, span{"  · " + song.DisplayArtist, t.dim})
		}
		duration := formatDuration(time.Duration(song.Duration) * time.Second)
		return line(w, base, left, []span{{duration + " ", t.dim}})
	}
}

func (m *Model) queueRow(w, i int, selected bool) string {
	t := m.theme
	base, gutter := m.rowBase(selected)
	item := m.queue[i]
	mark, title := span{"  ", t.plain}, t.plain
	if i == m.playingPosition() {
		mark, title = span{"▶ ", t.accent}, t.strong
		if m.status.Paused {
			mark.text = "⏸ "
		}
	}
	digits := len(strconv.Itoa(len(m.queue)))
	left := []span{gutter, mark, {fmt.Sprintf("%*d  ", digits, i+1), t.dim}, {item.Title, title}}
	if item.Artist != "" {
		left = append(left, span{"  · " + item.Artist, t.dim})
	}
	return line(w, base, left, []span{{formatDuration(item.Duration) + " ", t.dim}})
}

func (m *Model) statusLine(w int) string {
	t := m.theme
	s := m.status
	if s == nil {
		return line(w, t.plain, []span{{" ■ stopped", t.dim}}, nil)
	}
	icon := "▶"
	if s.Paused {
		icon = "⏸"
	}
	left := []span{{fmt.Sprintf(" %s %s/%s", icon, formatDuration(s.Elapsed), formatDuration(s.Total)), t.accent}}
	name := strings.Join(nonEmpty(s.Artist, s.Title), " – ")
	if name != "" {
		left = append(left, span{"  " + name, t.accent})
	}
	if s.Position >= 0 && s.Count > 0 {
		left = append(left, span{fmt.Sprintf("  [%d/%d]", s.Position+1, s.Count), t.dim})
	}
	return line(w, t.plain, left, nil)
}

func (m *Model) footer(w int) string {
	t := m.theme
	if m.flash != "" && m.clock.Now().Before(m.flashUntil) {
		return line(w, t.plain, []span{{" " + m.flash, t.err}}, nil)
	}
	return line(w, t.plain, []span{{" " + footerHint, t.dim}}, nil)
}

// popup returns the help or the volume pop-up, or "" for none.
func (m *Model) popup(w, h int) string {
	switch {
	case m.helpOpen: // help covers the volume pop-up
		return m.helpBox(w, h)
	case m.volume != nil && m.clock.Now().Before(m.volumeUntil):
		return m.volumeBox(w, h)
	}
	return ""
}

// framed puts lines (styled, each at most inner columns wide) in the pop-up
// frame when it fits in w×h, or returns them cut to fit without the frame.
func (m *Model) framed(lines []string, w, h int) string {
	if h >= 3 && w >= 5 {
		if len(lines) > h-2 {
			lines = lines[:h-2]
		}
		return m.theme.popup.Render(strings.Join(lines, "\n"))
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

func (m *Model) helpBox(w, h int) string {
	t := m.theme
	table := keyTable()
	column := keyColumn(table)
	widest := column + 3
	for _, k := range table {
		widest = max(widest, column+3+width(k.action))
	}
	for _, about := range aboutLines() {
		widest = max(widest, width(about))
	}
	inner := min(widest, max(1, w-4))
	if h < 3 || w < 5 {
		inner = w
	}
	lines := []string{line(inner, t.plain, []span{{"Keys", t.strong}}, nil)}
	for _, k := range table {
		lines = append(lines, line(inner, t.plain, []span{{pad(k.keys, column) + "   ", t.accent}, {k.action, t.plain}}, nil))
	}
	lines = append(lines, "")
	for _, about := range aboutLines() {
		lines = append(lines, line(inner, t.plain, []span{{about, t.dim}}, nil))
	}
	return m.framed(lines, w, h)
}

func (m *Model) volumeBox(w, h int) string {
	t := m.theme
	v := *m.volume
	level := max(0, min(100, v.Level))
	cells := max(10, min(30, w-18))
	if h < 3 || w < cells+18 {
		text := fmt.Sprintf(" Volume %d%% ", level)
		if v.Muted {
			text = " Volume muted "
		}
		return line(min(w, width(text)), t.plain, []span{{text, t.strong}}, nil)
	}
	inner := cells + 14 // "Volume ▕" + bar + "▏ 100%"
	if v.Muted {
		return m.framed([]string{line(inner, t.plain, []span{{"Volume", t.strong}, {"  muted", t.dim}}, nil)}, w, h)
	}
	filled := (level*cells + 50) / 100
	bar := []span{
		{"Volume ", t.strong},
		{"▕", t.dim},
		{strings.Repeat("█", filled), t.accent},
		{strings.Repeat("░", cells-filled), t.dim},
		{"▏", t.dim},
		{fmt.Sprintf(" %3d%%", level), t.plain},
	}
	return m.framed([]string{line(inner, t.plain, bar, nil)}, w, h)
}

func nonEmpty(s ...string) []string {
	var out []string
	for _, v := range s {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
