# kodipilot

A keyboard terminal UI for [Kodi](https://kodi.tv)'s music library. Browse album
artists → albums → tracks and play them in Kodi: on the Kodi machine itself, over
SSH, or from another computer over HTTP. Starting a track queues the rest of its
album, as Kodi's own UI does.

```
 Library ▸ Artists ▸ Brian Eno ▸ Ambient 1                                2/4
  01  1/1                                                               17:01
▌ 02  2/1                                                               08:55
  03  1/2      ╭──────────────────────────────────────────────╮         11:52
  04  2/2      │ Volume ▕█████████████████░░░░░░░░░░░░░▏  55% │         06:00
               ╰──────────────────────────────────────────────╯


 ▶ 03:12/08:55  Brian Eno – 2/1  [2/4]
 ? help  Tab view  → open  ← back  Enter play  Space pause  +/- volume  q quit
```

- **Library** and **Player** views, switched with Tab. The player view shows
  Kodi's play queue in play order, with the playing item, repeat and shuffle.
- Playback keys work in both views: pause, next, previous, stop and volume.
- One static binary and no saved state: everything comes from Kodi.
- Quitting leaves the music playing.

## Install

```sh
go install github.com/pswies/kodipilot/cmd/kodipilot@latest
```

Each [release](https://github.com/pswies/kodipilot/releases) has binaries for
Linux and macOS (amd64 and arm64) and `.deb` packages:

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

Flags override the environment, which overrides the config file,
`~/.config/kodipilot/config.toml` (or `$XDG_CONFIG_HOME/kodipilot/config.toml`):

```toml
host = "media-box"
user = "kodi"
password = "secret" # keep the file private: chmod 600 config.toml
```

kodipilot warns when a config file with a password is readable by others.
`--version` prints the version and `--help` the flags and keys. Colours follow
light and dark terminals and stay off when `NO_COLOR` is set.

## Keys

| Key             | Action                              |
|-----------------|-------------------------------------|
| Up / Down       | move                                |
| PgUp / PgDn     | move by a page                      |
| Home / End      | first / last row                    |
| Right           | open: artist → albums → tracks      |
| Left, Backspace | back                                |
| Enter           | on an artist: open                  |
|                 | on an album: play it from track 1   |
|                 | on a track: play the album from it  |
|                 | in the queue: play that item        |
| Tab             | switch between Library and Player   |
| Space           | pause / resume                      |
| n / p           | next / previous track               |
| s               | stop                                |
| + / -           | volume up / down by 5%              |
| ? / h           | help (Esc or q closes it)           |
| q               | quit (the music keeps playing)      |

Playing an album or track replaces whatever plays, music or video.

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
