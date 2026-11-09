package ui

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pswies/kodipilot/internal/kodi"
	"github.com/pswies/kodipilot/internal/kodi/koditest"
)

type errString string

func (e errString) Error() string { return string(e) }

func unavailable(json.RawMessage) (any, error) {
	return nil, &kodi.UnavailableError{Addr: "127.0.0.1:9090", Err: errString("connection refused")}
}

func waitingHarness(t *testing.T) *harness {
	music := koditest.NewMusic()
	music.Handle("AudioLibrary.GetArtists", unavailable)
	h := &harness{t: t, kodi: music, clock: &fakeClock{}}
	h.m = New(music, h.clock, Options{})
	h.send(tea.WindowSizeMsg{Width: 80, Height: 10})
	h.run(h.m.Init())
	return h
}

func TestWaitingForKodi(t *testing.T) {
	h := waitingHarness(t)
	h.want("waiting for Kodi…", "connection refused", "■ stopped")

	h.advance(time.Second)
	if n := h.count("AudioLibrary.GetArtists"); n != 2 {
		t.Errorf("GetArtists called %d times, want 2 (a retry after a second)", n)
	}
	h.kodi.Reply("AudioLibrary.GetArtists", map[string]any{"artists": koditest.Artists()})
	h.advance(time.Second)
	h.want("Library │ Queue   Artists", "Brian Eno")
	h.wantNot("waiting")
}

func TestQuitKeys(t *testing.T) {
	for _, keys := range [][]string{{"Q"}, {"ctrl+c"}, {"esc"}, {"enter", "esc", "esc"}, {"?", "ctrl+c"}} {
		h := newHarness(t, 80, 12)
		h.press(keys[:len(keys)-1]...)
		if h.quit {
			t.Fatalf("%v: quit early", keys)
		}
		h.press(keys[len(keys)-1])
		if !h.quit {
			t.Errorf("%v did not quit", keys)
		}
	}
	for _, key := range []string{"Q", "esc"} {
		h := waitingHarness(t)
		h.press(key)
		if !h.quit {
			t.Errorf("%s did not quit while waiting for Kodi", key)
		}
	}
	h := newHarness(t, 80, 12)
	h.press("tab", "esc", "q")
	if h.quit {
		t.Error("Esc or q quit in the queue view")
	}
}

func TestBrowse(t *testing.T) {
	h := newHarness(t, 80, 12)
	h.want("Library │ Queue   Artists", "Brian Eno", "David Byrne", "Żywiołak", "■ stopped", "? help")
	h.wantNot("No Albums")

	h.press("enter") // Brian Eno
	h.want("Library │ Queue   Artists ▸ Brian Eno", "1975  Another Day")
	h.press("down", "enter") // Ambient 1: Enter opens an album, as in Kodi
	h.want("Brian Eno ▸ Ambient 1", "01  1/1", "17:01")
	if playing, _, _ := h.kodi.Playing(); playing {
		t.Error("Enter on an album started playback")
	}

	// Back keeps each level's cursor.
	h.press("left")
	if got := h.selected(); !strings.Contains(got, "Ambient 1") {
		t.Errorf("selected %q after going back", got)
	}
	h.press("right", "backspace", "esc")
	h.want("Library │ Queue   Artists")
	if got := h.selected(); !strings.HasPrefix(got, "Brian Eno") {
		t.Errorf("selected %q", got)
	}
}

func TestEnterOnTrackPlaysAlbumFromIt(t *testing.T) {
	for _, key := range []string{"enter", "right"} {
		h := newHarness(t, 80, 12)
		h.press("enter", "down", "enter", "down", key)
		if q := h.kodi.Queue(); !slices.Equal(q, []int{101, 102, 103, 104}) {
			t.Errorf("%s: queue = %v", key, q)
		}
		if playing, _, pos := h.kodi.Playing(); !playing || pos != 1 {
			t.Errorf("%s: playing %v at %d, want position 1", key, playing, pos)
		}
		h.want("▶ 03:12/08:55  Brian Eno – 2/1  [2/4]")
	}
}

