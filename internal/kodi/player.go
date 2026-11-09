package kodi

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"strings"
	"sync"
	"time"
)

// MusicPlaylist is the id of Kodi's music playlist, the play queue.
const MusicPlaylist = 0

// VolumeStep is the volume change, in percent, of one ChangeVolume step.
const VolumeStep = 5

// Errors of playback actions that Kodi would refuse or ignore.
var (
	ErrNotPlaying  = errors.New("nothing is playing")
	ErrPlayingItem = errors.New("the playing item can't be removed")
)

// Player controls playback and the music playlist, the queue. Every
// JSON-RPC method that changes something is called from here. Its operations
// are serialised, so that, for example, two quick volume steps or album
// starts never interleave. It is safe for concurrent use.
//
// Methods that act on the audio player return ErrNotPlaying, without making
// a change, when no audio plays.
type Player struct {
	client Client
	mu     sync.Mutex
}

// NewPlayer returns a Player that controls Kodi through client.
func NewPlayer(client Client) *Player {
	return &Player{client: client}
}

// Status describes what the audio player is playing.
type Status struct {
	Artist   string
	Title    string
	Album    string
	Elapsed  time.Duration
	Total    time.Duration
	Paused   bool
	Position int    // index in the queue, -1 when unknown
	Count    int    // number of items in the queue
	Repeat   string // "off", "one" or "all"
	Shuffled bool
}

// QueueItem is an entry of the music playlist.
type QueueItem struct {
	ID       int // the song id
	Title    string
	Artist   string
	Album    string
	Duration time.Duration
}

// Volume is Kodi's global volume.
type Volume struct {
	Level int // 0–100
	Muted bool
}

// Play replaces whatever plays, music or video, with songs, in the given
// order, starting at position: it stops every active player, clears the music
// playlist, adds the songs and opens the playlist at position.
func (p *Player) Play(ctx context.Context, songs []Song, position int) error {
	if len(songs) == 0 {
		return nil
	}
	position = max(0, min(position, len(songs)-1))
	p.mu.Lock()
	defer p.mu.Unlock()

	players, err := p.activePlayers(ctx)
	if err != nil {
		return err
	}
	for _, player := range players {
		if err := p.call(ctx, "Player.Stop", map[string]any{"playerid": player.ID}); err != nil {
			return err
		}
	}
	if err := p.call(ctx, "Playlist.Clear", map[string]any{"playlistid": MusicPlaylist}); err != nil {
		return err
	}
	if err := p.add(ctx, songs); err != nil {
		return err
	}
	return p.open(ctx, position)
}

// Enqueue appends songs to the queue, in the given order, without starting
// playback.
func (p *Player) Enqueue(ctx context.Context, songs []Song) error {
	if len(songs) == 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.add(ctx, songs)
}

// Remove removes the queue item at position. Like Kodi, it refuses to remove
// the playing item, with ErrPlayingItem.
func (p *Player) Remove(ctx context.Context, position int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	id, ok, err := p.audioPlayer(ctx)
	if err != nil {
		return err
	}
	if ok {
		var props struct {
			Position int `json:"position"`
		}
		err := p.client.Call(ctx, "Player.GetProperties", map[string]any{
			"playerid": id, "properties": []string{"position"},
		}, &props)
		if err != nil {
			return err
		}
		if props.Position == position {
			return ErrPlayingItem
		}
	}
	return p.call(ctx, "Playlist.Remove", map[string]any{"playlistid": MusicPlaylist, "position": position})
}

// Swap swaps two queue items.
func (p *Player) Swap(ctx context.Context, position1, position2 int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.call(ctx, "Playlist.Swap", map[string]any{
		"playlistid": MusicPlaylist, "position1": position1, "position2": position2,
	})
}

// Clear empties the queue.
func (p *Player) Clear(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.call(ctx, "Playlist.Clear", map[string]any{"playlistid": MusicPlaylist})
}

// PlayPause pauses or resumes the audio player.
func (p *Player) PlayPause(ctx context.Context) error {
	return p.onAudio(ctx, "Player.PlayPause", nil)
}

// Next skips to the next track.
func (p *Player) Next(ctx context.Context) error {
	return p.onAudio(ctx, "Player.GoTo", map[string]any{"to": "next"})
}

