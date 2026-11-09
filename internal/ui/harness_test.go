package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

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
	h.m = New(h.kodi, h.clock, Options{})
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

// keyBytes are the bytes terminals send for the named keys (xterm style);
// printable keys send themselves.
var keyBytes = map[string]string{
	"up": "\x1b[A", "down": "\x1b[B", "right": "\x1b[C", "left": "\x1b[D",
	"shift+up": "\x1b[1;2A", "shift+down": "\x1b[1;2B", "shift+right": "\x1b[1;2C", "shift+left": "\x1b[1;2D",
	"pgup": "\x1b[5~", "pgdown": "\x1b[6~", "home": "\x1b[H", "end": "\x1b[F",
	"delete": "\x1b[3~", "f8": "\x1b[19~", "enter": "\r", "tab": "\t", "shift+tab": "\x1b[Z",
	"backspace": "\x7f", "esc": "\x1b", "space": " ", "ctrl+c": "\x03", "f5": "\x1b[15~", "ctrl+r": "\x12",
}

// decodeKey decodes terminal input into the key message Bubble Tea would
// deliver.
func decodeKey(t *testing.T, input string) tea.KeyPressMsg {
	t.Helper()
	var decoder uv.EventDecoder
	n, event := decoder.Decode([]byte(input))
	key, ok := event.(uv.KeyPressEvent)
	if !ok || n != len(input) {
		t.Fatalf("%q decodes to %#v (%d bytes)", input, event, n)
	}
	return tea.KeyPressMsg(key)
}

// press sends keys, named as Bubble Tea names them, decoded from the bytes a
// terminal sends.
func (h *harness) press(keys ...string) {
	h.t.Helper()
	for _, k := range keys {
		input, ok := keyBytes[k]
		if !ok {
			input = k
		}
		msg := decodeKey(h.t, input)
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
