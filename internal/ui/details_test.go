package ui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pswies/kodipilot/internal/artwork"
	"github.com/pswies/kodipilot/internal/kodi/koditest"
)

// writeCover writes a small gradient PNG and returns its path.
func writeCover(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			img.Set(x, y, color.RGBA{uint8(x * 4), uint8(y * 4), 128, 255})
		}
	}
	path := filepath.Join(t.TempDir(), "cover.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return path
}

// detailsHarness returns a harness with images, at width×height, with the
// fake's covers at cover ("" for none).
func detailsHarness(t *testing.T, width, height int, cover string) *harness {
	t.Helper()
	music := koditest.NewMusic()
	if cover != "" {
		music.SetCover(cover)
	}
	h := &harness{t: t, kodi: music, clock: &fakeClock{now: time.Date(2026, 11, 9, 20, 0, 0, 0, time.UTC)}}
	h.m = New(music, h.clock, Options{Images: artwork.NewLoader(artwork.Config{})})
	h.send(tea.WindowSizeMsg{Width: width, Height: height})
	h.run(h.m.Init())
	h.advance(detailsDelay)
	return h
}

func TestPaneToggle(t *testing.T) {
	h := detailsHarness(t, 120, 30, "")
	h.want("Biography")
	if l := h.screen()[2]; !strings.Contains(l, "│▌ Brian Eno") { // under the column header
		t.Errorf("no pane left of the list: %q", l)
	}
	h.press("i")
	h.wantNot("Biography")
	if strings.Contains(h.screen()[2], "│") {
		t.Errorf("divider without the pane: %q", h.screen()[2])
	}
	h.press("i")
	h.want("Biography")
}

func TestFullScreenDetails(t *testing.T) {
	h := newHarness(t, 80, 22) // too short for the stacked pane; no images, so the text is on top
	h.advance(detailsDelay)
	h.wantNot("Biography")
	for _, close := range []string{"esc", "left", "i"} {
		h.press("i")
		h.advance(detailsDelay)
		h.want("Brian Eno", "Biography", "Albums  5")
		h.wantNot("David Byrne") // the list is hidden
		h.press(close)
		h.want("David Byrne")
		h.wantNot("Biography")
		if h.quit || len(h.m.levels) != 1 {
			t.Fatalf("%s did more than close the details", close)
		}
	}
	// Navigation keeps working, and the details follow.
	h.press("i", "down")
	h.advance(detailsDelay)
	h.want("David Byrne", "Albums  1")
	h.wantNot("Brian Eno")

	// Widening the terminal turns the full screen into the pane.
	h.send(tea.WindowSizeMsg{Width: 120, Height: 24})
	h.want("David Byrne", "│")
	h.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	h.wantNot("Albums  1")
}

func TestArtistDetails(t *testing.T) {
	h := detailsHarness(t, 120, 30, "")
	h.want("Genre   Ambient, Electronic", "Born    1948-05-15", "Active  1970s, 2020s", "Albums  5",
		"Biography", "English musician, composer and record producer.", "A pioneer of ambient music.")
	h.wantNot("Style", "Formed")
	h.press("down")
	h.advance(detailsDelay)
	h.want("Albums  1")
	h.wantNot("Genre", "Born", "Biography")
}

func TestAlbumDetails(t *testing.T) {
	h := detailsHarness(t, 120, 30, "")
	h.press("enter", "down")
	h.advance(detailsDelay)
	h.want("Ambient 1", "Artist    Brian Eno", "Year      1978", "Genre     Ambient", "Label     Polydor",
		"Type      album", "Length    43:48", "Contents  4 tracks", "Review", "Four pieces for airports.")
	h.wantNot("Mood", "Style")
}

func TestTrackDetails(t *testing.T) {
	h := detailsHarness(t, 120, 30, "")
	h.press("enter", "down", "enter", "down")
	h.advance(detailsDelay)
	h.want("Artist    Brian Eno", "Album     Ambient 1", "Track     2", "Duration  08:55", "Composer  Brian Eno",
		"Rating    yours 8/10", "Played    3 times, last 2026-11-01 21:00:00", "Audio     1411 kbit/s · 44.1 kHz · stereo",
		"/pub/music/Brian Eno/Ambient 1/02.flac", "Comment", "Remastered.", "Lyrics", "(instrumental)")
	h.press("up")
	h.advance(detailsDelay)
	h.want("Artist    Brian Eno", "Duration  17:01")
	h.wantNot("Composer", "Lyrics", "Album  ")

	// The queue shows the details of its items too.
	h.press("p", "tab")
	h.advance(detailsDelay)
	h.want("Library │ Queue", "Duration  17:01")
}

