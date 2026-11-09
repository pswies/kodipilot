package koditest

import (
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/pswies/kodipilot/internal/kodi"
)

// Player ids of the fake.
const (
	AudioPlayerID = 0
	VideoPlayerID = 1
)

// Artists returns the fake library's album artists, in Kodi's order. "No
// Albums" has no albums.
func Artists() []kodi.Artist {
	return []kodi.Artist{
		{ID: 1, Name: "Brian Eno", Label: "Brian Eno"},
		{ID: 2, Name: "David Byrne", Label: "David Byrne"},
		{ID: 3, Name: "No Albums", Label: "No Albums"},
		{ID: 4, Name: "Żywiołak", Label: "Żywiołak"},
	}
}

// Albums returns the fake library's albums, unsorted. Album 12 has two album
// artists; album 13 has no year; album 41 has a long title in wide characters.
func Albums() []kodi.Album {
	return []kodi.Album{
		{ID: 10, Title: "Music for Films", Year: 1978, ArtistIDs: []int{1}, DisplayArtist: "Brian Eno"},
		{ID: 11, Title: "Ambient 1", Year: 1978, ArtistIDs: []int{1}, DisplayArtist: "Brian Eno"},
		{ID: 12, Title: "My Life in the Bush of Ghosts", Year: 1981, ArtistIDs: []int{1, 2},
			DisplayArtist: "Brian Eno / David Byrne"},
		{ID: 13, Title: "another green world", ArtistIDs: []int{1}, DisplayArtist: "Brian Eno"},
		{ID: 14, Title: "Another Day", Year: 1975, ArtistIDs: []int{1}, DisplayArtist: "Brian Eno"},
		{ID: 40, Title: "Muzyka psychoaktywna", Year: 2010, ArtistIDs: []int{4}, DisplayArtist: "Żywiołak"},
		{ID: 41, Title: "日本語のアルバム、とても長いタイトル", Year: 2012, ArtistIDs: []int{4}, DisplayArtist: "Żywiołak"},
	}
}

// Songs returns the fake library's songs of an album, unsorted. Album 11 has
// one disc (disc 0), album 12 two discs.
func Songs(albumID int) []kodi.Song {
	switch albumID {
	case 11:
		return []kodi.Song{
			{ID: 104, Title: "2/2", Track: 4, Duration: 360, DisplayArtist: "Brian Eno"},
			{ID: 101, Title: "1/1", Track: 1, Duration: 1021, DisplayArtist: "Brian Eno"},
			{ID: 103, Title: "1/2", Track: 3, Duration: 712, DisplayArtist: "Brian Eno"},
			{ID: 102, Title: "2/1", Track: 2, Duration: 535, DisplayArtist: "Brian Eno"},
		}
	case 12:
		return []kodi.Song{
			{ID: 202, Title: "B", Track: 1, Disc: 2, Duration: 100, DisplayArtist: "Brian Eno"},
			{ID: 201, Title: "b-side", Track: 2, Disc: 1, Duration: 100, DisplayArtist: "David Byrne"},
			{ID: 200, Title: "A", Track: 2, Disc: 1, Duration: 100, DisplayArtist: "Brian Eno"},
			{ID: 199, Title: "Z", Track: 1, Disc: 1, Duration: 100, DisplayArtist: "Brian Eno"},
		}
	}
	return nil
}

// Music is a Fake that behaves like Kodi with the fake library loaded: it
// keeps a music playlist, an audio player with repeat and shuffle modes, an
// optional video player and a mixer, and answers the methods kodipilot uses
// accordingly. Like Kodi, it refuses to remove the playing item. Unlike Kodi,
// clearing the playlist stops playback, and shuffle does not reorder it.
type Music struct {
	*Fake

	mu       sync.Mutex
	video    bool
	cover    string // art URL of every album, and of artist 1
	playing  bool
	paused   bool
	position int
	elapsed  int   // seconds into the playing song
	queue    []int // song ids
	repeat   string
	shuffled bool
	volume   kodi.Volume
}

