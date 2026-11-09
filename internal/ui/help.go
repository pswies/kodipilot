package ui

import (
	"fmt"
	"slices"
	"strings"
)

// Author and Homepage are shown in the help and in --help.
const (
	Author   = "Piotr Święs"
	Homepage = "https://github.com/pswies/kodipilot"
)

// intro opens the help.
const intro = "kodipilot lets you steer Kodi's music from any terminal: browse your library, " +
	"line up albums and keep the music going without reaching for the remote. " +
	"I wrote it for my own living room, where the projector plays and the laptop rests on my knees. " +
	"I hope it brings your record collection a little closer, too."

const (
	helpMaxWidth  = 76 // the help box, frame and padding included
	kodiColumnMin = 70 // the least inner width that shows the Kodi column
	keyIndent     = 2  // before the key column
	columnGap     = 2  // between the columns
)

// helpFooter is the last line of the help.
func helpFooter() string { return Author + " · " + strings.TrimPrefix(Homepage, "https://") }

// helpLine is a line of the help: text from the left and, optionally,
// text against the right edge.
type helpLine struct{ left, right []span }

// helpDoc lays out the help for inner columns, styled with t: the title row
// (when titled), the introduction, the key table in sections and the
// footer. Keys are right-aligned in their column and actions wrap within
// theirs; the Kodi equivalents form a dim third column when there is room.
func helpDoc(table []keySection, t theme, inner int, version string, titled bool) []helpLine {
	var lines []helpLine
	if titled {
		if version != "" && version[0] >= '0' && version[0] <= '9' {
			version = "v" + version
		}
		lines = append(lines, helpLine{[]span{{"kodipilot", t.strong}}, []span{{version, t.dim}}}, helpLine{})
	}
	text := flow{room: func(int) int { return max(1, inner) }}
	text.text(intro, nil, t.plain)
	for _, l := range text.lines {
		lines = append(lines, helpLine{left: l})
	}
	lines = append(lines, helpLine{})

	keyWidth, kodiWidth := 0, 0
	for _, section := range table {
		for _, row := range section.rows {
			keyWidth = max(keyWidth, width(row.label))
			kodiWidth = max(kodiWidth, width(row.kodi))
		}
	}
	withKodi := inner >= kodiColumnMin
	actionWidth := inner - keyIndent - keyWidth - columnGap
	if withKodi {
		actionWidth -= kodiWidth + columnGap
	}
	kodiCell := func(s string) []span {
		if !withKodi || s == "" {
			return nil
		}
		return []span{{pad(s, kodiWidth), t.dim}}
	}

	kodiHeading := withKodi // over the first section with Kodi keys
	for _, section := range table {
		// The title, then a rule to the end of the line, with the Kodi
		// column's heading set into the first one.
		title := width(section.title)
		heading := []span{{section.title, t.strong}}
		if kodiHeading && slices.ContainsFunc(section.rows, func(r keyRow) bool { return r.kodi != "" }) {
			column := inner - kodiWidth
			heading = append(heading,
				span{" " + rule(column-title-2) + " ", t.dim},
				span{"in Kodi", t.dim},
				span{" " + rule(inner-column-len("in Kodi")-1), t.dim})
			kodiHeading = false
		} else {
			heading = append(heading, span{" " + rule(inner-title-1), t.dim})
		}
		lines = append(lines, helpLine{left: heading})
		for _, row := range section.rows {
			action := flow{room: func(int) int { return max(1, actionWidth) }}
			action.text(row.action, nil, t.plain)
			for i, spans := range action.lines {
				keys, right := "", []span(nil)
				if i == 0 {
					keys, right = strings.Repeat(" ", keyWidth-width(row.label))+row.label, kodiCell(row.kodi)
				}
				left := []span{{strings.Repeat(" ", keyIndent) + pad(keys, keyWidth+columnGap), t.accent}}
				lines = append(lines, helpLine{append(left, spans...), right})
			}
		}
		lines = append(lines, helpLine{})
	}
	footer := helpFooter()
	return append(lines, helpLine{left: []span{{strings.Repeat(" ", max(0, (inner-width(footer))/2)) + footer, t.dim}}})
}

// helpInner returns the inner width of the help box on this screen.
func (m *Model) helpInner() int {
	if m.height < 3 || m.width < 5 {
		return m.width // no frame
	}
	return min(helpMaxWidth, m.width) - 4
}

// helpDoc returns the help as rendered lines for the help box. The lines
// are kept until the width or the theme changes.
func (m *Model) helpDoc() []string {
	inner := m.helpInner()
	if m.helpCache.lines != nil && m.helpCache.inner == inner {
		return m.helpCache.lines
	}
	var out []string
	for _, l := range helpDoc(m.keys, m.theme, inner, m.opts.Version, true) {
		out = append(out, line(inner, m.theme.plain, l.left, l.right))
	}
	m.helpCache.inner, m.helpCache.lines = inner, out
	return out
}

// helpRows returns how many help lines the box shows at once, leaving a line
// for the scroll hint when the help does not fit.
func (m *Model) helpRows() int {
	rows := m.height - 2 // the frame
	if len(m.helpDoc()) > rows {
		rows-- // the scroll hint
	}
	return max(0, rows)
}

func (m *Model) helpBox(w, h int) string {
	lines, rows := m.helpDoc(), m.helpRows()
	if len(lines) <= rows {
		return m.framed(lines, w, h)
	}
	top := min(m.helpTop, len(lines)-rows)
	shown := slices.Clone(lines[top : top+rows])
	hint := fmt.Sprintf("↑↓ scroll  %d–%d of %d  Esc close", top+1, top+rows, len(lines))
	return m.framed(append(shown, line(m.helpInner(), m.theme.plain, []span{{hint, m.theme.dim}}, nil)), w, h)
}

// KeysHelp returns the introduction, the keys and the footer as plain text,
// for --help.
func KeysHelp() string {
	const inner = helpMaxWidth - 4
	var b strings.Builder
	for _, l := range helpDoc(keyTable(), theme{}, inner, "", false) {
		b.WriteString(strings.TrimRight(line(inner, theme{}.plain, l.left, l.right), " ") + "\n")
	}
	return b.String()
}

// rule returns a horizontal rule n columns long.
func rule(n int) string { return strings.Repeat("─", max(0, n)) }
