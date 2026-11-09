package art

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

var (
	red  = color.RGBA{200, 30, 30, 255}
	blue = color.RGBA{20, 40, 220, 255}
)

// paint returns a w×h image coloured by f.
func paint(w, h int, f func(x, y int) color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, f(x, y))
		}
	}
	return img
}

// cell is a rendered single cell: its glyph and the SGR parameters.
type cell struct {
	glyph string
	sgr   string
}

func renderCell(t *testing.T, img image.Image, opts Options) cell {
	t.Helper()
	out := Render(img, 1, 1, opts)
	plain := ansi.Strip(out)
	sgr := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSuffix(out, "\x1b[0m"), "\x1b["), "m"+plain)
	return cell{plain, sgr}
}

func TestSolidColour(t *testing.T) {
	got := renderCell(t, paint(12, 24, func(int, int) color.Color { return red }), Options{})
	if got.glyph != " " || got.sgr != "48;2;200;30;30" {
		t.Errorf("solid red = %+v, want a space on red", got)
	}
}

func TestSplits(t *testing.T) {
	for _, tc := range []struct {
		name  string
		f     func(x, y int) color.Color
		glyph string
		sgr   string // foreground (ink) then background
	}{
		{"vertical", func(x, _ int) color.Color { return pick(x < 6) }, "▌", "38;2;200;30;30;48;2;20;40;220"},
		{"vertical, red right", func(x, _ int) color.Color { return pick(x >= 6) }, "▌", "38;2;20;40;220;48;2;200;30;30"},
		{"horizontal", func(_, y int) color.Color { return pick(y < 12) }, "▀", "38;2;200;30;30;48;2;20;40;220"},
		{"checker", func(x, y int) color.Color { return pick(x < 6 == (y < 12)) }, "▚", "38;2;200;30;30;48;2;20;40;220"},
		{"quadrant", func(x, y int) color.Color { return pick(x >= 6 && y >= 12) }, "▗", "38;2;200;30;30;48;2;20;40;220"},
		{"sextant", func(x, y int) color.Color { return pick(x < 6 && y < 8) }, "\U0001FB00", "38;2;200;30;30;48;2;20;40;220"},
	} {
		got := renderCell(t, paint(12, 24, tc.f), Options{})
		if got.glyph != tc.glyph || got.sgr != tc.sgr {
			t.Errorf("%s: %q %s, want %q %s", tc.name, got.glyph, got.sgr, tc.glyph, tc.sgr)
		}
	}
}

func pick(isRed bool) color.Color {
	if isRed {
		return red
	}
	return blue
}

func TestBlocksModeHasNoSextants(t *testing.T) {
	img := paint(12, 24, func(x, y int) color.Color { return pick(x < 6 && y < 8) })
	if got := renderCell(t, img, Options{Mode: Blocks}); got.glyph >= "\U0001FB00" {
		t.Errorf("Blocks mode drew %q", got.glyph)
	}
}

func TestPalette256(t *testing.T) {
	got := renderCell(t, paint(4, 8, func(int, int) color.Color { return color.RGBA{255, 0, 0, 255} }), Options{Color: Palette256})
	if got.sgr != "48;5;196" {
		t.Errorf("pure red in 256 colours = %+v", got)
	}
	gray := renderCell(t, paint(4, 8, func(int, int) color.Color { return color.RGBA{128, 128, 128, 255} }), Options{Color: Palette256})
	if gray.sgr != "48;5;244" {
		t.Errorf("gray in 256 colours = %+v", gray)
	}
}

func TestSizeAndWidth(t *testing.T) {
	img := paint(97, 61, func(x, y int) color.Color {
		return color.RGBA{uint8(x * 2), uint8(y * 4), uint8((x * y) % 256), 255}
	})
	for _, opts := range []Options{{}, {Mode: Blocks}, {Mode: ASCII}, {Color: Palette256}, {Color: NoColor}} {
		for _, size := range [][2]int{{1, 1}, {7, 3}, {60, 30}, {13, 40}} {
			out := Render(img, size[0], size[1], opts)
			lines := strings.Split(out, "\n")
			if len(lines) != size[1] {
				t.Errorf("%+v %v: %d lines", opts, size, len(lines))
			}
			for _, l := range lines {
				plain := ansi.Strip(l)
				if w := ansi.StringWidth(l); w != size[0] {
					t.Errorf("%+v %v: line is %d wide", opts, size, w)
				}
				for _, r := range plain {
					if w := ansi.StringWidth(string(r)); w != 1 {
						t.Errorf("glyph %q is %d wide", r, w)
					}
				}
			}
		}
	}
	if Render(img, 0, 5, Options{}) != "" || Render(image.NewRGBA(image.Rect(0, 0, 0, 0)), 2, 1, Options{}) != "  " {
		t.Error("empty sizes or images")
	}
}

func TestNoColorASCII(t *testing.T) {
	img := paint(40, 20, func(x, _ int) color.Color { v := uint8(x * 255 / 39); return color.RGBA{v, v, v, 255} })
	out := Render(img, 10, 2, Options{Color: NoColor})
	if strings.Contains(out, "\x1b") {
		t.Errorf("escape codes without colour: %q", out)
	}
	line := strings.Split(out, "\n")[0]
	if line[0] != ' ' || line[len(line)-1] != '@' {
		t.Errorf("ramp = %q, want dark to bright", line)
	}
	coloured := Render(img, 10, 2, Options{Mode: ASCII})
	if !strings.Contains(coloured, "38;2;") || strings.Contains(coloured, "48;2;") {
		t.Errorf("ASCII mode with colour: %q", coloured)
	}
}

func TestTransparencyOverBlack(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 8))
	for i := range img.Pix {
		img.Pix[i] = 0
	}
	if got := renderCell(t, img, Options{}); got.sgr != "48;2;0;0;0" {
		t.Errorf("transparent = %+v", got)
	}
}

func TestFit(t *testing.T) {
	for _, tc := range []struct{ w, h, maxCols, maxRows, cols, rows int }{
		{100, 100, 60, 30, 60, 30}, // square: twice as many columns as rows
		{100, 100, 40, 30, 40, 20},
		{200, 100, 60, 30, 60, 15},
		{100, 400, 60, 30, 15, 30},
		{100, 100, 0, 30, 0, 0},
		{1000, 1, 10, 10, 10, 1},
	} {
		cols, rows := Fit(tc.w, tc.h, tc.maxCols, tc.maxRows)
		if cols != tc.cols || rows != tc.rows {
			t.Errorf("Fit(%d, %d, %d, %d) = %d, %d; want %d, %d",
				tc.w, tc.h, tc.maxCols, tc.maxRows, cols, rows, tc.cols, tc.rows)
		}
	}
}

func TestThumbnail(t *testing.T) {
	img := paint(1000, 500, func(int, int) color.Color { return blue })
	th := Thumbnail(img, 320)
	if b := th.Bounds(); b.Dx() != 320 || b.Dy() != 160 {
		t.Errorf("thumbnail %v", b)
	}
	if c := th.RGBAAt(100, 100); c != blue {
		t.Errorf("colour %v", c)
	}
}

// BenchmarkRender60x30 renders a 60×30 cell image from a cached thumbnail.
func BenchmarkRender60x30(b *testing.B) {
	img := Thumbnail(paint(900, 900, func(x, y int) color.Color {
		return color.RGBA{uint8(x ^ y), uint8(x * y >> 4), uint8(y), 255}
	}), 320)
	b.ReportAllocs()
	for b.Loop() {
		Render(img, 60, 30, Options{})
	}
}
