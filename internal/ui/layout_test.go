package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pswies/kodipilot/internal/artwork"
)

func TestImageBoxSizes(t *testing.T) {
	m := New(nil, &fakeClock{}, Options{Images: artwork.NewLoader(artwork.Config{})})
	for _, tc := range []struct{ w, cols int }{
		{60, 60}, {81, 81}, {82, 82}, {83, 83}, // whole width: 32 would leave under 50
		{84, 32}, {113, 32}, {114, 32}, {115, 32},
		{116, 64}, {145, 64}, {146, 64}, {147, 64},
		{148, 96}, {200, 96},
		{8, 8},
	} {
		cols, rows, ok := m.imageBox(tc.w, 40)
		if !ok || cols != tc.cols || rows != cols/2 {
			t.Errorf("pane %d: box %dx%d (%v), want %dx%d", tc.w, cols, rows, ok, tc.cols, tc.cols/2)
		}
		if cols < tc.w && tc.w-cols-besideGap < besideMin {
			t.Errorf("pane %d: %d columns beside the box", tc.w, tc.w-cols-besideGap)
		}
	}
	for _, size := range [][2]int{{7, 40}, {100, 3}} {
		if _, _, ok := m.imageBox(size[0], size[1]); ok {
			t.Errorf("a box in %v", size)
		}
	}
	if _, _, ok := New(nil, &fakeClock{}, Options{}).imageBox(200, 40); ok {
		t.Error("a box with images off")
	}
}

// column returns the display column where s starts in line l, or -1.
func column(l, s string) int {
	i := strings.Index(l, s)
	if i < 0 {
		return -1
	}
	return ansi.StringWidth(l[:i])
}

// end returns the display column after the last non-space of l.
func end(l string) int { return ansi.StringWidth(strings.TrimRight(l, " ")) }

func TestColumnHeaders(t *testing.T) {
	h := newHarness(t, 80, 12)
	header := func() string { return h.screen()[1] }
	row := func(text string) string {
		for _, l := range h.screen()[2:] {
			if strings.Contains(l, text) {
				return l
			}
		}
		t.Fatalf("no row with %q:\n%s", text, strings.Join(h.screen(), "\n"))
		return ""
	}

	// Artists.
	if column(header(), "Artist") != column(row("Brian Eno"), "Brian Eno") || end(header()) != end(row("Brian Eno")) {
		t.Errorf("artists:\n%s\n%s", header(), row("Brian Eno"))
	}
	// Albums.
	h.press("enter")
	ambient := row("Ambient 1")
	if column(header(), "Year") != column(ambient, "1978") || column(header(), "Album") != column(ambient, "Ambient") ||
		end(header()) != end(ambient) || !strings.HasSuffix(strings.TrimRight(ambient, " "), "43:48") {
		t.Errorf("albums:\n%s\n%s", header(), ambient)
	}
	// Tracks.
	h.press("down", "enter")
	track := row("2/1")
	if column(header(), "#") != column(track, "02")+1 || column(header(), "Title") != column(track, "2/1") || end(header()) != end(track) {
		t.Errorf("tracks:\n%s\n%s", header(), track)
	}
	// The queue.
	h.press("p", "tab")
	item := row("1/2")
	if column(header(), "Title") != column(item, "1/2") || column(header(), "Artist") != column(item, "Brian Eno") ||
		column(header(), "#") != column(item, "3") || end(header()) != end(item) {
		t.Errorf("queue:\n%s\n%s", header(), item)
	}
}

func TestColumnsDropWhenNarrow(t *testing.T) {
	h := newHarness(t, 80, 12)
	at := func(width int) (string, string) {
		h.send(tea.WindowSizeMsg{Width: width, Height: 12})
		return h.screen()[1], h.screen()[2]
	}
	for _, tc := range []struct {
		keys       []string
		width      int
		gone, kept string
	}{
		{nil, countColumnMin - 1, "Albums", "Artist"},
		{nil, countColumnMin, "", "Albums"},
		{[]string{"enter"}, lengthColumnMin - 1, "Length", "Year"},
		{nil, yearColumnMin - 1, "Year", "Album"},
		{[]string{"down", "enter"}, lengthColumnMin - 1, "Length", "Title"},
		{[]string{"p", "tab"}, artistColumnMin - 1, "Artist", "Length"},
		{nil, artistColumnMin, "", "Artist"},
	} {
		h.press(tc.keys...)
		header, first := at(tc.width)
		if tc.gone != "" && strings.Contains(header, tc.gone) || !strings.Contains(header, tc.kept) {
			t.Errorf("width %d: header %q, want %q without %q", tc.width, header, tc.kept, tc.gone)
		}
		if w := ansi.StringWidth(first); w > tc.width {
			t.Errorf("width %d: row %q", tc.width, first)
		}
	}
}

func TestCursorVisibleUnderHeader(t *testing.T) {
	h := newHarness(t, 80, 6) // two list rows
	h.press("enter")
	for _, key := range []string{"end", "home", "pgdown", "pgdown", "end", "pgup"} {
		h.press(key)
		if h.selected() == "" {
			t.Errorf("after %s the cursor is off screen:\n%s", key, strings.Join(h.screen(), "\n"))
		}
		if !strings.Contains(h.screen()[1], "Year") {
			t.Errorf("after %s the header scrolled away", key)
		}
	}
}
