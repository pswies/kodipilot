package artwork

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pswies/kodipilot/internal/art"
)

// pngBytes returns a w×h PNG in one colour.
func pngBytes(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// kodiArt returns the Kodi art URL of a source.
func kodiArt(source string) string {
	return "image://" + strings.ReplaceAll(url.QueryEscape(source), "+", "%20") + "/"
}

var green = color.RGBA{10, 200, 30, 255}

func TestSource(t *testing.T) {
	for in, want := range map[string]string{
		"image://%2fpub%2fArchive%2fAxiom%20(2014)%2fcover.jpg/": "/pub/Archive/Axiom (2014)/cover.jpg",
		"image://https%3a%2f%2fassets.fanart.tv%2fa.jpg/":        "https://assets.fanart.tv/a.jpg",
		"image://music@%2fpub%2fa.flac/":                         "music@/pub/a.flac",
		"/plain/path.jpg":                                        "/plain/path.jpg",
	} {
		if got := Source(in); got != want {
			t.Errorf("Source(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLocalFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Axiom (2014)", "cover.png")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, pngBytes(t, 40, 20, green), 0o644)

	l := NewLoader(Config{})
	img, err := l.Image(t.Context(), kodiArt(path))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 40 || b.Dy() != 20 || img.RGBAAt(5, 5) != green {
		t.Errorf("image %v %v", b, img.RGBAAt(5, 5))
	}
	out, err := l.Render(t.Context(), kodiArt(path), 20, 20, art.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(out, "\n"); len(lines) != 5 { // 40×20 px: 20 columns, 5 rows
		t.Errorf("render has %d lines", len(lines))
	}
	if cached, ok := l.Cached(kodiArt(path), 20, 20, art.Options{}); !ok || cached != out {
		t.Error("render not cached")
	}
}

func TestHTTP(t *testing.T) {
	var agent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/a.png":
			agent = r.UserAgent()
			w.Write(pngBytes(t, 8, 8, green))
		case "/big.png":
			w.Write(bytes.Repeat([]byte{0}, 2000))
		case "/slow.png":
			time.Sleep(300 * time.Millisecond)
			w.Write(pngBytes(t, 8, 8, green))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	l := NewLoader(Config{UserAgent: "kodipilot/test", Timeout: 100 * time.Millisecond, MaxBytes: 1000})

	if _, err := l.Image(t.Context(), kodiArt(srv.URL+"/a.png")); err != nil {
		t.Fatal(err)
	}
	if agent != "kodipilot/test" {
		t.Errorf("User-Agent %q", agent)
	}
	for _, name := range []string{"/big.png", "/slow.png", "/missing.png"} {
		if _, err := l.Image(t.Context(), kodiArt(srv.URL+name)); !errors.Is(err, ErrNoImage) {
			t.Errorf("%s: err = %v, want ErrNoImage", name, err)
		}
	}
}

func TestKodiImageEndpoint(t *testing.T) {
	artURL := "image://music@%2fpub%2fAxiom%2f01.flac/"
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "kodi" || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		path = r.URL.EscapedPath()
		w.Write(pngBytes(t, 8, 8, green))
	}))
	defer srv.Close()

	l := NewLoader(Config{KodiURL: srv.URL, User: "kodi", Password: "secret"})
	if _, err := l.Image(t.Context(), artURL); err != nil {
		t.Fatal(err)
	}
	if want := "/image/image%3A%2F%2Fmusic%40%252fpub%252fAxiom%252f01.flac%2F"; path != want {
		t.Errorf("path %s, want %s", path, want)
	}
	wrong := NewLoader(Config{KodiURL: srv.URL, User: "kodi", Password: "wrong"})
	if _, err := wrong.Image(t.Context(), artURL); !errors.Is(err, ErrNoImage) {
		t.Errorf("wrong password: err = %v", err)
	}
}

func TestNoSource(t *testing.T) {
	l := NewLoader(Config{})
	for _, u := range []string{"image://music@%2fpub%2fa.flac/", "image://ftp%3a%2f%2fx%2fa.jpg/", "", "image:///"} {
		if _, err := l.Image(t.Context(), u); !errors.Is(err, ErrNoImage) {
			t.Errorf("%q: err = %v", u, err)
		}
	}
}

func TestFailuresCachedButNotCancellations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "late.png")
	l := NewLoader(Config{})
	if _, err := l.Image(t.Context(), path); err == nil {
		t.Fatal("no error for a missing file")
	}
	os.WriteFile(path, pngBytes(t, 4, 4, green), 0o644)
	if _, err := l.Image(t.Context(), path); err == nil {
		t.Error("the failure was not cached")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(pngBytes(t, 4, 4, green))
	}))
	defer srv.Close()
	if _, err := l.Image(ctx, srv.URL+"/a.png"); err == nil {
		t.Fatal("no error with a cancelled context")
	}
	if _, err := l.Image(t.Context(), srv.URL+"/a.png"); err != nil {
		t.Errorf("a cancelled load was cached: %v", err)
	}
}

func TestLRU(t *testing.T) {
	c := newLRU[string, int](2)
	c.put("a", 1)
	c.put("b", 2)
	c.get("a") // b is now the least recently used
	c.put("c", 3)
	if _, ok := c.get("b"); ok {
		t.Error("b not evicted")
	}
	for k, want := range map[string]int{"a": 1, "c": 3} {
		if v, ok := c.get(k); !ok || v != want {
			t.Errorf("%s = %d, %v", k, v, ok)
		}
	}
}