// NewMusic returns a Music with nothing playing and the volume at 50.
func NewMusic() *Music {
	m := &Music{Fake: NewFake(), repeat: "off", volume: kodi.Volume{Level: 50}}
	m.Reply("AudioLibrary.GetArtists", map[string]any{"artists": Artists()})
	m.Reply("AudioLibrary.GetAlbums", map[string]any{"albums": Albums()})
	m.Handle("AudioLibrary.GetSongs", func(raw json.RawMessage) (any, error) {
		var p struct{ Filter struct{ AlbumID int } }
		json.Unmarshal(raw, &p)
		return map[string]any{"songs": Songs(p.Filter.AlbumID)}, nil
	})
	m.handle("Player.GetActivePlayers", m.activePlayers)
	m.handle("Player.Stop", m.stop)
	m.handle("Playlist.Clear", func(json.RawMessage) any {
		m.queue, m.playing, m.paused = nil, false, false
		return "OK"
	})
	m.handle("Playlist.Add", m.add)
	m.Handle("Playlist.Remove", m.remove)
	m.handle("Playlist.Swap", m.swap)
	m.handle("Player.Seek", m.seek)
	m.handle("Player.SetRepeat", func(raw json.RawMessage) any {
		var p struct{ Repeat string }
		json.Unmarshal(raw, &p)
		if p.Repeat == "cycle" {
			p.Repeat = map[string]string{"off": "all", "all": "one", "one": "off"}[m.repeat]
		}
		m.repeat = p.Repeat
		return "OK"
	})
	m.handle("Player.SetShuffle", func(raw json.RawMessage) any {
		var p struct{ Shuffle any }
		json.Unmarshal(raw, &p)
		if on, ok := p.Shuffle.(bool); ok {
			m.shuffled = on
		} else {
			m.shuffled = !m.shuffled
		}
		return "OK"
	})
	m.handle("Application.SetMute", func(raw json.RawMessage) any {
		var p struct{ Mute any }
		json.Unmarshal(raw, &p)
		if on, ok := p.Mute.(bool); ok {
			m.volume.Muted = on
		} else {
			m.volume.Muted = !m.volume.Muted
		}
		return m.volume.Muted
	})
	m.handle("Player.Open", m.open)
	m.handle("Player.PlayPause", func(json.RawMessage) any {
		m.paused = !m.paused
		return map[string]int{"speed": speed(m.paused)}
	})
	m.handle("Player.GoTo", m.goTo)
	m.handle("Player.GetItem", m.item)
	m.handle("Player.GetProperties", m.properties)
	m.handle("Playlist.GetProperties", func(json.RawMessage) any { return map[string]int{"size": len(m.queue)} })
	m.handle("Playlist.GetItems", m.items)
	m.handle("Application.GetProperties", func(json.RawMessage) any {
		return map[string]any{"volume": m.volume.Level, "muted": m.volume.Muted}
	})
	m.handle("AudioLibrary.GetArtistDetails", m.artistDetails)
	m.handle("AudioLibrary.GetAlbumDetails", m.albumDetails)
	m.handle("AudioLibrary.GetSongDetails", m.songDetails)
	m.handle("Application.SetVolume", func(raw json.RawMessage) any {
		var p struct{ Volume int }
		json.Unmarshal(raw, &p)
		m.volume.Level = p.Volume
		return p.Volume
	})
	return m
}

// SetCover makes every album, and Brian Eno, have the image at path, as a
// Kodi art URL.
func (m *Music) SetCover(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cover = "image://" + strings.ReplaceAll(url.QueryEscape(path), "+", "%20") + "/"
}

// SetVideo starts or stops a fake video player.
func (m *Music) SetVideo(on bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.video = on
}

// SetVolume changes the volume as if done in Kodi itself.
func (m *Music) SetVolume(v kodi.Volume) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.volume = v
}

// SetModes sets the repeat mode ("off", "one", "all") and shuffle.
func (m *Music) SetModes(repeat string, shuffled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.repeat, m.shuffled = repeat, shuffled
}

// Modes returns the repeat mode and shuffle.
func (m *Music) Modes() (repeat string, shuffled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.repeat, m.shuffled
}

