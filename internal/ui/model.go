// Package ui is the full-screen interface.
package ui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/jithin-jz/ytune/internal/player"
	"github.com/jithin-jz/ytune/internal/playlist"
	"github.com/jithin-jz/ytune/internal/youtube"
)

const (
	tickInterval   = 500 * time.Millisecond
	noticeDuration = 5 * time.Second
)

// Player is what the interface needs from the audio player. Its commands return
// straight away and take effect in the order they're called.
type Player interface {
	Load(path string)
	Stop()
	TogglePause()
	Seek(seconds float64)
	ChangeVolume(delta int)
	Status() (player.Status, error)
}

// Source is what the interface needs to find and fetch music. Its methods block,
// so they're only ever called from commands.
type Source interface {
	Search(query string) ([]youtube.Track, error)
	Resolve(url string) ([]youtube.Track, error)
	Fetch(track youtube.Track) (string, error)
}

type focus int

const (
	focusSearch focus = iota
	focusResults
	focusQueue
	focusCount
)

type (
	tickMsg   struct{}
	statusMsg struct {
		status player.Status
		err    error
		token  int
	}
	searchMsg struct {
		seq    int
		query  string
		tracks []youtube.Track
		err    error
	}
	linkMsg struct {
		tracks []youtube.Track
		err    error
	}
	fetchedMsg struct {
		token int
		path  string
		err   error
	}
	noticeExpiredMsg struct{ seq int }
	updatedMsg       struct{ version string }
)

// Model is the whole state of the interface.
type Model struct {
	player  Player
	source  Source
	update  func() (string, error)
	startup string
	// after delivers msg once d has passed. Tests replace it to avoid waiting.
	after func(d time.Duration, msg tea.Msg) tea.Cmd

	playlist *playlist.Playlist
	tracks   []youtube.Track // The current search results.

	input        textinput.Model
	results      list
	queue        list
	resultsTitle string
	focus        focus
	width        int
	height       int

	status    player.Status
	active    bool // A track is playing or being fetched.
	loading   bool
	loadToken int // Lets a finished fetch notice it was overtaken by a newer one.
	searchSeq int // The same for searches.

	notice        string
	noticeIsError bool
	noticeSeq     int

	fatal error
}

// New returns the interface. startup is an optional search or link to open
// straight away. update is an optional background check for a newer yt-dlp,
// which returns the new version if it installed one.
func New(p Player, source Source, startup string, update func() (string, error)) *Model {
	input := textinput.New()
	input.Placeholder = "Search YouTube, or paste a link"
	input.SetVirtualCursor(false)
	styles := textinput.DefaultDarkStyles()
	styles.Focused.Text = lipgloss.NewStyle()
	styles.Blurred.Text = lipgloss.NewStyle()
	styles.Focused.Placeholder = styleMuted
	styles.Blurred.Placeholder = styleMuted
	styles.Focused.Prompt = styleKey
	styles.Blurred.Prompt = styleMuted
	input.SetStyles(styles)
	input.Focus()

	return &Model{
		player:  p,
		source:  source,
		update:  update,
		startup: startup,
		after: func(d time.Duration, msg tea.Msg) tea.Cmd {
			return tea.Tick(d, func(time.Time) tea.Msg { return msg })
		},
		playlist:     playlist.New(),
		input:        input,
		resultsTitle: "Results",
		status:       player.Status{Position: -1, Duration: -1, Volume: 100},
	}
}

// Err returns the error that ended the program, if one did.
func (m *Model) Err() error {
	return m.fatal
}

