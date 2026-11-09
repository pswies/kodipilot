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

func TestLayout(t *testing.T) {
	for _, tc := range []struct{ width, height, list, pane int }{
		{80, 30, 80, 0},
		{89, 30, 89, 0},
		{90, 30, 32, 57},
		{100, 30, 32, 67},
		{119, 30, 35, 83},
		{120, 30, 32, 87},
		{200, 30, 50, 149},
		{240, 50, 60, 179},
		{200, 13, 200, 0},
	} {
		h := newHarness(t, tc.width, tc.height)
		if list, pane := h.m.layout(); list != tc.list || pane != tc.pane {
			t.Errorf("%dx%d: list %d, pane %d; want %d, %d", tc.width, tc.height, list, pane, tc.list, tc.pane)
		}
	}
}

func TestPaneToggle(t *testing.T) {
	h := detailsHarness(t, 120, 30, "")
	h.want("Biography")
	if l := h.screen()[1]; !strings.Contains(l, "│▌ Brian Eno") {
		t.Errorf("no pane left of the list: %q", l)
	}
	h.press("i")
	h.wantNot("Biography")
	if strings.Contains(h.screen()[1], "│") {
		t.Errorf("divider without the pane: %q", h.screen()[1])
	}
	h.press("i")
	h.want("Biography")
}

func TestFullScreenDetails(t *testing.T) {
	h := detailsHarness(t, 80, 24, "")
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
	list, pane := h.m.layout()
	row := strings.Split(h.m.render(), "\n")[1]
	if !strings.HasPrefix(row, " \x1b[") {
		t.Errorf("the image does not start at the pane's left edge: %q", row)
	}
	plain := []rune(ansi.Strip(row))
	if len(plain) != pane+1+list || plain[pane] != '│' || !strings.HasPrefix(string(plain[pane+1:]), "▌ Brian Eno") {
		t.Errorf("row %q: want the pane in %d columns, │, then the list", string(plain), pane)
	}
	// The text below the image starts at the same edge.
	h.want("\n Brian Eno")
}

func TestMissingCover(t *testing.T) {
	h := detailsHarness(t, 120, 30, "/no/such/cover.jpg")
	// No image: the text starts at the top of the pane.
	if l := h.screen()[1]; !strings.HasPrefix(l, " Brian Eno") {
		t.Errorf("first pane line %q", l)
	}
	h.wantNot("loading")
}

func TestPlaceholder(t *testing.T) {
	lines := placeholder(20, 5)
	if len(lines) != 5 || !strings.Contains(strings.Join(lines, ""), "loading…") {
		t.Errorf("placeholder %q", lines)
	}
	for _, l := range lines {
		if w := ansi.StringWidth(l); w != 20 {
			t.Errorf("placeholder line %q is %d wide", l, w)
		}
	}
	if placeholder(1, 5) != nil {
		t.Error("placeholder in 1 column")
	}
}

func TestLongTextCut(t *testing.T) {
	h := detailsHarness(t, 120, 16, "")
	h.press("enter", "down", "enter", "down")
	h.advance(detailsDelay)
	screen := strings.Join(h.screen(), "\n")
	if !strings.Contains(screen, "…") {
		t.Errorf("long details not cut with …:\n%s", screen)
	}
}

func TestWrap(t *testing.T) {
	got := wrap("one two three four\n\n\nfive\tsix", 9)
	want := []string{"one two", "three", "four", "", "five six"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("wrap = %q, want %q", got, want)
	}
	for _, l := range wrap("Zażółć gęślą jaźń 日本語のアルバムとても長い", 7) {
		if width(l) > 7 {
			t.Errorf("line %q too wide", l)
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
