// Package ui is kodipilot's Bubble Tea model: a library browser (album
// artists → albums → tracks) and a queue view of Kodi's play queue, with a
// status line and help, volume and confirmation pop-ups. The key table in
// keys.go binds every key.
//
// All Kodi calls run as commands, outside Update, and report back as
// messages; timers go through a Clock so the model can be tested without a
// terminal or real time.
package ui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"io"
	"net"
	"net/url"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/pswies/kodipilot/internal/art"
	"github.com/pswies/kodipilot/internal/artwork"
	"github.com/pswies/kodipilot/internal/kodi"
)

const (
	callTimeout    = 5 * time.Second  // bounds each command's Kodi calls
	libraryTimeout = 60 * time.Second // loading the whole library
	artistTimeout  = 30 * time.Second // an artist's songs, one call per album
	pollInterval   = time.Second      // status refresh, and retry while waiting for Kodi
	popupDuration  = 2 * time.Second  // the volume pop-up
	flashDuration  = 5 * time.Second  // an error in the footer
)

type viewKind int

const (
	libraryView viewKind = iota
	queueView
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
	rows    []int      // rows of a several-disc album: song indexes, -disc before each disc after the first
	list    cursor     // over the items; top counts rows
}

// discRows returns the rows of an album's tracks with a separator before
// each disc after the first, or nil for a single disc.
func discRows(songs []kodi.Song) []int {
	var rows []int
	for i, song := range songs {
		if i > 0 && song.Disc != songs[i-1].Disc {
			rows = append(rows, -max(1, song.Disc))
		}
		rows = append(rows, i)
	}
	if len(rows) == len(songs) {
		return nil
	}
	return rows
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

// Options configure a Model.
type Options struct {
	// Images loads cover art and artist images for the details pane; nil
	// shows none.
	Images *artwork.Loader
	// Art is how images are drawn (when the terminal has colours).
	Art art.Mode
	// Version is shown in the help.
	Version string
}

// Model is the Bubble Tea model of kodipilot.
type Model struct {
	opts   Options
	client kodi.Client
	player *kodi.Player
	clock  Clock
	theme  theme
	dark   bool
	bg, fg color.Color // the terminal's colours, nil until known
	colors colorprofile.Profile

	keys          []keySection
	width, height int
	view          viewKind
	helpOpen      bool
	helpTop       int // the first help line shown
	helpCache     struct {
		inner int
		lines []string
	}
	confirmClear bool // asking whether to clear the queue
	confirmQuit  bool // asking whether to quit
	paneHidden   bool // the user hid the details pane
	detailsFull  bool // the details fill the screen (when the pane does not fit)

	info            map[itemKey]*details // fetched details
	detailsKey      itemKey              // the item the details follow
	detailsSequence int                  // identifies the one details timer that counts
	detailsTop      int                  // the details' scroll position, in lines
	artPending      map[string]bool      // images being rendered
	artFailed       map[string]bool      // art URLs without an image
	offline         bool                 // Kodi did not answer the last poll
	video           bool                 // a video plays

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
	flashErr     bool // the flash is an error
	flashUntil   time.Time
	actions      int  // counts actions, so that polls that overlap one are redone
	polling      bool // a status poll is running
	pollAgain    bool // poll again once the running poll is done
	tickSequence int  // identifies the one scheduled tick that counts
}

// New returns a model that talks to Kodi through client.
func New(client kodi.Client, clock Clock, opts Options) *Model {
	m := &Model{
		opts:        opts,
		info:        make(map[itemKey]*details),
		artPending:  make(map[string]bool),
		artFailed:   make(map[string]bool),
		client:      client,
		keys:        keyTable(),
		player:      kodi.NewPlayer(client),
		clock:       clock,
		dark:        true,
		colors:      colorprofile.TrueColor,
		lastPlaying: -1,
	}
	m.theme = newTheme(m.dark, m.colors, nil, nil)
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
		video     bool
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
	actionMsg struct {
		info string // what the action did, to flash
		err  error
	}
	volumeMsg struct {
		volume kodi.Volume
		err    error
	}
	redrawMsg struct{} // a pop-up or an error message has expired
)

// Init starts loading the library and asks for the terminal's colours.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.loadLibrary(), tea.RequestBackgroundColor, tea.RequestForegroundColor)
}