// Playing reports whether audio plays (or is paused), and at which position.
func (m *Music) Playing() (playing, paused bool, position int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.playing, m.paused, m.position
}

// Queue returns the song ids in the music playlist.
func (m *Music) Queue() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]int(nil), m.queue...)
}

// Volume returns the current volume.
func (m *Music) Volume() kodi.Volume {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.volume
}

// handle registers a handler that runs with the state locked.
func (m *Music) handle(method string, h func(json.RawMessage) any) {
	m.Handle(method, func(raw json.RawMessage) (any, error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		return h(raw), nil
	})
}

func (m *Music) activePlayers(json.RawMessage) any {
	players := []map[string]any{}
	if m.playing {
		players = append(players, map[string]any{"playerid": AudioPlayerID, "type": "audio"})
	}
	if m.video {
		players = append(players, map[string]any{"playerid": VideoPlayerID, "type": "video"})
	}
	return players
}

func (m *Music) stop(raw json.RawMessage) any {
	var p struct{ PlayerID int }
	json.Unmarshal(raw, &p)
	if p.PlayerID == VideoPlayerID {
		m.video = false
	} else {
		m.playing, m.paused = false, false
	}
	return "OK"
}

func (m *Music) add(raw json.RawMessage) any {
	var p struct{ Item []struct{ SongID int } }
	json.Unmarshal(raw, &p)
	for _, item := range p.Item {
		m.queue = append(m.queue, item.SongID)
	}
	return "OK"
}

func (m *Music) open(raw json.RawMessage) any {
	var p struct{ Item struct{ Position int } }
	json.Unmarshal(raw, &p)
	if p.Item.Position < len(m.queue) {
		m.playing, m.paused, m.position, m.elapsed = true, false, p.Item.Position, 192
	}
	return "OK"
}

func (m *Music) goTo(raw json.RawMessage) any {
	var p struct{ To any }
	json.Unmarshal(raw, &p)
	switch to := p.To.(type) {
	case string:
		if to == "next" {
			m.position++
		} else {
			m.position--
		}
	case float64:
		m.position = int(to)
	}
	m.position = max(0, min(m.position, len(m.queue)-1))
	m.elapsed = 192
	return "OK"
}

func (m *Music) remove(raw json.RawMessage) (any, error) {
	var p struct{ Position int }
	json.Unmarshal(raw, &p)
	m.mu.Lock()
	defer m.mu.Unlock()
	if p.Position < 0 || p.Position >= len(m.queue) || m.playing && p.Position == m.position {
		return nil, &kodi.Error{Code: -32602, Message: "Invalid params."}
	}
	m.queue = slices.Delete(m.queue, p.Position, p.Position+1)
	if m.playing && p.Position < m.position {
		m.position--
	}
	return "OK", nil
}

func (m *Music) swap(raw json.RawMessage) any {
	var p struct{ Position1, Position2 int }
	json.Unmarshal(raw, &p)
	a, b := p.Position1, p.Position2
	if a < 0 || b < 0 || a >= len(m.queue) || b >= len(m.queue) {
		return "OK"
	}
	m.queue[a], m.queue[b] = m.queue[b], m.queue[a]
	switch m.position {
	case a:
		m.position = b
	case b:
		m.position = a
	}
	return "OK"
}

func (m *Music) seek(raw json.RawMessage) any {
	var p struct{ Value struct{ Step string } }
	json.Unmarshal(raw, &p)
	switch p.Value.Step {
	case "smallforward":
		m.elapsed += 30
	case "smallbackward":
		m.elapsed = max(0, m.elapsed-30)
	}
	return map[string]any{"percentage": 0}
}

func (m *Music) song(id int) kodi.Song {
	for _, album := range Albums() {
		for _, s := range Songs(album.ID) {
			if s.ID == id {
				return s
			}
		}
	}
	return kodi.Song{ID: id}
}

func (m *Music) item(json.RawMessage) any {
	if !m.playing {
		return map[string]any{"item": map[string]any{}}
	}
	s := m.song(m.queue[m.position])
	return map[string]any{"item": map[string]any{
		"title": s.Title, "label": s.Title, "artist": []string{s.DisplayArtist}, "album": "", "type": "song",
	}}
}

