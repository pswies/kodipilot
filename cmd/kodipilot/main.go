// Command kodipilot is a keyboard terminal UI for Kodi's music library:
// browse album artists, albums and tracks, and play them in Kodi.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

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

// Kodi's web server gets this long to answer the first ping.
const pingTimeout = 5 * time.Second

// passwordTries is how often a typed password may be wrong.
const passwordTries = 3

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "kodipilot:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	var flags config.Settings
	var showVersion bool
	fs := flag.NewFlagSet("kodipilot", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {} // --help prints the usage to stdout below; errors say how to get it
	fs.StringVar(&flags.Host, "host", "", "")
	fs.IntVar(&flags.Port, "port", 0, "")
	fs.StringVar(&flags.Transport, "transport", "", "")
	fs.StringVar(&flags.User, "user", "", "")
	fs.StringVar(&flags.Art, "art", "", "")
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
	typed := false // the password came from the prompt
	prompt := func(user, addr string) (string, error) {
		typed = true
		return promptPassword(stderr, user, addr)
	}
	cfg, warnings, err := config.Load(config.Sources{Flags: flags, Getenv: os.Getenv, Path: path, Prompt: prompt})
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
		if err := checkLogin(&cfg, typed, prompt, stderr); err != nil {
			return err
		}
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
	uiOpts := ui.Options{Version: buildVersion(), Art: map[string]art.Mode{"symbols": art.Symbols, "blocks": art.Blocks, "ascii": art.ASCII}[cfg.Art]}
	if cfg.Art != "off" {
		uiOpts.Images = artwork.NewLoader(images)
	}
	_, err = tea.NewProgram(ui.New(client, ui.SystemClock{}, uiOpts), opts...).Run()
	return err
}

// checkLogin pings Kodi's web server before the UI starts. When Kodi
// rejects the password and it was typed, it asks again, up to passwordTries
// in all; otherwise it explains what to check. Other failures are left to
// the UI, which waits for Kodi.
func checkLogin(cfg *config.Config, typed bool, prompt func(user, addr string) (string, error), stderr io.Writer) error {
	for try := 1; ; try++ {
		ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
		err := kodi.NewHTTPClient(cfg.URL(), cfg.User, cfg.Password).Call(ctx, "JSONRPC.Ping", nil, nil)
		cancel()
		if !errors.Is(err, kodi.ErrUnauthorized) {
			return nil
		}
		if !typed || try == passwordTries {
			return fmt.Errorf("the web server of Kodi at %s rejected user %q and the password.\n"+
				"Check them in Kodi under Settings → Services → Control, and in %s or the config file",
				cfg.Addr(), cfg.User, config.EnvPassword)
		}
		fmt.Fprintln(stderr, "Kodi rejected the password; try again.")
		if cfg.Password, err = prompt(cfg.User, cfg.Addr()); err != nil {
			return err
		}
	}
}

// promptPassword reads a password from the terminal without echo. Ctrl-C
// restores the terminal and exits with 130.
func promptPassword(out io.Writer, user, addr string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("no terminal to ask for the password; set %s or the config file", config.EnvPassword)
	}
	state, err := term.GetState(fd)
	if err != nil {
		return "", err
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	fmt.Fprintf(out, "Kodi password for %s at %s: ", user, addr)
	password, err := interruptible(
		func() ([]byte, error) { return term.ReadPassword(fd) },
		func() { term.Restore(fd, state); fmt.Fprintln(out) },
		signals, os.Exit)
	fmt.Fprintln(out)
	return string(password), err
}

// interruptible runs read; when a signal arrives first, it calls restore
// and exits with 130, the shell's code for Ctrl-C.
func interruptible(read func() ([]byte, error), restore func(), signals <-chan os.Signal, exit func(int)) ([]byte, error) {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-signals:
			restore()
			exit(130)
		case <-done:
		}
	}()
	return read()
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

Environment: %s, %s, %s, %s.
Config file: $XDG_CONFIG_HOME/kodipilot/config.toml or ~/.config/kodipilot/config.toml
  with host, port, transport, user, password and art. Flags override the
  environment, which overrides the file. Without a password, kodipilot asks.

%s`,
		config.DefaultHost, config.DefaultTCPPort, config.DefaultHTTPPort, config.DefaultHTTPUser,
		config.EnvHost, config.EnvUser, config.EnvPassword, config.EnvArt, ui.KeysHelp())
}