// retheme makes the styles follow the terminal's colours.
func (m *Model) retheme() {
	m.theme = newTheme(m.dark, m.colors, m.bg, m.fg)
	m.helpCache.lines = nil
}

// Update handles a message.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := m.update(msg)
	m.scroll()
	return m, tea.Batch(cmd, m.followDetails())
}

func (m *Model) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.BackgroundColorMsg:
		m.dark, m.bg = msg.IsDark(), msg.Color
		m.retheme()
	case tea.ForegroundColorMsg:
		m.fg = msg.Color
		m.retheme()
	case tea.ColorProfileMsg:
		m.colors = msg.Profile
		m.retheme()
	case tea.KeyPressMsg:
		return m.key(msg.String())

	case libraryMsg:
		return m.loaded(msg)
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
		var flash tea.Cmd
		switch {
		case msg.err != nil:
			flash = m.showError(msg.err)
		case msg.info != "":
			flash = m.showInfo(msg.info)
		default:
			m.flash = "" // a success clears an older message
		}
		return tea.Batch(flash, m.refresh())
	case detailsDueMsg:
		if msg.sequence == m.detailsSequence {
			return m.fetchDetails()
		}
	case detailsMsg:
		if msg.err != nil {
			return m.showError(msg.err)
		}
		m.info[msg.key] = msg.details // cached by id, whether still highlighted or not
	case artMsg:
		delete(m.artPending, msg.pending)
		if msg.err != nil { // timeouts too: a blocked source is not tried again
			m.artFailed[msg.url] = true
		}
	case volumeMsg:
		if msg.err != nil {
			return m.showError(msg.err)
		}
		m.flash = ""
		return m.showVolume(msg.volume)
	}
	return nil
}

// key handles a key press, named as Bubble Tea names it.
func (m *Model) key(key string) tea.Cmd {
	switch {
	case key == "ctrl+c":
		return tea.Quit
	case m.helpOpen:
		m.helpKey(key)
		return nil
	case m.confirmQuit:
		return m.quitKey(key)
	case m.confirmClear:
		return m.confirmKey(key)
	case m.fullDetails() && (key == "esc" || key == "left"):
		m.detailsFull = false
		return nil
	}
	if do := handler(m.keys, m.view, key); do != nil {
		return do(m)
	}
	return nil
}

// helpKey handles a key while help is open: the closing keys close it, the
// moving keys scroll it, and other keys are ignored.
func (m *Model) helpKey(key string) {
	page := max(1, m.helpRows())
	switch key {
	case "esc", "?", "h", "q", "Q":
		m.helpOpen = false
	case "up":
		m.helpTop--
	case "down":
		m.helpTop++
	case "pgup":
		m.helpTop -= page
	case "pgdown":
		m.helpTop += page
	case "home":
		m.helpTop = 0
	case "end":
		m.helpTop = farAway
	}
	m.helpTop = max(0, min(m.helpTop, len(m.helpDoc())-page))
}

// confirmKey handles a key while the clear-the-queue question is open.
func (m *Model) confirmKey(key string) tea.Cmd {
	switch key {
	case "y", "Y":
		m.confirmClear = false
		return m.actWith(func(ctx context.Context) (string, error) {
			return "Queue cleared", m.player.Clear(ctx)
		})
	case "n", "N", "esc":
		m.confirmClear = false
	}
	return nil
}

// farAway is a cursor move that reaches either end of any list.
const farAway = 1 << 30

// Key handlers, bound in the key table. Library and Queue handlers are
// bound only in their view.

func (m *Model) up() tea.Cmd       { m.move(-1); return nil }
func (m *Model) down() tea.Cmd     { m.move(+1); return nil }
func (m *Model) pageUp() tea.Cmd   { m.move(-max(1, m.listHeight())); return nil }
func (m *Model) pageDown() tea.Cmd { m.move(+max(1, m.listHeight())); return nil }
func (m *Model) home() tea.Cmd     { m.move(-farAway); return nil }
func (m *Model) end() tea.Cmd      { m.move(+farAway); return nil }

