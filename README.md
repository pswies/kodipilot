# kodipilot

A keyboard terminal UI for [Kodi](https://kodi.tv)'s music library. Browse album
artists → albums → tracks, play them or add them to Kodi's queue, and manage the
queue: on the Kodi machine itself, over SSH, or from another computer over HTTP.
The keys follow Kodi's own keyboard map wherever Kodi has an equivalent (Space,
x, `.` `,`, `+` `-`, F8, Tab), and starting a track queues the rest of its album,
as Kodi does.

```
 Library │ Queue                                 2/4 · repeat all · shuffle off
    1  1/1  · Brian Eno                                                   17:01
  ▶ 2  2/1  · Brian Eno                                                   08:55
    3  1/2  · Br╭──────────────────────────────────────────────╮          11:52
▌   4  2/2  · Br│ Volume ▕█████████████████░░░░░░░░░░░░░▏  55% │          06:00
                ╰──────────────────────────────────────────────╯


 ▶ 03:12/08:55  Brian Eno – 2/1  [2/4]                                    ⟳ all
 ? help  Tab view  Enter open  p play  q queue  Space pause  +/- volume  Q quit
```

- **Library** and **Queue** views, tabs switched with Tab. The library plays or
  queues a whole artist, an album or a track; the queue view shows Kodi's queue
  in play order and lets you jump, remove, reorder and clear.
- Pause, stop, next and previous, seek, volume, mute, repeat and shuffle work in
  both views.
- A details pane with everything Kodi knows about the highlighted artist,
  album or track, and its cover or artist image drawn in the terminal.
- One static binary and no saved state: everything comes from Kodi.
- Quitting (`Q`, after a confirmation, or ctrl+c at once) leaves the music
  playing.

## Install

```sh
go install github.com/pswies/kodipilot/cmd/kodipilot@latest
```

