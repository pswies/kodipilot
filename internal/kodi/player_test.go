package kodi_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pswies/kodipilot/internal/kodi"
	"github.com/pswies/kodipilot/internal/kodi/koditest"
)

// startPlayer returns a Player on a fake Kodi playing album 11 from track 2
// (when audio is set) and, when video is set, a video.
func startPlayer(t *testing.T, audio, video bool) (*kodi.Player, *koditest.Music, []kodi.Song) {
	t.Helper()
	lib, music := loadLibrary(t)
	songs, err := lib.Songs(ctx(t), 11)
	if err != nil {
		t.Fatal(err)
	}
	player := kodi.NewPlayer(music)
	if audio {
		if err := player.Play(ctx(t), songs, 1); err != nil {
			t.Fatal(err)
		}
	}
	music.SetVideo(video)
	music.Reset()
	return player, music, songs
}

func callStrings(calls []koditest.Call) []string {
	var out []string
	for _, c := range calls {
		out = append(out, strings.TrimSpace(c.Method+" "+c.Params))
	}
	return out
}

func TestPlayReplacesPlayback(t *testing.T) {
	player, music, songs := startPlayer(t, true, true)
	if err := player.Play(ctx(t), songs, 2); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"Player.GetActivePlayers",
		`Player.Stop {"playerid":0}`,
		`Player.Stop {"playerid":1}`,
		`Playlist.Clear {"playlistid":0}`,
		`Playlist.Add {"item":[{"songid":101},{"songid":102},{"songid":103},{"songid":104}],"playlistid":0}`,
		`Player.Open {"item":{"playlistid":0,"position":2}}`,
	}
	if got := callStrings(music.Calls()); !slices.Equal(got, want) {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestPlayNothingPlaying(t *testing.T) {
	player, music, songs := startPlayer(t, false, false)
	if err := player.Play(ctx(t), songs, 0); err != nil {
		t.Fatal(err)
	}
	want := []string{"Player.GetActivePlayers", "Playlist.Clear", "Playlist.Add", "Player.Open"}
	if got := music.Methods(); !slices.Equal(got, want) {
		t.Errorf("methods = %v, want %v", got, want)
	}
	if playing, _, pos := music.Playing(); !playing || pos != 0 {
		t.Errorf("playing = %v at %d", playing, pos)
	}
}

func TestControls(t *testing.T) {
	player, music, _ := startPlayer(t, true, true)
	for _, f := range []func() error{
		func() error { return player.PlayPause(ctx(t)) },
		func() error { return player.Next(ctx(t)) },
		func() error { return player.Previous(ctx(t)) },
		func() error { return player.GoTo(ctx(t), 3) },
		func() error { return player.Stop(ctx(t)) },
	} {
		if err := f(); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, c := range callStrings(music.Calls()) {
		if c != "Player.GetActivePlayers" {
			got = append(got, c)
		}
	}
	want := []string{
		`Player.PlayPause {"playerid":0}`,
		`Player.GoTo {"playerid":0,"to":"next"}`,
		`Player.GoTo {"playerid":0,"to":"previous"}`,
		`Player.GoTo {"playerid":0,"to":3}`,
		`Player.Stop {"playerid":0}`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
}

func TestControlsWithoutAudioPlayer(t *testing.T) {
	player, music, _ := startPlayer(t, false, true)
	for name, f := range map[string]func() error{
		"PlayPause":     func() error { return player.PlayPause(ctx(t)) },
		"Next":          func() error { return player.Next(ctx(t)) },
		"Previous":      func() error { return player.Previous(ctx(t)) },
		"Stop":          func() error { return player.Stop(ctx(t)) },
		"Seek":          func() error { return player.Seek(ctx(t), true) },
		"CycleRepeat":   func() error { return player.CycleRepeat(ctx(t)) },
		"ToggleShuffle": func() error { return player.ToggleShuffle(ctx(t)) },
	} {
		if err := f(); !errors.Is(err, kodi.ErrNotPlaying) {
			t.Errorf("%s: err = %v, want ErrNotPlaying", name, err)
		}
	}
	for _, m := range music.Methods() {
		if m != "Player.GetActivePlayers" {
			t.Errorf("unexpected call %s", m)
		}
	}
	if status, err := player.NowPlaying(ctx(t)); status != nil || err != nil {
		t.Errorf("NowPlaying = %v, %v; want nil", status, err)
	}
}

func TestGoToWhenStoppedOpensQueue(t *testing.T) {
	player, music, _ := startPlayer(t, true, false)
	player.Stop(ctx(t))
	music.Reset()
	if err := player.GoTo(ctx(t), 2); err != nil {
		t.Fatal(err)
	}
	want := []string{"Player.GetActivePlayers", `Player.Open {"item":{"playlistid":0,"position":2}}`}
	if got := callStrings(music.Calls()); !slices.Equal(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
}

func setVolumeCalls(music *koditest.Music) []string {
	var out []string
	for _, c := range music.Calls() {
		if c.Method == "Application.SetVolume" {
			out = append(out, c.Params)
		}
	}
	return out
}

func TestVolumeSteps(t *testing.T) {
	player, music, _ := startPlayer(t, false, false)
	for _, steps := range []int{+1, +1, -1} {
		if _, err := player.ChangeVolume(ctx(t), steps); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{`{"volume":55}`, `{"volume":60}`, `{"volume":55}`}
	if got := setVolumeCalls(music); !slices.Equal(got, want) {
		t.Errorf("SetVolume = %v, want %v", got, want)
	}
	if first := music.Calls()[0]; first.Method != "Application.GetProperties" || first.Params != `{"properties":["volume","muted"]}` {
		t.Errorf("first call = %v", first)
	}
	if slices.Contains(music.Methods(), "Player.GetActivePlayers") {
		t.Error("volume needs no player")
	}
}

func TestVolumeClamped(t *testing.T) {
	player, music, _ := startPlayer(t, false, false)
	music.SetVolume(kodi.Volume{Level: 98})
	for range 2 {
		if v, _ := player.ChangeVolume(ctx(t), +1); v.Level != 100 {
			t.Errorf("volume = %d, want 100", v.Level)
		}
	}
	music.SetVolume(kodi.Volume{Level: 3, Muted: true})
	for range 2 {
		if v, _ := player.ChangeVolume(ctx(t), -1); v != (kodi.Volume{Level: 0, Muted: true}) {
			t.Errorf("volume = %v, want 0 muted", v)
		}
	}
	want := []string{`{"volume":100}`, `{"volume":0}`} // no call at a bound
	if got := setVolumeCalls(music); !slices.Equal(got, want) {
		t.Errorf("SetVolume = %v, want %v", got, want)
	}
}

func TestNowPlayingAndQueue(t *testing.T) {
	player, music, _ := startPlayer(t, true, false)
	music.SetModes("all", true)
	status, err := player.NowPlaying(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	want := kodi.Status{
		Artist: "Brian Eno", Title: "2/1", Elapsed: 3*time.Minute + 12*time.Second, Total: 535 * time.Second,
		Position: 1, Count: 4, Repeat: "all", Shuffled: true,
	}
	if *status != want {
		t.Errorf("status = %+v\nwant %+v", *status, want)
	}
	queue, err := player.Queue(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(queue) != 4 || queue[1].ID != 102 || queue[1].Title != "2/1" || queue[1].Duration != 535*time.Second {
		t.Errorf("queue = %+v", queue)
	}
}

// wantCalls checks the calls made since the last Reset, except
// Player.GetActivePlayers.
func wantCalls(t *testing.T, music *koditest.Music, want ...string) {
	t.Helper()
	var got []string
	for _, c := range callStrings(music.Calls()) {
		if c != "Player.GetActivePlayers" {
			got = append(got, c)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestPlayArtist(t *testing.T) {
	player, music, _ := startPlayer(t, false, false)
	lib, err := kodi.LoadLibrary(ctx(t), music)
	if err != nil {
		t.Fatal(err)
	}
	songs, err := lib.ArtistSongs(ctx(t), 2) // David Byrne: one album, two discs
	if err != nil {
		t.Fatal(err)
	}
	music.Reset()
	if err := player.Play(ctx(t), songs, 0); err != nil {
		t.Fatal(err)
	}
	wantCalls(t, music,
		`Playlist.Clear {"playlistid":0}`,
		`Playlist.Add {"item":[{"songid":199},{"songid":200},{"songid":201},{"songid":202}],"playlistid":0}`,
		`Player.Open {"item":{"playlistid":0,"position":0}}`,
	)
}

func TestArtistSongsInDisplayOrder(t *testing.T) {
	lib, _ := loadLibrary(t)
	songs, err := lib.ArtistSongs(ctx(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	// Albums 14, 11, 10, 12, 13; only 11 and 12 have songs in the fake.
	want := []int{101, 102, 103, 104, 199, 200, 201, 202}
	if got := songIDs(songs); !slices.Equal(got, want) {
		t.Errorf("songs = %v, want %v", got, want)
	}
}

func TestEnqueue(t *testing.T) {
	player, music, songs := startPlayer(t, true, false)
	if err := player.Enqueue(ctx(t), songs[2:3]); err != nil {
		t.Fatal(err)
	}
	wantCalls(t, music, `Playlist.Add {"item":[{"songid":103}],"playlistid":0}`)
	if q := music.Queue(); !slices.Equal(q, []int{101, 102, 103, 104, 103}) {
		t.Errorf("queue = %v", q)
	}
	if _, _, pos := music.Playing(); pos != 1 {
		t.Errorf("enqueueing moved playback to %d", pos)
	}
}

func TestEnqueueStartsNothing(t *testing.T) {
	player, music, songs := startPlayer(t, false, false)
	if err := player.Enqueue(ctx(t), songs); err != nil {
		t.Fatal(err)
	}
	wantCalls(t, music,
		`Playlist.Add {"item":[{"songid":101},{"songid":102},{"songid":103},{"songid":104}],"playlistid":0}`)
	if playing, _, _ := music.Playing(); playing {
		t.Error("Enqueue started playback")
	}
}

func TestSeekRepeatShuffle(t *testing.T) {
	player, music, _ := startPlayer(t, true, false)
	for _, f := range []func() error{
		func() error { return player.Seek(ctx(t), true) },
		func() error { return player.Seek(ctx(t), false) },
		func() error { return player.CycleRepeat(ctx(t)) },
		func() error { return player.ToggleShuffle(ctx(t)) },
	} {
		if err := f(); err != nil {
			t.Fatal(err)
		}
	}
	wantCalls(t, music,
		`Player.Seek {"playerid":0,"value":{"step":"smallforward"}}`,
		`Player.Seek {"playerid":0,"value":{"step":"smallbackward"}}`,
		`Player.SetRepeat {"playerid":0,"repeat":"cycle"}`,
		`Player.SetShuffle {"playerid":0,"shuffle":"toggle"}`,
	)
	if repeat, shuffled := music.Modes(); repeat != "all" || !shuffled {
		t.Errorf("modes = %s, %v", repeat, shuffled)
	}
}

func TestToggleMute(t *testing.T) {
	player, music, _ := startPlayer(t, false, false)
	v, err := player.ToggleMute(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if v != (kodi.Volume{Level: 50, Muted: true}) {
		t.Errorf("volume = %+v", v)
	}
	wantCalls(t, music,
		`Application.SetMute {"mute":"toggle"}`,
		`Application.GetProperties {"properties":["volume","muted"]}`,
	)
	if v, _ := player.ToggleMute(ctx(t)); v.Muted {
		t.Error("second toggle left it muted")
	}
}

func TestRemove(t *testing.T) {
	player, music, _ := startPlayer(t, true, false) // playing position 1
	if err := player.Remove(ctx(t), 3); err != nil {
		t.Fatal(err)
	}
	wantCalls(t, music,
		`Player.GetProperties {"playerid":0,"properties":["position"]}`,
		`Playlist.Remove {"playlistid":0,"position":3}`,
	)
	if q := music.Queue(); !slices.Equal(q, []int{101, 102, 103}) {
		t.Errorf("queue = %v", q)
	}

	music.Reset()
	if err := player.Remove(ctx(t), 1); !errors.Is(err, kodi.ErrPlayingItem) {
		t.Errorf("removing the playing item: err = %v", err)
	}
	if slices.Contains(music.Methods(), "Playlist.Remove") {
		t.Error("Playlist.Remove called for the playing item")
	}

	player.Stop(ctx(t))
	music.Reset()
	if err := player.Remove(ctx(t), 1); err != nil {
		t.Fatal(err)
	}
	wantCalls(t, music, `Playlist.Remove {"playlistid":0,"position":1}`)
}

func TestSwapAndClear(t *testing.T) {
	player, music, _ := startPlayer(t, true, false)
	if err := player.Swap(ctx(t), 1, 2); err != nil {
		t.Fatal(err)
	}
	if err := player.Clear(ctx(t)); err != nil {
		t.Fatal(err)
	}
	wantCalls(t, music,
		`Playlist.Swap {"playlistid":0,"position1":1,"position2":2}`,
		`Playlist.Clear {"playlistid":0}`,
	)
}