func TestDetailsDebounced(t *testing.T) {
	h := detailsHarness(t, 120, 30, "")
	h.kodi.Reset()
	h.press("down")
	h.advance(detailsDelay / 2)
	h.press("down") // Żywiołak, before David Byrne's timer fires
	h.advance(detailsDelay / 2)
	if n := h.count("AudioLibrary.GetArtistDetails"); n != 0 {
		t.Errorf("%d fetches before the cursor rested", n)
	}
	h.advance(detailsDelay / 2)
	if calls := h.kodi.Calls(); len(calls) != 1 || !strings.Contains(calls[0].Params, `"artistid":4`) {
		t.Errorf("calls = %v, want one fetch for Żywiołak", calls)
	}

	// Cached: going back fetches nothing.
	h.kodi.Reset()
	h.press("up") // David Byrne, never fetched
	h.advance(detailsDelay)
	h.press("up") // Brian Eno, fetched at the start
	h.advance(detailsDelay)
	if n := h.count("AudioLibrary.GetArtistDetails"); n != 1 {
		t.Errorf("%d fetches, want 1", n)
	}
}

func TestStaleDetailsIgnored(t *testing.T) {
	h := detailsHarness(t, 120, 30, "")
	h.send(detailsMsg{key: itemKey{artistItem, 2}, details: &details{title: "Stale"}})
	h.want("Brian Eno")
	h.wantNot("Stale")
	h.send(detailsDueMsg{sequence: h.m.detailsSequence - 1}) // an old timer
	h.press("down")
	h.want("Stale") // it was cached for David Byrne all along
}

func TestCover(t *testing.T) {
	h := detailsHarness(t, 120, 30, writeCover(t))
	screen := strings.Join(h.screen(), "\n")
	if !strings.ContainsAny(screen, "▀▌▘▝▖▗▚ ") || !strings.Contains(h.m.render(), "\x1b[") {
		t.Errorf("no cover art:\n%s", screen)
	}
	h.want("Brian Eno", "Biography")

	// The pane is on the left, the image at its left edge (after the
	// one-column margin), and the list right of the divider.
	list, pane := h.m.layout().listWidth, h.m.layout().paneWidth
	row := strings.Split(h.m.render(), "\n")[2]
	if !strings.HasPrefix(row, " \x1b[") {
		t.Errorf("the image does not start at the pane's left edge: %q", row)
	}
	plain := []rune(ansi.Strip(row))
	if len(plain) != pane+1+list || plain[pane] != '│' || !strings.HasPrefix(string(plain[pane+1:]), "▌ Brian Eno") {
		t.Errorf("row %q: want the pane in %d columns, │, then the list", string(plain), pane)
	}
	// The text is beside the image, two columns right of it.
	first := []rune(ansi.Strip(strings.Split(h.m.render(), "\n")[1]))
	if art := artColumns(h); !strings.HasPrefix(string(first[1+art:]), "  Brian Eno") || art < 10 {
		t.Errorf("row %q: want the title beside the image", string(plain))
	}
}

func TestMissingCover(t *testing.T) {
	h := detailsHarness(t, 120, 30, "/no/such/cover.jpg")
	// A failed image: the box says so, and the text is beside it.
	h.want("no image available")
	h.wantNot("loading")
	if l := h.screen()[1]; !strings.HasPrefix(l, " ╭") || !strings.Contains(l, "╮  Brian Eno") {
		t.Errorf("first pane line %q", l)
	}
}

