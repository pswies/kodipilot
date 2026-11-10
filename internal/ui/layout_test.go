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
	for _, tc := range []struct {
		w, cols int
		beside  bool
	}{
		{32, 32, false}, {60, 32, false}, {81, 32, false}, {83, 32, false}, // the text below
		{84, 32, true}, {113, 32, true}, {115, 32, true},
		{116, 64, true}, {146, 64, true}, {147, 64, true},
		{148, 96, true}, {200, 96, true},
	} {
		cols, rows, ok := m.imageBox(tc.w, 40)
		if !ok || cols != tc.cols || rows != cols/2 {
			t.Errorf("pane %d: box %dx%d (%v), want %dx%d", tc.w, cols, rows, ok, tc.cols, tc.cols/2)
		}
		if beside := tc.w-cols-besideGap >= besideMin; beside != tc.beside {
			t.Errorf("pane %d: text beside the box %v, want %v", tc.w, beside, tc.beside)
		}
	}
	for _, size := range [][2]int{{31, 40}, {8, 40}, {100, 3}} {
		if _, _, ok := m.imageBox(size[0], size[1]); ok {
			t.Errorf("a box in %v", size)
		}
	}
	if _, _, ok := New(nil, &fakeClock{}, Options{}).imageBox(200, 40); ok {
		t.Error("a box with images off")
	}
}

func TestFullScreenDetailsStacked(t *testing.T) {
	h := detailsHarness(t, 80, 24, writeCover(t))
	h.press("i")
	h.advance(detailsDelay)
	lines := docLines(h)
	art := artColumns(h)
	if art > 32 {
		t.Fatalf("image %d columns wide, want the 32-column box", art)
	}
	for y := range 16 {
		if strings.Contains(lines[y], "Brian Eno") {
			t.Errorf("text beside the box on line %d: %q", y, lines[y])
		}
		if w := ansi.StringWidth(strings.TrimRight(lines[y], " ")); w > 1+32 {
			t.Errorf("line %d of the box is %d wide: %q", y, w, lines[y])
		}
	}
	if got := strings.TrimRight(lines[17], " "); got != " Brian Eno" {
		t.Errorf("first text line %q, want the title under the box", got)
	}
	h.want("Brian Eno") // 16 + 1 rows: the title is on screen
}

func TestNoBoxUnder32Columns(t *testing.T) {
	h := detailsHarness(t, 33, 24, writeCover(t)) // full-screen details, 31 inner columns
	h.press("i")
	h.advance(detailsDelay)
	if w, _ := h.m.paneSize(); w != 31 {
		t.Fatalf("inner width %d", w)
	}
	if l := strings.TrimRight(docLines(h)[0], " "); l != " Brian Eno" {
		t.Errorf("first line %q, want the title at the top", l)
	}
	h.wantNot("no image", "loading", "╭")
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