func TestPlayKey(t *testing.T) {
	h := newHarness(t, 80, 12)
	h.press("p") // Brian Eno: every album, in display order
	if q := h.kodi.Queue(); !slices.Equal(q, []int{101, 102, 103, 104, 199, 200, 201, 202}) {
		t.Errorf("artist: queue = %v", q)
	}
	h.press("enter", "down", "p") // Ambient 1 from track 1
	if q := h.kodi.Queue(); !slices.Equal(q, []int{101, 102, 103, 104}) {
		t.Errorf("album: queue = %v", q)
	}
	h.want("▶ 03:12/17:01  Brian Eno – 1/1  [1/4]")
	h.press("enter", "end", "p") // the album from its last track
	if _, _, pos := h.kodi.Playing(); pos != 3 {
		t.Errorf("track: position %d, want 3", pos)
	}
}

func TestQueueKey(t *testing.T) {
	h := newHarness(t, 80, 12)
	h.press("down", "q") // David Byrne
	h.want("Queued 4 tracks: David Byrne")
	h.press("up", "enter", "down", "q") // Ambient 1
	h.want("Queued 4 tracks: Ambient 1")
	h.press("enter", "down", "q") // track 2/1
	h.want("Queued 1 track: 2/1")
	want := []int{199, 200, 201, 202, 101, 102, 103, 104, 102}
	if q := h.kodi.Queue(); !slices.Equal(q, want) {
		t.Errorf("queue = %v, want %v", q, want)
	}
	if playing, _, _ := h.kodi.Playing(); playing {
		t.Error("q started playback")
	}
	if slices.Contains(h.kodi.Methods(), "Playlist.Clear") {
		t.Error("q cleared the queue")
	}
	h.advance(flashDuration)
	h.wantNot("Queued")
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
	h.press("enter", "down", "p")

	h.press("space")
	if _, paused, _ := h.kodi.Playing(); !paused {
		t.Error("Space did not pause")
	}
	h.want("⏸ 03:12/17:01")
	h.press("space", ".")
	if _, paused, pos := h.kodi.Playing(); paused || pos != 1 {
		t.Errorf("paused %v at %d", paused, pos)
	}
	h.press(",")
	if _, _, pos := h.kodi.Playing(); pos != 0 {
		t.Errorf("position %d after ,", pos)
	}
	h.press("shift+right", "shift+right", "shift+left")
	h.want("▶ 03:42/17:01")
	h.press("x")
	if playing, _, _ := h.kodi.Playing(); playing {
		t.Error("x did not stop")
	}
	h.want("■ stopped")
}

func TestRepeatShuffleMute(t *testing.T) {
	h := newHarness(t, 80, 12)
	h.press("enter", "down", "p")
	h.wantNot("⟳", "⤮", "muted")
	h.press("r")
	h.want("⟳ all")
	h.press("r")
	h.want("⟳ one")
	h.press("r")
	h.wantNot("⟳")
	h.press("s")
	h.want("⤮")
	h.press("s")
	h.wantNot("⤮")

	h.press("f8")
	h.want("Volume  muted") // the pop-up
	h.advance(popupDuration)
	h.want("muted") // the status line
	h.press("m")
	h.want("Volume ▕", "50%")
	h.advance(popupDuration)
	h.wantNot("muted")
	if v := h.kodi.Volume(); v.Muted {
		t.Error("still muted in Kodi")
	}
}

func TestNothingPlaying(t *testing.T) {
	h := newHarness(t, 80, 12)
	for _, key := range []string{"r", "s", "shift+right", "shift+left", "space", "x", ".", ","} {
		h.kodi.Reset()
		h.press(key)
		h.want("nothing is playing")
		for _, method := range h.kodi.Methods() {
			if strings.HasPrefix(method, "Player.") && method != "Player.GetActivePlayers" {
				t.Errorf("%s: called %s", key, method)
			}
		}
		h.advance(flashDuration)
	}
	h.press("m") // mute works without a player
	h.want("muted")
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
	h := newHarness(t, 90, 40)
	h.press("?")
	for _, section := range keyTable() {
		h.want(section.title)
		for _, row := range section.rows {
			h.want(row.label, row.action)
		}
	}
	h.want("Author: Piotr Święs", "https://github.com/pswies/kodipilot")
	h.wantNot("scroll")

	h.kodi.Reset()
	h.press(".", "enter", "tab", "+", "x", "p", "m", "esc")
	if calls := h.kodi.Methods(); len(calls) > 0 {
		t.Errorf("keys not ignored while help is open: %v", calls)
	}
	h.wantNot("Author") // Esc closed it
	for _, keys := range [][]string{{"?", "?"}, {"h", "h"}, {"?", "q"}, {"h", "Q"}} {
		h.press(keys[0])
		h.want("Author")
		h.press(keys[1])
		h.wantNot("Author")
		if h.quit {
			t.Fatalf("%v quit", keys)
		}
	}
}

