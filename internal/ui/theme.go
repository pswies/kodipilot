package ui

import (
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

func newTheme(dark bool, profile colorprofile.Profile) theme {
	pick := lipgloss.Complete(profile)
	lightDark := lipgloss.LightDark(dark)
	c := lipgloss.Color
	none := lipgloss.NoColor{}

	accent := lightDark(pick(c("4"), c("26"), c("#3E63DD")), pick(c("12"), c("111"), c("#82AAFF")))
	grey := lightDark(pick(c("8"), c("245"), c("#8A8F98")), pick(c("8"), c("244"), c("#7C8190")))
	selection := lightDark(pick(none, c("254"), c("#E9EDF9")), pick(none, c("236"), c("#262B3A")))
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