// quit asks whether to quit; ctrl+c quits at once.
func (m *Model) quit() tea.Cmd {
	m.confirmQuit = true
	return nil
}

func (m *Model) quitNow() tea.Cmd { return tea.Quit }

// quitKey handles a key while the quit question is open.
func (m *Model) quitKey(key string) tea.Cmd {
	switch key {
	case "y", "Y", "enter", "Q":
		return tea.Quit
	case "n", "N", "esc":
		m.confirmQuit = false
	}
	return nil
}

func (m *Model) openHelp() tea.Cmd {
	m.helpOpen, m.helpTop = true, 0
	return nil
}

func (m *Model) switchView() tea.Cmd {
	if m.view == queueView {
		m.view = libraryView
		return nil
	}
	m.view = queueView
	return m.refresh() // fetch the queue now
}

func (m *Model) pause() tea.Cmd       { return m.act(m.player.PlayPause) }
func (m *Model) stop() tea.Cmd        { return m.act(m.player.Stop) }
func (m *Model) next() tea.Cmd        { return m.act(m.player.Next) }
func (m *Model) previous() tea.Cmd    { return m.act(m.player.Previous) }
func (m *Model) cycleRepeat() tea.Cmd { return m.act(m.player.CycleRepeat) }

func (m *Model) toggleShuffle() tea.Cmd { return m.act(m.player.ToggleShuffle) }

func (m *Model) seekBack() tea.Cmd {
	return m.act(func(ctx context.Context) error { return m.player.Seek(ctx, false) })
}

func (m *Model) seekForward() tea.Cmd {
	return m.act(func(ctx context.Context) error { return m.player.Seek(ctx, true) })
}

func (m *Model) volumeUp() tea.Cmd   { return m.changeVolume(+1) }
func (m *Model) volumeDown() tea.Cmd { return m.changeVolume(-1) }

func (m *Model) changeVolume(steps int) tea.Cmd {
	return m.setVolume(func(ctx context.Context) (kodi.Volume, error) { return m.player.ChangeVolume(ctx, steps) })
}

func (m *Model) mute() tea.Cmd { return m.setVolume(m.player.ToggleMute) }

func (m *Model) setVolume(f func(context.Context) (kodi.Volume, error)) tea.Cmd {
	m.actions++
	return m.command(func(ctx context.Context) tea.Msg {
		volume, err := f(ctx)
		return volumeMsg{volume: volume, err: err}
	})
}

// open opens the selected artist or album; on a track, it plays the album
// from that track, as Kodi does.
func (m *Model) open() tea.Cmd {
	l := m.current()
	if l == nil || l.len() == 0 {
		return nil
	}
	switch l.kind {
	case artistsLevel:
		artist := l.artists[l.list.index]
		m.levels = append(m.levels, level{kind: albumsLevel, title: artist.Name, albums: m.lib.Albums(artist.ID)})
		return nil
	case albumsLevel:
		album, depth, lib := l.albums[l.list.index], len(m.levels), m.lib
		return m.command(func(ctx context.Context) tea.Msg {
			songs, err := lib.Songs(ctx, album.ID)
			return songsMsg{album: album, depth: depth, songs: songs, err: err}
		})
	default:
		return m.play()
	}
}

// opened shows the tracks of an album, unless the user has moved on.
func (m *Model) opened(msg songsMsg) tea.Cmd {
	if msg.err != nil {
		return m.showError(msg.err)
	}
	l := m.current()
	if len(m.levels) == msg.depth && l.kind == albumsLevel && l.len() > 0 && l.albums[l.list.index].ID == msg.album.ID {
		m.levels = append(m.levels, level{
			kind: tracksLevel, title: msg.album.Title, songs: msg.songs, album: msg.album, rows: discRows(msg.songs),
		})
	}
	return nil
}

func (m *Model) back() tea.Cmd {
	if len(m.levels) > 1 {
		m.levels = m.levels[:len(m.levels)-1]
	}
	return nil
}

func (m *Model) backOrQuit() tea.Cmd {
	if len(m.levels) > 1 {
		return m.back()
	}
	return m.quit()
}

