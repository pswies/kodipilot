package ui

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/pswies/kodipilot/internal/art"
	"github.com/pswies/kodipilot/internal/artwork"
	"github.com/pswies/kodipilot/internal/kodi"
)

const (
	detailsDelay   = 120 * time.Millisecond // the details fetch waits for the cursor to rest
	artShare       = 55                     // the image takes at most this % of the pane height
	paneMinWidth   = 90                     // narrower terminals have no pane
	paneMinHeight  = 14
	wideWidth      = 120 // from here the list gets a quarter of the width
	listMinWidth   = 32
	listMaxWidth   = 40 // on medium terminals
	detailLabelGap = 2
)

type itemKind int

const (
	artistItem itemKind = iota + 1
	albumItem
	songItem
)

// itemKey identifies the item the details are about.
type itemKey struct {
	kind itemKind
	id   int
}

// details is the content of the details pane.
type details struct {
	image  string  // Kodi art URL, or ""
	title  string  // in the accent
	fields []field // short values
	texts  []field // long texts, each under its label
	file   string  // dimmed at the end of the fields

	loading bool // a stand-in while the details load
}

type field struct{ label, value string }

// add appends a field unless the value is empty.
func (d *details) add(label, value string) {
	if value = strings.TrimSpace(value); value != "" {
		d.fields = append(d.fields, field{label, value})
	}
}

func (d *details) addText(label, value string) {
	if value = strings.TrimSpace(value); value != "" {
		d.texts = append(d.texts, field{label, value})
	}
}

// Messages of the details pane.
type (
	detailsDueMsg struct{ sequence int } // the cursor has rested
	detailsMsg    struct {
		key     itemKey
		details *details
		err     error
	}
	artMsg struct {
		url, pending string
		err          error
	}
)

// highlighted returns the item under the cursor, and a fallback title for
// it while its details load.
func (m *Model) highlighted() (itemKey, string, bool) {
	if m.view == queueView {
		if len(m.queue) == 0 || m.queue[m.queueList.index].ID <= 0 {
			return itemKey{}, "", false
		}
		item := m.queue[m.queueList.index]
		return itemKey{songItem, item.ID}, item.Title, true
	}
	l := m.current()
	if l == nil || l.len() == 0 {
		return itemKey{}, "", false
	}
	i := l.list.index
	switch l.kind {
	case artistsLevel:
		return itemKey{artistItem, l.artists[i].ID}, l.artists[i].Name, true
	case albumsLevel:
		return itemKey{albumItem, l.albums[i].ID}, l.albums[i].Title, true
	default:
		return itemKey{songItem, l.songs[i].ID}, l.songs[i].Title, true
	}
}

// shown returns the details to show for the highlighted item. While they
// load, a stand-in has the item's name and, for a track, the album cover
// when it is known, so that the pane does not jump about.
func (m *Model) shown() *details {
	key, name, ok := m.highlighted()
	if !ok {
		return &details{}
	}
	if d := m.info[key]; d != nil {
		return d
	}
	d := &details{title: name, loading: true}
	if l := m.current(); m.view == libraryView && l.kind == tracksLevel {
		if album := m.info[itemKey{albumItem, l.album.ID}]; album != nil {
			d.image = album.image
		}
	}
	return d
}

// layout returns the width of the list and of the details pane (0 for
// none), which share the screen with a one-column divider: the pane on the
// left, the list on the right.
func (m *Model) layout() (list, pane int) {
	w := m.width
	if _, _, ok := m.highlighted(); !ok || m.paneHidden || !m.paneFits() {
		return w, 0
	}
	if w >= wideWidth {
		list = max(listMinWidth, w/4)
	} else {
		list = min(listMaxWidth, max(listMinWidth, w*3/10))
	}
	return list, w - list - 1
}

func (m *Model) paneFits() bool { return m.width >= paneMinWidth && m.height >= paneMinHeight }

// fullDetails reports whether the details fill the screen instead of the
// list (on terminals too small for the pane).
func (m *Model) fullDetails() bool {
	_, _, ok := m.highlighted()
	return ok && m.detailsFull && !m.paneFits()
}

