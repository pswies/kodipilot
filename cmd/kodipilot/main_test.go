package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestHelpAndVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run([]string{"--help"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--host", "--transport", "KODIPILOT_PASSWORD", "Keys, everywhere:", "Keys, queue:", "Piotr Święs"} {
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
