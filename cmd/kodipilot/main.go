// Command kodipilot is a keyboard terminal UI for Kodi's music library:
// browse album artists, albums and tracks, and play them in Kodi.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"golang.org/x/term"

	"github.com/pswies/kodipilot/internal/art"
	"github.com/pswies/kodipilot/internal/artwork"
	"github.com/pswies/kodipilot/internal/config"
	"github.com/pswies/kodipilot/internal/kodi"
	"github.com/pswies/kodipilot/internal/ui"
)

// version is set by release builds: -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "kodipilot:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	var flags config.Settings
	var showVersion bool
	var artFlag string
	fs := flag.NewFlagSet("kodipilot", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {} // --help prints the usage to stdout below; errors say how to get it
	fs.StringVar(&flags.Host, "host", "", "")
	fs.IntVar(&flags.Port, "port", 0, "")
	fs.StringVar(&flags.Transport, "transport", "", "")
	fs.StringVar(&flags.User, "user", "", "")
	fs.StringVar(&artFlag, "art", "symbols", "")
	fs.BoolVar(&showVersion, "version", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, usage())
			return nil
		}
		return errors.New("see kodipilot --help")
	}
	switch {
	case fs.NArg() > 0:
		return fmt.Errorf("unexpected argument %q; see kodipilot --help", fs.Arg(0))
	case !slices.Contains([]string{"symbols", "blocks", "ascii", "off"}, artFlag):
		return fmt.Errorf("--art=%s: want symbols, blocks, ascii or off", artFlag)
	case showVersion:
		fmt.Fprintln(stdout, "kodipilot", buildVersion())
		return nil
	case !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())):
		return errors.New("needs a terminal (over SSH, use ssh -t)")
	}

	path, err := config.DefaultPath(os.Getenv)
	if err != nil {
		return err
	}
	cfg, warnings, err := config.Load(config.Sources{
		Flags: flags, Getenv: os.Getenv, Path: path, Prompt: promptPassword(stderr),
	})
	for _, w := range warnings {
		fmt.Fprintln(stderr, "kodipilot: warning:", w)
	}
	if err != nil {
		return err
	}

	var client kodi.Client
	images := artwork.Config{UserAgent: "kodipilot/" + buildVersion()}
	switch cfg.Transport {
	case config.HTTP:
		client = kodi.NewHTTPClient(cfg.URL(), cfg.User, cfg.Password)
		images.KodiURL, images.User, images.Password = "http://"+cfg.Addr(), cfg.User, cfg.Password
	default:
		tcp := kodi.NewTCPClient(cfg.Addr())
		defer tcp.Close()
		client = tcp
	}

	var opts []tea.ProgramOption
	if os.Getenv("NO_COLOR") != "" { // https://no-color.org: any value
		opts = append(opts, tea.WithColorProfile(colorprofile.Ascii))
	}
	uiOpts := ui.Options{Art: map[string]art.Mode{"symbols": art.Symbols, "blocks": art.Blocks, "ascii": art.ASCII}[artFlag]}
	if artFlag != "off" {
		uiOpts.Images = artwork.NewLoader(images)
	}
	_, err = tea.NewProgram(ui.New(client, ui.SystemClock{}, uiOpts), opts...).Run()
	return err
}

// promptPassword returns a prompt that reads a password from the terminal
// without echo.
func promptPassword(out io.Writer) func(user, addr string) (string, error) {
	return func(user, addr string) (string, error) {
		fd := int(os.Stdin.Fd())
		if !term.IsTerminal(fd) {
			return "", fmt.Errorf("no terminal to ask for it; set %s or the config file", config.EnvPassword)
		}
		fmt.Fprintf(out, "Kodi password for %s at %s: ", user, addr)
		password, err := term.ReadPassword(fd)
		fmt.Fprintln(out)
		return string(password), err
	}
}

// buildVersion returns the version set at link time or, for go install
// builds, the module version.
func buildVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

func usage() string {
	return fmt.Sprintf(`kodipilot — browse Kodi's music library from the keyboard and play it in Kodi.

Usage:
  kodipilot [flags]

Flags:
  --host HOST        Kodi's host (default %s)
  --port PORT        port (default %d for tcp, %d for http)
  --transport T      tcp (Kodi's JSON-RPC socket) or http (Kodi's web server);
                     default: tcp for a loopback host, http otherwise
  --user USER        web server user (default %s)
  --art MODE         images in the details pane: symbols (default), blocks
                     (for fonts without sextants), ascii or off
  --version          print the version
  --help             print this help

Environment: %s, %s, %s.
Config file: $XDG_CONFIG_HOME/kodipilot/config.toml or ~/.config/kodipilot/config.toml
  with host, port, transport, user and password. Flags override the environment,
  which overrides the file. Without a password, kodipilot asks for it.

%s`,
		config.DefaultHost, config.DefaultTCPPort, config.DefaultHTTPPort, config.DefaultHTTPUser,
		config.EnvHost, config.EnvUser, config.EnvPassword, ui.KeysHelp())
}
