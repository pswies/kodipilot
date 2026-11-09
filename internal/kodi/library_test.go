package kodi_test

import (
	"slices"
	"testing"

	"github.com/pswies/kodipilot/internal/kodi"
	"github.com/pswies/kodipilot/internal/kodi/koditest"
)

func loadLibrary(t *testing.T) (*kodi.Library, *koditest.Music) {
	t.Helper()
	music := koditest.NewMusic()
	lib, err := kodi.LoadLibrary(ctx(t), music)
	if err != nil {
		t.Fatal(err)
	}
	return lib, music
}

func albumIDs(albums []kodi.Album) []int {
	var ids []int
	for _, a := range albums {
		ids = append(ids, a.ID)
	}
	return ids
}

func songIDs(songs []kodi.Song) []int {
	var ids []int
	for _, s := range songs {
		ids = append(ids, s.ID)
	}
	return ids
}

func TestLibraryRequests(t *testing.T) {
	_, music := loadLibrary(t)
	calls := music.Calls()
	want := koditest.Call{
		Method: "AudioLibrary.GetArtists",
		Params: `{"albumartistsonly":true,"sort":{"ignorearticle":true,"method":"artist"}}`,
	}
	if calls[0] != want || calls[1].Method != "AudioLibrary.GetAlbums" {
		t.Errorf("calls = %v", calls)
	}
}

func TestLibraryDropsArtistsWithoutAlbums(t *testing.T) {
	lib, _ := loadLibrary(t)
	var ids []int
	for _, a := range lib.Artists() {
		ids = append(ids, a.ID)
	}
	if !slices.Equal(ids, []int{1, 2, 4}) {
		t.Errorf("artists = %v, want [1 2 4]", ids)
	}
}

func TestLibraryAlbumUnderEachArtist(t *testing.T) {
	lib, _ := loadLibrary(t)
	if !slices.Contains(albumIDs(lib.Albums(1)), 12) {
		t.Errorf("album 12 missing under artist 1")
	}
	if got := albumIDs(lib.Albums(2)); !slices.Equal(got, []int{12}) {
		t.Errorf("artist 2 albums = %v, want [12]", got)
	}
}

func TestLibraryAlbumOrder(t *testing.T) {
	lib, _ := loadLibrary(t)
	// 1975; 1978 "Ambient 1" before "Music for Films"; 1981; no year last.
	if got := albumIDs(lib.Albums(1)); !slices.Equal(got, []int{14, 11, 10, 12, 13}) {
		t.Errorf("albums = %v", got)
	}
}

func TestLibrarySongOrder(t *testing.T) {
	lib, _ := loadLibrary(t)
	for album, want := range map[int][]int{
		11: {101, 102, 103, 104}, // disc 0
		12: {199, 200, 201, 202}, // discs 1 and 2
	} {
		songs, err := lib.Songs(ctx(t), album)
		if err != nil {
			t.Fatal(err)
		}
		if got := songIDs(songs); !slices.Equal(got, want) {
			t.Errorf("album %d songs = %v, want %v", album, got, want)
		}
	}
}

func TestLibrarySongsCached(t *testing.T) {
	lib, music := loadLibrary(t)
	lib.Songs(ctx(t), 11)
	lib.Songs(ctx(t), 11)
	n := 0
	for _, m := range music.Methods() {
		if m == "AudioLibrary.GetSongs" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("GetSongs called %d times, want 1", n)
	}
}
