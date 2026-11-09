// Package ui is kodipilot's Bubble Tea model: a library browser (album
// artists → albums → tracks) and a player view of Kodi's play queue, with a
// status line, a help pop-up and a volume pop-up.
//
// All Kodi calls run as commands, outside Update, and report back as
// messages; timers go through a Clock so the model can be tested without a
// terminal or real time.
package ui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/pswies/kodipilot/internal/kodi"
)

const (
	callTimeout   = 5 * time.Second // bounds each command's Kodi calls
	pollInterval  = time.Second     // status refresh, and retry while waiting for Kodi
	popupDuration = 2 * time.Second // the volume pop-up
	flashDuration = 5 * time.Second // an error in the footer
)

type viewKind int

const (
	libraryView viewKind = iota
	playerView
)

type levelKind int

const (
	artistsLevel levelKind = iota
	albumsLevel
	tracksLevel
)

// level is one step of the library browser. Only the slice of its kind is
// set.
type level struct {
	kind    levelKind
	title   string
	artists []kodi.Artist
	albums  []kodi.Album
	songs   []kodi.Song
	album   kodi.Album // the album of a tracks level
	list    cursor
}

func (l *level) len() int { return len(l.artists) + len(l.albums) + len(l.songs) }

// cursor is the selected row of a list and the first row shown.
type cursor struct{ index, top int }

// move moves the cursor by delta within a list of n rows.
func (c *cursor) move(delta, n int) { c.index = max(0, min(c.index+delta, n-1)) }

// scroll keeps the cursor within the height rows shown of a list of n rows.
func (c *cursor) scroll(height, n int) {
	c.move(0, n)
	if height <= 0 {
		return
	}
	if c.index < c.top {
		c.top = c.index
	} else if c.index >= c.top+height {
		c.top = c.index - height + 1
	}
	c.top = max(0, min(c.top, n-height))
}

// Model is the Bubble Tea model of kodipilot.
type Model struct {
	client kodi.Client
	player *kodi.Player
	clock  Clock
	theme  theme
	dark   bool
	colors colorprofile.Profile

	width, height int
	view          viewKind
	helpOpen      bool

	lib     *kodi.Library // nil while waiting for Kodi
	waitErr error         // why Kodi is not there yet
	levels  []level

	queue        []kodi.QueueItem
	queueList    cursor
	queueShown   bool // the queue has been shown, with the cursor on the playing item
	lastPlaying  int  // the playing position at the last queue update
	status       *kodi.Status
	volume       *kodi.Volume // nil until known
	volumeUntil  time.Time    // the volume pop-up shows until then
	flash        string
	flashUntil   time.Time
	actions      int  // counts actions, so that polls that overlap one are redone
	polling      bool // a status poll is running
	pollAgain    bool // poll again once the running poll is done
	tickSequence int  // identifies the one scheduled tick that counts
}

// New returns a model that talks to Kodi through client.
func New(client kodi.Client, clock Clock) *Model {
	m := &Model{
		client:      client,
		player:      kodi.NewPlayer(client),
		clock:       clock,
		dark:        true,
		colors:      colorprofile.TrueColor,
		lastPlaying: -1,
	}
	m.theme = newTheme(m.dark, m.colors)
	return m
}

// Messages from commands and timers.
type (
	libraryMsg struct {
		lib *kodi.Library
		err error
	}
	retryMsg struct{}
	tickMsg  struct{ sequence int }
	pollMsg  struct {
		actions   int // Model.actions when the poll started
		status    *kodi.Status
		volume    kodi.Volume
		queue     []kodi.QueueItem
		withQueue bool
		err       error
	}
	songsMsg struct {
		album kodi.Album
		depth int // len(Model.levels) when the album was opened
		songs []kodi.Song
		err   error
	}
	actionMsg struct{ err error }
	volumeMsg struct {
		volume kodi.Volume
		err    error
	}
	redrawMsg struct{} // a pop-up or an error message has expired
)

// Init starts loading the library and asks for the terminal background.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.loadLibrary(), tea.RequestBackgroundColor)
}

// Update handles a message.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := m.update(msg)
	m.scroll()
	return m, cmd
}

