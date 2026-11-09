// Package art draws images as colour terminal art, in the manner of chafa's
// "symbols" mode: every character cell gets the glyph, from a set of block
// and sextant symbols, and the foreground and background colours that
// together approximate the cell's pixels best.
//
// A character cell is taken to be twice as tall as it is wide. The image is
// resampled (by area averaging) to a grid of 2×6 samples per cell; every
// candidate glyph splits those 12 samples in two, each side gets its mean
// colour, and the split with the least squared error wins.
package art

import (
	"image"
	"image/draw"
	"math/bits"
	"strconv"
	"strings"
)

// Mode selects the glyphs.
type Mode int

const (
	// Symbols uses block elements, quadrants and sextants (U+1FB00–U+1FB3B).
	Symbols Mode = iota
	// Blocks uses block elements and quadrants only, for fonts without
	// sextants.
	Blocks
	// ASCII uses a brightness ramp of ASCII characters.
	ASCII
)

// Color selects how colours are written.
type Color int

const (
	// TrueColor writes 24-bit colours.
	TrueColor Color = iota
	// Palette256 writes the nearest colours of the xterm 256-colour palette.
	Palette256
	// NoColor writes no colours: the ASCII mode as grayscale shapes.
	NoColor
)

// Options control rendering.
type Options struct {
	Mode  Mode
	Color Color
}

// Fit returns the largest size in character cells, at most maxCols×maxRows,
// that keeps the aspect ratio of a w×h pixel image. Both are at least 1
// unless a maximum is below 1.
func Fit(w, h, maxCols, maxRows int) (cols, rows int) {
	if maxCols < 1 || maxRows < 1 || w < 1 || h < 1 {
		return 0, 0
	}
	// A cell is 1 wide and 2 tall: cols/(2·rows) = w/h.
	rows = maxRows
	cols = (2*rows*w + h/2) / h
	if cols > maxCols {
		cols = maxCols
		rows = (cols*h + w) / (2 * w)
	}
	return max(1, cols), max(1, min(rows, maxRows))
}

// Thumbnail returns img as RGBA (over black, where it is transparent),
// scaled down so that neither side exceeds maxSide. Rendering from a
// thumbnail is as good as from the original as long as it has at least two
// pixels per cell column and six per cell row.
func Thumbnail(img image.Image, maxSide int) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	full, ok := img.(*image.RGBA)
	if !ok || b.Min != (image.Point{}) {
		full = image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(full, full.Bounds(), img, b.Min, draw.Src)
	}
	if w <= maxSide && h <= maxSide || maxSide < 1 {
		return full
	}
	tw, th := maxSide, max(1, h*maxSide/w)
	if h > w {
		tw, th = max(1, w*maxSide/h), maxSide
	}
	samples := resample(full, tw, th)
	out := image.NewRGBA(image.Rect(0, 0, tw, th))
	for i, s := range samples {
		out.Pix[4*i], out.Pix[4*i+1], out.Pix[4*i+2], out.Pix[4*i+3] = s.r, s.g, s.b, 0xff
	}
	return out
}

// Render draws img in exactly cols×rows character cells, stretching it if
// the size does not keep its aspect ratio (see Fit). Lines are separated by
// "\n", and every line ends by resetting the colours. Every glyph is one
// column wide.
func Render(img image.Image, cols, rows int, opts Options) string {
	if cols < 1 || rows < 1 {
		return ""
	}
	if b := img.Bounds(); b.Dx() < 1 || b.Dy() < 1 {
		return blank(cols, rows)
	}
	src := Thumbnail(img, 0)
	samples := resample(src, cols*cellW, rows*cellH)
	glyphs := glyphsFor(opts.Mode)

	var b strings.Builder
	b.Grow(rows * cols * 40)
	out := writer{b: &b, color: opts.Color}
	var cell [cellSamples]rgb
	for y := range rows {
		for x := range cols {
			for i := range cellSamples {
				cell[i] = samples[(y*cellH+i/cellW)*cols*cellW+x*cellW+i%cellW]
			}
			if opts.Mode == ASCII || opts.Color == NoColor {
				out.ascii(mean(cell[:], fullMask), opts.Color != NoColor)
				continue
			}
			g, fg, bg := best(&cell, glyphs)
			out.glyph(g, fg, bg)
		}
		out.endLine(y < rows-1)
	}
	return b.String()
}

