package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// words collapses runs of spaces and frame characters, so that wrapped text
// can be searched for.
func words(s string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("│", " ", "╭", " ", "╮", " ", "╰", " ", "╯", " ", "─", " ").Replace(s)), " ")
}

func TestHelpContent(t *testing.T) {
	h := newHarness(t, 80, 70)
	h.m.opts.Version = "0.0.4"
	h.press("?")
	text := words(strings.Join(h.screen(), "\n"))
	want := []string{"kodipilot v0.0.4", intro, "Piotr Święs · github.com/pswies/kodipilot"}
	for _, section := range keyTable() {
		want = append(want, section.title)
		for _, row := range section.rows {
			want = append(want, row.label, row.action)
		}
	}
	for _, w := range want {
		if !strings.Contains(text, words(w)) {
			t.Errorf("help lacks %q", w)
		}
	}
	h.wantNot("↑↓ scroll")
}

func TestHelpKeys(t *testing.T) {
	h := newHarness(t, 90, 70)
	h.press("?")
	h.kodi.Reset()
	h.press(".", "enter", "tab", "+", "x", "p", "m", "]", "esc")
	if calls := h.kodi.Methods(); len(calls) > 0 {
		t.Errorf("keys not ignored while help is open: %v", calls)
	}
	h.wantNot("Piotr Święs") // Esc closed it
	for _, keys := range [][]string{{"?", "?"}, {"h", "h"}, {"?", "q"}, {"h", "Q"}} {
		h.press(keys[0])
		h.want("Piotr Święs")
		h.press(keys[1])
		h.wantNot("Piotr Święs")
		if h.quit {
			t.Fatalf("%v quit", keys)
		}
	}
}

func TestHelpScrolls(t *testing.T) {
	h := newHarness(t, 80, 20)
	h.m.opts.Version = "9.9.9"
	h.press("?")
	h.want("kodipilot lets you", "↑↓ scroll")
	h.wantNot("Piotr Święs")
	h.press("end")
	h.want("Piotr Święs · github.com/pswies/kodipilot")
	h.wantNot("kodipilot lets you")
	h.press("pgup", "home")
	h.want("kodipilot lets you")
	h.want("v9.9.9")
	h.press("down")
	h.wantNot("v9.9.9")
	h.press("up")
	h.want("v9.9.9")
}

// TestHelpColumns checks the layout of --help, which the help box shares: in
// every section the keys end, and the actions start, at the same column, and
// wrapped actions continue under the action column.
func TestHelpColumns(t *testing.T) {
	table := keyTable()
	lines := strings.Split(KeysHelp(), "\n")
	keyEnd := -1
	for _, section := range table {
		at := index(lines, section.title)
		if at < 0 {
			t.Fatalf("no section %s", section.title)
		}
		for _, l := range lines[at+1:] {
			if l == "" {
				break
			}
			first := len(l) - len(strings.TrimLeft(l, " "))
			column := ansi.StringWidth(l[:first])
			if row := rowOf(section, l); row != nil {
				end := ansi.StringWidth(l[:first+len(row.label)])
				if keyEnd < 0 {
					keyEnd = end
				}
				if end != keyEnd {
					t.Errorf("%q: keys end at %d, want %d", l, end, keyEnd)
				}
				rest := l[first+len(row.label):]
				column = end + len(rest) - len(strings.TrimLeft(rest, " "))
			}
			if column != keyEnd+columnGap {
				t.Errorf("%q: action at column %d, want %d", l, column, keyEnd+columnGap)
			}
		}
	}
}

func index(lines []string, s string) int {
	for i, l := range lines {
		if strings.TrimSpace(l) == s || strings.HasPrefix(l, s+" ") {
			return i
		}
	}
	return -1
}

// rowOf returns the row whose keys start line l, or nil for a continuation.
func rowOf(section keySection, l string) *keyRow {
	for i, row := range section.rows {
		if strings.HasPrefix(strings.TrimLeft(l, " "), row.label+"  ") {
			return &section.rows[i]
		}
	}
	return nil
}

func TestHelpWidths(t *testing.T) {
	for _, width := range []int{80, 76, 73, 60, 40} {
		h := newHarness(t, width, 80)
		h.press("?")
		h.want("Piotr Święs")
		h.wantNot("in Kodi")
		for _, l := range h.screen() {
			if w := ansi.StringWidth(l); w > width {
				t.Errorf("width %d: line %q is %d wide", width, l, w)
			}
		}
	}
}

// TestHelpWithoutKodiColumn checks that no Kodi equivalent appears beside
// an action, and that the actions use the width it freed.
func TestHelpWithoutKodiColumn(t *testing.T) {
	text := words(KeysHelp())
	for _, kodi := range []string{"in Kodi", "same", "Backspace, Esc Backspace, Esc"} {
		if strings.Contains(text, kodi) {
			t.Errorf("--help shows the Kodi column (%q)", kodi)
		}
	}
	plain := KeysHelp()
	for _, action := range []string{"open; on a track: play its album from there", "play now: artist, album, or from a track",
		"show / hide (full screen when narrow)"} {
		if !strings.Contains(plain, action) {
			t.Errorf("--help wraps %q", action)
		}
	}
}

func TestKeysHelp(t *testing.T) {
	text := words(KeysHelp())
	for _, w := range []string{intro, "Everywhere", "Details", "Library", "Queue", helpFooter()} {
		if !strings.Contains(text, words(w)) {
			t.Errorf("--help lacks %q", w)
		}
	}
	for _, l := range strings.Split(KeysHelp(), "\n") {
		if w := ansi.StringWidth(l); w > helpMaxWidth-4 || strings.Contains(l, "\x1b") {
			t.Errorf("--help line %q (%d wide)", l, w)
		}
	}
}