func (m *Model) detailsShown() bool {
	_, pane := m.layout()
	return pane > 0 || m.fullDetails()
}

// toggleDetails shows or hides the pane, or the full-screen details when
// the pane does not fit.
func (m *Model) toggleDetails() tea.Cmd {
	if m.paneFits() {
		m.paneHidden = !m.paneHidden
	} else {
		m.detailsFull = !m.detailsFull
	}
	return nil
}

// followDetails runs after every update: when the highlighted item changes,
// it schedules a details fetch for when the cursor rests, and it starts
// rendering the image the pane needs.
func (m *Model) followDetails() tea.Cmd {
	if m.paneFits() {
		m.detailsFull = false
	}
	key, _, ok := m.highlighted()
	if !ok || !m.detailsShown() {
		m.detailsKey = itemKey{}
		return nil
	}
	if d := m.info[key]; d != nil {
		m.hadImage = d.image != "" && !m.artFailed[d.image]
	}
	var cmds []tea.Cmd
	if key != m.detailsKey {
		m.detailsKey, m.detailsTop = key, 0
		m.detailsSequence++
		if m.info[key] == nil {
			cmds = append(cmds, m.clock.After(detailsDelay, detailsDueMsg{m.detailsSequence}))
		}
	}
	return tea.Batch(append(cmds, m.renderArt())...)
}

// fetchDetails fetches the details of the highlighted item.
func (m *Model) fetchDetails() tea.Cmd {
	key, name, ok := m.highlighted()
	if !ok || m.info[key] != nil || m.lib == nil {
		return nil
	}
	lib := m.lib
	return m.command(func(ctx context.Context) tea.Msg {
		d, err := loadDetails(ctx, lib, key, name)
		return detailsMsg{key, d, err}
	})
}

func loadDetails(ctx context.Context, lib *kodi.Library, key itemKey, name string) (*details, error) {
	switch key.kind {
	case artistItem:
		a, err := lib.ArtistDetails(ctx, key.id)
		if err != nil {
			return nil, err
		}
		return artistDetails(name, a, len(lib.Albums(key.id))), nil
	case albumItem:
		a, err := lib.AlbumDetails(ctx, key.id)
		if err != nil {
			return nil, err
		}
		songs, err := lib.Songs(ctx, key.id)
		if err != nil {
			return nil, err
		}
		return albumDetails(name, a, len(songs)), nil
	default:
		s, err := lib.SongDetails(ctx, key.id)
		if err != nil {
			return nil, err
		}
		return songDetails(name, s), nil
	}
}

func artistDetails(name string, a kodi.ArtistDetails, albums int) *details {
	d := &details{image: a.Image(), title: name}
	d.add("Genre", strings.Join(a.Genre, ", "))
	d.add("Style", strings.Join(a.Style, ", "))
	d.add("Born", a.Born)
	d.add("Formed", a.Formed)
	d.add("Active", strings.Join(a.YearsActive, ", "))
	d.add("Albums", plainCount(albums))
	d.addText("Biography", a.Description)
	return d
}

func albumDetails(name string, a kodi.AlbumDetails, tracks int) *details {
	d := &details{image: a.Image(), title: cmp.Or(a.Title, name)}
	d.add("Artist", a.DisplayArtist)
	d.add("Year", year(a.Year))
	d.add("Genre", strings.Join(a.Genre, ", "))
	d.add("Style", strings.Join(a.Style, ", "))
	d.add("Mood", strings.Join(a.Mood, ", "))
	d.add("Label", a.Label)
	d.add("Type", a.ReleaseType)
	if a.Duration > 0 {
		d.add("Length", formatDuration(time.Duration(a.Duration)*time.Second))
	}
	contents := ""
	if tracks > 0 {
		contents = plural(tracks, "track")
	}
	if a.TotalDiscs > 1 {
		contents = strings.TrimPrefix(contents+", "+plural(a.TotalDiscs, "disc"), ", ")
	}
	d.add("Contents", contents)
	d.addText("Review", a.Description)
	return d
}