// Filter is a tea.WithFilter function that keeps Bubble Tea measuring text one
// character at a time. Windows Terminal reports mode 2027, which would switch it
// to measuring by grapheme clusters, but that's far from how wide Indic text
// really is there: a Hindi title that takes up 24 columns measures as 17.
func Filter(_ tea.Model, msg tea.Msg) tea.Msg {
	if report, ok := msg.(tea.ModeReportMsg); ok && report.Mode == ansi.ModeUnicodeCore {
		return nil
	}
	return msg
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.after(tickInterval, tickMsg{})}
	if m.update != nil {
		cmds = append(cmds, m.updateCmd())
	}
	if m.startup != "" {
		m.input.SetValue(m.startup)
		cmds = append(cmds, m.submit(m.startup))
	}
	return tea.Batch(cmds...)
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.SetWidth(max(m.contentWidth()-lipgloss.Width(m.input.Prompt)-1, 1))
		m.results.clamp(m.resultsHeight())
		m.queue.clamp(m.queueHeight())
		return m, nil
	case tea.KeyPressMsg:
		return m, m.handleKey(msg)
	case tickMsg:
		return m, tea.Batch(m.statusCmd(), m.after(tickInterval, tickMsg{}))
	case statusMsg:
		return m, m.handleStatus(msg)
	case searchMsg:
		return m, m.handleSearch(msg)
	case linkMsg:
		return m, m.handleLink(msg)
	case fetchedMsg:
		return m, m.handleFetched(msg)
	case noticeExpiredMsg:
		if msg.seq == m.noticeSeq {
			m.notice = ""
		}
		return m, nil
	case updatedMsg:
		return m, m.setNotice("Updated yt-dlp to "+msg.version, false)
	}
	// Anything else, such as pasted text, belongs to the search box.
	if m.focus == focusSearch {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.String()
	switch key {
	case "ctrl+c":
		return tea.Quit
	case "tab":
		m.setFocus((m.focus + 1) % focusCount)
		return nil
	case "shift+tab":
		m.setFocus((m.focus + focusCount - 1) % focusCount)
		return nil
	}
	if m.focus == focusSearch {
		return m.handleSearchKey(msg, key)
	}
	if selected, height := m.focusedList(); selected.navigate(key, height) {
		return nil
	}

	switch key {
	case keySearch:
		m.setFocus(focusSearch)
	case keyQuit:
		return tea.Quit
	case keyPause:
		m.player.TogglePause()
	case keySeekBack:
		m.player.Seek(-seekSeconds)
	case keySeekForward:
		m.player.Seek(seekSeconds)
	case keyVolumeDown:
		m.player.ChangeVolume(-volumeStep)
	case keyVolumeUp, keyVolumeUpAlt:
		m.player.ChangeVolume(volumeStep)
	case keyNext:
		if _, ok := m.playlist.Upcoming(); ok {
			return m.advance()
		}
	case keyPrevious:
		if m.playlist.Back() {
			return m.startCurrent()
		}
	case keyEnqueue:
		if m.focus == focusResults && len(m.tracks) > 0 {
			m.playlist.Add(m.tracks[m.results.cursor])
			return m.queueChanged()
		}
	case keyRemove:
		if m.focus == focusQueue && len(m.playlist.Tracks) > 0 {
			if m.playlist.Remove(m.queue.cursor) {
				return m.advance()
			}
			m.refreshQueue()
		}
	case "enter":
		return m.playSelected()
	}
	return nil
}