func TestPlaceholder(t *testing.T) {
	for _, tc := range []struct {
		w, h int
		want string
	}{
		{30, 7, "no image available"},
		{22, 4, "no image available"},
		{21, 4, "no image"},
		{12, 4, "no image"},
		{11, 4, ""},
		{8, 4, ""},
	} {
		lines := placeholder(tc.w, tc.h, "no image available", "no image")
		text := strings.TrimSpace(strings.Trim(strings.Join(lines, ""), "╭╮╰╯─│ "))
		text = strings.Join(strings.Fields(strings.ReplaceAll(text, "│", " ")), " ")
		if len(lines) != tc.h || text != tc.want {
			t.Errorf("%dx%d: %d lines with %q, want %q", tc.w, tc.h, len(lines), text, tc.want)
		}
		for _, l := range lines {
			if w := ansi.StringWidth(l); w != tc.w {
				t.Errorf("%dx%d: line %q is %d wide", tc.w, tc.h, l, w)
			}
		}
	}
	if placeholder(1, 5) != nil {
		t.Error("placeholder in 1 column")
	}
}

// textStart returns where the title starts in the details document.
func textStart(h *harness, title string) (row, col int) {
	for y, l := range docLines(h) {
		if i := strings.Index(l, title); i >= 0 {
			return y, ansi.StringWidth(l[:i])
		}
	}
	return -1, -1
}

func TestTextStartsInTheSamePlace(t *testing.T) {
	cover := writeCover(t)
	for _, size := range [][2]int{{120, 30}, {170, 45}, {100, 20}, {90, 30}, {240, 60}} {
		with := detailsHarness(t, size[0], size[1], cover)
		without := detailsHarness(t, size[0], size[1], "")
		failed := detailsHarness(t, size[0], size[1], "/no/such/cover.jpg")
		r1, c1 := textStart(with, "Brian Eno")
		r2, c2 := textStart(without, "Brian Eno")
		r3, c3 := textStart(failed, "Brian Eno")
		if r1 < 0 || r1 != r2 || c1 != c2 || r1 != r3 || c1 != c3 {
			t.Errorf("%v: text at (%d, %d) with an image, (%d, %d) without, (%d, %d) failed", size, r1, c1, r2, c2, r3, c3)
		}
	}
}

func TestLoadingFrameFillsTheBox(t *testing.T) {
	h := detailsHarness(t, 120, 30, writeCover(t))
	h.press("down") // David Byrne: details not fetched yet
	w, height := h.m.paneSize()
	cols, rows, ok := h.m.imageBox(w, height)
	if !ok {
		t.Fatal("no image box")
	}
	lines := docLines(h)
	if !strings.Contains(strings.Join(lines[:rows], ""), "loading…") {
		t.Fatalf("no loading frame:\n%s", strings.Join(lines, "\n"))
	}
	if top := []rune(lines[0]); string(top[1]) != "╭" || string(top[cols]) != "╮" {
		t.Errorf("frame top %q is not %d wide", lines[0], cols)
	}
	if bottom := []rune(lines[rows-1]); string(bottom[1]) != "╰" || string(bottom[cols]) != "╯" {
		t.Errorf("frame bottom %q is not on row %d", lines[rows-1], rows)
	}
}

func TestNoImageBoxWhenTiny(t *testing.T) {
	h := detailsHarness(t, 20, 6, "") // full-screen details in 3 rows
	h.press("i")
	h.advance(detailsDelay)
	w, height := h.m.paneSize()
	if _, _, ok := h.m.imageBox(w, height); ok {
		t.Fatalf("an image box in %dx%d", w, height)
	}
	h.wantNot("╭", "no image")
	h.want("Brian Eno")
}

// docLines returns the details document as plain lines.
func docLines(h *harness) []string {
	w, height := h.m.paneSize()
	var out []string
	for _, l := range h.m.detailsDoc(w+2, height) {
		out = append(out, ansi.Strip(l))
	}
	return out
}

// artColumns returns the width of the image at the top of the document.
func artColumns(h *harness) int {
	w, height := h.m.paneSize()
	cols, rows, _ := h.m.imageBox(w, height)
	s, ok := h.m.cachedArt(h.m.shown().image, cols, rows)
	if !ok {
		h.t.Fatal("the image is not rendered")
	}
	return ansi.StringWidth(strings.Split(s, "\n")[0])
}

// beside returns the text of a document line right of the image.
func beside(l string, art int) string {
	r := []rune(l)
	return string(r[min(len(r), 1+art+besideGap):])
}