// Previous goes back to the previous track.
func (p *Player) Previous(ctx context.Context) error {
	return p.onAudio(ctx, "Player.GoTo", map[string]any{"to": "previous"})
}

// Stop stops the audio player.
func (p *Player) Stop(ctx context.Context) error {
	return p.onAudio(ctx, "Player.Stop", nil)
}

// Seek jumps forward or back by Kodi's small step (30 s by default).
func (p *Player) Seek(ctx context.Context, forward bool) error {
	step := "smallbackward"
	if forward {
		step = "smallforward"
	}
	return p.onAudio(ctx, "Player.Seek", map[string]any{"value": map[string]string{"step": step}})
}

// CycleRepeat switches repeat from off to all to one and back to off.
func (p *Player) CycleRepeat(ctx context.Context) error {
	return p.onAudio(ctx, "Player.SetRepeat", map[string]any{"repeat": "cycle"})
}

// ToggleShuffle turns shuffle on or off.
func (p *Player) ToggleShuffle(ctx context.Context) error {
	return p.onAudio(ctx, "Player.SetShuffle", map[string]any{"shuffle": "toggle"})
}

// GoTo plays the queue item at position: it jumps there when audio plays,
// and otherwise opens the music playlist at that position.
func (p *Player) GoTo(ctx context.Context, position int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	id, ok, err := p.audioPlayer(ctx)
	if err != nil {
		return err
	}
	if ok {
		return p.call(ctx, "Player.GoTo", map[string]any{"playerid": id, "to": position})
	}
	return p.open(ctx, position)
}

// Volume returns Kodi's global volume.
func (p *Player) Volume(ctx context.Context) (Volume, error) {
	var props struct {
		Volume int  `json:"volume"`
		Muted  bool `json:"muted"`
	}
	err := p.client.Call(ctx, "Application.GetProperties", map[string]any{
		"properties": []string{"volume", "muted"},
	}, &props)
	if err != nil {
		return Volume{}, err
	}
	return Volume{Level: props.Volume, Muted: props.Muted}, nil
}

// ChangeVolume changes the volume by steps × VolumeStep, clamped to 0–100
// (Kodi's own increment is a single point), and returns the new volume. At a
// bound it changes nothing. It works whether anything plays or not.
func (p *Player) ChangeVolume(ctx context.Context, steps int) (Volume, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	vol, err := p.Volume(ctx)
	if err != nil {
		return Volume{}, err
	}
	target := max(0, min(100, vol.Level+steps*VolumeStep))
	if target != vol.Level {
		if err := p.call(ctx, "Application.SetVolume", map[string]any{"volume": target}); err != nil {
			return Volume{}, err
		}
	}
	vol.Level = target
	return vol, nil
}

// ToggleMute mutes or unmutes Kodi and returns the new volume. It works
// whether anything plays or not.
func (p *Player) ToggleMute(ctx context.Context) (Volume, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.call(ctx, "Application.SetMute", map[string]any{"mute": "toggle"}); err != nil {
		return Volume{}, err
	}
	return p.Volume(ctx)
}

// NowPlaying returns the audio player's status, or nil when no audio plays.
func (p *Player) NowPlaying(ctx context.Context) (*Status, error) {
	id, ok, err := p.audioPlayer(ctx)
	if err != nil || !ok {
		return nil, err
	}
	var item struct {
		Item struct {
			Title  string   `json:"title"`
			Label  string   `json:"label"`
			Artist []string `json:"artist"`
			Album  string   `json:"album"`
		} `json:"item"`
	}
	err = p.client.Call(ctx, "Player.GetItem", map[string]any{
		"playerid": id, "properties": []string{"title", "artist", "album"},
	}, &item)
	if err != nil {
		return nil, err
	}
	var props struct {
		Time      kodiTime `json:"time"`
		TotalTime kodiTime `json:"totaltime"`
		Speed     int      `json:"speed"`
		Position  *int     `json:"position"`
		Repeat    string   `json:"repeat"`
		Shuffled  bool     `json:"shuffled"`
	}
	err = p.client.Call(ctx, "Player.GetProperties", map[string]any{
		"playerid":   id,
		"properties": []string{"time", "totaltime", "speed", "position", "repeat", "shuffled"},
	}, &props)
	if err != nil {
		return nil, err
	}
	var playlist struct {
		Size int `json:"size"`
	}
	err = p.client.Call(ctx, "Playlist.GetProperties", map[string]any{
		"playlistid": MusicPlaylist, "properties": []string{"size"},
	}, &playlist)
	if err != nil {
		return nil, err
	}
	status := &Status{
		Artist:   strings.Join(item.Item.Artist, ", "),
		Title:    cmp.Or(item.Item.Title, item.Item.Label),
		Album:    item.Item.Album,
		Elapsed:  props.Time.duration(),
		Total:    props.TotalTime.duration(),
		Paused:   props.Speed == 0,
		Position: -1,
		Count:    playlist.Size,
		Repeat:   props.Repeat,
		Shuffled: props.Shuffled,
	}
	if props.Position != nil {
		status.Position = *props.Position
	}
	return status, nil
}

