package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pswies/kodipilot/internal/artwork"
	"github.com/pswies/kodipilot/internal/kodi"
	"github.com/pswies/kodipilot/internal/kodi/koditest"
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

func TestLayoutTiers(t *testing.T) {
	for _, tc := range []struct {
		width, height int
		tier          tier
		list, pane    int // widths
	}{
		{80, 24, listOnly, 80, 0},
		{80, 40, stacked, 80, 80},
		{60, 50, stacked, 60, 60},
		{89, 26, stacked, 89, 89}, // a body of 23 rows
		{89, 25, listOnly, 89, 0},
		{30, 60, stacked, 30, 30},
		{90, 30, sideBySide, 32, 57},
		{100, 30, sideBySide, 32, 67},
		{119, 30, sideBySide, 35, 83},
		{120, 30, sideBySide, 32, 87},
		{200, 30, sideBySide, 50, 149},
		{240, 50, sideBySide, 60, 179},
		{200, 13, listOnly, 200, 0},
	} {
		h := newHarness(t, tc.width, tc.height)
		l := h.m.layout()
		if l.tier != tc.tier || l.listWidth != tc.list || l.paneWidth != tc.pane {
			t.Errorf("%dx%d: %+v, want tier %d, list %d, pane %d", tc.width, tc.height, l, tc.tier, tc.list, tc.pane)
		}
		body := tc.height - 3
		switch l.tier {
		case stacked:
			if l.paneHeight < stackedMinDetail || l.listHeight < stackedMinList || l.paneHeight+1+l.listHeight != body {
				t.Errorf("%dx%d: details %d, list %d rows of %d", tc.width, tc.height, l.paneHeight, l.listHeight, body)
			}
		case sideBySide:
			if l.paneHeight != body || l.listHeight != body {
				t.Errorf("%dx%d: heights %+v", tc.width, tc.height, l)
			}
		default:
			if l.listHeight != body {
				t.Errorf("%dx%d: list %d rows, want %d", tc.width, tc.height, l.listHeight, body)
			}
		}
	}
}

func TestStackedHeights(t *testing.T) {
	for body, details := range map[int]int{23: 12, 24: 13, 30: 17, 40: 22, 50: 28, 100: 55} {
		h := newHarness(t, 80, body+3)
		if l := h.m.layout(); l.paneHeight != details || l.listHeight != body-details-1 {
			t.Errorf("body %d: details %d, list %d; want details %d", body, l.paneHeight, l.listHeight, details)
		}
	}
}

func TestStackedScreen(t *testing.T) {
	h := detailsHarness(t, 80, 40, writeCover(t))
	l := h.m.layout()
	lines := h.screen()
	divider := 1 + l.paneHeight
	if lines[divider] != strings.Repeat("─", 80) {
		t.Errorf("line %d is %q, want the divider", divider, lines[divider])
	}
	if !strings.Contains(lines[divider+1], "Artist") || !strings.Contains(lines[divider+2], "▌ Brian Eno") {
		t.Errorf("no list under the divider:\n%s", strings.Join(lines, "\n"))
	}
	// The image rules at the full width: 78 inner columns leave too little
	// beside the 32-column box, so the text is below it.
	if cols, _, _ := h.m.imageBox(h.m.paneSize()); cols != 32 {
		t.Errorf("box %d columns", cols)
	}
	if strings.Contains(strings.Join(lines[1:17], ""), "Brian Eno  ") || !strings.HasPrefix(lines[18], " Brian Eno") {
		t.Errorf("the text is not under the image:\n%s", strings.Join(lines[:20], "\n"))
	}
	// 89 columns leave 53 beside it.
	h.send(tea.WindowSizeMsg{Width: 89, Height: 40})
	if !strings.Contains(h.screen()[1], "  Brian Eno") {
		t.Errorf("the text is not beside the image: %q", h.screen()[1])
	}
}

func TestStackedToggle(t *testing.T) {
	h := newHarness(t, 80, 40) // no images: the biography is in view
	h.advance(detailsDelay)
	h.want("Biography")
	h.press("i")
	if l := h.m.layout(); l.tier != listOnly || l.listHeight != 37 {
		t.Errorf("hidden: %+v", l)
	}
	h.wantNot("Biography", "────")
	h.press("i")
	if h.m.layout().tier != stacked {
		t.Error("i did not bring the details back")
	}
	h.want("Biography")
}

func TestStackedPaging(t *testing.T) {
	music := koditest.NewMusic()
	var artists []kodi.Artist
	var albums []kodi.Album
	for i := 1; i <= 60; i++ {
		artists = append(artists, kodi.Artist{ID: i, Name: fmt.Sprintf("Artist %02d", i)})
		albums = append(albums, kodi.Album{ID: i, Title: "Album", ArtistIDs: []int{i}})
	}
	music.Reply("AudioLibrary.GetArtists", map[string]any{"artists": artists})
	music.Reply("AudioLibrary.GetAlbums", map[string]any{"albums": albums})
	h := &harness{t: t, kodi: music, clock: &fakeClock{}}
	h.m = New(music, h.clock, Options{})
	h.send(tea.WindowSizeMsg{Width: 80, Height: 40})
	h.run(h.m.Init())
	rows := h.m.layout().listHeight - 1
	if h.m.layout().tier != stacked || rows != 15 {
		t.Fatalf("layout %+v", h.m.layout())
	}
	h.press("pgdown")
	if got := h.selected(); !strings.HasPrefix(got, fmt.Sprintf("Artist %02d", 1+rows)) {
		t.Errorf("after PgDn: %q", got)
	}
	h.press("pgdown", "pgdown", "end")
	if got := h.selected(); !strings.HasPrefix(got, "Artist 60") {
		t.Errorf("after End: %q (the cursor must stay on screen)", got)
	}
}

func TestTallNarrowSizes(t *testing.T) {
	h := detailsHarness(t, 80, 40, writeCover(t))
	h.press("down", "down")
	h.advance(detailsDelay)
	for _, size := range [][2]int{{30, 60}, {50, 80}, {20, 40}, {60, 50}, {80, 40}, {89, 26}, {31, 30}, {32, 30}} {
		for _, keys := range [][]string{nil, {"]", "]"}, {"i"}, {"i"}} {
			h.press(keys...)
			h.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			h.advance(detailsDelay)
			lines := strings.Split(h.m.render(), "\n")
			if len(lines) > size[1] {
				t.Errorf("%v: %d lines", size, len(lines))
			}
			for _, l := range lines {
				if w := ansi.StringWidth(l); w > size[0] {
					t.Errorf("%v: line %q is %d wide", size, ansi.Strip(l), w)
				}
			}
		}
	}
}
