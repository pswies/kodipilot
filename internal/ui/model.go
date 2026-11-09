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
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/pswies/kodipilot/internal/art"
	"github.com/pswies/kodipilot/internal/artwork"
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

// Options configure a Model.
type Options struct {
	// Images loads cover art and artist images for the details pane; nil
	// shows none.
	Images *artwork.Loader
	// Art is how images are drawn (when the terminal has colours).
	Art art.Mode
}

// Model is the Bubble Tea model of kodipilot.
type Model struct {
	opts   Options
	client kodi.Client
	player *kodi.Player
	clock  Clock
	theme  theme
	dark   bool
	colors colorprofile.Profile

	keys          []keySection
	width, height int
	view          viewKind
	helpOpen      bool
	helpTop       int  // the first help line shown
	confirmClear  bool // asking whether to clear the queue
	paneHidden    bool // the user hid the details pane
	detailsFull   bool // the details fill the screen (when the pane does not fit)

	info            map[itemKey]*details // fetched details
	detailsKey      itemKey              // the item the details follow
	detailsSequence int                  // identifies the one details timer that counts
	artPending      map[string]bool      // images being rendered
	artFailed       map[string]bool      // art URLs without an image
	hadImage        bool                 // the last details shown had an image

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

// Init starts loading the library and asks for the terminal background.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.loadLibrary(), tea.RequestBackgroundColor)
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
		var flash tea.Cmd
		switch {
		case msg.err != nil:
			flash = m.showError(msg.err)
		case msg.info != "":
			flash = m.showInfo(msg.info)
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
		if msg.err != nil && !errors.Is(msg.err, context.Canceled) && !errors.Is(msg.err, context.DeadlineExceeded) {
			m.artFailed[msg.url] = true
		}
	case volumeMsg:
		if msg.err != nil {
			return m.showError(msg.err)
		}
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
	m.helpTop = max(0, min(m.helpTop, len(m.helpLines(0))-page))
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

func (m *Model) quit() tea.Cmd { return tea.Quit }

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
		m.levels = append(m.levels, level{kind: tracksLevel, title: msg.album.Title, songs: msg.songs, album: msg.album})
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
	return tea.Quit
}

// selection returns the songs of the selected library row (an artist's
// albums, an album, or a track's album), the position of the selected track
// in them, and the row's name.
func (m *Model) selection() (songs func(context.Context) ([]kodi.Song, error), position int, name string) {
	l := m.current()
	if l == nil || l.len() == 0 {
		return nil, 0, ""
	}
	lib, i := m.lib, l.list.index
	switch l.kind {
	case artistsLevel:
		artist := l.artists[i]
		return func(ctx context.Context) ([]kodi.Song, error) { return lib.ArtistSongs(ctx, artist.ID) }, 0, artist.Name
	case albumsLevel:
		album := l.albums[i]
		return func(ctx context.Context) ([]kodi.Song, error) { return lib.Songs(ctx, album.ID) }, 0, album.Title
	default:
		all := l.songs
		return func(context.Context) ([]kodi.Song, error) { return all, nil }, i, all[i].Title
	}
}

// play replaces the queue with the selection and plays it.
func (m *Model) play() tea.Cmd {
	songs, position, _ := m.selection()
	if songs == nil {
		return nil
	}
	return m.act(func(ctx context.Context) error {
		list, err := songs(ctx)
		if err != nil {
			return err
		}
		return m.player.Play(ctx, list, position)
	})
}

// enqueue appends the selection to the queue: an artist's or album's songs,
// or one track.
func (m *Model) enqueue() tea.Cmd {
	songs, position, name := m.selection()
	if songs == nil {
		return nil
	}
	track := m.current().kind == tracksLevel
	return m.actWith(func(ctx context.Context) (string, error) {
		list, err := songs(ctx)
		if err != nil {
			return "", err
		}
		if track {
			list = list[position : position+1]
		}
		if err := m.player.Enqueue(ctx, list); err != nil {
			return "", err
		}
		return fmt.Sprintf("Queued %s: %s", plural(len(list), "track"), name), nil
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
	m.confirmClear = len(m.queue) > 0
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
func (m *Model) listHeight() int { return max(0, m.height-3) }

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
		m.levels[i].list.scroll(height, m.levels[i].len())
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
	return m.actWith(func(ctx context.Context) (string, error) { return "", f(ctx) })
}

// actWith runs an action that reports what it did; the report is flashed
// and the status refreshed afterwards.
func (m *Model) actWith(f func(context.Context) (string, error)) tea.Cmd {
	m.actions++
	return m.command(func(ctx context.Context) tea.Msg {
		info, err := f(ctx)
		return actionMsg{info: info, err: err}
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

func errorText(err error) string {
	var unavailable *kodi.UnavailableError
	if errors.As(err, &unavailable) {
		return "Kodi unavailable: " + err.Error()
	}
	return "Kodi: " + err.Error()
}

// plural returns "1 track", "2 tracks" and so on.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