// selection is the songs of the selected library row: an artist's albums,
// an album, or a track's album.
type selection struct {
	songs    func(context.Context) ([]kodi.Song, error)
	position int           // of the selected track in songs
	name     string        // of the row
	timeout  time.Duration // for fetching the songs and acting on them
	track    bool          // the row is a track
}

func (m *Model) selection() (selection, bool) {
	l := m.current()
	if l == nil || l.len() == 0 {
		return selection{}, false
	}
	lib, i := m.lib, l.list.index
	switch l.kind {
	case artistsLevel:
		artist := l.artists[i]
		return selection{
			songs:   func(ctx context.Context) ([]kodi.Song, error) { return lib.ArtistSongs(ctx, artist.ID) },
			name:    artist.Name,
			timeout: artistTimeout,
		}, true
	case albumsLevel:
		album := l.albums[i]
		return selection{
			songs:   func(ctx context.Context) ([]kodi.Song, error) { return lib.Songs(ctx, album.ID) },
			name:    album.Title,
			timeout: callTimeout,
		}, true
	default:
		all := l.songs
		return selection{
			songs:    func(context.Context) ([]kodi.Song, error) { return all, nil },
			position: i, name: all[i].Title, timeout: callTimeout, track: true,
		}, true
	}
}

// play replaces the queue with the selection and plays it.
func (m *Model) play() tea.Cmd {
	sel, ok := m.selection()
	if !ok {
		return nil
	}
	return m.actWithin(sel.timeout, func(ctx context.Context) (string, error) {
		list, err := sel.songs(ctx)
		if err != nil {
			return "", err
		}
		return "", m.player.Play(ctx, list, sel.position)
	})
}

// enqueue appends the selection to the queue: an artist's or album's songs,
// or one track.
func (m *Model) enqueue() tea.Cmd {
	sel, ok := m.selection()
	if !ok {
		return nil
	}
	return m.actWithin(sel.timeout, func(ctx context.Context) (string, error) {
		list, err := sel.songs(ctx)
		if err != nil {
			return "", err
		}
		if sel.track {
			list = list[sel.position : sel.position+1]
		}
		if err := m.player.Enqueue(ctx, list); err != nil {
			return "", err
		}
		return fmt.Sprintf("Queued %s: %s", plural(len(list), "track"), sel.name), nil
	})
}

// playSelected plays the selected queue item.
func (m *Model) playSelected() tea.Cmd {
	if len(m.queue) == 0 {
		return nil
	}
	position := m.queueList.index
	return m.act(func(ctx context.Context) error { return m.player.GoTo(ctx, position) })
}

func (m *Model) remove() tea.Cmd {
	if len(m.queue) == 0 {
		return nil
	}
	position := m.queueList.index
	return m.act(func(ctx context.Context) error { return m.player.Remove(ctx, position) })
}

func (m *Model) moveItemUp() tea.Cmd   { return m.moveItem(-1) }
func (m *Model) moveItemDown() tea.Cmd { return m.moveItem(+1) }

// moveItem swaps the selected queue item with its neighbour. The cursor,
// and the list until the next refresh, follow at once.
func (m *Model) moveItem(delta int) tea.Cmd {
	from := m.queueList.index
	to := from + delta
	if from >= len(m.queue) || to < 0 || to >= len(m.queue) {
		return nil
	}
	m.queue[from], m.queue[to] = m.queue[to], m.queue[from]
	m.queueList.index = to
	if s := m.status; s != nil {
		switch s.Position {
		case from:
			s.Position = to
		case to:
			s.Position = from
		}
		m.lastPlaying = s.Position
	}
	return m.act(func(ctx context.Context) error { return m.player.Swap(ctx, from, to) })
}

func (m *Model) askClear() tea.Cmd {
	if len(m.queue) == 0 {
		return m.showInfo("The queue is already empty.")
	}
	m.confirmClear = true
	return nil
}

// current returns the library level shown, or nil while waiting for Kodi.
func (m *Model) current() *level {
	if len(m.levels) == 0 {
		return nil
	}
	return &m.levels[len(m.levels)-1]
}

