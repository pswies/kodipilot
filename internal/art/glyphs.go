package art

import (
	"math/bits"
	"sync"
)

// glyph is a candidate symbol: the samples its ink covers in the 2×6 cell
// grid (bit 2·row+column).
type glyph struct {
	r    rune
	mask uint16
	n    int // ink samples
}

// region returns the mask of the samples in columns [c0,c1) and rows [r0,r1).
func region(c0, c1, r0, r1 int) uint16 {
	var m uint16
	for r := r0; r < r1; r++ {
		for c := c0; c < c1; c++ {
			m |= 1 << (r*cellW + c)
		}
	}
	return m
}

// Quadrants and halves split the cell into 2×2 parts of 1×3 samples.
var (
	upperLeft  = region(0, 1, 0, 3)
	upperRight = region(1, 2, 0, 3)
	lowerLeft  = region(0, 1, 3, 6)
	lowerRight = region(1, 2, 3, 6)
)

// blockGlyphs are the half blocks and quadrants. A split and its complement
// are the same choice with the colours swapped, so one of each is enough.
func blockGlyphs() []glyph {
	return []glyph{
		{r: '▀', mask: upperLeft | upperRight},
		{r: '▌', mask: upperLeft | lowerLeft},
		{r: '▘', mask: upperLeft},
		{r: '▝', mask: upperRight},
		{r: '▖', mask: lowerLeft},
		{r: '▗', mask: lowerRight},
		{r: '▚', mask: upperLeft | lowerRight},
	}
}

// sextantGlyphs are the sextants U+1FB00–U+1FB3B. Sextant pattern bits,
// from the least significant: top left, top right, middle left, middle
// right, bottom left, bottom right; each sextant row is 2 sample rows. The
// Unicode block leaves out the patterns that exist elsewhere: empty, full
// and the left and right halves.
func sextantGlyphs() []glyph {
	var out []glyph
	r := rune(0x1FB00)
	for p := 1; p < 63; p++ {
		if p == 0b010101 || p == 0b101010 {
			continue // ▌ and ▐
		}
		var m uint16
		for bit := range 6 {
			if p&(1<<bit) != 0 {
				row, col := bit/2, bit%2
				m |= region(col, col+1, 2*row, 2*row+2)
			}
		}
		out = append(out, glyph{r: r, mask: m})
		r++
	}
	return out
}

// glyphTables are the candidates of each mode, without duplicate splits.
var glyphTables = sync.OnceValue(func() map[Mode][]glyph {
	return map[Mode][]glyph{
		Blocks:  unique(blockGlyphs()),
		Symbols: unique(append(blockGlyphs(), sextantGlyphs()...)),
	}
})

func glyphsFor(mode Mode) []glyph { return glyphTables()[mode] }

// unique drops glyphs that split the cell like an earlier one (the same
// mask or its complement) and fills in the ink counts.
func unique(glyphs []glyph) []glyph {
	seen := map[uint16]bool{}
	var out []glyph
	for _, g := range glyphs {
		key := min(g.mask, fullMask&^g.mask)
		if seen[key] {
			continue
		}
		seen[key] = true
		g.n = bits.OnesCount16(g.mask)
		out = append(out, g)
	}
	return out
}
