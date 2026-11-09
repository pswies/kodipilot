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
		"properties": []string{"title", "year", "artistid", "displayartist"},
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
