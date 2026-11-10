package ui

// tier is how the screen's body is shared between the list and the details.
type tier int

const (
	listOnly    tier = iota // the list alone
	sideBySide              // the details on the left, the list on the right
	stacked                 // the details on top, the list below
	detailsOnly             // the details alone (on screens too small for a pane)
)

// The sizes that decide the tier.
const (
	sideMinWidth     = 90  // side by side from this width…
	sideMinHeight    = 14  // …and this height
	wideWidth        = 120 // from here the list gets a quarter of the width
	listMinWidth     = 32  // side by side
	listMaxWidth     = 40  // side by side, on medium terminals
	stackedMinDetail = 12  // stacked: the details' least height
	stackedMinList   = 10  // stacked: the list's least height, its column header included
	stackedShare     = 55  // stacked: the details' share of the body, in percent
)

// layout is where the list and the details go in the body, the rows between
// the header and the status line. A hidden part has zero size. Heights
// include the list's column header; widths include the details' margins.
type layout struct {
	tier                  tier
	listWidth, listHeight int
	paneWidth, paneHeight int
}

// paneTier returns the tier with a details pane that fits the screen, or
// listOnly when none does: side by side on wide screens, stacked on narrow
// but tall ones.
func (m *Model) paneTier() tier {
	switch body := m.middleHeight(); {
	case m.width >= sideMinWidth && m.height >= sideMinHeight:
		return sideBySide
	case m.width < sideMinWidth && body >= stackedMinDetail+1+stackedMinList:
		return stacked
	}
	return listOnly
}

// layout decides the tier and the sizes of the list and the details. The
// details show only for a highlighted item; the user can hide the pane (i),
// or show the details alone when no pane fits.
func (m *Model) layout() layout {
	w, body := m.width, m.middleHeight()
	_, _, item := m.highlighted()
	t := m.paneTier()
	switch {
	case !item || t != listOnly && m.paneHidden || t == listOnly && !m.detailsFull:
		return layout{tier: listOnly, listWidth: w, listHeight: body}
	case t == listOnly:
		return layout{tier: detailsOnly, paneWidth: w, paneHeight: body}
	case t == sideBySide:
		list := min(listMaxWidth, max(listMinWidth, w*3/10))
		if w >= wideWidth {
			list = max(listMinWidth, w/4)
		}
		return layout{tier: sideBySide, listWidth: list, listHeight: body, paneWidth: w - list - 1, paneHeight: body}
	default:
		details := max(stackedMinDetail, min((body*stackedShare+50)/100, body-1-stackedMinList))
		return layout{tier: stacked, listWidth: w, listHeight: body - details - 1, paneWidth: w, paneHeight: details}
	}
}

// fullDetails reports whether the details fill the body instead of the list.
func (m *Model) fullDetails() bool { return m.layout().tier == detailsOnly }

// detailsShown reports whether the details are on screen.
func (m *Model) detailsShown() bool { return m.layout().paneWidth > 0 }