func blank(cols, rows int) string {
	line := strings.Repeat(" ", cols)
	return strings.TrimSuffix(strings.Repeat(line+"\n", rows), "\n")
}

// rgb is a colour sample.
type rgb struct{ r, g, b uint8 }

// The sample grid of a cell: 2 columns by 6 rows.
const (
	cellW       = 2
	cellH       = 6
	cellSamples = cellW * cellH
	fullMask    = 1<<cellSamples - 1
)

// best returns the glyph that approximates the cell best, with its
// foreground (ink) and background colours.
func best(cell *[cellSamples]rgb, glyphs []glyph) (rune, rgb, rgb) {
	var total [3]int
	for _, s := range cell {
		total[0] += int(s.r)
		total[1] += int(s.g)
		total[2] += int(s.b)
	}
	// Minimising the squared error of a two-colour split is maximising
	// |S₁|²/n₁ + |S₀|²/n₀, where S is the sum of a side's samples.
	bestScore := float64(sq(total)) / cellSamples
	bestGlyph := glyph{r: ' '}
	for _, g := range glyphs {
		var ink [3]int
		for m := g.mask; m != 0; m &= m - 1 {
			s := cell[bits.TrailingZeros16(m)]
			ink[0] += int(s.r)
			ink[1] += int(s.g)
			ink[2] += int(s.b)
		}
		rest := [3]int{total[0] - ink[0], total[1] - ink[1], total[2] - ink[2]}
		score := float64(sq(ink))/float64(g.n) + float64(sq(rest))/float64(cellSamples-g.n)
		if score > bestScore+minGain {
			bestScore, bestGlyph = score, g
		}
	}
	if bestGlyph.mask == 0 {
		m := mean(cell[:], fullMask)
		return ' ', m, m
	}
	return bestGlyph.r, mean(cell[:], bestGlyph.mask), mean(cell[:], fullMask&^bestGlyph.mask)
}

// minGain is the least error reduction, in squared colour levels summed over
// the cell, for which a split beats a plain cell; it keeps noise from turning
// smooth areas into a scatter of glyphs.
const minGain = 64

func sq(v [3]int) int { return v[0]*v[0] + v[1]*v[1] + v[2]*v[2] }

// mean returns the mean colour of the samples selected by mask.
func mean(cell []rgb, mask uint16) rgb {
	var r, g, b, n int
	for m := mask; m != 0; m &= m - 1 {
		s := cell[bits.TrailingZeros16(m)]
		r, g, b, n = r+int(s.r), g+int(s.g), b+int(s.b), n+1
	}
	if n == 0 {
		return rgb{}
	}
	return rgb{uint8((r + n/2) / n), uint8((g + n/2) / n), uint8((b + n/2) / n)}
}

// resample scales src to w×h samples by area averaging (each sample is the
// mean of the source area it covers, partial pixels weighted).
func resample(src *image.RGBA, w, h int) []rgb {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	xs, ys := spans(sw, w), spans(sh, h)

	// Horizontal pass into rows of w float samples, then vertical.
	tmp := make([][3]float32, sh*w)
	for y := range sh {
		row := src.Pix[y*src.Stride:]
		for x, sp := range xs {
			var acc [3]float32
			for i, wt := range sp.weights {
				p := row[4*(sp.first+i):]
				acc[0] += wt * float32(p[0])
				acc[1] += wt * float32(p[1])
				acc[2] += wt * float32(p[2])
			}
			tmp[y*w+x] = acc
		}
	}
	out := make([]rgb, w*h)
	for y, sp := range ys {
		for x := range w {
			var acc [3]float32
			for i, wt := range sp.weights {
				t := tmp[(sp.first+i)*w+x]
				acc[0] += wt * t[0]
				acc[1] += wt * t[1]
				acc[2] += wt * t[2]
			}
			out[y*w+x] = rgb{clamp8(acc[0]), clamp8(acc[1]), clamp8(acc[2])}
		}
	}
	return out
}

// span is the source pixels one target sample covers, with weights that
// sum to 1.
type span struct {
	first   int
	weights []float32
}

