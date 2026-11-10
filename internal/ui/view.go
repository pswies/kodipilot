package ui

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pswies/kodipilot/internal/kodi"
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
	lines = append(lines, m.middle(w, m.middleHeight())...)
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

// middle renders the rows between the header and the status line: the
// details pane on the left and the list on the right, or only one of them.
func (m *Model) middle(w, height int) []string {
	if m.fullDetails() {
		return m.detailsLines(w, height)
	}
	list, pane := m.layout()
	rows := m.body(list, height)
	if pane == 0 {
		return rows
	}
	details := m.detailsLines(pane, height)
	divider := m.theme.dim.Render("│")
	for i := range rows {
		rows[i] = fill(details[i], pane) + divider + fill(rows[i], list)
	}
	return rows
}

// fill pads a rendered line with spaces to w columns.
func fill(s string, w int) string {
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}

// header renders the view tabs, the library breadcrumb and, on the right,
// the position. On narrow terminals the tabs shrink to "L │ Q" before the
// breadcrumb is cut.
func (m *Model) header(w int) string {
	t := m.theme
	var crumbs []string
	var right string
	if m.view == queueView && m.offline && len(m.queue) > 0 {
		right = "last known queue · Kodi unreachable"
	} else if m.view == queueView {
		right = m.queueSummary()
	} else if l := m.current(); l != nil {
		for _, level := range m.levels {
			crumbs = append(crumbs, level.title)
		}
		if l.len() > 0 {
			right = fmt.Sprintf("%d/%d", l.list.index+1, l.len())
		}
	}
	var trail []span
	for i, crumb := range crumbs {
		if i == len(crumbs)-1 {
			trail = append(trail, span{crumb, t.strong})
		} else {
			trail = append(trail, span{crumb, t.dim}, span{" ▸ ", t.dim})
		}
	}
	if len(trail) > 0 {
		trail = append([]span{{"   ", t.plain}}, trail...)
	}
	var rights []span
	if right != "" {
		rights = []span{{right + " ", t.dim}}
	}

	left := append(m.tabs(false), trail...)
	if spansWidth(left)+spansWidth(rights)+2 > w {
		left = append(m.tabs(true), trail...)
	}
	if len(trail) > 0 && spansWidth(left)+spansWidth(rights)+2 > w {
		rights = nil // the breadcrumb goes before the position
	}
	return line(w, t.plain, left, rights)
}

// tabs returns the view tabs, the active one in the accent: "Library │
// Queue", or "L │ Q" when short.
func (m *Model) tabs(short bool) []span {
	t := m.theme
	names := [2]string{"Library", "Queue"}
	if short {
		names = [2]string{"L", "Q"}
	}
	library, queue := t.strong, t.dim
	if m.view == queueView {
		library, queue = t.dim, t.strong
	}
	return []span{{" ", t.plain}, {names[0], library}, {" │ ", t.dim}, {names[1], queue}}
}

func spansWidth(spans []span) int {
	n := 0
	for _, s := range spans {
		n += width(clean(s.text))
	}
	return n
}

// queueSummary is the header's right side in the queue view: position,
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

