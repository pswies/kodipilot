package ui

import (
	"encoding/json"
	"fmt"
	"image/color"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/pswies/kodipilot/internal/artwork"
	"github.com/pswies/kodipilot/internal/kodi"
	"github.com/pswies/kodipilot/internal/kodi/koditest"
)

func TestHelpRules(t *testing.T) {
	for _, width := range []int{80, 60} {
		h := newHarness(t, width, 80)
		h.press("?")
		inner := h.m.helpInner()
		for _, section := range keyTable() {
			var heading string
			for _, l := range h.m.helpDoc() {
				if plain := ansi.Strip(l); strings.HasPrefix(plain, section.title+" ─") {
					heading = plain
				}
			}
			if heading == "" || ansi.StringWidth(heading) != inner || !strings.HasSuffix(heading, "─") {
				t.Errorf("width %d: %s heading %q", width, section.title, heading)
			}
			if strings.Contains(heading, "in Kodi") {
				t.Errorf("width %d: %q", width, heading)
			}
		}
	}
}

func TestFirstScreenUnreachable(t *testing.T) {
	music := koditest.NewMusic()
	music.Handle("AudioLibrary.GetArtists", unavailable)
	h := &harness{t: t, kodi: music, clock: &fakeClock{}}
	h.m = New(music, h.clock, Options{})
	h.send(tea.WindowSizeMsg{Width: 100, Height: 24})
	h.run(h.m.Init())
	if _, pane := h.m.layout(); pane != 0 {
		t.Errorf("a details pane with nothing to show: %d columns", pane)
	}
	text := words(strings.Join(h.screen(), "\n"))
	want := "Can't reach Kodi at 127.0.0.1:9090 (connection refused). Retrying…"
	if !strings.Contains(text, "Waiting for Kodi…") || !strings.Contains(text, want) || strings.Contains(text, "…  ") {
		t.Errorf("first screen:\n%s", strings.Join(h.screen(), "\n"))
	}
}

func TestPlainErrors(t *testing.T) {
	refused := &kodi.UnavailableError{Addr: "http://perun:8080/jsonrpc", Err: fmt.Errorf("dial tcp: %w", errRefused())}
	for err, want := range map[error]string{
		refused: "Can't reach Kodi at perun:8080 (connection refused). Is Kodi running? For another machine use --host.",
		&kodi.UnavailableError{Addr: "127.0.0.1:9090", Err: errString("dial: i/o timeout")}: "Can't reach Kodi at 127.0.0.1:9090 (i/o timeout).",
		fmt.Errorf("x: %w", kodi.ErrUnauthorized):                                           "Kodi rejected the user name or password. Check them in Kodi under Settings → Services → Control, then restart kodipilot.",
		&kodi.Error{Method: "Player.Open", Message: "Invalid params."}:                      "Kodi couldn't do that (Player.Open: Invalid params).",
	} {
		if got := errorText(err); got != want {
			t.Errorf("errorText(%v) = %q, want %q", err, got, want)
		}
	}
	if sentence("nothing is playing") != "Nothing is playing" || sentence("") != "" {
		t.Error("sentence")
	}
}

func TestUnauthorizedStopsRetrying(t *testing.T) {
	music := koditest.NewMusic()
	music.Handle("AudioLibrary.GetArtists", func(json.RawMessage) (any, error) {
		return nil, fmt.Errorf("http://perun:8080/jsonrpc: %w", kodi.ErrUnauthorized)
	})
	h := &harness{t: t, kodi: music, clock: &fakeClock{}}
	h.m = New(music, h.clock, Options{})
	h.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	h.run(h.m.Init())
	h.advance(5 * time.Second)
	if n := h.count("AudioLibrary.GetArtists"); n != 1 {
		t.Errorf("GetArtists called %d times, want 1", n)
	}
	h.want("Kodi rejected the user name or password.")
	h.wantNot("Waiting", "Retrying")
}

func TestKodiDownMidSession(t *testing.T) {
	h := newHarness(t, 80, 12)
	h.press("p", "tab")
	h.want("Library │ Queue", "1/8 · repeat off")
	h.kodi.Handle("Player.GetActivePlayers", unavailable)
	h.advance(time.Second)
	h.want("■ Kodi unreachable, retrying…", "last known queue · Kodi unreachable", "1  1/1")
	title, _ := h.m.queueColumns(80)
	if !strings.Contains(h.m.render(), h.m.theme.dim.Render(pad("2/1", title)+"  ")) {
		t.Error("the stale queue is not dimmed")
	}
	if !strings.Contains(h.m.render(), h.m.theme.err.Render(" ■ Kodi unreachable, retrying…")) {
		t.Error("the status line is not in the error colour")
	}
	h.kodi.Reply("Player.GetActivePlayers", []map[string]any{{"playerid": 0, "type": "audio"}})
	h.advance(time.Second)
	h.wantNot("unreachable")
	h.want("1/8 · repeat off")
}