// Queue returns the music playlist in the order Kodi plays it (shuffled, if
// shuffle is on).
func (p *Player) Queue(ctx context.Context) ([]QueueItem, error) {
	var result struct {
		Items []struct {
			ID       int      `json:"id"`
			Title    string   `json:"title"`
			Label    string   `json:"label"`
			Artist   []string `json:"artist"`
			Album    string   `json:"album"`
			Duration int      `json:"duration"`
		} `json:"items"`
	}
	err := p.client.Call(ctx, "Playlist.GetItems", map[string]any{
		"playlistid": MusicPlaylist,
		"properties": []string{"title", "artist", "album", "duration"},
	}, &result)
	if err != nil {
		return nil, err
	}
	queue := make([]QueueItem, len(result.Items))
	for i, item := range result.Items {
		queue[i] = QueueItem{
			ID:       item.ID,
			Title:    cmp.Or(item.Title, item.Label),
			Artist:   strings.Join(item.Artist, ", "),
			Album:    item.Album,
			Duration: time.Duration(item.Duration) * time.Second,
		}
	}
	return queue, nil
}

type activePlayer struct {
	ID   int    `json:"playerid"`
	Type string `json:"type"`
}

func (p *Player) activePlayers(ctx context.Context) ([]activePlayer, error) {
	var players []activePlayer
	if err := p.client.Call(ctx, "Player.GetActivePlayers", nil, &players); err != nil {
		return nil, err
	}
	return players, nil
}

// audioPlayer returns the id of the active audio player, if there is one.
func (p *Player) audioPlayer(ctx context.Context) (id int, ok bool, err error) {
	players, err := p.activePlayers(ctx)
	if err != nil {
		return 0, false, err
	}
	for _, player := range players {
		if player.Type == "audio" {
			return player.ID, true, nil
		}
	}
	return 0, false, nil
}

// onAudio calls method on the audio player, with params added to its id. It
// returns ErrNotPlaying when no audio plays.
func (p *Player) onAudio(ctx context.Context, method string, params map[string]any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	id, ok, err := p.audioPlayer(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotPlaying
	}
	args := map[string]any{"playerid": id}
	maps.Copy(args, params)
	return p.call(ctx, method, args)
}

// add appends songs to the music playlist.
func (p *Player) add(ctx context.Context, songs []Song) error {
	items := make([]map[string]int, len(songs))
	for i, song := range songs {
		items[i] = map[string]int{"songid": song.ID}
	}
	return p.call(ctx, "Playlist.Add", map[string]any{"playlistid": MusicPlaylist, "item": items})
}

// open starts playing the music playlist at position.
func (p *Player) open(ctx context.Context, position int) error {
	return p.call(ctx, "Player.Open", map[string]any{
		"item": map[string]int{"playlistid": MusicPlaylist, "position": position},
	})
}

func (p *Player) call(ctx context.Context, method string, params any) error {
	return p.client.Call(ctx, method, params, nil)
}

// kodiTime is Kodi's Global.Time.
type kodiTime struct {
	Hours        int `json:"hours"`
	Minutes      int `json:"minutes"`
	Seconds      int `json:"seconds"`
	Milliseconds int `json:"milliseconds"`
}

func (t kodiTime) duration() time.Duration {
	return time.Duration(t.Hours)*time.Hour + time.Duration(t.Minutes)*time.Minute +
		time.Duration(t.Seconds)*time.Second + time.Duration(t.Milliseconds)*time.Millisecond
}