func songDetails(name string, s kodi.SongDetails) *details {
	d := &details{image: s.Image(), title: cmp.Or(s.Title, name), file: s.File}
	d.add("Artist", strings.Join(s.Artist, ", "))
	d.add("Album", s.Album)
	d.add("Year", year(s.Year))
	switch {
	case s.Disc > 0 && s.Track > 0:
		d.add("Track", fmt.Sprintf("%d (disc %d)", s.Track, s.Disc))
	case s.Track > 0:
		d.add("Track", fmt.Sprint(s.Track))
	}
	if s.Duration > 0 {
		d.add("Duration", formatDuration(time.Duration(s.Duration)*time.Second))
	}
	d.add("Genre", strings.Join(s.Genre, ", "))
	d.add("Mood", strings.Join(s.Mood, ", "))
	d.add("Composer", s.Composer)
	d.add("Rating", rating(s.Rating, s.UserRating))
	d.add("Played", played(s.PlayCount, s.LastPlayed))
	d.add("Audio", audio(s.Bitrate, s.SampleRate, s.Channels))
	d.addText("Comment", s.Comment)
	d.addText("Lyrics", s.Lyrics)
	return d
}

func year(y int) string {
	if y <= 0 {
		return ""
	}
	return fmt.Sprint(y)
}

func plainCount(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprint(n)
}

func rating(kodiRating float64, user int) string {
	var parts []string
	if kodiRating > 0 {
		parts = append(parts, fmt.Sprintf("%.1f/10", kodiRating))
	}
	if user > 0 {
		parts = append(parts, fmt.Sprintf("yours %d/10", user))
	}
	return strings.Join(parts, " · ")
}

func played(count int, last string) string {
	if count <= 0 {
		return ""
	}
	s := fmt.Sprintf("%d times", count)
	if count == 1 {
		s = "once"
	}
	if last != "" {
		s += ", last " + last
	}
	return s
}

func audio(bitrate, sampleRate, channels int) string {
	var parts []string
	if bitrate > 0 {
		parts = append(parts, fmt.Sprintf("%d kbit/s", bitrate))
	}
	if sampleRate > 0 {
		parts = append(parts, strings.TrimSuffix(fmt.Sprintf("%.1f", float64(sampleRate)/1000), ".0")+" kHz")
	}
	switch {
	case channels == 1:
		parts = append(parts, "mono")
	case channels == 2:
		parts = append(parts, "stereo")
	case channels > 2:
		parts = append(parts, fmt.Sprintf("%d channels", channels))
	}
	return strings.Join(parts, " · ")
}

// artOptions returns how images are drawn on this terminal.
func (m *Model) artOptions() art.Options {
	opts := art.Options{Mode: m.opts.Art}
	switch m.colors {
	case colorprofile.TrueColor:
		opts.Color = art.TrueColor
	case colorprofile.ANSI256, colorprofile.ANSI:
		opts.Color = art.Palette256
	default:
		opts.Color = art.NoColor
	}
	return opts
}

// artBox returns the space for the image of the details shown in a pane
// w×h, or false when there is no image to show.
func (m *Model) artBox(d *details, w, h int) (cols, rows int, ok bool) {
	if d == nil || d.image == "" || m.opts.Images == nil || m.artFailed[d.image] {
		return 0, 0, false
	}
	cols, rows = w, h*artShare/100
	return cols, rows, cols >= 4 && rows >= 2
}

// renderArt starts rendering the image the details pane shows, unless it is
// rendered or rendering already.
func (m *Model) renderArt() tea.Cmd {
	d := m.shown()
	w, h := m.paneSize()
	cols, rows, ok := m.artBox(d, w, h)
	if !ok {
		return nil
	}
	opts, loader, url := m.artOptions(), m.opts.Images, d.image
	if _, done := loader.Cached(url, cols, rows, opts); done {
		return nil
	}
	pending := fmt.Sprint(url, cols, rows, opts)
	if m.artPending[pending] {
		return nil
	}
	m.artPending[pending] = true
	return commandWithin(artwork.DefaultTimeout, func(ctx context.Context) tea.Msg {
		_, err := loader.Render(ctx, url, cols, rows, opts)
		return artMsg{url, pending, err}
	})
}

