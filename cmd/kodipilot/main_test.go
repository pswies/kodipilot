package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/pswies/kodipilot/internal/config"
)

func TestHelpAndVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run([]string{"--help"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--host", "--transport", "KODIPILOT_PASSWORD", "Everywhere", "Queue", "in Kodi", "kodipilot lets you steer", "Piotr Święs"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("--help lacks %q", want)
		}
	}

	out.Reset()
	if err := run([]string{"--version"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "kodipilot ") {
		t.Errorf("--version printed %q", out.String())
	}
}

func TestBadArguments(t *testing.T) {
	var out, errOut bytes.Buffer
	for _, args := range [][]string{{"--password", "x"}, {"extra"}, {"--port", "x"}, {"--art", "sixel"}} {
		if err := run(args, &out, &errOut); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
}

// kodiWeb is a fake Kodi web server that accepts the password "secret".
func kodiWeb(t *testing.T) (config.Config, *int) {
	t.Helper()
	pings := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pings++
		if _, password, _ := r.BasicAuth(); password != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"pong"}`)
	}))
	t.Cleanup(srv.Close)
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	p, _ := strconv.Atoi(port)
	return config.Config{Transport: config.HTTP, Host: host, Port: p, User: "kodi"}, &pings
}

func TestCheckLogin(t *testing.T) {
	cfg, pings := kodiWeb(t)
	cfg.Password = "secret"
	if err := checkLogin(&cfg, false, nil, io.Discard); err != nil || *pings != 1 {
		t.Errorf("right password: %v after %d pings", err, *pings)
	}
}

func TestCheckLoginAsksAgain(t *testing.T) {
	cfg, _ := kodiWeb(t)
	cfg.Password = "wrong"
	answers := []string{"still wrong", "secret"}
	prompt := func(user, addr string) (string, error) {
		a := answers[0]
		answers = answers[1:]
		return a, nil
	}
	var out bytes.Buffer
	if err := checkLogin(&cfg, true, prompt, &out); err != nil || cfg.Password != "secret" {
		t.Errorf("err %v, password %q", err, cfg.Password)
	}
	if strings.Count(out.String(), "try again") != 2 {
		t.Errorf("output %q", out.String())
	}
}

func TestCheckLoginGivesUp(t *testing.T) {
	cfg, pings := kodiWeb(t)
	cfg.Password = "wrong"
	prompts := 0
	prompt := func(string, string) (string, error) { prompts++; return "wrong", nil }
	err := checkLogin(&cfg, true, prompt, io.Discard)
	if err == nil || prompts != passwordTries-1 || *pings != passwordTries {
		t.Fatalf("err %v after %d prompts, %d pings", err, prompts, *pings)
	}
	for _, want := range []string{cfg.Addr(), `"kodi"`, "Settings → Services → Control"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q lacks %q", err, want)
		}
	}

	// A password from the environment or the file is not asked again.
	*pings = 0
	if err := checkLogin(&cfg, false, nil, io.Discard); err == nil || *pings != 1 {
		t.Errorf("err %v after %d pings", err, *pings)
	}
}

func TestCheckLoginLeavesUnreachableToUI(t *testing.T) {
	cfg := config.Config{Transport: config.HTTP, Host: "127.0.0.1", Port: 1, User: "kodi"}
	if err := checkLogin(&cfg, false, nil, io.Discard); err != nil {
		t.Errorf("unreachable: %v", err)
	}
}

func TestInterruptedPrompt(t *testing.T) {
	signals := make(chan os.Signal, 1)
	unblock := make(chan struct{})
	restored, code := false, 0
	read := func() ([]byte, error) { <-unblock; return nil, errors.New("interrupted") }
	restore := func() { restored = true }
	exit := func(c int) { code = c; close(unblock) }
	signals <- os.Interrupt
	interruptible(read, restore, signals, exit)
	if !restored || code != 130 {
		t.Errorf("restored %v, exit code %d", restored, code)
	}

	// Without a signal, the read's answer comes through.
	got, err := interruptible(func() ([]byte, error) { return []byte("pw"), nil }, func() { t.Error("restored") },
		make(chan os.Signal), func(int) { t.Error("exited") })
	if string(got) != "pw" || err != nil {
		t.Errorf("read %q, %v", got, err)
	}
}
