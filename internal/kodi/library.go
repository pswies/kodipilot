package kodi

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// Artist is an album artist in Kodi's music library.
type Artist struct {
	ID    int    `json:"artistid"`
	Name  string `json:"artist"`
	Label string `json:"label"`
}

// Album is an album in Kodi's music library.
type Album struct {
	ID            int    `json:"albumid"`
	Title         string `json:"title"`
	Label         string `json:"label"`
	Year          int    `json:"year"` // 0 when unknown
	ArtistIDs     []int  `json:"artistid"`
	DisplayArtist string `json:"displayartist"`
	Duration      int    `json:"albumduration"` // seconds; 0 when unknown
}

// Song is a track of an album in Kodi's music library.
type Song struct {
	ID            int    `json:"songid"`
	Title         string `json:"title"`
	Label         string `json:"label"`
	Track         int    `json:"track"`
	Disc          int    `json:"disc"`
	Duration      int    `json:"duration"` // seconds
	DisplayArtist string `json:"displayartist"`
}

// Library is a snapshot of Kodi's album artists and albums. Songs are
// fetched on demand and cached. It is safe for concurrent use.
type Library struct {
	client  Client
	artists []Artist
	albums  map[int][]Album // by album artist id

	mu    sync.Mutex
	songs map[int][]Song // by album id
}

// LoadLibrary reads the album artists and albums from Kodi. Albums are listed
// under each of their album artists, sorted by year (unknown last) and title;
// artists without albums are dropped.
func LoadLibrary(ctx context.Context, client Client) (*Library, error) {
	var artists struct{ Artists []Artist }
	err := client.Call(ctx, "AudioLibrary.GetArtists", map[string]any{
		"albumartistsonly": true,
		"sort":             map[string]any{"method": "artist", "ignorearticle": true},
	}, &artists)
	if err != nil {
		return nil, fmt.Errorf("loading artists: %w", err)
	}
	var albums struct{ Albums []Album }
	err = client.Call(ctx, "AudioLibrary.GetAlbums", map[string]any{
		"properties": []string{"title", "year", "artistid", "displayartist", "albumduration"},
	}, &albums)
	if err != nil {
		return nil, fmt.Errorf("loading albums: %w", err)
	}

	byArtist := make(map[int][]Album)
	for _, album := range albums.Albums {
		album.Title = cmp.Or(album.Title, album.Label)
		for i, id := range album.ArtistIDs {
			if !slices.Contains(album.ArtistIDs[:i], id) {
				byArtist[id] = append(byArtist[id], album)
			}
		}
	}
	for _, list := range byArtist {
		slices.SortStableFunc(list, compareAlbums)
	}
	lib := &Library{client: client, albums: byArtist, songs: make(map[int][]Song)}
	for _, artist := range artists.Artists {
		if len(byArtist[artist.ID]) > 0 {
			artist.Name = cmp.Or(artist.Name, artist.Label)
			lib.artists = append(lib.artists, artist)
		}
	}
	return lib, nil
}

// Artists returns the album artists that have albums, in Kodi's order.
func (l *Library) Artists() []Artist { return l.artists }

// Albums returns the albums of an album artist.
func (l *Library) Albums(artistID int) []Album { return l.albums[artistID] }

// Songs returns the tracks of an album sorted by disc, track and title.
func (l *Library) Songs(ctx context.Context, albumID int) ([]Song, error) {
	l.mu.Lock()
	songs, ok := l.songs[albumID]
	l.mu.Unlock()
	if ok {
		return songs, nil
	}
	var result struct{ Songs []Song }
	err := l.client.Call(ctx, "AudioLibrary.GetSongs", map[string]any{
		"filter":     map[string]any{"albumid": albumID},
		"properties": []string{"title", "track", "disc", "duration", "displayartist"},
	}, &result)
	if err != nil {
		return nil, fmt.Errorf("loading songs: %w", err)
	}
	songs = result.Songs
	for i := range songs {
		songs[i].Title = cmp.Or(songs[i].Title, songs[i].Label)
	}
	slices.SortStableFunc(songs, compareSongs)

	l.mu.Lock()
	l.songs[albumID] = songs
	l.mu.Unlock()
	return songs, nil
}

// ArtistSongs returns the songs of all albums of an album artist, album
// after album in display order.
func (l *Library) ArtistSongs(ctx context.Context, artistID int) ([]Song, error) {
	var all []Song
	for _, album := range l.Albums(artistID) {
		songs, err := l.Songs(ctx, album.ID)
		if err != nil {
			return nil, err
		}
		all = append(all, songs...)
	}
	return all, nil
}

func compareAlbums(a, b Album) int {
	return cmp.Or(
		cmp.Compare(yearOrLast(a.Year), yearOrLast(b.Year)),
		cmp.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)),
	)
}

func compareSongs(a, b Song) int {
	return cmp.Or(
		cmp.Compare(a.Disc, b.Disc),
		cmp.Compare(a.Track, b.Track),
		cmp.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)),
	)
}

