package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func writeConfig(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil { // umask may have narrowed it
		t.Fatal(err)
	}
	return path
}

func noPrompt(t *testing.T) func(string, string) (string, error) {
	return func(string, string) (string, error) {
		t.Error("unexpected password prompt")
		return "", nil
	}
}

func TestDefaults(t *testing.T) {
	cfg, warnings, err := Load(Sources{Getenv: env(nil), Prompt: noPrompt(t)})
	if err != nil || len(warnings) > 0 {
		t.Fatal(err, warnings)
	}
	want := Config{Transport: TCP, Host: "127.0.0.1", Port: 9090, Art: "symbols"}
	if cfg != want {
		t.Errorf("cfg = %+v, want %+v", cfg, want)
	}
	if cfg.Addr() != "127.0.0.1:9090" {
		t.Errorf("Addr = %s", cfg.Addr())
	}
}

func TestPrecedence(t *testing.T) {
	path := writeConfig(t, `
host = "file-host"
port = 1111
transport = "tcp"
user = "file-user"
password = "file-password"
`, 0o600)
	getenv := env(map[string]string{EnvHost: "env-host", EnvUser: "env-user", EnvPassword: "env-password"})

	// File only.
	cfg, _, err := Load(Sources{Getenv: env(nil), Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "file-host" || cfg.Port != 1111 || cfg.Transport != TCP {
		t.Errorf("file: %+v", cfg)
	}

	// Environment over file.
	cfg, _, err = Load(Sources{Flags: Settings{Transport: "http"}, Getenv: getenv, Path: path, Prompt: noPrompt(t)})
	if err != nil {
		t.Fatal(err)
	}
	want := Config{Transport: HTTP, Host: "env-host", Port: 1111, User: "env-user", Password: "env-password", Art: "symbols"}
	if cfg != want {
		t.Errorf("env: %+v, want %+v", cfg, want)
	}

	// Flags over environment; a password flag is ignored.
	flags := Settings{Host: "flag-host", Port: 2222, Transport: "http", User: "flag-user", Password: "flag-password"}
	cfg, _, err = Load(Sources{Flags: flags, Getenv: getenv, Path: path, Prompt: noPrompt(t)})
	if err != nil {
		t.Fatal(err)
	}
	want = Config{Transport: HTTP, Host: "flag-host", Port: 2222, User: "flag-user", Password: "env-password", Art: "symbols"}
	if cfg != want {
		t.Errorf("flags: %+v, want %+v", cfg, want)
	}
}

func TestTransportByHost(t *testing.T) {
	for host, want := range map[string]Transport{
		"127.0.0.1": TCP, "localhost": TCP, "::1": TCP, "127.0.0.2": TCP,
		"perun": HTTP, "192.168.1.10": HTTP, "fe80::1": HTTP,
	} {
		cfg, _, err := Load(Sources{
			Flags: Settings{Host: host}, Getenv: env(nil),
			Prompt: func(string, string) (string, error) { return "secret", nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Transport != want {
			t.Errorf("%s: transport %s, want %s", host, cfg.Transport, want)
		}
	}
}

func TestHTTPDefaults(t *testing.T) {
	cfg, _, err := Load(Sources{
		Flags: Settings{Host: "perun"}, Getenv: env(map[string]string{EnvPassword: "secret"}), Prompt: noPrompt(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := Config{Transport: HTTP, Host: "perun", Port: 8080, User: "kodi", Password: "secret", Art: "symbols"}
	if cfg != want {
		t.Errorf("cfg = %+v, want %+v", cfg, want)
	}
	if cfg.URL() != "http://perun:8080/jsonrpc" {
		t.Errorf("URL = %s", cfg.URL())
	}
}

func TestPasswordPrompt(t *testing.T) {
	prompted := 0
	cfg, _, err := Load(Sources{
		Flags:  Settings{Host: "perun"},
		Getenv: env(nil),
		Prompt: func(user, addr string) (string, error) {
			prompted++
			if user != "kodi" || addr != "perun:8080" {
				t.Errorf("prompt for %s at %s", user, addr)
			}
			return "typed", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prompted != 1 || cfg.Password != "typed" {
		t.Errorf("prompted %d times, password %q", prompted, cfg.Password)
	}

	failure := errors.New("no terminal")
	_, _, err = Load(Sources{
		Flags: Settings{Host: "perun"}, Getenv: env(nil),
		Prompt: func(string, string) (string, error) { return "", failure },
	})
	if !errors.Is(err, failure) {
		t.Errorf("err = %v, want the prompt's error", err)
	}
}

func TestNoPromptForTCP(t *testing.T) {
	cfg, _, err := Load(Sources{Flags: Settings{Host: "perun", Transport: "tcp"}, Getenv: env(nil), Prompt: noPrompt(t)})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr() != "perun:9090" {
		t.Errorf("Addr = %s", cfg.Addr())
	}
}

func TestPermissionWarning(t *testing.T) {
	for _, tc := range []struct {
		content string
		mode    os.FileMode
		warn    bool
	}{
		{`password = "x"`, 0o644, true},
		{`password = "x"`, 0o640, true},
		{`password = "x"`, 0o600, false},
		{"host = \"perun\"\ntransport = \"tcp\"", 0o644, false},
	} {
		path := writeConfig(t, tc.content, tc.mode)
		_, warnings, err := Load(Sources{Getenv: env(nil), Path: path, Prompt: noPrompt(t)})
		if err != nil {
			t.Fatal(err)
		}
		warned := len(warnings) == 1 && strings.Contains(warnings[0], "chmod 600")
		if warned != tc.warn {
			t.Errorf("%q mode %o: warnings %v", tc.content, tc.mode, warnings)
		}
	}
}

func TestFileErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "none.toml")
	if _, _, err := Load(Sources{Getenv: env(nil), Path: missing}); err != nil {
		t.Errorf("missing file: %v", err)
	}
	bad := writeConfig(t, "host = ", 0o600)
	if _, _, err := Load(Sources{Getenv: env(nil), Path: bad}); err == nil {
		t.Error("broken file: no error")
	}
	unknown := writeConfig(t, `hots = "perun"`, 0o600)
	if _, warnings, _ := Load(Sources{Getenv: env(nil), Path: unknown}); len(warnings) != 1 {
		t.Errorf("unknown key: warnings %v", warnings)
	}
	if _, _, err := Load(Sources{Flags: Settings{Transport: "udp"}, Getenv: env(nil)}); err == nil {
		t.Error("bad transport: no error")
	}
}

func TestDefaultPath(t *testing.T) {
	path, err := DefaultPath(env(map[string]string{"XDG_CONFIG_HOME": "/xdg"}))
	if err != nil || path != "/xdg/kodipilot/config.toml" {
		t.Errorf("path = %s, %v", path, err)
	}
	path, err = DefaultPath(env(nil))
	if err != nil || !strings.HasSuffix(path, "/.config/kodipilot/config.toml") {
		t.Errorf("path = %s, %v", path, err)
	}
}

func TestArt(t *testing.T) {
	path := writeConfig(t, `art = "blocks"`, 0o600)
	for _, tc := range []struct {
		flag, env, want string
	}{
		{"", "", "blocks"},
		{"", "ascii", "ascii"},
		{"off", "ascii", "off"},
	} {
		cfg, _, err := Load(Sources{Flags: Settings{Art: tc.flag}, Getenv: env(map[string]string{EnvArt: tc.env}), Path: path})
		if err != nil || cfg.Art != tc.want {
			t.Errorf("flag %q env %q: art %q, %v; want %q", tc.flag, tc.env, cfg.Art, err, tc.want)
		}
	}
	if _, _, err := Load(Sources{Flags: Settings{Art: "sixel"}, Getenv: env(nil)}); err == nil {
		t.Error("unknown art: no error")
	}
}