func TestTextBesideImage(t *testing.T) {
	h := detailsHarness(t, 170, 45, writeCover(t)) // Brian Eno, with an image
	if pane := h.m.layout().paneWidth; pane != 127 {
		t.Fatalf("pane %d, want 127", pane)
	}
	art := artColumns(h)
	lines := docLines(h)
	if !strings.HasPrefix(beside(lines[0], art), "Brian Eno") {
		t.Errorf("title not beside the image: %q", lines[0])
	}
	if !strings.Contains(lines[1], "Genre   Ambient, Electronic") {
		t.Errorf("fields not beside the image: %q", lines[1])
	}
	for _, l := range lines {
		if w := ansi.StringWidth(l); w > 127 {
			t.Errorf("line %q is %d wide", l, w)
		}
	}
}

func TestLongTextFlowsUnderImage(t *testing.T) {
	music := koditest.NewMusic()
	cover := writeCover(t)
	music.SetCover(cover)
	music.Reply("AudioLibrary.GetArtistDetails", map[string]any{"artistdetails": map[string]any{
		"description": koditest.LongBiography + "\n\n" + koditest.LongBiography,
		"art":         map[string]string{"thumb": "image://" + strings.ReplaceAll(cover, "/", "%2f") + "/"},
	}})
	h := &harness{t: t, kodi: music, clock: &fakeClock{}}
	h.m = New(music, h.clock, Options{Images: artwork.NewLoader(artwork.Config{})})
	h.send(tea.WindowSizeMsg{Width: 170, Height: 45})
	h.run(h.m.Init())
	h.advance(detailsDelay)

	art := artColumns(h)
	lines := docLines(h)
	_, rows, _ := h.m.imageBox(h.m.paneSize())
	besideText, belowWide := false, false
	for y, l := range lines {
		switch {
		case y < rows && strings.HasPrefix(beside(l, art), "Żywiołak is a Polish folk band"):
			besideText = true // the biography starts beside the image…
		case y >= rows && ansi.StringWidth(strings.TrimRight(l, " ")) > 60 && l[1] != ' ':
			belowWide = true // …and continues under it at the full width
		}
	}
	if !besideText || !belowWide {
		t.Errorf("beside %v, below at full width %v:\n%s", besideText, belowWide, strings.Join(lines, "\n"))
	}
}

func TestStackedWhenNarrow(t *testing.T) {
	h := detailsHarness(t, 90, 30, writeCover(t)) // a 57-column pane
	art, lines := artColumns(h), docLines(h)
	_, rows, _ := h.m.imageBox(h.m.paneSize())
	if inner, _ := h.m.paneSize(); inner-art-besideGap >= besideMin {
		t.Fatalf("room beside the image: %d columns", inner-art-besideGap)
	}
	for y := range rows {
		if strings.Contains(lines[y], "Brian Eno") {
			t.Errorf("text beside the image on line %d: %q", y, lines[y])
		}
	}
	if got := strings.TrimRight(lines[rows+1], " "); got != " Brian Eno" {
		t.Errorf("first text line %q, want the title under the image", got)
	}
}

func TestDetailsScroll(t *testing.T) {
	h := detailsHarness(t, 120, 30, "")
	h.want("Space pause  Tab queue") // Brian Eno fits: no scroll hint
	h.wantNot("[ ] scroll", "more  ]")
	h.press("down", "down") // Żywiołak's long biography
	h.advance(detailsDelay)
	_, height := h.m.paneSize()
	pane := func() []string {
		var out []string
		for _, l := range h.screen()[1 : 1+height] {
			out = append(out, strings.TrimRight(strings.Split(l, "│")[0], " "))
		}
		return out
	}
	first, last := func() string { return pane()[0] }, func() string { return pane()[height-1] }
	h.want("[ ] scroll")
	if !strings.HasSuffix(last(), "↓ more  ]") || strings.Contains(first(), "↑ more") {
		t.Errorf("at the top: first %q, last %q", first(), last())
	}

	h.press("]")
	if want := min(height/2, len(docLines(h))-height); h.m.detailsTop != want || want < 2 {
		t.Errorf("] scrolled to %d, want %d", h.m.detailsTop, want)
	}
	if !strings.HasSuffix(first(), "↑ more  [") {
		t.Errorf("scrolled: first %q", first())
	}
	for range 20 {
		h.press("]")
	}
	if doc := docLines(h); h.m.detailsTop != len(doc)-height {
		t.Errorf("at the bottom: top %d, want %d", h.m.detailsTop, len(doc)-height)
	}
	if strings.Contains(last(), "↓ more") || !strings.Contains(strings.Join(pane(), " "), "The last paragraph ends here.") {
		t.Errorf("the end is not shown: %q", pane())
	}
	h.wantNot("…")
	for range 20 {
		h.press("[")
	}
	if h.m.detailsTop != 0 || strings.Contains(first(), "↑ more") {
		t.Errorf("at the top again: top %d, first %q", h.m.detailsTop, first())
	}

	h.press("]", "up") // another item starts at the top
	if h.m.detailsTop != 0 {
		t.Errorf("top %d after moving on", h.m.detailsTop)
	}
}

