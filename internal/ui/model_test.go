package ui

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pswies/kodipilot/internal/kodi"
	"github.com/pswies/kodipilot/internal/kodi/koditest"
)

// fakeClock is a manual Clock: timers fire when the test advances it.
type fakeClock struct {
	now    time.Time
	timers []timer
}

type timer struct {
	at  time.Time
	msg tea.Msg
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) After(d time.Duration, msg tea.Msg) tea.Cmd {
	c.timers = append(c.timers, timer{c.now.Add(d), msg})
	return nil
}

// harness drives a Model synchronously: commands run at once and their
// messages are fed back, as Bubble Tea would.
type harness struct {
	t     *testing.T
	m     *Model
	kodi  *koditest.Music
	clock *fakeClock
	quit  bool
}

func newHarness(t *testing.T, width, height int) *harness {
	t.Helper()
	h := &harness{
		t:     t,
		kodi:  koditest.NewMusic(),
		clock: &fakeClock{now: time.Date(2026, 11, 9, 20, 0, 0, 0, time.UTC)},
	}
	h.m = New(h.kodi, h.clock)
	h.send(tea.WindowSizeMsg{Width: width, Height: height})
	h.run(h.m.Init())
	return h
}

func (h *harness) send(msg tea.Msg) {
	_, cmd := h.m.Update(msg)
	h.run(cmd)
}

func (h *harness) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			h.run(c)
		}
	case tea.QuitMsg:
		h.quit = true
	default:
		h.send(msg)
	}
}

// advance moves the clock on and fires the timers that are due.
func (h *harness) advance(d time.Duration) {
	h.clock.now = h.clock.now.Add(d)
	for {
		i := slices.IndexFunc(h.clock.timers, func(t timer) bool { return !t.at.After(h.clock.now) })
		if i < 0 {
			return
		}
		msg := h.clock.timers[i].msg
		h.clock.timers = slices.Delete(h.clock.timers, i, i+1)
		h.send(msg)
	}
}

func keyMsg(name string) tea.KeyPressMsg {
	codes := map[string]rune{
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
		"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown, "home": tea.KeyHome, "end": tea.KeyEnd,
		"enter": tea.KeyEnter, "backspace": tea.KeyBackspace, "tab": tea.KeyTab, "esc": tea.KeyEscape,
	}
	switch {
	case name == "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case name == "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case codes[name] != 0:
		return tea.KeyPressMsg{Code: codes[name]}
	}
	return tea.KeyPressMsg{Code: []rune(name)[0], Text: name}
}

func (h *harness) press(keys ...string) {
	h.t.Helper()
	for _, k := range keys {
		msg := keyMsg(k)
		if msg.String() != k {
			h.t.Fatalf("key %q reads as %q", k, msg.String())
		}
		h.send(msg)
	}
}

// screen returns the screen as plain text lines.
func (h *harness) screen() []string {
	return strings.Split(ansi.Strip(h.m.render()), "\n")
}

func (h *harness) contains(text string) bool {
	return strings.Contains(strings.Join(h.screen(), "\n"), text)
}

func (h *harness) want(texts ...string) {
	h.t.Helper()
	for _, text := range texts {
		if !h.contains(text) {
			h.t.Errorf("screen lacks %q:\n%s", text, strings.Join(h.screen(), "\n"))
		}
	}
}

func (h *harness) wantNot(texts ...string) {
	h.t.Helper()
	for _, text := range texts {
		if h.contains(text) {
			h.t.Errorf("screen shows %q:\n%s", text, strings.Join(h.screen(), "\n"))
		}
	}
}

// selected returns the text of the selected row.
func (h *harness) selected() string {
	for _, l := range h.screen() {
		if strings.HasPrefix(l, "▌") {
			return strings.TrimSpace(strings.TrimPrefix(l, "▌"))
		}
	}
	return ""
}

func (h *harness) count(method string) int {
	n := 0
	for _, m := range h.kodi.Methods() {
		if m == method {
			n++
		}
	}
	return n
}