// listHeight is the number of list rows on screen.
func (m *Model) listHeight() int { return max(0, m.layout().listHeight-1) } // below the column header

// middleHeight is the number of rows between the header and the status
// line.
func (m *Model) middleHeight() int { return max(0, m.height-3) }

func (m *Model) move(delta int) {
	if m.view == queueView {
		m.queueList.move(delta, len(m.queue))
	} else if l := m.current(); l != nil {
		l.list.move(delta, l.len())
	}
}

func (m *Model) scroll() {
	height := m.listHeight()
	m.queueList.scroll(height, len(m.queue))
	for i := range m.levels {
		l := &m.levels[i]
		if l.rows == nil {
			l.list.scroll(height, l.len())
			continue
		}
		l.list.move(0, l.len())
		rows := cursor{index: slices.Index(l.rows, l.list.index), top: l.list.top}
		rows.scroll(height, len(l.rows))
		l.list.top = rows.top
	}
}

// command runs f as a command, with the usual timeout for Kodi calls.
func (m *Model) command(f func(context.Context) tea.Msg) tea.Cmd {
	return commandWithin(callTimeout, f)
}

// commandWithin runs f as a command with a timeout.
func commandWithin(timeout time.Duration, f func(context.Context) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return f(ctx)
	}
}

// act runs a playback action; the status is refreshed afterwards.
func (m *Model) act(f func(context.Context) error) tea.Cmd {
	return m.actWith(func(ctx context.Context) (string, error) { return "", f(ctx) })
}

// actWith runs an action that reports what it did; the report is flashed
// and the status refreshed afterwards.
func (m *Model) actWith(f func(context.Context) (string, error)) tea.Cmd {
	return m.actWithin(callTimeout, f)
}

// actWithin is actWith with a timeout.
func (m *Model) actWithin(timeout time.Duration, f func(context.Context) (string, error)) tea.Cmd {
	m.actions++
	return commandWithin(timeout, func(ctx context.Context) tea.Msg {
		info, err := f(ctx)
		return actionMsg{info: info, err: err}
	})
}

func (m *Model) loadLibrary() tea.Cmd {
	client := m.client
	return commandWithin(libraryTimeout, func(ctx context.Context) tea.Msg {
		lib, err := kodi.LoadLibrary(ctx, client)
		return libraryMsg{lib: lib, err: err}
	})
}

// reload loads the library again.
func (m *Model) reload() tea.Cmd { return m.loadLibrary() }

// loaded shows a loaded library, or why it did not load. While waiting
// for Kodi it retries every second, except when Kodi rejects the password.
func (m *Model) loaded(msg libraryMsg) tea.Cmd {
	reload := m.lib != nil
	var unavailable *kodi.UnavailableError
	m.offline = errors.As(msg.err, &unavailable)
	switch {
	case msg.err != nil && reload:
		return m.showError(msg.err) // keep the library shown
	case errors.Is(msg.err, kodi.ErrUnauthorized):
		m.waitErr = msg.err // retrying would not help
		return nil
	case msg.err != nil:
		m.waitErr = msg.err
		return m.clock.After(pollInterval, retryMsg{})
	}
	m.lib, m.waitErr = msg.lib, nil
	m.levels = []level{{kind: artistsLevel, title: "Artists", artists: msg.lib.Artists()}}
	m.info = make(map[itemKey]*details)
	var flash tea.Cmd
	if reload {
		flash = m.showInfo(fmt.Sprintf("Library reloaded: %s", plural(len(msg.lib.Artists()), "artist")))
	}
	return tea.Batch(flash, m.refresh())
}

// refresh polls the status now, instead of at the next tick.
func (m *Model) refresh() tea.Cmd {
	m.tickSequence++ // cancels the scheduled tick; the poll schedules the next one
	return m.poll()
}