func TestFullScreenDetailsScroll(t *testing.T) {
	h := detailsHarness(t, 80, 24, "")
	h.press("down", "down", "i")
	h.advance(detailsDelay)
	h.want("↓ more  ]", "[ ] scroll")
	h.press("]", "]")
	h.want("↑ more  [")
	if h.m.detailsTop == 0 {
		t.Error("] did not scroll the full-screen details")
	}
	h.press("i") // closed: [ and ] do nothing
	top := h.m.detailsTop
	h.press("]")
	if h.m.detailsTop != top {
		t.Error("] scrolled hidden details")
	}
}

func TestFlowText(t *testing.T) {
	f := flow{room: func(y int) int { return 9 }}
	f.text("one two three four\n\n\nfive\tsix", nil, lipgloss.NewStyle())
	var got []string
	for _, l := range f.lines {
		var s string
		for _, sp := range l {
			s += sp.text
		}
		got = append(got, s)
	}
	want := []string{"one two", "three", "four", "", "five six"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("flow = %q, want %q", got, want)
	}
	narrow := flow{room: func(y int) int { return 7 }}
	narrow.text("Zażółć gęślą jaźń 日本語のアルバムとても長い", []span{{"ab", lipgloss.NewStyle()}}, lipgloss.NewStyle())
	for _, l := range narrow.lines {
		if w := spansWidth(l); w > 7 {
			t.Errorf("line %v is %d wide", l, w)
		}
	}
}

// TestDetailsSizes renders the pane and the full-screen details at many
// sizes, with a cover: nothing panics and no line exceeds the width.
func TestDetailsSizes(t *testing.T) {
	h := detailsHarness(t, 120, 30, writeCover(t))
	h.press("enter", "down", "enter", "down")
	check := func(name string, widths, heights []int) {
		for _, height := range heights {
			for _, width := range widths {
				h.send(tea.WindowSizeMsg{Width: width, Height: height})
				h.advance(detailsDelay)
				lines := strings.Split(h.m.render(), "\n")
				if len(lines) > max(height, 1) {
					t.Errorf("%s %dx%d: %d lines", name, width, height, len(lines))
				}
				for _, l := range lines {
					if w := ansi.StringWidth(l); w > width {
						t.Errorf("%s %dx%d: line %q is %d wide", name, width, height, ansi.Strip(l), w)
					}
				}
			}
		}
	}
	small, smallH := seq(0, 30), seq(0, 8)
	large, largeH := []int{89, 90, 91, 100, 119, 120, 121, 160, 200, 240, 300}, []int{13, 14, 15, 24, 40, 60}
	check("pane", large, largeH)
	check("pane small", small, smallH)
	h.press("i") // hidden at large sizes; full screen at small ones
	check("toggled", large, largeH)
	check("full screen", small, smallH)
	h.press("+")
	check("volume", large, largeH)
	h.press("?")
	check("help", large, largeH)
}

func seq(from, to int) []int {
	var s []int
	for i := from; i <= to; i++ {
		s = append(s, i)
	}
	return s
}

func TestCoverStaysWhileLoading(t *testing.T) {
	h := detailsHarness(t, 120, 30, writeCover(t))
	h.press("enter", "down")
	h.advance(detailsDelay) // Ambient 1 with its cover
	h.press("enter", "down")
	// Track 2's details are not fetched yet, but the album cover stays.
	if _, ok := h.m.info[itemKey{songItem, 102}]; ok {
		t.Fatal("details fetched before the cursor rested")
	}
	if l := h.screen()[1]; !strings.Contains(h.m.render(), "48;2;") || strings.Contains(l, "│ 2/1") {
		t.Errorf("the cover went away while the details load: %q", l)
	}
	h.want("2/1")
}
