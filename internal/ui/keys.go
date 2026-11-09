package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Author and Homepage are shown in the help pop-up and in --help.
const (
	Author   = "Piotr Święs"
	Homepage = "https://github.com/pswies/kodipilot"
)

// footerHint is the key hint in the footer.
const footerHint = "? help  Tab view  → open  ← back  Enter play  Space pause  +/- volume  q quit"

// keyHelp documents one key binding; an empty keys continues the row above.
type keyHelp struct{ keys, action string }

// keyTable is the single list of key bindings, shown in the help pop-up and
// in --help.
func keyTable() []keyHelp {
	return []keyHelp{
		{"Up / Down", "move"},
		{"PgUp / PgDn", "move by a page"},
		{"Home / End", "first / last row"},
		{"Right", "open: artist → albums → tracks"},
		{"Left, Backspace", "back"},
		{"Enter", "on an artist: open"},
		{"", "on an album: play it from track 1"},
		{"", "on a track: play the album from it"},
		{"", "in the queue: play that item"},
		{"Tab", "switch between Library and Player"},
		{"Space", "pause / resume"},
		{"n / p", "next / previous track"},
		{"s", "stop"},
		{"+ / -", "volume up / down by 5%"},
		{"? / h", "this help (Esc, q close it)"},
		{"q", "quit (music keeps playing)"},
	}
}

// keyColumn is the display width of the keys column.
func keyColumn(table []keyHelp) int {
	width := 0
	for _, k := range table {
		width = max(width, ansi.StringWidth(k.keys))
	}
	return width
}

// aboutLines are the author and homepage lines.
func aboutLines() []string {
	return []string{"Author: " + Author, Homepage}
}

// KeysHelp returns the key bindings and the about lines as plain text, for
// --help.
func KeysHelp() string {
	table := keyTable()
	column := keyColumn(table)
	var b strings.Builder
	b.WriteString("Keys:\n")
	for _, k := range table {
		b.WriteString("  " + pad(k.keys, column) + "   " + k.action + "\n")
	}
	b.WriteString("\n" + strings.Join(aboutLines(), "\n") + "\n")
	return b.String()
}