func (m *Model) handleSearchKey(msg tea.KeyPressMsg, key string) tea.Cmd {
	switch key {
	case "enter":
		return m.submit(strings.TrimSpace(m.input.Value()))
	case "esc":
		m.setFocus(focusResults)
		return nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}

func (m *Model) playSelected() tea.Cmd {
	switch {
	case m.focus == focusResults && len(m.tracks) > 0:
		m.playlist.PlayNow(m.tracks[m.results.cursor])
		return m.startCurrent()
	case m.focus == focusQueue && len(m.playlist.Tracks) > 0:
		m.playlist.Jump(m.queue.cursor)
		return m.startCurrent()
	}
	return nil
}

func (m *Model) focusedList() (*list, int) {
	if m.focus == focusQueue {
		return &m.queue, m.queueHeight()
	}
	return &m.results, m.resultsHeight()
}

func (m *Model) setFocus(f focus) {
	m.focus = f
	if f == focusSearch {
		m.input.Focus()
	} else {
		m.input.Blur()
	}
}

func isURL(text string) bool {
	return strings.HasPrefix(text, "https://") || strings.HasPrefix(text, "http://")
}

func (m *Model) submit(text string) tea.Cmd {
	if text == "" {
		return nil
	}
	source := m.source
	if isURL(text) {
		return func() tea.Msg {
			tracks, err := source.Resolve(text)
			return linkMsg{tracks, err}
		}
	}
	m.searchSeq++
	seq := m.searchSeq
	m.resultsTitle = "Searching..."
	return func() tea.Msg {
		tracks, err := source.Search(text)
		return searchMsg{seq, text, tracks, err}
	}
}

func (m *Model) handleSearch(msg searchMsg) tea.Cmd {
	if msg.seq != m.searchSeq {
		return nil
	}
	if msg.err != nil {
		m.resultsTitle = "Results"
		return m.setNotice(msg.err.Error(), true)
	}
	m.tracks = msg.tracks
	m.resultsTitle = "Results for " + msg.query
	rows := make([]string, len(msg.tracks))
	for i, track := range msg.tracks {
		rows[i] = trackRow(track)
	}
	m.results.cursor = 0
	m.results.setRows(rows, m.resultsHeight())
	if len(rows) == 0 {
		return m.setNotice("No results.", true)
	}
	m.setFocus(focusResults)
	return nil
}

func (m *Model) handleLink(msg linkMsg) tea.Cmd {
	if msg.err != nil {
		return m.setNotice(msg.err.Error(), true)
	}
	if len(msg.tracks) == 0 {
		return m.setNotice("Nothing to play at that link.", true)
	}
	m.playlist.Add(msg.tracks...)
	return tea.Batch(m.queueChanged(), m.setNotice(fmt.Sprintf("Added %d to the queue.", len(msg.tracks)), false))
}

func (m *Model) queueChanged() tea.Cmd {
	m.refreshQueue()
	if !m.active {
		return m.advance()
	}
	if !m.loading {
		return m.prefetchCmd()
	}
	return nil
}

func (m *Model) refreshQueue() {
	rows := make([]string, len(m.playlist.Tracks))
	for i, track := range m.playlist.Tracks {
		marker := "  "
		if i == m.playlist.Index {
			marker = "▶ "
		}
		rows[i] = marker + track.Title
	}
	m.queue.setRows(rows, m.queueHeight())
}

func (m *Model) advance() tea.Cmd {
	if m.playlist.Advance() {
		return m.startCurrent()
	}
	m.loadToken++
	m.active, m.loading = false, false
	m.player.Stop()
	m.refreshQueue()
	return nil
}

func (m *Model) startCurrent() tea.Cmd {
	m.loadToken++
	m.active, m.loading = true, true
	m.player.Stop()
	m.refreshQueue()

	track, _ := m.playlist.Current()
	token, source := m.loadToken, m.source
	return func() tea.Msg {
		path, err := source.Fetch(track)
		return fetchedMsg{token, path, err}
	}
}

func (m *Model) handleFetched(msg fetchedMsg) tea.Cmd {
	if msg.token != m.loadToken {
		return nil
	}
	if msg.err != nil {
		return tea.Batch(m.setNotice(msg.err.Error(), true), m.advance())
	}
	m.loading = false
	m.player.Load(msg.path)
	return m.prefetchCmd()
}

// prefetchCmd downloads the next track while the current one plays. Failures
// are ignored here and reported if the track is actually played.
func (m *Model) prefetchCmd() tea.Cmd {
	track, ok := m.playlist.Upcoming()
	if !ok {
		return nil
	}
	source := m.source
	return func() tea.Msg {
		source.Fetch(track)
		return nil
	}
}

func (m *Model) statusCmd() tea.Cmd {
	token, p := m.loadToken, m.player
	return func() tea.Msg {
		status, err := p.Status()
		return statusMsg{status, err, token}
	}
}

func (m *Model) handleStatus(msg statusMsg) tea.Cmd {
	if errors.Is(msg.err, player.ErrClosed) {
		m.fatal = errors.New("mpv stopped unexpectedly")
		return tea.Quit
	}
	if msg.err != nil {
		return nil
	}
	m.status = msg.status
	// If the token changed, the user already moved on while this was in flight,
	// and advancing again would skip a track.
	if msg.status.Finished && msg.token == m.loadToken {
		return m.advance()
	}
	return nil
}

func (m *Model) updateCmd() tea.Cmd {
	update := m.update
	return func() tea.Msg {
		version, err := update()
		if err != nil || version == "" {
			return nil // A failed check just waits for the next launch.
		}
		return updatedMsg{version}
	}
}

func (m *Model) setNotice(text string, isError bool) tea.Cmd {
	m.noticeSeq++
	m.notice, m.noticeIsError = text, isError
	return m.after(noticeDuration, noticeExpiredMsg{m.noticeSeq})
}