func (m *Model) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
		m.theme = newTheme(m.dark, m.colors)
	case tea.ColorProfileMsg:
		m.colors = msg.Profile
		m.theme = newTheme(m.dark, m.colors)
	case tea.KeyPressMsg:
		return m.key(msg.String())

	case libraryMsg:
		if msg.err != nil {
			m.waitErr = msg.err
			return m.clock.After(pollInterval, retryMsg{})
		}
		m.lib, m.waitErr = msg.lib, nil
		m.levels = []level{{kind: artistsLevel, title: "Artists", artists: msg.lib.Artists()}}
		return m.refresh()
	case retryMsg:
		return m.loadLibrary()
	case tickMsg:
		if msg.sequence == m.tickSequence {
			return m.poll()
		}
	case pollMsg:
		return m.polled(msg)
	case songsMsg:
		return m.opened(msg)
	case actionMsg:
		if msg.err != nil {
			return tea.Batch(m.showError(msg.err), m.refresh())
		}
		return m.refresh()
	case volumeMsg:
		if msg.err != nil {
			return m.showError(msg.err)
		}
		return m.showVolume(msg.volume)
	}
	return nil
}

// key handles a key press.
func (m *Model) key(key string) tea.Cmd {
	if key == "ctrl+c" {
		return tea.Quit
	}
	if m.helpOpen { // only the closing keys work while help is open
		switch key {
		case "esc", "?", "h", "q", "Q":
			m.helpOpen = false
		}
		return nil
	}
	switch key {
	case "q", "Q":
		return tea.Quit
	case "?", "h":
		m.helpOpen = true
	case "tab", "shift+tab":
		if m.view == playerView {
			m.view = libraryView
			return nil
		}
		m.view = playerView
		return m.refresh() // fetch the queue now
	case "space":
		return m.act(m.player.PlayPause)
	case "n":
		return m.act(m.player.Next)
	case "p":
		return m.act(m.player.Previous)
	case "s":
		return m.act(m.player.Stop)
	case "+", "=":
		return m.changeVolume(+1)
	case "-":
		return m.changeVolume(-1)
	case "up":
		m.move(-1)
	case "down":
		m.move(+1)
	case "pgup":
		m.move(-m.listHeight())
	case "pgdown":
		m.move(+m.listHeight())
	case "home":
		m.move(-farAway)
	case "end":
		m.move(+farAway)
	case "right":
		return m.open()
	case "enter":
		return m.enter()
	case "left", "backspace":
		if m.view == libraryView && len(m.levels) > 1 {
			m.levels = m.levels[:len(m.levels)-1]
		}
	}
	return nil
}

// farAway is a cursor move that reaches either end of any list.
const farAway = 1 << 30

// current returns the library level shown, or nil while waiting for Kodi.
func (m *Model) current() *level {
	if len(m.levels) == 0 {
		return nil
	}
	return &m.levels[len(m.levels)-1]
}

// listHeight is the number of list rows on screen.
func (m *Model) listHeight() int { return max(0, m.height-3) }

func (m *Model) move(delta int) {
	if m.view == playerView {
		m.queueList.move(delta, len(m.queue))
	} else if l := m.current(); l != nil {
		l.list.move(delta, l.len())
	}
}

func (m *Model) scroll() {
	height := m.listHeight()
	m.queueList.scroll(height, len(m.queue))
	for i := range m.levels {
		m.levels[i].list.scroll(height, m.levels[i].len())
	}
}

// open opens the selected artist or album.
func (m *Model) open() tea.Cmd {
	l := m.current()
	if m.view != libraryView || l == nil || l.len() == 0 {
		return nil
	}
	switch l.kind {
	case artistsLevel:
		artist := l.artists[l.list.index]
		m.levels = append(m.levels, level{kind: albumsLevel, title: artist.Name, albums: m.lib.Albums(artist.ID)})
	case albumsLevel:
		album, depth, lib := l.albums[l.list.index], len(m.levels), m.lib
		return m.command(func(ctx context.Context) tea.Msg {
			songs, err := lib.Songs(ctx, album.ID)
			return songsMsg{album: album, depth: depth, songs: songs, err: err}
		})
	}
	return nil
}

// opened shows the tracks of an album, unless the user has moved on.
func (m *Model) opened(msg songsMsg) tea.Cmd {
	if msg.err != nil {
		return m.showError(msg.err)
	}
	l := m.current()
	if len(m.levels) == msg.depth && l.kind == albumsLevel && l.len() > 0 && l.albums[l.list.index].ID == msg.album.ID {
		m.levels = append(m.levels, level{kind: tracksLevel, title: msg.album.Title, songs: msg.songs, album: msg.album})
	}
	return nil
}