// spans maps n source pixels onto m target samples.
func spans(n, m int) []span {
	out := make([]span, m)
	scale := float64(n) / float64(m)
	for i := range out {
		lo, hi := float64(i)*scale, float64(i+1)*scale
		first := int(lo)
		last := min(n-1, int(hi-1e-9))
		sp := span{first: first, weights: make([]float32, last-first+1)}
		for p := first; p <= last; p++ {
			overlap := min(hi, float64(p+1)) - max(lo, float64(p))
			sp.weights[p-first] = float32(overlap / scale)
		}
		out[i] = sp
	}
	return out
}

func clamp8(v float32) uint8 {
	switch {
	case v <= 0:
		return 0
	case v >= 255:
		return 255
	}
	return uint8(v + 0.5)
}

// writer writes glyphs with colours, leaving out colour changes that are
// not needed.
type writer struct {
	b      *strings.Builder
	color  Color
	fg, bg rgb
	hasFg  bool // fg is set on the terminal
	hasBg  bool
	buf    []byte
}

func (w *writer) glyph(r rune, fg, bg rgb) {
	setFg := r != ' ' && (!w.hasFg || fg != w.fg)
	setBg := !w.hasBg || bg != w.bg
	if setFg || setBg {
		w.buf = append(w.buf[:0], "\x1b["...)
		if setFg {
			w.buf = w.appendColour(w.buf, fg, 38)
			w.fg, w.hasFg = fg, true
		}
		if setBg {
			if setFg {
				w.buf = append(w.buf, ';')
			}
			w.buf = w.appendColour(w.buf, bg, 48)
			w.bg, w.hasBg = bg, true
		}
		w.buf = append(w.buf, 'm')
		w.b.Write(w.buf)
	}
	w.b.WriteRune(r)
}

// ramp holds ASCII characters from dark to bright.
const ramp = " .:-=+*#%@"

func (w *writer) ascii(c rgb, colour bool) {
	luma := (299*int(c.r) + 587*int(c.g) + 114*int(c.b)) / 1000
	ch := ramp[luma*len(ramp)/256]
	if colour && ch != ' ' && (!w.hasFg || c != w.fg) {
		w.buf = w.appendColour(append(w.buf[:0], "\x1b["...), c, 38)
		w.b.Write(append(w.buf, 'm'))
		w.fg, w.hasFg = c, true
	}
	w.b.WriteByte(ch)
}

func (w *writer) endLine(more bool) {
	if w.hasFg || w.hasBg {
		w.b.WriteString("\x1b[0m")
	}
	w.hasFg, w.hasBg = false, false
	if more {
		w.b.WriteByte('\n')
	}
}

// appendColour appends the SGR parameters of a foreground (38) or
// background (48) colour.
func (w *writer) appendColour(buf []byte, c rgb, layer int) []byte {
	buf = strconv.AppendInt(buf, int64(layer), 10)
	if w.color == Palette256 {
		buf = append(buf, ";5;"...)
		return strconv.AppendInt(buf, int64(palette256(c)), 10)
	}
	buf = append(buf, ";2;"...)
	buf = strconv.AppendInt(buf, int64(c.r), 10)
	buf = append(buf, ';')
	buf = strconv.AppendInt(buf, int64(c.g), 10)
	buf = append(buf, ';')
	return strconv.AppendInt(buf, int64(c.b), 10)
}

// palette256 returns the nearest colour of the xterm 256-colour palette:
// the 6×6×6 cube (16–231) or the gray ramp (232–255).
func palette256(c rgb) int {
	levels := [6]int{0, 95, 135, 175, 215, 255}
	nearest := func(v uint8) int {
		best := 0
		for i, l := range levels {
			if abs(int(v)-l) < abs(int(v)-levels[best]) {
				best = i
			}
		}
		return best
	}
	r, g, b := nearest(c.r), nearest(c.g), nearest(c.b)
	cube := 16 + 36*r + 6*g + b
	cubeErr := sqDist(c, levels[r], levels[g], levels[b])

	gray := (int(c.r) + int(c.g) + int(c.b)) / 3
	step := max(0, min(23, (gray-8+5)/10))
	v := 8 + 10*step
	if sqDist(c, v, v, v) < cubeErr {
		return 232 + step
	}
	return cube
}

func sqDist(c rgb, r, g, b int) int {
	dr, dg, db := int(c.r)-r, int(c.g)-g, int(c.b)-b
	return dr*dr + dg*dg + db*db
}

func abs(n int) int { return max(n, -n) }