func (m *Music) properties(json.RawMessage) any {
	total := 0
	if m.playing {
		total = m.song(m.queue[m.position]).Duration
	}
	return map[string]any{
		"time":      globalTime(m.elapsed),
		"totaltime": globalTime(total),
		"speed":     speed(m.paused),
		"position":  m.position,
		"repeat":    m.repeat,
		"shuffled":  m.shuffled,
	}
}

func (m *Music) items(json.RawMessage) any {
	items := []map[string]any{}
	for _, id := range m.queue {
		s := m.song(id)
		items = append(items, map[string]any{
			"id": s.ID, "type": "song", "title": s.Title, "label": s.Title,
			"artist": []string{s.DisplayArtist}, "duration": s.Duration,
		})
	}
	return map[string]any{"items": items}
}

func speed(paused bool) int {
	if paused {
		return 0
	}
	return 1
}

// globalTime returns seconds as Kodi's Global.Time.
func globalTime(seconds int) map[string]int {
	return map[string]int{"hours": seconds / 3600, "minutes": seconds / 60 % 60, "seconds": seconds % 60}
}

// artistDetails knows a lot about Brian Eno and nothing about the others.
func (m *Music) artistDetails(raw json.RawMessage) any {
	var p struct{ ArtistID int }
	json.Unmarshal(raw, &p)
	d := map[string]any{"artistid": p.ArtistID, "label": "", "genre": []string{}, "art": map[string]string{}}
	if p.ArtistID == 1 {
		d["description"] = "English musician, composer and record producer.\n\nA pioneer of ambient music."
		d["genre"] = []string{"Ambient", "Electronic"}
		d["born"] = "1948-05-15"
		d["yearsactive"] = []string{"1970s", "2020s"}
		if m.cover != "" {
			d["art"] = map[string]string{"thumb": m.cover}
		}
	}
	return map[string]any{"artistdetails": d}
}

// albumDetails gives every album a cover (when set); Ambient 1 has more.
func (m *Music) albumDetails(raw json.RawMessage) any {
	var p struct{ AlbumID int }
	json.Unmarshal(raw, &p)
	d := map[string]any{"albumid": p.AlbumID, "genre": []string{}, "art": map[string]string{}}
	for _, a := range Albums() {
		if a.ID == p.AlbumID {
			d["title"], d["year"], d["displayartist"] = a.Title, a.Year, a.DisplayArtist
		}
	}
	if m.cover != "" {
		d["art"] = map[string]string{"thumb": m.cover}
		d["thumbnail"] = m.cover
	}
	if p.AlbumID == 11 {
		d["genre"] = []string{"Ambient"}
		d["albumlabel"] = "Polydor"
		d["releasetype"] = "album"
		d["albumduration"] = 2628
		d["totaldiscs"] = 1
		d["description"] = "Four pieces for airports."
	}
	return map[string]any{"albumdetails": d}
}

// songDetails gives song 102 the full set of details.
func (m *Music) songDetails(raw json.RawMessage) any {
	var p struct{ SongID int }
	json.Unmarshal(raw, &p)
	s := m.song(p.SongID)
	d := map[string]any{
		"songid": s.ID, "title": s.Title, "track": s.Track, "disc": s.Disc, "duration": s.Duration,
		"artist": []string{s.DisplayArtist}, "genre": []string{}, "art": map[string]string{},
	}
	if m.cover != "" {
		d["art"] = map[string]string{"album.thumb": m.cover}
	}
	if p.SongID == 102 {
		d["album"] = "Ambient 1"
		d["year"] = 1978
		d["genre"] = []string{"Ambient"}
		d["displaycomposer"] = "Brian Eno"
		d["userrating"] = 8
		d["playcount"] = 3
		d["lastplayed"] = "2026-11-01 21:00:00"
		d["bitrate"] = 1411
		d["samplerate"] = 44100
		d["channels"] = 2
		d["comment"] = "Remastered."
		d["lyrics"] = "(instrumental)"
		d["file"] = "/pub/music/Brian Eno/Ambient 1/02.flac"
	}
	return map[string]any{"songdetails": d}
}