// body renders the list area, a column header and the rows: exactly height
// lines.
func (m *Model) body(w, height int) []string {
	t := m.theme
	rows := make([]string, 0, height)
	note := func(text string, style lipgloss.Style) {
		f := flow{room: func(int) int { return max(1, w-4) }}
		f.text(text, []span{{"  ", t.plain}}, style)
		for _, l := range f.lines {
			if len(rows) < height {
				rows = append(rows, line(w, t.plain, l, nil))
			}
		}
	}
	switch l := m.current(); {
	case m.view == queueView && len(m.queue) == 0:
		note("queue is empty — pick something in the library (Tab)", t.dim)
	case m.view == queueView:
		rows = append(rows, m.listHeader(w, nil))
		c := m.queueList
		for i := c.top; i < min(len(m.queue), c.top+height-1); i++ {
			rows = append(rows, m.queueRow(w, i, i == c.index))
		}
	case l == nil && errors.Is(m.waitErr, kodi.ErrUnauthorized):
		note(errorText(m.waitErr), t.err)
	case l == nil:
		note("Waiting for Kodi…", t.dim)
		if m.waitErr != nil {
			note(errorText(m.waitErr)+" Retrying…", t.err)
		}
	case l.len() == 0 && l.kind == artistsLevel:
		note("Kodi's music library has no album artists yet. Scan your music in Kodi, then restart.", t.dim)
	case l.len() == 0 && l.kind == tracksLevel:
		note("No tracks.", t.dim)
	case l.len() == 0:
		note("No albums.", t.dim)
	case l.rows != nil: // tracks of several discs, with a row before each disc
		rows = append(rows, m.listHeader(w, l))
		for _, r := range l.rows[min(l.list.top, len(l.rows)):min(len(l.rows), l.list.top+height-1)] {
			if r < 0 {
				rows = append(rows, line(w, t.plain, []span{{"  Disc " + strconv.Itoa(-r), t.dim}}, nil))
			} else {
				rows = append(rows, m.libraryRow(w, l, r, r == l.list.index))
			}
		}
	default:
		rows = append(rows, m.listHeader(w, l))
		for i := l.list.top; i < min(l.len(), l.list.top+height-1); i++ {
			rows = append(rows, m.libraryRow(w, l, i, i == l.list.index))
		}
	}
	rows = rows[:min(len(rows), height)]
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

// The narrowest lists that show a column; narrower lists drop it.
const (
	countColumnMin  = 20 // artists: Albums
	yearColumnMin   = 22 // albums: Year
	lengthColumnMin = 24 // albums, tracks and the queue: Length
	artistColumnMin = 60 // the queue: Artist
)

// listHeader returns the column header of the library level l, or of the
// queue when l is nil, aligned with the rows.
func (m *Model) listHeader(w int, l *level) string {
	t := m.theme
	length := []span(nil)
	if w >= lengthColumnMin {
		length = []span{{"Length ", t.dim}}
	}
	switch {
	case l == nil:
		title, artist := m.queueColumns(w)
		left := []span{{"    " + fmt.Sprintf("%*s  ", m.queueDigits(), "#") + pad("Title", title), t.dim}}
		if artist > 0 {
			left = append(left, span{"  Artist", t.dim})
		}
		return line(w, t.plain, left, length)
	case l.kind == artistsLevel:
		var count []span
		if w >= countColumnMin {
			count = []span{{"Albums ", t.dim}}
		}
		return line(w, t.plain, []span{{"  Artist", t.dim}}, count)
	case l.kind == albumsLevel:
		year := ""
		if w >= yearColumnMin {
			year = "Year  "
		}
		return line(w, t.plain, []span{{"  " + year + "Album", t.dim}}, length)
	default:
		return line(w, t.plain, []span{{"     #  Title", t.dim}}, length)
	}
}

func (m *Model) libraryRow(w int, l *level, i int, selected bool) string {
	t := m.theme
	base, gutter := m.rowBase(selected)
	var length []span
	if w >= lengthColumnMin {
		length = []span{{" ", t.dim}}
	}
	switch l.kind {
	case artistsLevel:
		artist := l.artists[i]
		var count []span
		if w >= countColumnMin {
			count = []span{{strconv.Itoa(len(m.lib.Albums(artist.ID))) + " ", t.dim}}
		}
		return line(w, base, []span{gutter, {artist.Name, t.plain}}, count)
	case albumsLevel:
		album := l.albums[i]
		left := []span{gutter}
		if w >= yearColumnMin {
			year := "    "
			if album.Year > 0 {
				year = fmt.Sprintf("%4d", album.Year)
			}
			left = append(left, span{year + "  ", t.dim})
		}
		left = append(left, span{album.Title, t.plain})
		var right []span
		if m.status != nil && m.status.AlbumID == album.ID {
			right = append(right, span{"▶ ", t.dim})
		}
		if length != nil && album.Duration > 0 {
			right = append(right, span{formatDuration(time.Duration(album.Duration)*time.Second) + " ", t.dim})
		}
		return line(w, base, left, right)
	default:
		song := l.songs[i]
		number := "  "
		if song.Track > 0 {
			number = fmt.Sprintf("%02d", song.Track)
		}
		mark, title := span{"  ", t.plain}, t.plain
		if m.status != nil && m.status.SongID == song.ID {
			mark, title = span{m.playIcon() + " ", t.accent}, t.strong
		}
		left := []span{gutter, mark, {number + "  ", t.dim}, {song.Title, title}}
		if song.DisplayArtist != "" && song.DisplayArtist != l.album.DisplayArtist {
			left = append(left, span{"  · " + song.DisplayArtist, t.dim})
		}
		if length != nil {
			length[0].text = formatDuration(time.Duration(song.Duration)*time.Second) + " "
		}
		return line(w, base, left, length)
	}
}

// queueDigits is the width of the queue's position numbers.
func (m *Model) queueDigits() int { return len(strconv.Itoa(len(m.queue))) }

// queueColumns returns the widths of the queue's title and artist columns
// (0 when the list is too narrow for the artist).
func (m *Model) queueColumns(w int) (title, artist int) {
	room := w - 4 - m.queueDigits() - 2 // the gutter, the mark, the number
	if w >= lengthColumnMin {
		room -= len("00:00 ") + 2
	}
	if w < artistColumnMin {
		return max(1, room), 0
	}
	artist = min(28, room*2/5)
	return room - artist - 2, artist
}

func (m *Model) queueRow(w, i int, selected bool) string {
	t := m.theme
	base, gutter := m.rowBase(selected)
	item := m.queue[i]
	mark, title := span{"  ", t.plain}, t.plain
	switch {
	case m.offline: // the last known queue, perhaps stale
		title = t.dim
	case i == m.playingPosition():
		mark, title = span{m.playIcon() + " ", t.accent}, t.strong
	}
	titleWidth, artistWidth := m.queueColumns(w)
	left := []span{gutter, mark, {fmt.Sprintf("%*d  ", m.queueDigits(), i+1), t.dim}}
	if artistWidth > 0 {
		left = append(left, span{pad(clean(item.Title), titleWidth) + "  ", title}, span{item.Artist, t.dim})
	} else {
		left = append(left, span{item.Title, title})
	}
	var length []span
	if w >= lengthColumnMin {
		length = []span{{formatDuration(item.Duration) + " ", t.dim}}
	}
	return line(w, base, left, length)
}

// playIcon is ▶ or, when paused, ⏸.
func (m *Model) playIcon() string {
	if m.status != nil && m.status.Paused {
		return "⏸"
	}
	return "▶"
}

// statusLine shows what plays: "▶ 03:12/08:55  Artist – Title · Album
// [2/4]", the album dropped first when narrow.
func (m *Model) statusLine(w int) string {
	t := m.theme
	s := m.status
	switch {
	case m.offline:
		return line(w, t.plain, []span{{" ■ Kodi unreachable, retrying…", t.err}}, nil)
	case s == nil && m.video:
		return line(w, t.plain, []span{{" ■ video playing", t.dim}}, m.indicators())
	case s == nil:
		return line(w, t.plain, []span{{" ■ stopped", t.dim}}, m.indicators())
	}
	compose := func(album bool) []span {
		spans := []span{{fmt.Sprintf(" %s %s/%s", m.playIcon(), formatDuration(s.Elapsed), formatDuration(s.Total)), t.accent}}
		if name := strings.Join(nonEmpty(s.Artist, s.Title), " – "); name != "" {
			spans = append(spans, span{"  " + name, t.accent})
		}
		if album && s.Album != "" {
			spans = append(spans, span{" · " + s.Album, t.dim})
		}
		if s.Position >= 0 && s.Count > 0 {
			spans = append(spans, span{fmt.Sprintf("  [%d/%d]", s.Position+1, s.Count), t.dim})
		}
		return spans
	}
	left, right := compose(true), m.indicators()
	if spansWidth(left)+spansWidth(right)+2 > w {
		left = compose(false)
	}
	return line(w, t.plain, left, right)
}

// indicators are the status line's compact marks for repeat, shuffle and
// mute, when they apply.
func (m *Model) indicators() []span {
	var marks []string
	if s := m.status; s != nil {
		if s.Repeat == "all" || s.Repeat == "one" {
			marks = append(marks, "⟳ "+s.Repeat)
		}
		if s.Shuffled {
			marks = append(marks, "⤮")
		}
	}
	if m.volume != nil && m.volume.Muted {
		marks = append(marks, "muted")
	}
	if len(marks) == 0 {
		return nil
	}
	return []span{{strings.Join(marks, "  ") + " ", m.theme.accent}}
}

// footer shows a flashed message or the key hints of the view. Hints that
// do not fit are dropped whole, from the end.
func (m *Model) footer(w int) string {
	t := m.theme
	if m.flash != "" && m.clock.Now().Before(m.flashUntil) {
		style := t.accent
		if m.flashErr {
			style = t.err
		}
		return line(w, t.plain, []span{{" " + sentence(m.flash), style}}, nil)
	}
	hints := m.hints()
	for len(hints) > 1 && width(" "+strings.Join(hints, "  ")) > w {
		hints = hints[:len(hints)-1]
	}
	return line(w, t.plain, []span{{" " + strings.Join(hints, "  "), t.dim}}, nil)
}

// hints returns the key hints for the footer.
func (m *Model) hints() []string {
	var hints []string
	switch {
	case m.fullDetails():
		hints = []string{"Esc close"}
	case m.view == queueView:
		hints = []string{"? help", "Enter play", "d remove", "J/K move", "c clear", "Tab library", "Q quit"}
	default:
		hints = []string{"? help", "Enter open", "p play", "q add", "Space pause", "Tab queue", "Q quit"}
	}
	if m.detailsOverflow() {
		at := max(len(hints)-1, 1) // before "Q quit"
		hints = slices.Insert(hints, at, "[ ] scroll")
	}
	return hints
}

// popup returns the help, a question (quit, clear the queue) or the volume
// pop-up, in that order of precedence, or "" for none.
func (m *Model) popup(w, h int) string {
	switch {
	case m.helpOpen:
		return m.helpBox(w, h)
	case m.confirmQuit:
		t := m.theme
		lines := []string{
			line(26, t.plain, []span{{"Quit kodipilot?", t.strong}}, []span{{"y / n", t.dim}}),
			line(26, t.plain, []span{{"The music keeps playing.", t.plain}}, nil),
		}
		if w < 30 {
			lines = []string{line(max(1, w-4), t.plain, []span{{"Quit?", t.strong}, {"  y / n", t.dim}}, nil)}
		}
		return m.framed(lines, w, h)
	case m.confirmClear:
		t := m.theme
		question := []span{{"Clear the queue?", t.strong}, {"  y / n", t.dim}}
		return m.framed([]string{line(min(24, max(1, w-4)), t.plain, question, nil)}, w, h)
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