// poll fetches the status, the volume and, in the queue view, the queue.
func (m *Model) poll() tea.Cmd {
	if m.polling {
		m.pollAgain = true
		return nil
	}
	m.polling = true
	actions, withQueue := m.actions, m.view == queueView
	return m.command(func(ctx context.Context) tea.Msg {
		msg := pollMsg{actions: actions, withQueue: withQueue}
		if msg.status, msg.video, msg.err = m.player.Playing(ctx); msg.err != nil {
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
	var unavailable *kodi.UnavailableError
	m.offline = errors.As(msg.err, &unavailable) // the status line says so
	if msg.err != nil {
		m.status, m.volume, m.video = nil, nil, false
		if !m.offline {
			cmd = m.showError(msg.err)
		}
	} else {
		m.status, m.video = msg.status, msg.video
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

// setQueue shows a new queue. The cursor starts on the playing item,
// follows it while it stays there, and otherwise keeps its item when it can.
func (m *Model) setQueue(queue []kodi.QueueItem) {
	playing, i := m.playingPosition(), m.queueList.index
	switch {
	case !m.queueShown:
		if playing >= 0 && len(queue) > 0 {
			i, m.queueShown = playing, true
		}
	case playing >= 0 && i == m.lastPlaying:
		i = playing
	default:
		i = sameItem(m.queue, queue, i)
	}
	m.queue, m.lastPlaying, m.queueList.index = queue, playing, i
	m.queueList.move(0, len(queue))
}

// sameItem returns where the item at i in old is in queue: at i, or else at
// the nearest position with the same song. When the item is gone, i stays.
func sameItem(old, queue []kodi.QueueItem, i int) int {
	if i >= len(old) || i < len(queue) && queue[i].ID == old[i].ID {
		return i
	}
	best := -1
	for j, item := range queue {
		if item.ID == old[i].ID && (best < 0 || abs(j-i) < abs(best-i)) {
			best = j
		}
	}
	if best < 0 {
		return i
	}
	return best
}

func abs(n int) int { return max(n, -n) }

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

// showError flashes an error. Refusals such as "nothing is playing" are
// flashed as information.
func (m *Model) showError(err error) tea.Cmd {
	if errors.Is(err, kodi.ErrNotPlaying) || errors.Is(err, kodi.ErrPlayingItem) {
		return m.showInfo(err.Error())
	}
	cmd := m.showInfo(errorText(err))
	m.flashErr = true
	return cmd
}

// showInfo flashes a message in the footer.
func (m *Model) showInfo(text string) tea.Cmd {
	m.flash, m.flashErr = text, false
	m.flashUntil = m.clock.Now().Add(flashDuration)
	return m.clock.After(flashDuration, redrawMsg{})
}

// errorText explains an error in a plain sentence, with the technical cause
// as a short suffix where it helps.
func errorText(err error) string {
	var unavailable *kodi.UnavailableError
	var kerr *kodi.Error
	switch {
	case errors.Is(err, kodi.ErrUnauthorized):
		return "Kodi rejected the user name or password. " +
			"Check them in Kodi under Settings → Services → Control, then restart kodipilot."
	case errors.As(err, &unavailable):
		addr := unavailable.Addr
		if u, err := url.Parse(addr); err == nil && u.Host != "" {
			addr = u.Host
		}
		text := fmt.Sprintf("Can't reach Kodi at %s (%s).", addr, cause(unavailable.Err))
		if errors.Is(err, syscall.ECONNREFUSED) {
			text += " Is Kodi running? For another machine use --host."
		}
		return text
	case errors.As(err, &kerr):
		return fmt.Sprintf("Kodi couldn't do that (%s: %s).", kerr.Method, strings.TrimSuffix(kerr.Message, "."))
	}
	return "Something went wrong: " + err.Error()
}

// cause names the cause of a network error in a few words.
func cause(err error) string {
	var dns *net.DNSError
	var timeout interface{ Timeout() bool }
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.As(err, &dns):
		return "unknown host"
	case errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout():
		return "no answer"
	case errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET):
		return "connection closed"
	}
	text := err.Error()
	if i := strings.LastIndex(text, ": "); i >= 0 {
		text = text[i+2:]
	}
	return text
}

// sentence returns text with its first letter in upper case.
func sentence(text string) string {
	if text == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(text)
	return string(unicode.ToUpper(r)) + text[size:]
}

// plural returns "1 track", "2 tracks" and so on.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