// paneSize returns the inner size of the details pane or the full-screen
// details: one column of margin each side.
func (m *Model) paneSize() (w, h int) {
	_, pane := m.layout()
	if m.fullDetails() {
		pane = m.width
	}
	return max(0, pane-2), m.listHeight()
}

// detailsLines renders the details of the highlighted item in exactly
// height lines of width w (margins included): the window of the details
// document at the scroll position, with dim indicators when there is more
// above or below.
func (m *Model) detailsLines(w, height int) []string {
	doc := m.detailsDoc(w, height)
	top := min(m.detailsTop, max(0, len(doc)-height))
	out := slices.Clone(doc[top:min(len(doc), top+height)])
	indicator := func(text string) string {
		return " " + line(max(0, w-2), m.theme.plain, nil, []span{{text, m.theme.dim}})
	}
	if top > 0 && len(out) > 0 {
		out[0] = indicator("↑ more  [")
	}
	if top+height < len(doc) && len(out) > 0 {
		out[len(out)-1] = indicator("↓ more  ]")
	}
	for len(out) < height {
		out = append(out, "")
	}
	return out
}

// detailsOverflow reports whether the details shown do not fit, so that
// they can be scrolled.
func (m *Model) detailsOverflow() bool {
	if !m.detailsShown() {
		return false
	}
	w, h := m.paneSize()
	return len(m.detailsDoc(w+2, h)) > h
}

// scrollDetails scrolls the details by half the pane height, within the
// document.
func (m *Model) scrollDetails(direction int) tea.Cmd {
	if !m.detailsShown() {
		return nil
	}
	w, h := m.paneSize()
	last := max(0, len(m.detailsDoc(w+2, h))-h)
	m.detailsTop = max(0, min(last, m.detailsTop+direction*max(1, h/2)))
	return nil
}

func (m *Model) scrollDetailsUp() tea.Cmd   { return m.scrollDetails(-1) }
func (m *Model) scrollDetailsDown() tea.Cmd { return m.scrollDetails(+1) }

// detailsDoc lays out the details of the highlighted item as one document of
// lines w columns wide (margins included), for a pane height lines tall.
//
// The image sits at the top left. When there are at least besideMin columns
// to its right, the text starts there and flows on under the image at the
// full width; otherwise the text starts below the image.
func (m *Model) detailsDoc(w, height int) []string {
	t := m.theme
	inner := max(0, w-2)
	d := m.shown()

	var art []string
	cols, rows, ok := m.artBox(d, inner, height)
	if !ok && d.loading && m.hadImage { // keep the image's place
		cols, rows, ok = inner, height*artShare/100, inner >= 4 && height*artShare/100 >= 2
	}
	if ok {
		if s, done := m.cachedArt(d.image, cols, rows); done {
			art = strings.Split(s, "\n")
		} else {
			for _, l := range placeholder(min(cols, 2*rows), rows) {
				art = append(art, t.dim.Render(l))
			}
		}
	}
	artWidth, side, textTop := 0, 0, 0
	if len(art) > 0 {
		artWidth = ansi.StringWidth(art[0])
		if inner-artWidth-besideGap >= besideMin {
			side = artWidth + besideGap
		} else {
			textTop = len(art) + 1 // below the image and a blank line
		}
	}

	f := flow{room: func(y int) int {
		if y+textTop < len(art) {
			return min(inner-side, textMaxWidth)
		}
		return min(inner, textMaxWidth)
	}}
	if d.title != "" {
		f.text(d.title, nil, t.strong)
	}
	labelWidth := 0
	for _, field := range d.fields {
		labelWidth = max(labelWidth, width(field.label))
	}
	for _, field := range d.fields {
		label := []span{{pad(field.label, labelWidth+detailLabelGap), t.dim}}
		f.text(field.value, label, t.plain)
	}
	if d.file != "" {
		f.text(d.file, nil, t.dim)
	}
	for _, field := range d.texts {
		f.lines = append(f.lines, nil)
		f.text(field.label, nil, t.dim)
		f.text(field.value, nil, t.plain)
	}

	var doc []string
	for y := 0; y < max(len(art), textTop+len(f.lines)); y++ {
		var spans []span
		if i := y - textTop; i >= 0 && i < len(f.lines) {
			spans = f.lines[i]
		}
		switch {
		case y < len(art) && side > 0:
			doc = append(doc, " "+fill(art[y], side)+line(inner-side, t.plain, spans, nil))
		case y < len(art):
			doc = append(doc, " "+art[y])
		default:
			doc = append(doc, " "+line(inner, t.plain, spans, nil))
		}
	}
	return doc
}