func TestWaitingForKodi(t *testing.T) {
	music := koditest.NewMusic()
	music.Handle("AudioLibrary.GetArtists", func(json.RawMessage) (any, error) {
		return nil, &kodi.UnavailableError{Addr: "127.0.0.1:9090", Err: errString("connection refused")}
	})
	h := &harness{t: t, kodi: music, clock: &fakeClock{}}
	h.m = New(music, h.clock)
	h.send(tea.WindowSizeMsg{Width: 80, Height: 10})
	h.run(h.m.Init())
	h.want("waiting for Kodi…", "connection refused", "■ stopped")

	h.advance(time.Second)
	if n := h.count("AudioLibrary.GetArtists"); n != 2 {
		t.Errorf("GetArtists called %d times, want 2 (a retry after a second)", n)
	}
	music.Reply("AudioLibrary.GetArtists", map[string]any{"artists": koditest.Artists()})
	h.advance(time.Second)
	h.want("Library ▸ Artists", "Brian Eno")
	h.wantNot("waiting")
}

func TestQuitWhileWaiting(t *testing.T) {
	music := koditest.NewMusic()
	music.Handle("AudioLibrary.GetArtists", func(json.RawMessage) (any, error) {
		return nil, &kodi.UnavailableError{Addr: "x", Err: errString("refused")}
	})
	h := &harness{t: t, kodi: music, clock: &fakeClock{}}
	h.m = New(music, h.clock)
	h.run(h.m.Init())
	h.press("q")
	if !h.quit {
		t.Error("q did not quit")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestBrowseAndPlayTrack(t *testing.T) {
	h := newHarness(t, 80, 12)
	h.want("Library ▸ Artists", "Brian Eno", "David Byrne", "Żywiołak", "■ stopped", "? help")
	h.wantNot("No Albums")

	h.press("enter") // Brian Eno
	h.want("Library ▸ Artists ▸ Brian Eno", "1975  Another Day")
	h.press("down", "right") // Ambient 1
	h.want("Brian Eno ▸ Ambient 1", "01  1/1", "17:01")
	h.press("down")
	if got := h.selected(); !strings.HasPrefix(got, "02  2/1") {
		t.Errorf("selected %q", got)
	}
	h.kodi.Reset()
	h.press("enter")
	if q := h.kodi.Queue(); !slices.Equal(q, []int{101, 102, 103, 104}) {
		t.Errorf("queue = %v", q)
	}
	if playing, _, pos := h.kodi.Playing(); !playing || pos != 1 {
		t.Errorf("playing %v at %d, want position 1", playing, pos)
	}
	h.want("▶ 03:12/08:55  Brian Eno – 2/1  [2/4]")

	// Back keeps each level's cursor.
	h.press("left")
	h.want("Library ▸ Artists ▸ Brian Eno")
	if got := h.selected(); !strings.Contains(got, "Ambient 1") {
		t.Errorf("selected %q after going back", got)
	}
	h.press("backspace", "left")
	h.want("Library ▸ Artists")
	if got := h.selected(); !strings.HasPrefix(got, "Brian Eno") {
		t.Errorf("selected %q", got)
	}
}

func TestEnterOnAlbumPlaysFromStart(t *testing.T) {
	h := newHarness(t, 80, 12)
	h.press("enter", "down", "enter") // Brian Eno, Ambient 1
	if q := h.kodi.Queue(); !slices.Equal(q, []int{101, 102, 103, 104}) {
		t.Errorf("queue = %v", q)
	}
	if _, _, pos := h.kodi.Playing(); pos != 0 {
		t.Errorf("position %d, want 0", pos)
	}
	h.want("Library ▸ Artists ▸ Brian Eno", "▶ 03:12/17:01  Brian Eno – 1/1  [1/4]")
}

func TestPageAndEnds(t *testing.T) {
	h := newHarness(t, 80, 5) // two list rows
	h.press("enter")          // five albums
	h.press("pgdown")
	if got := h.selected(); !strings.Contains(got, "Music for Films") {
		t.Errorf("after PgDn: %q", got)
	}
	h.press("end")
	if got := h.selected(); !strings.Contains(got, "another green world") {
		t.Errorf("after End: %q", got)
	}
	h.press("pgup", "home")
	if got := h.selected(); !strings.Contains(got, "Another Day") {
		t.Errorf("after Home: %q", got)
	}
}

func TestPlaybackKeys(t *testing.T) {
	h := newHarness(t, 80, 12)
	h.press("enter", "down", "enter")
	h.kodi.Reset()

	h.press("space")
	if _, paused, _ := h.kodi.Playing(); !paused {
		t.Error("space did not pause")
	}
	h.want("⏸ 03:12/17:01")
	h.press("space", "n")
	if _, paused, pos := h.kodi.Playing(); paused || pos != 1 {
		t.Errorf("paused %v at %d", paused, pos)
	}
	h.press("p")
	if _, _, pos := h.kodi.Playing(); pos != 0 {
		t.Errorf("position %d after p", pos)
	}
	h.press("s")
	if playing, _, _ := h.kodi.Playing(); playing {
		t.Error("s did not stop")
	}
	h.want("■ stopped")
	for _, method := range []string{"Player.PlayPause", "Player.GoTo", "Player.Stop"} {
		if h.count(method) == 0 {
			t.Errorf("no %s call", method)
		}
	}
}

func TestVolumePopup(t *testing.T) {
	h := newHarness(t, 80, 12)
	h.wantNot("Volume") // the first reading shows nothing
	h.press("+")
	h.want("Volume", "55%")
	if v := h.kodi.Volume(); v.Level != 55 {
		t.Errorf("volume %d", v.Level)
	}
	h.press("=", "-", "-")
	h.want("50%")
	h.advance(1999 * time.Millisecond)
	h.want("Volume")
	h.advance(time.Millisecond)
	h.wantNot("Volume")

	// A change made in Kodi shows on the next refresh.
	h.kodi.SetVolume(kodi.Volume{Level: 70})
	h.advance(time.Second)
	h.want("Volume", "70%")
	h.advance(2 * time.Second)
	h.wantNot("Volume")
	h.kodi.SetVolume(kodi.Volume{Level: 70, Muted: true})
	h.advance(time.Second)
	h.want("Volume", "muted")
}

func TestHelp(t *testing.T) {
	h := newHarness(t, 80, 30)
	h.press("?")
	for _, k := range keyTable() {
		h.want(k.action)
		if k.keys != "" {
			h.want(k.keys)
		}
	}
	h.want("Author: Piotr Święs", "https://github.com/pswies/kodipilot")

	h.kodi.Reset()
	h.press("n", "enter", "tab", "+")
	if calls := h.kodi.Methods(); len(calls) > 0 {
		t.Errorf("keys not ignored while help is open: %v", calls)
	}
	h.press("q")
	if h.quit {
		t.Fatal("q quit while help was open")
	}
	h.wantNot("Author")
	h.press("h")
	h.want("Author")
	h.press("esc")
	h.wantNot("Author")
	h.press("?", "?")
	h.wantNot("Author")
	h.press("h", "h")
	h.wantNot("Author")
	h.press("q")
	if !h.quit {
		t.Error("q did not quit")
	}
}

func TestCtrlCQuitsEvenInHelp(t *testing.T) {
	h := newHarness(t, 80, 30)
	h.press("?", "ctrl+c")
	if !h.quit {
		t.Error("ctrl+c did not quit")
	}
}

func TestKeysHelp(t *testing.T) {
	text := KeysHelp()
	for _, k := range keyTable() {
		if !strings.Contains(text, k.action) {
			t.Errorf("--help lacks %q", k.action)
		}
	}
	for _, about := range aboutLines() {
		if !strings.Contains(text, about) {
			t.Errorf("--help lacks %q", about)
		}
	}
}

func TestPlayerView(t *testing.T) {
	h := newHarness(t, 80, 12)
	h.press("tab")
	h.want("Player ▸ Queue", "queue is empty — pick something in the library (Tab)")
	h.press("tab")
	h.want("Library ▸ Artists")

	h.press("enter", "down", "right", "down", "down", "enter") // Ambient 1 from track 3
	h.kodi.SetModes("all", true)
	h.press("tab")
	h.want("Player ▸ Queue", "3/4 · repeat all · shuffle on", "1  1/1", "4  2/2")
	if got := h.selected(); !strings.HasPrefix(got, "▶ 3  1/2") {
		t.Errorf("selected %q, want the playing item", got)
	}

	// The cursor follows the playing item while it is on it.
	h.press("n")
	if got := h.selected(); !strings.HasPrefix(got, "▶ 4  2/2") {
		t.Errorf("selected %q after next", got)
	}

	h.press("home", "down")
	h.kodi.Reset()
	h.press("enter")
	if !slices.Contains(h.kodi.Calls(), koditest.Call{Method: "Player.GoTo", Params: `{"playerid":0,"to":1}`}) {
		t.Errorf("calls = %v, want Player.GoTo to 1", h.kodi.Calls())
	}
	if got := h.selected(); !strings.HasPrefix(got, "▶ 2  2/1") {
		t.Errorf("selected %q", got)
	}
	h.press("space")
	h.want("⏸ 2  2/1")

	// The library keeps its cursor.
	h.press("tab")
	h.want("Library ▸ Artists ▸ Brian Eno ▸ Ambient 1")
	if got := h.selected(); !strings.HasPrefix(got, "03  1/2") {
		t.Errorf("library selection %q", got)
	}
}

func TestErrorsFlash(t *testing.T) {
	h := newHarness(t, 80, 12)
	h.press("enter", "down", "enter")
	h.kodi.Handle("Player.PlayPause", func(json.RawMessage) (any, error) {
		return nil, &kodi.Error{Code: -32100, Message: "Failed to execute method."}
	})
	h.press("space")
	h.want("Kodi: Player.PlayPause: Failed to execute method.")
	h.wantNot("? help")
	h.advance(5 * time.Second)
	h.want("? help")
	h.wantNot("Failed")
}

func TestLostConnection(t *testing.T) {
	h := newHarness(t, 80, 12)
	h.press("enter", "down", "enter", "+")
	h.advance(3 * time.Second)
	h.kodi.Handle("Player.GetActivePlayers", func(json.RawMessage) (any, error) {
		return nil, &kodi.UnavailableError{Addr: "127.0.0.1:9090", Err: errString("connection refused")}
	})
	h.advance(time.Second)
	h.want("■ stopped", "Kodi unavailable: 127.0.0.1:9090: connection refused")

	// Back again: the first reading after the gap shows no pop-up.
	h.kodi.Reply("Player.GetActivePlayers", []map[string]any{{"playerid": 0, "type": "audio"}})
	h.advance(time.Second)
	h.want("▶ 03:12")
	h.wantNot("Volume")
}

// TestSizes renders every small terminal size, with and without pop-ups:
// nothing panics and nothing exceeds the terminal.
func TestSizes(t *testing.T) {
	h := newHarness(t, 80, 24)
	h.press("end", "enter", "end") // Żywiołak's albums; the last has a long wide-character title
	h.kodi.Reply("AudioLibrary.GetSongs", map[string]any{"songs": []kodi.Song{
		{ID: 1, Title: "Zażółć gęślą jaźń\tz tabulatorem", Track: 1, Duration: 4000, DisplayArtist: "Gość"},
		{ID: 2, Title: "日本語の曲", Track: 2, Duration: 61},
	}})
	h.press("right", "enter")

	check := func(name string) {
		for height := 0; height <= 8; height++ {
			for width := 0; width <= 30; width++ {
				h.send(tea.WindowSizeMsg{Width: width, Height: height})
				out := h.m.render()
				if out == "" {
					continue
				}
				lines := strings.Split(out, "\n")
				if len(lines) > height {
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
	check("tracks")
	h.press("tab")
	check("queue")
	h.press("+")
	check("volume")
	h.press("?")
	check("help")
}

func TestTextHelpers(t *testing.T) {
	texts := []string{"Zażółć gęślą jaźń — Żywiołak", "日本語のアルバム", "Sigur Rós", "a\tb\nc", "é", ""}
	for _, text := range texts {
		for w := 0; w < 40; w++ {
			if got := width(truncate(clean(text), w)); got > w {
				t.Errorf("truncate(%q, %d) is %d wide", text, w, got)
			}
			if got := width(pad(clean(text), w)); got != w {
				t.Errorf("pad(%q, %d) is %d wide", text, w, got)
			}
		}
	}
	for text, want := range map[string]string{"Zażółć gęślą": "Zażół…", "Zażółć": "Zażółć", "日本語": "日…"} {
		w := 6
		if text == "日本語" {
			w = 4
		}
		if got := truncate(text, w); got != want {
			t.Errorf("truncate(%q, %d) = %q, want %q", text, w, got, want)
		}
	}
	for d, want := range map[time.Duration]string{0: "00:00", 340 * time.Second: "05:40", 3723 * time.Second: "1:02:03"} {
		if got := formatDuration(d); got != want {
			t.Errorf("formatDuration(%v) = %s, want %s", d, got, want)
		}
	}
}
