package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/jithin-jz/ytune/internal/youtube"
)

// The layout deliberately keeps nothing to the right of a song title: no second
// column and no right-hand border. Terminals disagree by a few columns on how
// wide Hindi or Malayalam text is, and no measurement matches them all, so
// anything drawn after a title would land in the wrong place on some of them.
const (
	// The rows that aren't lists: the search box and its heading, a heading for
	// each list, the now-playing section and the help line.
	fixedRows   = 8
	sidePadding = 1

	// Below this the progress line can't fit a track that's over an hour long.
	minWidth  = 40
	minHeight = 12
)

// View implements tea.Model.
func (m *Model) View() tea.View {
	view := tea.NewView(m.render())
	view.AltScreen = true
	view.WindowTitle = "ytune"
	if cursor := m.input.Cursor(); cursor != nil && m.fitsScreen() {
		cursor.Position.X += sidePadding
		cursor.Position.Y += 1
		view.Cursor = cursor
	}
	return view
}

func (m *Model) fitsScreen() bool {
	return m.width >= minWidth && m.height >= minHeight
}

func (m *Model) render() string {
	if !m.fitsScreen() {
		return "The window is too small for ytune."
	}
	width := m.contentWidth()
	var lines []string

	lines = append(lines, heading("Search", m.width, m.focus == focusSearch))
	lines = append(lines, m.rows([]string{m.input.View()}, 1)...)

	lines = append(lines, heading(m.resultsTitle, m.width, m.focus == focusResults))
	height := m.resultsHeight()
	lines = append(lines, m.rows(m.results.render(width, height, m.focus == focusResults), height)...)

	lines = append(lines, heading("Queue", m.width, m.focus == focusQueue))
	height = m.queueHeight()
	lines = append(lines, m.rows(m.queue.render(width, height, m.focus == focusQueue), height)...)

	lines = append(lines, heading("Now playing", m.width, false))
	lines = append(lines, m.rows(m.nowPlaying(width), 2)...)

	lines = append(lines, m.footer())
	return strings.Join(lines, "\n")
}

func (m *Model) contentWidth() int {
	return m.width - 2*sidePadding
}

// resultsHeight and queueHeight split the spare rows between the two lists,
// giving most of them to the results.
func (m *Model) resultsHeight() int {
	return max(m.height-fixedRows, 2) * 3 / 5
}

func (m *Model) queueHeight() int {
	return max(m.height-fixedRows, 2) - m.resultsHeight()
}

// rows indents lines and pads them with blank rows to exactly height.
func (m *Model) rows(lines []string, height int) []string {
	padding := strings.Repeat(" ", sidePadding)
	result := make([]string, 0, height)
	for i := range height {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		result = append(result, padding+fit(line, m.contentWidth())+padding)
	}
	return result
}

func (m *Model) nowPlaying(width int) []string {
	track, ok := m.playlist.Current()
	if !m.active || !ok {
		return []string{styleMuted.Render("Nothing playing. Press / to search.")}
	}
	if m.loading {
		return []string{"Loading " + track.Title + "..."}
	}
	icon := "▶"
	if m.status.Paused {
		icon = "||"
	}
	title := icon + " " + track.Title
	if track.Channel != "" {
		title += styleMuted.Render(" · " + track.Channel)
	}
	details := fmt.Sprintf("  %s / %s  vol %d",
		formatTime(m.status.Position), formatTime(m.status.Duration), m.status.Volume)
	barWidth := max(width-ansi.StringWidthWc(details), 1)
	bar := progressBar(m.status.Position, m.status.Duration, barWidth)
	return []string{title, styleProgress.Render(bar) + details}
}

func (m *Model) footer() string {
	if m.notice != "" {
		if m.noticeIsError {
			return fit(styleError.Render(" "+m.notice), m.width)
		}
		return fit(" "+m.notice, m.width)
	}
	var parts []string
	for _, item := range helpItems {
		parts = append(parts, styleKey.Render(item.key)+" "+styleMuted.Render(item.label))
	}
	return fit(" "+strings.Join(parts, "  "), m.width)
}

// heading draws a section title on a rule: ── Queue ─────────
func heading(title string, width int, focused bool) string {
	style := styleBorder
	if focused {
		style = styleBorderFocused
	}
	label := " " + truncate(title, width-4) + " "
	return style.Render("──" + label + strings.Repeat("─", max(width-2-ansi.StringWidthWc(label), 0)))
}

// fit cuts or pads a line to exactly width columns, ignoring color codes.
func fit(line string, width int) string {
	line = truncate(line, width)
	return line + strings.Repeat(" ", max(width-ansi.StringWidthWc(line), 0))
}

// truncate cuts a line down to width. TruncateWc alone isn't enough: it decides
// whether to cut by measuring with GraphemeWidth, so it can hand back a Hindi or
// Malayalam line that's still too long. Asking for less forces the cut.
func truncate(line string, width int) string {
	if ansi.StringWidthWc(line) <= width {
		return line
	}
	for limit := width; limit > 0; limit-- {
		if cut := ansi.TruncateWc(line, limit, "…"); ansi.StringWidthWc(cut) <= width {
			return cut
		}
	}
	return ""
}

// formatTime shows seconds as mm:ss or h:mm:ss. Negative means unknown.
func formatTime(seconds float64) string {
	if seconds < 0 {
		return "--:--"
	}
	total := int(seconds)
	hours, minutes, secs := total/3600, total/60%60, total%60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, secs)
	}
	return fmt.Sprintf("%02d:%02d", minutes, secs)
}

func progressBar(position, duration float64, width int) string {
	fraction := 0.0
	if position > 0 && duration > 0 {
		fraction = min(position/duration, 1)
	}
	filled := int(fraction*float64(width-1) + 0.5)
	return strings.Repeat("━", filled) + "●" + strings.Repeat("─", width-1-filled)
}

func trackRow(track youtube.Track) string {
	var details []string
	if track.Channel != "" {
		details = append(details, track.Channel)
	}
	if track.Duration > 0 {
		details = append(details, formatTime(float64(track.Duration)))
	}
	if len(details) == 0 {
		return track.Title
	}
	// Single spaces only: the renderer jumps over runs of spaces to the column it
	// expects, which would land on top of a title that's wider than it thinks.
	return track.Title + styleMuted.Render(" ("+strings.Join(details, " · ")+")")
}
