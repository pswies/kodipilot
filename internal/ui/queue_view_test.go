package ui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pswies/kodipilot/internal/kodi/koditest"
)

// queueHarness plays Ambient 1 from track 3 and shows the queue view.
func queueHarness(t *testing.T) *harness {
	h := newHarness(t, 80, 12)
	h.press("enter", "down", "enter", "down", "down", "p")
	h.press("tab")
	return h
}

func (h *harness) wantQueue(want ...int) {
	h.t.Helper()
	if q := h.kodi.Queue(); !slices.Equal(q, want) {
		h.t.Errorf("Kodi's queue = %v, want %v", q, want)
	}
}

func (h *harness) wantSelected(prefix string) {
	h.t.Helper()
	if got := h.selected(); !strings.HasPrefix(got, prefix) {
		h.t.Errorf("selected %q, want %q…", got, prefix)
	}
}

func TestQueueView(t *testing.T) {
	h := newHarness(t, 80, 12)
	h.press("tab")
	h.want("Library │ Queue", "queue is empty — pick something in the library (Tab)")
	h.press("tab")
	h.want("Library │ Queue   Artists")

	h.press("enter", "down", "enter", "down", "down", "p")
	h.kodi.SetModes("all", true)
	h.press("tab")
	h.want("Library │ Queue", "3/4 · repeat all · shuffle on", "1  1/1", "4  2/2", "⟳ all  ⤮")
	h.wantSelected("▶ 3  1/2") // the cursor starts on the playing item

	// The cursor follows the playing item while it is on it.
	h.press(".")
	h.wantSelected("▶ 4  2/2")

	for _, key := range []string{"enter", "p"} {
		h.press("home", "down")
		h.kodi.Reset()
		h.press(key)
		if !slices.Contains(h.kodi.Calls(), koditest.Call{Method: "Player.GoTo", Params: `{"playerid":0,"to":1}`}) {
			t.Errorf("%s: calls = %v, want Player.GoTo to 1", key, h.kodi.Calls())
		}
		h.wantSelected("▶ 2  2/1")
	}
	h.press("space")
	h.want("⏸ 2  2/1")

	// The library keeps its cursor.
	h.press("tab")
	h.want("Library │ Queue   Artists ▸ Brian Eno ▸ Ambient 1")
	h.wantSelected("03  1/2")
}

func TestQueueViewSeeks(t *testing.T) {
	h := queueHarness(t)
	h.press("right", "right", "left")
	h.want("▶ 03:42/11:52")
	if !slices.Contains(h.kodi.Calls(), koditest.Call{
		Method: "Player.Seek", Params: `{"playerid":0,"value":{"step":"smallbackward"}}`,
	}) {
		t.Errorf("no backward seek in %v", h.kodi.Calls())
	}
}

func TestQueueViewStartsWhenStopped(t *testing.T) {
	h := queueHarness(t)
	h.press("x", "home", "enter")
	if playing, _, pos := h.kodi.Playing(); !playing || pos != 0 {
		t.Errorf("playing %v at %d, want position 0", playing, pos)
	}
}

func TestRemove(t *testing.T) {
	h := queueHarness(t) // playing 103, the third item
	h.press("delete")
	h.want("The playing item can't be removed")
	h.wantQueue(101, 102, 103, 104)

	h.press("end", "d")
	h.wantQueue(101, 102, 103)
	h.wantSelected("▶ 3  1/2") // the last item gone, the cursor moves up to the playing one

	h.press("home", "delete")
	h.wantQueue(102, 103)
	h.wantSelected("1  2/1") // the next item takes the cursor's place
	h.want("2/2 ·")          // the playing item moved up
}

