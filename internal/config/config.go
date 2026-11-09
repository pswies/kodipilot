// Package config resolves how to reach Kodi from command-line flags, the
// environment and the config file, in that order of precedence.
package config

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"github.com/BurntSushi/toml"
)

// Transport is the way kodipilot talks to Kodi.
type Transport string

// Transports.
const (
	TCP  Transport = "tcp"  // Kodi's raw JSON-RPC socket
	HTTP Transport = "http" // Kodi's web server
)

// Defaults.
const (
	DefaultHost     = "127.0.0.1"
	DefaultTCPPort  = 9090
	DefaultHTTPPort = 8080
	DefaultHTTPUser = "kodi"
)

// Environment variables.
const (
	EnvHost     = "KODIPILOT_HOST"
	EnvUser     = "KODIPILOT_USER"
	EnvPassword = "KODIPILOT_PASSWORD"
)

// Settings is one source of settings. Zero fields are unset.
type Settings struct {
	Host      string `toml:"host"`
	Port      int    `toml:"port"`
	Transport string `toml:"transport"`
	User      string `toml:"user"`
	Password  string `toml:"password"`
}

// Sources are the inputs that Load merges.
type Sources struct {
	Flags  Settings                                // from the command line; Password is ignored
	Getenv func(key string) string                 // usually os.Getenv
	Path   string                                  // the config file; a missing file is fine
	Prompt func(user, addr string) (string, error) // asks for the HTTP password when none is set
}

// Config says how to reach Kodi.
type Config struct {
	Transport Transport
	Host      string
	Port      int
	User      string // HTTP only
	Password  string // HTTP only
}

// Addr returns host:port.
func (c Config) Addr() string { return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) }

// URL returns the JSON-RPC endpoint of Kodi's web server.
func (c Config) URL() string { return "http://" + c.Addr() + "/jsonrpc" }

// Load merges the flags, the environment and the config file and fills in
// defaults: host 127.0.0.1; TCP for loopback hosts and HTTP otherwise; port
// 9090 for TCP and 8080 for HTTP; HTTP user "kodi". When HTTP has no
// password, Load asks Sources.Prompt for one. Warnings, such as a config file
// with a password that others can read, are returned for the caller to show.
func Load(src Sources) (Config, []string, error) {
	file, warnings, err := readFile(src.Path)
	if err != nil {
		return Config{}, warnings, err
	}
	env := Settings{
		Host:     src.Getenv(EnvHost),
		User:     src.Getenv(EnvUser),
		Password: src.Getenv(EnvPassword),
	}
	flags := src.Flags
	flags.Password = "" // passwords never come from flags
	s := merge(flags, env, file)

	cfg := Config{Host: cmp.Or(s.Host, DefaultHost), Port: s.Port, User: s.User, Password: s.Password}
	switch Transport(s.Transport) {
	case TCP, HTTP:
		cfg.Transport = Transport(s.Transport)
	case "":
		cfg.Transport = HTTP
		if isLoopback(cfg.Host) {
			cfg.Transport = TCP
		}
	default:
		return Config{}, warnings, fmt.Errorf("unknown transport %q (want tcp or http)", s.Transport)
	}
	if cfg.Port < 0 || cfg.Port > 65535 {
		return Config{}, warnings, fmt.Errorf("invalid port %d", cfg.Port)
	}
	if cfg.Transport == TCP {
		cfg.User, cfg.Password = "", ""
		cfg.Port = cmp.Or(cfg.Port, DefaultTCPPort)
		return cfg, warnings, nil
	}
	cfg.Port = cmp.Or(cfg.Port, DefaultHTTPPort)
	cfg.User = cmp.Or(cfg.User, DefaultHTTPUser)
	if cfg.Password == "" && src.Prompt != nil {
		if cfg.Password, err = src.Prompt(cfg.User, cfg.Addr()); err != nil {
			return Config{}, warnings, fmt.Errorf("reading the password: %w", err)
		}
	}
	return cfg, warnings, nil
}

// DefaultPath returns the config file path: $XDG_CONFIG_HOME/kodipilot/config.toml,
// or ~/.config/kodipilot/config.toml.
func DefaultPath(getenv func(string) string) (string, error) {
	dir := getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("finding the config file: %w", err)
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "kodipilot", "config.toml"), nil
}

// readFile reads the config file at path, if there is one.
func readFile(path string) (Settings, []string, error) {
	var s Settings
	if path == "" {
		return s, nil, nil
	}
	meta, err := toml.DecodeFile(path, &s)
	if errors.Is(err, fs.ErrNotExist) {
		return Settings{}, nil, nil
	}
	if err != nil {
		return Settings{}, nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var warnings []string
	for _, key := range meta.Undecoded() {
		warnings = append(warnings, fmt.Sprintf("%s: unknown setting %q", path, key.String()))
	}
	if s.Password != "" {
		if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o077 != 0 {
			warnings = append(warnings, fmt.Sprintf(
				"%s holds a password but is readable by others (mode %04o); run: chmod 600 %s",
				path, info.Mode().Perm(), path))
		}
	}
	return s, warnings, nil
}

// merge returns the first set value of each field, in the order of layers.
func merge(layers ...Settings) Settings {
	var s Settings
	for _, l := range layers {
		s.Host = cmp.Or(s.Host, l.Host)
		s.Port = cmp.Or(s.Port, l.Port)
		s.Transport = cmp.Or(s.Transport, l.Transport)
		s.User = cmp.Or(s.User, l.User)
		s.Password = cmp.Or(s.Password, l.Password)
	}
	return s
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
