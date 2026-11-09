// Package artwork loads the images behind Kodi's art URLs (cover art, artist
// thumbnails) and renders them as terminal art, caching both.
//
// Kodi names images "image://<url-encoded source>/". A Loader tries, in
// order: the source as a local file (kodipilot runs on the Kodi machine), the
// source as an http(s) URL, and Kodi's web server (GET /image/<art URL>),
// which also serves art embedded in music files.
package artwork

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // decoders: the first frame of a GIF
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "golang.org/x/image/webp"

	"github.com/pswies/kodipilot/internal/art"
)

// ErrNoImage reports that no source had the image.
var ErrNoImage = errors.New("no image")

// Defaults of Config.
const (
	DefaultTimeout  = 10 * time.Second
	DefaultMaxBytes = 10 << 20
	thumbnailSide   = 320 // pixels: enough for a full-screen render
	imageCacheSize  = 64
	renderCacheSize = 128
)

// Config says where a Loader may fetch images from.
type Config struct {
	// KodiURL is the base URL of Kodi's web server, such as
	// "http://media-box:8080", or "" when it is not used.
	KodiURL        string
	User, Password string // for KodiURL

	UserAgent string        // sent to web servers, such as "kodipilot/0.0.2"
	Timeout   time.Duration // per download; DefaultTimeout when 0
	MaxBytes  int64         // per download; DefaultMaxBytes when 0
}

// Loader fetches, decodes and renders images. It is safe for concurrent use.
type Loader struct {
	cfg     Config
	http    *http.Client
	images  *lru[string, loaded]
	renders *lru[renderKey, string]
}

type loaded struct {
	img *image.RGBA
	err error
}

type renderKey struct {
	url              string
	maxCols, maxRows int
	opts             art.Options
}

// NewLoader returns a Loader.
func NewLoader(cfg Config) *Loader {
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = DefaultMaxBytes
	}
	return &Loader{
		cfg:     cfg,
		http:    &http.Client{Timeout: cfg.Timeout},
		images:  newLRU[string, loaded](imageCacheSize),
		renders: newLRU[renderKey, string](renderCacheSize),
	}
}

// Render returns the image of a Kodi art URL as terminal art that fits in
// maxCols×maxRows cells and keeps the image's aspect ratio.
func (l *Loader) Render(ctx context.Context, artURL string, maxCols, maxRows int, opts art.Options) (string, error) {
	key := renderKey{artURL, maxCols, maxRows, opts}
	if s, ok := l.renders.get(key); ok {
		return s, nil
	}
	img, err := l.Image(ctx, artURL)
	if err != nil {
		return "", err
	}
	b := img.Bounds()
	cols, rows := art.Fit(b.Dx(), b.Dy(), maxCols, maxRows)
	s := art.Render(img, cols, rows, opts)
	l.renders.put(key, s)
	return s, nil
}

// Cached returns a render that is already cached, without loading anything.
func (l *Loader) Cached(artURL string, maxCols, maxRows int, opts art.Options) (string, bool) {
	return l.renders.get(renderKey{artURL, maxCols, maxRows, opts})
}

// Image returns the image of a Kodi art URL, decoded and scaled down. Images
// and failures are cached; a failure wraps ErrNoImage.
func (l *Loader) Image(ctx context.Context, artURL string) (*image.RGBA, error) {
	if r, ok := l.images.get(artURL); ok {
		return r.img, r.err
	}
	img, err := l.load(ctx, artURL)
	if ctx.Err() == nil { // a cancelled load may work next time
		l.images.put(artURL, loaded{img, err})
	}
	return img, err
}

func (l *Loader) load(ctx context.Context, artURL string) (*image.RGBA, error) {
	source := Source(artURL)
	var errs []error
	try := func(img image.Image, err error) (*image.RGBA, bool) {
		if err != nil {
			errs = append(errs, err)
			return nil, false
		}
		return art.Thumbnail(img, thumbnailSide), true
	}
	if filepath.IsAbs(source) {
		if img, ok := try(l.file(source)); ok {
			return img, nil
		}
	}
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		if img, ok := try(l.fetch(ctx, source, false)); ok {
			return img, nil
		}
	}
	if l.cfg.KodiURL != "" && strings.HasPrefix(artURL, "image://") {
		kodi := strings.TrimSuffix(l.cfg.KodiURL, "/") + "/image/" + strings.ReplaceAll(url.QueryEscape(artURL), "+", "%20")
		if img, ok := try(l.fetch(ctx, kodi, true)); ok {
			return img, nil
		}
	}
	return nil, fmt.Errorf("%s: %w", artURL, errors.Join(append([]error{ErrNoImage}, errs...)...))
}

// Source returns the source inside a Kodi art URL: "image://%2fa%2fb.jpg/"
// gives "/a/b.jpg". Other URLs are returned as they are.
func Source(artURL string) string {
	inner, ok := strings.CutPrefix(artURL, "image://")
	if !ok {
		return artURL
	}
	inner = strings.TrimSuffix(inner, "/")
	if s, err := url.PathUnescape(inner); err == nil {
		return s
	}
	return inner
}

func (l *Loader) file(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return decode(io.LimitReader(f, 4*l.cfg.MaxBytes), path)
}

// fetch downloads and decodes an image; auth adds Kodi's credentials.
func (l *Loader) fetch(ctx context.Context, u string, auth bool) (image.Image, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if l.cfg.UserAgent != "" {
		req.Header.Set("User-Agent", l.cfg.UserAgent)
	}
	if auth && l.cfg.User != "" {
		req.SetBasicAuth(l.cfg.User, l.cfg.Password)
	}
	resp, err := l.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP status %s", u, resp.Status)
	}
	if resp.ContentLength > l.cfg.MaxBytes {
		return nil, fmt.Errorf("%s: larger than %d bytes", u, l.cfg.MaxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, l.cfg.MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", u, err)
	}
	if int64(len(data)) > l.cfg.MaxBytes {
		return nil, fmt.Errorf("%s: larger than %d bytes", u, l.cfg.MaxBytes)
	}
	return decode(bytes.NewReader(data), u)
}

func decode(r io.Reader, name string) (image.Image, error) {
	img, _, err := image.Decode(r)
	if err != nil {
		return nil, fmt.Errorf("%s: decoding: %w", name, err)
	}
	return img, nil
}

// lru is a small least-recently-used cache, safe for concurrent use.
type lru[K comparable, V any] struct {
	mu    sync.Mutex
	size  int
	tick  uint64
	items map[K]*entry[V]
}

type entry[V any] struct {
	value V
	used  uint64
}

func newLRU[K comparable, V any](size int) *lru[K, V] {
	return &lru[K, V]{size: size, items: make(map[K]*entry[V], size)}
}

func (c *lru[K, V]) get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false
	}
	c.tick++
	e.used = c.tick
	return e.value, true
}

func (c *lru[K, V]) put(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tick++
	if e, ok := c.items[key]; ok {
		e.value, e.used = value, c.tick
		return
	}
	if len(c.items) >= c.size {
		var oldest K
		first := true
		for k, e := range c.items {
			if first || e.used < c.items[oldest].used {
				oldest, first = k, false
			}
		}
		delete(c.items, oldest)
	}
	c.items[key] = &entry[V]{value: value, used: c.tick}
}