const (
	besideGap    = 2  // between the image and the text beside it
	besideMin    = 28 // the least width for text beside the image
	textMaxWidth = 88 // the details text is no wider, for reading
)

// flow collects wrapped lines whose width depends on their position, so
// that text can run beside the image and then under it.
type flow struct {
	room  func(y int) int // the width of line y
	lines [][]span
}

// text appends text in a style, wrapped by paragraphs. The first line starts
// with prefix and the others are indented to match.
func (f *flow) text(text string, prefix []span, style lipgloss.Style) {
	indent := 0
	for _, s := range prefix {
		indent += width(s.text)
	}
	first := true
	for i, para := range paragraphs(text) {
		if i > 0 {
			f.lines = append(f.lines, nil)
		}
		for para != "" {
			w := max(1, f.room(len(f.lines))-indent)
			next, rest := cut(para, w)
			lead := []span{{strings.Repeat(" ", indent), style}}
			if first {
				lead, first = prefix, false
			}
			f.lines = append(f.lines, append(slices.Clone(lead), span{next, style}))
			para = rest
		}
	}
}

// paragraphs splits text at line breaks into cleaned, non-empty paragraphs.
func paragraphs(text string) []string {
	var out []string
	for _, p := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if p = strings.TrimSpace(clean(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// cut returns the first line of text wrapped at w columns, breaking at a
// space when it can, and the rest.
func cut(text string, w int) (string, string) {
	end := 0 // the end of the words that fit
	for end < len(text) {
		start := end
		if start > 0 {
			start++ // past the space
		}
		stop := strings.IndexByte(text[start:], ' ')
		if stop < 0 {
			stop = len(text)
		} else {
			stop += start
		}
		if width(text[:stop]) > w {
			break
		}
		end = stop
	}
	if end == 0 { // a word longer than the line: break it
		first := ansi.Truncate(text, w, "")
		if first == "" {
			_, size := utf8.DecodeRuneInString(text)
			first = text[:size]
		}
		return first, strings.TrimLeft(text[len(first):], " ")
	}
	return text[:end], strings.TrimLeft(text[end:], " ")
}

// placeholder is a dim frame with "loading…" in it.
func placeholder(w, h int) []string {
	if w < 2 || h < 2 {
		return nil
	}
	lines := []string{"╭" + strings.Repeat("─", w-2) + "╮"}
	for i := 1; i < h-1; i++ {
		inside := strings.Repeat(" ", w-2)
		if i == (h-1)/2 {
			inside = pad(strings.Repeat(" ", max(0, (w-2-8)/2))+"loading…", w-2)
		}
		lines = append(lines, "│"+inside+"│")
	}
	return append(lines, "╰"+strings.Repeat("─", w-2)+"╯")
}

// cachedArt returns the rendered image, if it is ready.
func (m *Model) cachedArt(url string, cols, rows int) (string, bool) {
	if url == "" || m.opts.Images == nil {
		return "", false
	}
	return m.opts.Images.Cached(url, cols, rows, m.artOptions())
}
