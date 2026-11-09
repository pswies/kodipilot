package ui

import (
	"cmp"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Author and Homepage are shown in the help pop-up and in --help.
const (
	Author   = "Piotr Święs"
	Homepage = "https://github.com/pswies/kodipilot"
)

// footerHint is the key hint in the footer.
const footerHint = "? help  Tab view  Enter open  p play  q queue  Space pause  +/- volume  Q quit"

// scope says in which view a section of the key table applies.
type scope int

const (
	everywhere scope = iota
	inLibrary
	inPlayer
)

// keySection is a part of the key table that applies in one scope.
type keySection struct {
	title string
	scope scope
	rows  []keyRow
}

// keyRow is one row of the key table: how help shows the keys, what they do,
// the Kodi equivalent (shown in the README) and the bindings.
type keyRow struct {
	label  string
	action string
	kodi   string
	keys   []binding
}

// binding binds a key, named as Bubble Tea reports it, to a handler.
type binding struct {
	key string
	do  func(*Model) tea.Cmd
}

// bind binds keys to one handler.
func bind(do func(*Model) tea.Cmd, keys ...string) []binding {
	b := make([]binding, len(keys))
	for i, k := range keys {
		b[i] = binding{k, do}
	}
	return b
}

// keyTable is the single key table. It drives key handling, the help pop-up
// and --help; a test checks that the README shows it. Keys follow Kodi's
// default keyboard map where Kodi has an equivalent.
func keyTable() []keySection {
	return []keySection{
		{"Everywhere", everywhere, []keyRow{
			{"↑ ↓ PgUp PgDn Home End", "move", "↑ ↓ PgUp PgDn Home End", slices.Concat(
				bind((*Model).up, "up"), bind((*Model).down, "down"),
				bind((*Model).pageUp, "pgup"), bind((*Model).pageDown, "pgdown"),
				bind((*Model).home, "home"), bind((*Model).end, "end"))},
			{"Tab", "switch Library ⇄ Player", "Tab", bind((*Model).switchView, "tab", "shift+tab")},
			{"Space", "pause / resume", "Space", bind((*Model).pause, "space")},
			{"x", "stop", "X", bind((*Model).stop, "x")},
			{". / ,", "next / previous track", ". / ,", slices.Concat(
				bind((*Model).next, "."), bind((*Model).previous, ","))},
			{"shift+← / shift+→", "seek back / forward", "← / → while playing", slices.Concat(
				bind((*Model).seekBack, "shift+left"), bind((*Model).seekForward, "shift+right"))},
			{"+ = / -", "volume up / down by 5%", "+ = / -", slices.Concat(
				bind((*Model).volumeUp, "+", "="), bind((*Model).volumeDown, "-"))},
			{"F8, m", "mute on / off", "F8", bind((*Model).mute, "f8", "m")},
			{"r", "repeat: off → all → one → off", "", bind((*Model).cycleRepeat, "r")},
			{"s", "shuffle on / off", "", bind((*Model).toggleShuffle, "s")},
			{"? / h", "this help", "", bind((*Model).openHelp, "?", "h")},
			{"Q, ctrl+c", "quit (the music keeps playing)", "", bind((*Model).quit, "Q", "ctrl+c")},
		}},
		{"Library", inLibrary, []keyRow{
			{"Enter, →", "open; on a track: play its album from there", "Enter", bind((*Model).open, "enter", "right")},
			{"←, Backspace, Esc", "back (Esc at the top quits)", "Backspace, Esc", slices.Concat(
				bind((*Model).back, "left", "backspace"), bind((*Model).backOrQuit, "esc"))},
			{"p", "play: artist, album, or album from the track", "P", bind((*Model).play, "p")},
			{"q", "add to the queue", "Q", bind((*Model).enqueue, "q")},
		}},
		{"Player", inPlayer, []keyRow{
			{"Enter, p", "play the selected item", "Enter, P", bind((*Model).playSelected, "enter", "p")},
			{"← / →", "seek back / forward", "← / → while playing", slices.Concat(
				bind((*Model).seekBack, "left"), bind((*Model).seekForward, "right"))},
			{"Delete, d", "remove the selected item", "Delete", bind((*Model).remove, "delete", "d")},
			{"shift+↑ / shift+↓, K / J", "move the selected item up / down", "", slices.Concat(
				bind((*Model).moveItemUp, "shift+up", "K"), bind((*Model).moveItemDown, "shift+down", "J"))},
			{"c", "clear the queue (asks first)", "", bind((*Model).askClear, "c")},
		}},
	}
}

// handler returns the handler of key in view, or nil.
func handler(table []keySection, view viewKind, key string) func(*Model) tea.Cmd {
	in := inLibrary
	if view == playerView {
		in = inPlayer
	}
	for _, section := range table {
		if section.scope != everywhere && section.scope != in {
			continue
		}
		for _, row := range section.rows {
			for _, b := range row.keys {
				if b.key == key {
					return b.do
				}
			}
		}
	}
	return nil
}

// keyColumn is the display width of the widest key label.
func keyColumn(table []keySection) int {
	width := 0
	for _, section := range table {
		for _, row := range section.rows {
			width = max(width, ansi.StringWidth(row.label))
		}
	}
	return width
}

// aboutLines are the author and homepage lines.
func aboutLines() []string {
	return []string{"Author: " + Author, Homepage}
}

// KeysHelp returns the key table and the about lines as plain text, for
// --help.
func KeysHelp() string {
	table := keyTable()
	column := keyColumn(table)
	var b strings.Builder
	for _, section := range table {
		b.WriteString("Keys, " + strings.ToLower(section.title) + ":\n")
		for _, row := range section.rows {
			b.WriteString("  " + pad(row.label, column) + "   " + row.action + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(strings.Join(aboutLines(), "\n") + "\n")
	return b.String()
}

// KeysMarkdown returns the key table as Markdown, as the README shows it.
func KeysMarkdown() string {
	var b strings.Builder
	for i, section := range keyTable() {
		if i > 0 {
			b.WriteString("\n")
		}
		withKodi := slices.ContainsFunc(section.rows, func(r keyRow) bool { return r.kodi != "" })
		b.WriteString("**" + section.title + "**\n\n")
		if withKodi {
			b.WriteString("| Key | Action | Kodi |\n|---|---|---|\n")
		} else {
			b.WriteString("| Key | Action |\n|---|---|\n")
		}
		for _, row := range section.rows {
			cells := []string{row.label, row.action}
			if withKodi {
				cells = append(cells, cmp.Or(row.kodi, "—"))
			}
			b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
		}
	}
	return b.String()
}