func TestHelpScrolls(t *testing.T) {
	h := newHarness(t, 80, 20)
	h.press("?")
	h.want("Everywhere", "↑↓ scroll")
	h.wantNot("Author")
	h.press("end")
	h.want("Author", "https://github.com/pswies/kodipilot")
	h.wantNot("Everywhere")
	h.press("pgup", "home")
	h.want("Everywhere")
	h.press("down")
	h.wantNot("Everywhere")
	h.press("up")
	h.want("Everywhere")
}

func TestKeysHelp(t *testing.T) {
	text := KeysHelp()
	for _, section := range keyTable() {
		for _, row := range section.rows {
			if !strings.Contains(text, row.label) || !strings.Contains(text, row.action) {
				t.Errorf("--help lacks %q %q", row.label, row.action)
			}
		}
	}
	for _, about := range aboutLines() {
		if !strings.Contains(text, about) {
			t.Errorf("--help lacks %q", about)
		}
	}
}

func TestReadmeShowsKeyTable(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), KeysMarkdown()) {
		t.Errorf("README.md lacks the key table; it should contain:\n%s", KeysMarkdown())
	}
}

// TestKeyBindings checks that every bound key is a name Bubble Tea gives to
// real terminal input and that no key is bound twice in a view.
func TestKeyBindings(t *testing.T) {
	seen := map[scope]map[string]bool{}
	for _, section := range keyTable() {
		if seen[section.scope] == nil {
			seen[section.scope] = map[string]bool{}
		}
		for _, row := range section.rows {
			for _, b := range row.keys {
				input, ok := keyBytes[b.key]
				if !ok {
					input = b.key
				}
				if got := decodeKey(t, input).String(); got != b.key {
					t.Errorf("bound key %q arrives as %q", b.key, got)
				}
				if seen[section.scope][b.key] || section.scope != everywhere && seen[everywhere][b.key] {
					t.Errorf("key %q bound twice", b.key)
				}
				seen[section.scope][b.key] = true
			}
		}
	}
}

// TestKeyEncodings checks that the encodings of other terminals (kitty
// keyboard protocol, xterm modifyOtherKeys, SS3 arrows) arrive under the
// bound names.
func TestKeyEncodings(t *testing.T) {
	for input, want := range map[string]string{
		"\x1b[113;2u":   "Q",
		"\x1b[27;2;81~": "Q",
		"\x1b[107;2u":   "K",
		"\x1b[106;2u":   "J",
		"\x1b[99;5u":    "ctrl+c",
		"\x1bOD":        "left",
		"\x1bOC":        "right",
		"\x1b[3;1~":     "delete",
		"\x1b[1;2A":     "shift+up",
	} {
		if got := decodeKey(t, input).String(); got != want {
			t.Errorf("%q arrives as %q, want %q", input, got, want)
		}
	}
}

func TestErrorsFlash(t *testing.T) {
	h := newHarness(t, 80, 12)
	h.press("enter", "down", "p")
	h.kodi.Handle("Player.PlayPause", func(json.RawMessage) (any, error) {
		return nil, &kodi.Error{Code: -32100, Message: "Failed to execute method."}
	})
	h.press("space")
	h.want("Kodi: Player.PlayPause: Failed to execute method.")
	h.wantNot("? help")
	if !h.m.flashErr {
		t.Error("a Kodi error is not flashed as an error")
	}
	h.advance(5 * time.Second)
	h.want("? help")
	h.wantNot("Failed")
}

func TestLostConnection(t *testing.T) {
	h := newHarness(t, 80, 12)
	h.press("enter", "down", "p", "+")
	h.advance(3 * time.Second)
	h.kodi.Handle("Player.GetActivePlayers", unavailable)
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
	h.press("right", "p", "r", "s", "q")

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
	h.press("c")
	check("clear question")
	h.press("n", "m")
	check("muted")
	h.press("?")
	check("help")
	h.press("end")
	check("help scrolled")
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
