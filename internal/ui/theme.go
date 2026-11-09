package ui

import (
	"image/color"
	"math"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// theme holds the styles: one accent colour plus muted greys, with variants
// for dark and light terminals and explicit choices for 16 and 256 colours.
// Without colour (NO_COLOR) the selection bar, bold text and the ▶ marks
// still carry the meaning.
type theme struct {
	plain    lipgloss.Style // the main text
	dim      lipgloss.Style // secondary text: years, durations, counts, hints
	accent   lipgloss.Style // the current level, now playing
	strong   lipgloss.Style // accent and bold
	selected lipgloss.Style // the base of the selected row
	bar      lipgloss.Style // the selection bar at the left edge
	err      lipgloss.Style // errors
	popup    lipgloss.Style // the pop-up frame
}

// selectionShare is how far the selection band moves from the terminal's
// background toward its foreground.
const selectionShare = 0.08

// newTheme returns the styles for a dark or light terminal with the colour
// profile. The terminal's background and foreground, when known (nil
// otherwise), give the selection band a colour that suits it.
func newTheme(dark bool, profile colorprofile.Profile, bg, fg color.Color) theme {
	pick := lipgloss.Complete(profile)
	lightDark := lipgloss.LightDark(dark)
	c := lipgloss.Color
	none := lipgloss.NoColor{}

	accent := lightDark(pick(c("4"), c("26"), c("#3E63DD")), pick(c("12"), c("111"), c("#82AAFF")))
	grey := lightDark(pick(c("8"), c("242"), c("#646A78")), pick(c("8"), c("247"), c("#9399A8")))
	selection := lightDark(pick(none, c("254"), c("#E9EDF9")), pick(none, c("236"), c("#262B3A")))
	if bg != nil && profile == colorprofile.TrueColor {
		if fg == nil {
			fg = lightDark(c("#000000"), c("#FFFFFF"))
		}
		selection = blend(bg, fg, selectionShare)
		grey = readable(grey, selection, fg)
	}
	red := lightDark(pick(c("1"), c("161"), c("#C93C56")), pick(c("9"), c("211"), c("#F7768E")))

	base := lipgloss.NewStyle()
	return theme{
		plain:    base,
		dim:      base.Foreground(grey),
		accent:   base.Foreground(accent),
		strong:   base.Foreground(accent).Bold(true),
		selected: base.Background(selection).Bold(true),
		bar:      base.Foreground(accent),
		err:      base.Foreground(red),
		popup:    base.Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(0, 1),
	}
}

// blend returns the colour share of the way from a to b.
func blend(a, b color.Color, share float64) color.Color {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	mix := func(x, y uint32) uint8 {
		return uint8((float64(x)*(1-share) + float64(y)*share) / 257)
	}
	return color.RGBA{mix(ar, br), mix(ag, bg), mix(ab, bb), 0xff}
}

// minContrast is the least contrast of dim text with the selection band.
const minContrast = 4.5

// readable returns grey, moved toward fg as far as needed for a WCAG
// contrast of minContrast with bg.
func readable(grey, bg, fg color.Color) color.Color {
	for share := 0.0; share <= 1; share += 0.05 {
		if c := blend(grey, fg, share); contrast(c, bg) >= minContrast {
			return c
		}
	}
	return fg
}

// contrast returns the WCAG contrast ratio of two colours.
func contrast(a, b color.Color) float64 {
	la, lb := luminance(a), luminance(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

// luminance returns the WCAG relative luminance of a colour.
func luminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	channel := func(v uint32) float64 {
		x := float64(v) / 0xffff
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(r) + 0.7152*channel(g) + 0.0722*channel(b)
}