func TestSwap(t *testing.T) {
	h := queueHarness(t)
	h.press("home", "shift+down")
	h.wantQueue(102, 101, 103, 104)
	h.wantSelected("2  1/1") // the cursor follows the item
	h.press("J")
	h.wantQueue(102, 103, 101, 104)
	h.wantSelected("3  1/1")
	if _, _, pos := h.kodi.Playing(); pos != 1 {
		t.Errorf("playing position %d, want 1 (the playing item moved up)", pos)
	}
	h.want("▶ 2  1/2")

	h.press("K", "shift+up", "shift+up") // the last is a no-op at the top
	h.wantQueue(101, 102, 103, 104)
	h.wantSelected("1  1/1")

	h.press("end", "shift+down") // a no-op at the bottom
	h.wantQueue(101, 102, 103, 104)
	h.wantSelected("4  2/2")
}

func TestClear(t *testing.T) {
	h := queueHarness(t)
	h.press("c")
	h.want("Clear the queue?", "y / n")
	h.kodi.Reset()
	h.press(".", "x", "c", "enter") // ignored
	if calls := h.kodi.Methods(); len(calls) > 0 {
		t.Errorf("keys not ignored while asking: %v", calls)
	}
	h.press("n")
	h.wantNot("Clear the queue?")
	h.wantQueue(101, 102, 103, 104)
	h.press("c", "esc")
	h.wantQueue(101, 102, 103, 104)

	h.press("c", "y")
	h.wantQueue()
	h.want("Queue cleared", "queue is empty")
	h.press("c") // nothing to clear: no question
	h.wantNot("Clear the queue?")
}

func TestQueueCursorKeepsItem(t *testing.T) {
	h := queueHarness(t)
	h.press("end", "enter", "home", "down", "down") // playing 104; cursor on 103
	h.wantSelected("3  1/2")
	// Someone else removes the first item.
	if _, err := h.kodi.Serve("Playlist.Remove", map[string]int{"playlistid": 0, "position": 0}); err != nil {
		t.Fatal(err)
	}
	h.advance(pollInterval)
	h.wantSelected("2  1/2")
}

func (h *harness) header() string {
	return strings.Split(h.m.render(), "\n")[0]
}

func TestHeaderTabs(t *testing.T) {
	h := newHarness(t, 80, 12)
	h.press("enter")
	if got := strings.TrimSpace(h.screen()[0]); !strings.HasPrefix(got, "Library │ Queue   Artists ▸ Brian Eno") || !strings.HasSuffix(got, "1/5") {
		t.Errorf("library header %q", got)
	}
	strong, dim := h.m.theme.strong, h.m.theme.dim
	if raw := h.header(); !strings.Contains(raw, strong.Render("Library")) || !strings.Contains(raw, dim.Render("Queue")) {
		t.Errorf("Library is not the highlighted tab: %q", raw)
	}

	h.press("left", "p", "tab")
	got := strings.TrimSpace(h.screen()[0])
	if !strings.HasPrefix(got, "Library │ Queue") || strings.Contains(got, "▸") || strings.Contains(got, "Artists") {
		t.Errorf("queue header %q", got)
	}
	if !strings.HasSuffix(got, "1/8 · repeat off · shuffle off") {
		t.Errorf("queue header lacks the position: %q", got)
	}
	if raw := h.header(); !strings.Contains(raw, dim.Render("Library")) || !strings.Contains(raw, strong.Render("Queue")) {
		t.Errorf("Queue is not the highlighted tab: %q", raw)
	}
}

func TestHeaderNarrow(t *testing.T) {
	h := newHarness(t, 40, 12)
	for width, want := range map[int]string{
		40: " Library │ Queue   Artists          1/3 ", // full tabs
		25: " L │ Q   Artists     1/3 ",                // short tabs before the breadcrumb is cut
		12: " L │ Q   Ar…",                             // then the position goes and the breadcrumb is cut
	} {
		h.send(tea.WindowSizeMsg{Width: width, Height: 12})
		if got := h.screen()[0]; got != want {
			t.Errorf("width %d: %q, want %q", width, got, want)
		}
	}
	for width := range 41 {
		h.send(tea.WindowSizeMsg{Width: width, Height: 12})
		if got := ansi.StringWidth(h.header()); got > width {
			t.Errorf("width %d: header is %d wide", width, got)
		}
	}
}
