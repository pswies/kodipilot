package ui

import (
	"cmp"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// The key hints in the footer; the second while the details can scroll.
const (
	footerHint       = "? help  Tab view  Enter open  p play  q queue  Space pause  +/- volume  Q quit"
	footerScrollHint = "? help  Tab view  Enter open  p play  q queue  Space pause  [ ] scroll  Q quit"
)

// scope says in which view a section of the key table applies.
type scope int

const (
	everywhere scope = iota
	inLibrary
	inQueue
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
			{"↑ ↓ PgUp PgDn", "move", "same", slices.Concat(
				bind((*Model).up, "up"), bind((*Model).down, "down"),
				bind((*Model).pageUp, "pgup"), bind((*Model).pageDown, "pgdown"))},
			{"Home / End", "first / last row", "same", slices.Concat(
				bind((*Model).home, "home"), bind((*Model).end, "end"))},
			{"Tab", "switch Library ⇄ Queue", "Tab", bind((*Model).switchView, "tab", "shift+tab")},
			{"Space", "pause / resume", "Space", bind((*Model).pause, "space")},
			{"x", "stop", "X", bind((*Model).stop, "x")},
			{". / ,", "next / previous track", ". / ,", slices.Concat(
				bind((*Model).next, "."), bind((*Model).previous, ","))},
			{"shift+← / shift+→", "seek back / forward", "← / →", slices.Concat(
				bind((*Model).seekBack, "shift+left"), bind((*Model).seekForward, "shift+right"))},
			{"+ = / -", "volume up / down by 5%", "+ = / -", slices.Concat(
				bind((*Model).volumeUp, "+", "="), bind((*Model).volumeDown, "-"))},
			{"F8, m", "mute on / off", "F8", bind((*Model).mute, "f8", "m")},
			{"r", "repeat: off → all → one → off", "", bind((*Model).cycleRepeat, "r")},
			{"s", "shuffle on / off", "", bind((*Model).toggleShuffle, "s")},
			{"? / h", "this help", "", bind((*Model).openHelp, "?", "h")},
			{"Q, ctrl+c", "quit (the music keeps playing)", "", bind((*Model).quit, "Q", "ctrl+c")},
		}},
		{"Details", everywhere, []keyRow{
			{"i", "show / hide (full screen when narrow)", "I", bind((*Model).toggleDetails, "i")},
			{"[ / ]", "scroll the details", "", slices.Concat(
				bind((*Model).scrollDetailsUp, "["), bind((*Model).scrollDetailsDown, "]"))},
		}},
		{"Library", inLibrary, []keyRow{
			{"Enter, →", "open; on a track: play its album from there", "Enter", bind((*Model).open, "enter", "right")},
			{"←, Backspace, Esc", "back (Esc at the top quits)", "Backspace, Esc", slices.Concat(
				bind((*Model).back, "left", "backspace"), bind((*Model).backOrQuit, "esc"))},
			{"p", "play now: artist, album, or from a track", "P", bind((*Model).play, "p")},
			{"q", "add to the queue", "Q", bind((*Model).enqueue, "q")},
		}},
		{"Queue", inQueue, []keyRow{
			{"Enter, p", "play the selected item", "Enter, P", bind((*Model).playSelected, "enter", "p")},
			{"← / →", "seek back / forward", "← / →", slices.Concat(
				bind((*Model).seekBack, "left"), bind((*Model).seekForward, "right"))},
			{"Delete, d", "remove the selected item", "Delete", bind((*Model).remove, "delete", "d")},
			{"shift+↑ / ↓, K / J", "move the selected item up / down", "", slices.Concat(
				bind((*Model).moveItemUp, "shift+up", "K"), bind((*Model).moveItemDown, "shift+down", "J"))},
			{"c", "clear the queue (asks first)", "", bind((*Model).askClear, "c")},
		}},
	}
}

// handler returns the handler of key in view, or nil.
func handler(table []keySection, view viewKind, key string) func(*Model) tea.Cmd {
	in := inLibrary
	if view == queueView {
		in = inQueue
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