func TestVideoPlaying(t *testing.T) {
	h := newHarness(t, 80, 12)
	h.kodi.SetVideo(true)
	h.advance(time.Second)
	h.want("■ video playing")
}

func TestSuccessClearsFlash(t *testing.T) {
	h := newHarness(t, 80, 12)
	h.press("x")
	h.want("Nothing is playing")
	h.press("p", "x")
	h.wantNot("Nothing is playing")
}

func TestFooters(t *testing.T) {
	for _, tc := range []struct {
		keys  []string
		width int
		want  string
	}{
		{nil, 120, " ? help  Enter open  p play  q add  Space pause  Tab queue  Q quit"},
		{[]string{"tab"}, 120, " ? help  Enter play  d remove  J/K move  c clear  Tab library  Q quit"},
		{nil, 50, " ? help  Enter open  p play  q add  Space pause"},
		{[]string{"tab"}, 50, " ? help  Enter play  d remove  J/K move  c clear"},
		{[]string{"i"}, 50, " Esc close"},
	} {
		h := newHarness(t, tc.width, 12)
		h.press(tc.keys...)
		lines := h.screen()
		if got := strings.TrimRight(lines[len(lines)-1], " "); got != tc.want {
			t.Errorf("%v at %d: footer %q, want %q", tc.keys, tc.width, got, tc.want)
		}
	}
	// The scroll hint joins while the details overflow.
	h := detailsHarness(t, 120, 30, "")
	h.press("down", "down")
	h.advance(detailsDelay)
	h.want("Tab queue  [ ] scroll  Q quit")
}

func TestEmptyStates(t *testing.T) {
	h := newHarness(t, 100, 12)
	h.press("enter") // Brian Eno; Another Day has no tracks
	h.press("enter")
	h.want("No tracks.")
	h.press("tab", "c")
	h.want("The queue is already empty.")
	h.wantNot("Clear the queue?")

	music := koditest.NewMusic()
	music.Reply("AudioLibrary.GetArtists", map[string]any{"artists": []kodi.Artist{}})
	empty := &harness{t: t, kodi: music, clock: &fakeClock{}}
	empty.m = New(music, empty.clock, Options{})
	empty.send(tea.WindowSizeMsg{Width: 100, Height: 12})
	empty.run(empty.m.Init())
	if text := words(strings.Join(empty.screen(), "\n")); !strings.Contains(text,
		"Kodi's music library has no album artists yet. Scan your music in Kodi, then restart.") {
		t.Errorf("empty library:\n%s", strings.Join(empty.screen(), "\n"))
	}
}

func TestNowPlayingInLibrary(t *testing.T) {
	h := newHarness(t, 100, 12)
	h.press("enter", "down", "enter", "down", "p") // Ambient 1 from track 2
	h.want("▶ 02  2/1", "  01  1/1", "▶ 03:12/08:55  Brian Eno – 2/1 · Ambient 1  [2/4]")
	h.press("space")
	h.want("⏸ 02  2/1")
	h.press("left")
	for _, l := range h.screen()[1 : len(h.screen())-2] {
		if strings.Contains(l, "Ambient 1") && !strings.Contains(l, "▶ 43:48") {
			t.Errorf("the playing album is not marked: %q", l)
		}
		if strings.Contains(l, "Music for Films") && strings.Contains(l, "▶") {
			t.Errorf("another album is marked: %q", l)
		}
	}
	// Narrow: the album goes first.
	h.send(tea.WindowSizeMsg{Width: 42, Height: 12})
	h.want("⏸ 03:12/08:55  Brian Eno – 2/1  [2/4]")
}

func TestDiscSeparators(t *testing.T) {
	h := newHarness(t, 100, 12)
	h.press("enter", "end", "up", "enter") // My Life in the Bush of Ghosts: two discs
	h.want("01  Z", "Disc 2", "01  B")
	lines := h.screen()
	disc := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "Disc 2" {
			disc = i
		}
	}
	if disc < 0 || !strings.Contains(lines[disc-1], "b-side") || !strings.Contains(lines[disc+1], "01  B") {
		t.Errorf("Disc 2 not between the discs:\n%s", strings.Join(lines, "\n"))
	}
	h.press("down", "down", "down") // over the separator
	if got := h.selected(); !strings.HasPrefix(got, "01  B") {
		t.Errorf("selected %q, want the first track of disc 2", got)
	}
	h.press("up")
	if got := h.selected(); !strings.Contains(got, "b-side") {
		t.Errorf("selected %q", got)
	}
}

func TestDiscSeparatorsScroll(t *testing.T) {
	h := newHarness(t, 100, 6) // three rows for five
	h.press("enter", "end", "up", "enter", "end")
	if got := h.selected(); !strings.HasPrefix(got, "01  B") {
		t.Errorf("selected %q: the cursor is not shown", got)
	}
}

