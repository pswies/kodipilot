package kodi_test

import (
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
		if err := player.PlayAlbum(ctx(t), songs, 1); err != nil {
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

func TestPlayAlbumReplacesPlayback(t *testing.T) {
	player, music, songs := startPlayer(t, true, true)
	if err := player.PlayAlbum(ctx(t), songs, 2); err != nil {
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

func TestPlayAlbumNothingPlaying(t *testing.T) {
	player, music, songs := startPlayer(t, false, false)
	if err := player.PlayAlbum(ctx(t), songs, 0); err != nil {
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
	player.PlayPause(ctx(t))
	player.Next(ctx(t))
	player.Previous(ctx(t))
	player.Stop(ctx(t))
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
	if len(queue) != 4 || queue[1].Title != "2/1" || queue[1].Duration != 535*time.Second {
		t.Errorf("queue = %+v", queue)
	}
}