The [latest release](https://github.com/pswies/kodipilot/releases/latest) has
binaries for Linux and macOS (amd64 and arm64), `.deb` packages and checksums.
On a Debian or Ubuntu Kodi box, for example a Raspberry Pi, download the `.deb`
for your architecture (`kodipilot_<version>_linux_arm64.deb` on a Pi) and
install it:

```sh
sudo apt install ./kodipilot_*_linux_arm64.deb
```

Homebrew: coming soon.

## Usage

### On the Kodi machine (local TCP)

By default kodipilot uses Kodi's JSON-RPC socket at `127.0.0.1:9090`, which works
out of the box. Over SSH, ask for a terminal:

```sh
ssh -t media-box kodipilot
```

### From another computer (HTTP)

Any host other than a loopback address goes through Kodi's web server,
`http://HOST:8080/jsonrpc`, with HTTP Basic authentication. Turn it on in Kodi
under **Settings → Services → Control → Allow remote control via HTTP**, and set
a port, a username (Kodi's default is `kodi`) and a password. Then:

```sh
kodipilot --host media-box
```

kodipilot asks for the password unless the environment or the config file has
it. There is deliberately no password flag: it would show in the process list.
The web server sends the password in clear text, so keep to a trusted network.

### Flags, environment and config file

| Flag          | Environment          | Config file | Default                                     |
|---------------|----------------------|-------------|---------------------------------------------|
| `--host`      | `KODIPILOT_HOST`     | `host`      | `127.0.0.1`                                 |
| `--port`      |                      | `port`      | `9090` for tcp, `8080` for http             |
| `--transport` |                      | `transport` | `tcp` for a loopback host, `http` otherwise |
| `--user`      | `KODIPILOT_USER`     | `user`      | `kodi` (http only)                          |
|               | `KODIPILOT_PASSWORD` | `password`  | asked for (http only)                       |
| `--art`       | `KODIPILOT_ART`      | `art`       | `symbols`                                   |

Flags override the environment, which overrides the config file,
`~/.config/kodipilot/config.toml` (or `$XDG_CONFIG_HOME/kodipilot/config.toml`):

```toml
host = "media-box"
user = "kodi"
password = "secret" # keep the file private: chmod 600 config.toml
```

`art` sets how images are drawn: `symbols` (the default), `blocks` for fonts
without the sextant characters (U+1FB00–U+1FB3B), `ascii`, or `off`.

kodipilot warns when a config file with a password is readable by others.
`--version` prints the version and `--help` the flags and keys. Colours follow
light and dark terminals and stay off when `NO_COLOR` is set.

## Keys

`?` shows these keys in the app, and `kodipilot --help` prints them.

**Everywhere**

| Key | Action | Kodi |
|---|---|---|
| ↑ ↓ PgUp PgDn | move | same |
| Home / End | first / last row | same |
| Tab | switch Library ⇄ Queue | Tab |
| Space | pause / resume | Space |
| x | stop | x |
| . / , | next / previous track | . / , |
| shift+← / shift+→ | seek back / forward | ← / → |
| + = / - | volume up / down by 5% | + = / - |
| F8, m | mute on / off | F8 |
| r | repeat: off → all → one → off | — |
| s | shuffle on / off | — |
| F5, ctrl+r | reload the library | — |
| ? / h | this help | — |
| Q | quit, after asking (the music keeps playing) | — |
| ctrl+c | quit now | — |

**Details**

| Key | Action | Kodi |
|---|---|---|
| i | show / hide (full screen when narrow) | i |
| [ / ] | scroll the details | — |

**Library**

| Key | Action | Kodi |
|---|---|---|
| Enter, → | open; on a track: play its album from there | Enter |
| ←, Backspace, Esc | back (Esc at the top asks to quit) | Backspace, Esc |
| p | play now: artist, album, or from a track | p |
| q | add to the queue | q |

**Queue**

| Key | Action | Kodi |
|---|---|---|
| Enter, p | play the selected item | Enter, p |
| ← / → | seek back / forward | ← / → |
| Delete, d | remove the selected item | Delete |
| shift+↑ / ↓, K / J | move the selected item up / down | — |
| c | clear the queue (asks first) | — |

Playing replaces whatever plays, music or video; queueing appends to Kodi's
queue without starting playback. Repeat, shuffle and seek need something
playing. Kodi refuses to remove the playing item, and so does kodipilot.

## Details pane

On terminals at least 90 columns wide and 14 rows tall, a pane left of the list
shows the highlighted item. On 120 columns and more the list takes a quarter of
the width. Narrower but tall terminals (at least 26 rows) show the details above
the list, across the full width. `i`, Kodi's info key, hides and shows the pane; on smaller terminals
it opens the details full-screen instead (Esc, `←` or `i` close them). `[` and
`]` scroll the details. The image sits at the top left in a box 32, 64 or 96
columns wide, the largest that leaves the text 50 columns beside it; the text
flows on underneath. On narrower panes the image keeps the 32-column box, with
the text beside it while 20 columns remain and below it otherwise; under 32
columns there is no image.

- **Artist:** image, genre, style, born / formed / years active, album count and
  biography.
- **Album:** cover, artist, year, genre, style, mood, record label, release type,
  length, tracks and discs, and the review.
- **Track:** cover, artist, album, year, track, duration, genre, mood, composer,
  ratings, plays, audio format, file, comment and lyrics.

Fields Kodi leaves empty are left out. Images are drawn with coloured block and
sextant characters, in the manner of [chafa](https://hpjansson.org/chafa/)'s
symbols mode, by a small pure-Go renderer. kodipilot finds them, in order:

1. as local files, when it runs on the Kodi machine;
2. on the web, for `http(s)` sources such as fanart.tv (10 s, 10 MB at most);
3. from Kodi's web server (`/image/`), with the HTTP transport, which also
   serves art embedded in music files.

## Build and test

With Go 1.26 or later:

```sh
go build ./cmd/kodipilot
go test -race ./...
go vet ./... && staticcheck ./...
goreleaser release --snapshot --clean   # every release artifact, into dist/
```

The tests need no Kodi. They run against an in-memory fake and a fake JSON-RPC
TCP server (`internal/kodi/koditest`).

## License

[MIT](LICENSE) © Piotr Święs · <https://github.com/pswies/kodipilot>