func TestReload(t *testing.T) {
	h := newHarness(t, 100, 12)
	h.press("enter")
	h.kodi.Reset()
	h.press("f5")
	h.want("Library reloaded: 3 artists", "Library │ Queue   Artists")
	if n := h.count("AudioLibrary.GetArtists"); n != 1 {
		t.Errorf("GetArtists called %d times", n)
	}
	h.press("ctrl+r")
	if n := h.count("AudioLibrary.GetArtists"); n != 2 {
		t.Errorf("ctrl+r: GetArtists called %d times", n)
	}
}

func TestSameItemDuplicates(t *testing.T) {
	q := func(ids ...int) []kodi.QueueItem {
		var items []kodi.QueueItem
		for _, id := range ids {
			items = append(items, kodi.QueueItem{ID: id})
		}
		return items
	}
	for _, tc := range []struct {
		old, queue []kodi.QueueItem
		i, want    int
	}{
		{q(1, 2, 1, 3), q(1, 2, 1, 3), 2, 2}, // unchanged
		{q(1, 2, 1, 3), q(2, 1, 3), 2, 1},    // the item before went away
		{q(1, 2, 3, 1), q(1, 2, 3, 4, 1), 3, 4},
		{q(5, 1, 2, 1), q(1, 2, 1), 3, 2}, // nearest of the two
		{q(1, 2), q(3, 4), 1, 1},          // gone: the cursor stays
	} {
		if got := sameItem(tc.old, tc.queue, tc.i); got != tc.want {
			t.Errorf("sameItem(%v, %v, %d) = %d, want %d", tc.old, tc.queue, tc.i, got, tc.want)
		}
	}
}

func TestArtTimeoutDoesNotLoop(t *testing.T) {
	var requests atomic.Int32
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)

	music := koditest.NewMusic()
	music.Reply("AudioLibrary.GetArtistDetails", map[string]any{"artistdetails": map[string]any{
		"art": map[string]string{"thumb": srv.URL + "/slow.jpg"},
	}})
	h := &harness{t: t, kodi: music, clock: &fakeClock{}}
	loader := artwork.NewLoader(artwork.Config{Timeout: 50 * time.Millisecond})
	h.m = New(music, h.clock, Options{Images: loader})
	h.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	h.run(h.m.Init())
	h.advance(detailsDelay)
	for range 5 {
		h.advance(time.Second)
	}
	if n := requests.Load(); n != 1 {
		t.Errorf("%d requests for the image, want 1", n)
	}
	h.wantNot("loading")
	h.want("Brian Eno")
}

func TestDetailsTextWidthCapped(t *testing.T) {
	h := detailsHarness(t, 300, 40, "")
	h.press("down", "down")
	h.advance(detailsDelay)
	w, height := h.m.paneSize()
	box, _, _ := h.m.imageBox(w, height)
	for _, l := range docLines(h) {
		if strings.HasPrefix(l, " ╭") || strings.HasPrefix(l, " │") || strings.HasPrefix(l, " ╰") {
			l = string([]rune(l)[1+box+besideGap:]) // the image does not count
		}
		if w := ansi.StringWidth(strings.TrimRight(l, " ")); w > 1+textMaxWidth {
			t.Errorf("details line is %d wide: %q", w, l)
		}
	}
}

func TestGreyContrast(t *testing.T) {
	for _, tc := range []struct {
		dark   bool
		bg, fg string
	}{
		{false, "#FFFFFF", "#000000"},
		{false, "#FDF6E3", "#586E75"}, // Solarized light
		{true, "#000000", "#FFFFFF"},
		{true, "#1E1E1E", "#D4D4D4"},
		{true, "#282A36", "#F8F8F2"}, // Dracula
	} {
		bg, fg := lipgloss.Color(tc.bg), lipgloss.Color(tc.fg)
		for name, th := range map[string]theme{
			"fixed":   newTheme(tc.dark, colorprofile.TrueColor, nil, nil),
			"blended": newTheme(tc.dark, colorprofile.TrueColor, bg, fg),
		} {
			grey, selection := th.dim.GetForeground(), th.selected.GetBackground()
			for what, against := range map[string]color.Color{"background": bg, "selection": selection} {
				if what == "selection" && name == "fixed" && contrast(bg, selection) > 1.5 {
					continue // the fixed band is meant for usual backgrounds only
				}
				if c := contrast(grey, against); c < 4.5 {
					t.Errorf("%s on %s (%s): contrast %.2f with the %s", name, tc.bg, map[bool]string{true: "dark", false: "light"}[tc.dark], c, what)
				}
			}
		}
	}
}

func errRefused() error { return syscall.ECONNREFUSED }