func yearOrLast(year int) int {
	if year == 0 {
		return 9999
	}
	return year
}

// The properties requested for details. Kodi rejects a whole request that
// names a property it does not know, so these are exactly the ones Kodi's
// JSON-RPC API (v13) defines for each kind.
var (
	artistDetailProperties = []string{
		"description", "genre", "style", "born", "formed", "yearsactive", "thumbnail", "fanart", "art",
	}
	albumDetailProperties = []string{
		"title", "displayartist", "description", "genre", "style", "mood", "albumlabel", "year",
		"releasetype", "albumduration", "totaldiscs", "thumbnail", "art",
	}
	songDetailProperties = []string{
		"title", "artist", "album", "year", "genre", "duration", "track", "disc", "rating", "userrating",
		"playcount", "lastplayed", "comment", "lyrics", "file", "bitrate", "samplerate", "channels",
		"displaycomposer", "mood", "thumbnail", "art",
	}
)

// ArtistDetails is what Kodi knows about an artist besides the name.
type ArtistDetails struct {
	Description string            `json:"description"`
	Genre       []string          `json:"genre"`
	Style       []string          `json:"style"`
	Born        string            `json:"born"`
	Formed      string            `json:"formed"`
	YearsActive []string          `json:"yearsactive"`
	Thumbnail   string            `json:"thumbnail"`
	Fanart      string            `json:"fanart"`
	Art         map[string]string `json:"art"`
}

// Image returns the art URL of the artist's image, or "".
func (a ArtistDetails) Image() string { return cmp.Or(a.Art["thumb"], a.Thumbnail) }

// AlbumDetails is what Kodi knows about an album.
type AlbumDetails struct {
	Title         string            `json:"title"`
	DisplayArtist string            `json:"displayartist"`
	Description   string            `json:"description"` // the review
	Genre         []string          `json:"genre"`
	Style         []string          `json:"style"`
	Mood          []string          `json:"mood"`
	Label         string            `json:"albumlabel"` // the record label
	Year          int               `json:"year"`
	ReleaseType   string            `json:"releasetype"`
	Duration      int               `json:"albumduration"` // seconds
	TotalDiscs    int               `json:"totaldiscs"`
	Thumbnail     string            `json:"thumbnail"`
	Art           map[string]string `json:"art"`
}

// Image returns the art URL of the album's cover, or "".
func (a AlbumDetails) Image() string { return cmp.Or(a.Art["thumb"], a.Thumbnail) }

// SongDetails is what Kodi knows about a song.
type SongDetails struct {
	Title      string            `json:"title"`
	Artist     []string          `json:"artist"`
	Album      string            `json:"album"`
	Year       int               `json:"year"`
	Genre      []string          `json:"genre"`
	Mood       []string          `json:"mood"`
	Duration   int               `json:"duration"` // seconds
	Track      int               `json:"track"`
	Disc       int               `json:"disc"`
	Rating     float64           `json:"rating"`     // 0–10
	UserRating int               `json:"userrating"` // 0–10
	PlayCount  int               `json:"playcount"`
	LastPlayed string            `json:"lastplayed"`
	Comment    string            `json:"comment"`
	Lyrics     string            `json:"lyrics"`
	File       string            `json:"file"`
	Bitrate    int               `json:"bitrate"`    // kbit/s
	SampleRate int               `json:"samplerate"` // Hz
	Channels   int               `json:"channels"`
	Composer   string            `json:"displaycomposer"`
	Thumbnail  string            `json:"thumbnail"`
	Art        map[string]string `json:"art"`
}

// Image returns the art URL of the song's album cover, or "".
func (s SongDetails) Image() string {
	return cmp.Or(s.Art["album.thumb"], s.Art["thumb"], s.Thumbnail)
}

// ArtistDetails fetches the details of an artist.
func (l *Library) ArtistDetails(ctx context.Context, artistID int) (ArtistDetails, error) {
	var result struct{ ArtistDetails ArtistDetails }
	err := l.client.Call(ctx, "AudioLibrary.GetArtistDetails", map[string]any{
		"artistid": artistID, "properties": artistDetailProperties,
	}, &result)
	return result.ArtistDetails, err
}

// AlbumDetails fetches the details of an album.
func (l *Library) AlbumDetails(ctx context.Context, albumID int) (AlbumDetails, error) {
	var result struct{ AlbumDetails AlbumDetails }
	err := l.client.Call(ctx, "AudioLibrary.GetAlbumDetails", map[string]any{
		"albumid": albumID, "properties": albumDetailProperties,
	}, &result)
	return result.AlbumDetails, err
}

// SongDetails fetches the details of a song.
func (l *Library) SongDetails(ctx context.Context, songID int) (SongDetails, error) {
	var result struct{ SongDetails SongDetails }
	err := l.client.Call(ctx, "AudioLibrary.GetSongDetails", map[string]any{
		"songid": songID, "properties": songDetailProperties,
	}, &result)
	return result.SongDetails, err
}