// enter opens an artist, plays an album or track, or jumps to a queue item.
func (m *Model) enter() tea.Cmd {
	if m.view == playerView {
		if len(m.queue) == 0 {
			return nil
		}
		position := m.queueList.index
		return m.act(func(ctx context.Context) error { return m.player.GoTo(ctx, position) })
	}
	l := m.current()
	if l == nil || l.len() == 0 {
		return nil
	}
	switch l.kind {
	case artistsLevel:
		return m.open()
	case albumsLevel:
		album, lib := l.albums[l.list.index], m.lib
		return m.act(func(ctx context.Context) error {
			songs, err := lib.Songs(ctx, album.ID)
			if err != nil {
				return err
			}
			return m.player.PlayAlbum(ctx, songs, 0)
		})
	default:
		songs, position := l.songs, l.list.index
		return m.act(func(ctx context.Context) error { return m.player.PlayAlbum(ctx, songs, position) })
	}
}

// command runs f with a timeout as a command.
func (m *Model) command(f func(context.Context) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		return f(ctx)
	}
}

// act runs a playback action; the status is refreshed afterwards.
func (m *Model) act(f func(context.Context) error) tea.Cmd {
	m.actions++
	return m.command(func(ctx context.Context) tea.Msg { return actionMsg{err: f(ctx)} })
}

func (m *Model) changeVolume(steps int) tea.Cmd {
	m.actions++
	return m.command(func(ctx context.Context) tea.Msg {
		volume, err := m.player.ChangeVolume(ctx, steps)
		return volumeMsg{volume: volume, err: err}
	})
}

func (m *Model) loadLibrary() tea.Cmd {
	client := m.client
	return m.command(func(ctx context.Context) tea.Msg {
		lib, err := kodi.LoadLibrary(ctx, client)
		return libraryMsg{lib: lib, err: err}
	})
}

// refresh polls the status now, instead of at the next tick.
func (m *Model) refresh() tea.Cmd {
	m.tickSequence++ // cancels the scheduled tick; the poll schedules the next one
	return m.poll()
}

// poll fetches the status, the volume and, in the player view, the queue.
func (m *Model) poll() tea.Cmd {
	if m.polling {
		m.pollAgain = true
		return nil
	}
	m.polling = true
	actions, withQueue := m.actions, m.view == playerView
	return m.command(func(ctx context.Context) tea.Msg {
		msg := pollMsg{actions: actions, withQueue: withQueue}
		if msg.status, msg.err = m.player.NowPlaying(ctx); msg.err != nil {
			return msg
		}
		if msg.volume, msg.err = m.player.Volume(ctx); msg.err != nil {
			return msg
		}
		if withQueue {
			msg.queue, msg.err = m.player.Queue(ctx)
		}
		return msg
	})
}

// polled applies a poll's results and schedules the next poll.
func (m *Model) polled(msg pollMsg) tea.Cmd {
	m.polling = false
	if m.pollAgain || msg.actions != m.actions {
		// An action ran meanwhile: these results may predate it.
		m.pollAgain = false
		return m.poll()
	}
	var cmd tea.Cmd
	if msg.err != nil {
		m.status, m.volume = nil, nil
		cmd = m.showError(msg.err)
	} else {
		m.status = msg.status
		if m.volume != nil && *m.volume != msg.volume {
			cmd = m.showVolume(msg.volume) // changed outside kodipilot
		}
		m.volume = &msg.volume
		if msg.withQueue {
			m.setQueue(msg.queue)
		}
	}
	m.tickSequence++
	return tea.Batch(cmd, m.clock.After(pollInterval, tickMsg{m.tickSequence}))
}

// setQueue shows a new queue. The cursor starts on the playing item and
// follows it while it stays there.
func (m *Model) setQueue(queue []kodi.QueueItem) {
	m.queue = queue
	playing := m.playingPosition()
	switch {
	case playing < 0:
	case !m.queueShown && len(queue) > 0:
		m.queueList.index = playing
		m.queueShown = true
	case m.queueList.index == m.lastPlaying:
		m.queueList.index = playing
	}
	m.lastPlaying = playing
	m.queueList.move(0, len(queue))
}

// playingPosition returns the queue position that plays, or -1.
func (m *Model) playingPosition() int {
	if m.status == nil {
		return -1
	}
	return m.status.Position
}

func (m *Model) showVolume(volume kodi.Volume) tea.Cmd {
	m.volume = &volume
	m.volumeUntil = m.clock.Now().Add(popupDuration)
	return m.clock.After(popupDuration, redrawMsg{})
}

func (m *Model) showError(err error) tea.Cmd {
	m.flash = errorText(err)
	m.flashUntil = m.clock.Now().Add(flashDuration)
	return m.clock.After(flashDuration, redrawMsg{})
}

func errorText(err error) string {
	var unavailable *kodi.UnavailableError
	if errors.As(err, &unavailable) {
		return "Kodi unavailable: " + err.Error()
	}
	return "Kodi: " + err.Error()
}
