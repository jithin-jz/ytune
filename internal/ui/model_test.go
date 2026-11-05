package ui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/jithin-jz/ytune/internal/player"
	"github.com/jithin-jz/ytune/internal/youtube"
)

var testTracks = []youtube.Track{
	{ID: "a", Title: "Song [a]", Channel: "Artist", Duration: 100},
	{ID: "b", Title: "Song [b]", Channel: "Artist", Duration: 100},
	{ID: "c", Title: "Song [c]", Channel: "Artist", Duration: 100},
}

type fakePlayer struct {
	loaded   []string
	paused   bool
	seeks    []float64
	volume   int
	finished bool
	gone     bool
	flaky    bool
}

func (p *fakePlayer) Load(path string)       { p.loaded = append(p.loaded, path) }
func (p *fakePlayer) Stop()                  {}
func (p *fakePlayer) TogglePause()           { p.paused = !p.paused }
func (p *fakePlayer) Seek(seconds float64)   { p.seeks = append(p.seeks, seconds) }
func (p *fakePlayer) ChangeVolume(delta int) { p.volume += delta }
func (p *fakePlayer) Status() (player.Status, error) {
	if p.gone {
		return player.Status{}, player.ErrClosed
	}
	if p.flaky {
		return player.Status{}, errors.New("mpv did not answer")
	}
	finished := p.finished
	p.finished = false
	return player.Status{Position: 10, Duration: 100, Paused: p.paused, Volume: p.volume, Finished: finished}, nil
}

type fakeSource struct {
	tracks   []youtube.Track
	failing  map[string]bool
	searches []string
	fetched  []string
	err      error
}

func (s *fakeSource) Search(query string) ([]youtube.Track, error) {
	s.searches = append(s.searches, query)
	return s.tracks, s.err
}

func (s *fakeSource) Resolve(string) ([]youtube.Track, error) {
	return s.tracks[:2], s.err
}

func (s *fakeSource) Fetch(track youtube.Track) (string, error) {
	s.fetched = append(s.fetched, track.ID)
	if s.failing[track.ID] {
		return "", fmt.Errorf("%s is unavailable", track.ID)
	}
	return track.ID + ".webm", nil
}

type harness struct {
	t      *testing.T
	m      *Model
	player *fakePlayer
	source *fakeSource
	quit   bool
}

func newHarness(t *testing.T, startup string) *harness {
	t.Helper()
	p := &fakePlayer{volume: 100}
	source := &fakeSource{tracks: testTracks, failing: map[string]bool{}}
	m := New(p, source, startup, nil)
	m.after = func(time.Duration, tea.Msg) tea.Cmd { return nil }
	h := &harness{t: t, m: m, player: p, source: source}
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	return h
}

// run performs a command the way Bubble Tea would, feeding results back in.
func (h *harness) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, inner := range msg {
			h.run(inner)
		}
	case tea.QuitMsg:
		h.quit = true
	default:
		h.send(msg)
	}
}

func (h *harness) send(msg tea.Msg) {
	_, cmd := h.m.Update(msg)
	h.run(cmd)
}

func keyMsg(name string) tea.KeyPressMsg {
	special := map[string]rune{
		"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab, "up": tea.KeyUp,
		"down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight, "home": tea.KeyHome,
		"end": tea.KeyEnd, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
	}
	if code, ok := special[name]; ok {
		return tea.KeyPressMsg{Code: code}
	}
	if name == "space" {
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	}
	if name == "ctrl+c" {
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	if name == "shift+tab" {
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	}
	return tea.KeyPressMsg{Code: []rune(name)[0], Text: name}
}

func (h *harness) press(keys ...string) {
	for _, key := range keys {
		h.send(keyMsg(key))
	}
}

func (h *harness) search() {
	h.t.Helper()
	h.press("s", "o", "n", "g", "enter")
}

func (h *harness) finishTrack() {
	h.player.finished = true
	h.send(h.m.statusCmd()())
}

func (h *harness) wantLoaded(want ...string) {
	h.t.Helper()
	if !slices.Equal(h.player.loaded, want) {
		h.t.Fatalf("loaded %v, want %v", h.player.loaded, want)
	}
}

func queueIDs(m *Model) []string {
	var ids []string
	for _, track := range m.playlist.Tracks {
		ids = append(ids, track.ID)
	}
	return ids
}

func TestSearchFillsResultsAndMovesFocusToThem(t *testing.T) {
	h := newHarness(t, "")
	h.search()
	if len(h.m.results.rows) != 3 {
		t.Fatalf("got %d rows", len(h.m.results.rows))
	}
	if h.m.focus != focusResults {
		t.Fatal("focus should move to the results")
	}
	if h.source.searches[0] != "song" {
		t.Fatalf("searched for %q", h.source.searches[0])
	}
}

func TestEnterPlaysTheHighlightedResult(t *testing.T) {
	h := newHarness(t, "")
	h.search()
	h.press("down", "enter")
	h.wantLoaded("b.webm")
}

func TestAddingToAnIdlePlayerStartsPlaying(t *testing.T) {
	h := newHarness(t, "")
	h.search()
	h.press("a")
	h.wantLoaded("a.webm")
}

func TestQueuePlaysOnWhenATrackFinishes(t *testing.T) {
	h := newHarness(t, "")
	h.search()
	h.press("a", "down", "a")
	h.wantLoaded("a.webm")
	h.finishTrack()
	h.wantLoaded("a.webm", "b.webm")
}

func TestTheNextTrackDownloadsAhead(t *testing.T) {
	h := newHarness(t, "")
	h.search()
	h.press("a", "down", "a")
	if !slices.Equal(h.source.fetched, []string{"a", "b"}) {
		t.Fatalf("fetched %v", h.source.fetched)
	}
}

func TestLastTrackFinishingReturnsToIdle(t *testing.T) {
	h := newHarness(t, "")
	h.search()
	h.press("a")
	h.finishTrack()
	if h.m.active {
		t.Fatal("should be idle")
	}
	if !strings.Contains(h.m.render(), "Nothing playing") {
		t.Fatal("the screen should say nothing is playing")
	}
}

func TestAddingAfterTheQueueEndedPlaysTheNewTrack(t *testing.T) {
	h := newHarness(t, "")
	h.search()
	h.press("a")
	h.finishTrack()
	h.press("down", "a")
	h.wantLoaded("a.webm", "b.webm")
}

func TestNextAndBackMoveThroughTheQueue(t *testing.T) {
	h := newHarness(t, "")
	h.search()
	h.press("a", "down", "a", "n", "b")
	h.wantLoaded("a.webm", "b.webm", "a.webm")
}

func TestNextAtTheEndOfTheQueueKeepsPlaying(t *testing.T) {
	h := newHarness(t, "")
	h.search()
	h.press("a", "n")
	h.wantLoaded("a.webm")
	if !h.m.active {
		t.Fatal("playback should continue")
	}
}

func TestATrackThatFailsIsSkipped(t *testing.T) {
	h := newHarness(t, "")
	h.source.failing["a"] = true
	h.search()
	h.press("a", "down", "a")
	h.wantLoaded("b.webm")
	if !h.m.noticeIsError || !strings.Contains(h.m.notice, "a is unavailable") {
		t.Fatalf("notice: %q", h.m.notice)
	}
}

func TestASlowFetchCannotReplaceANewerChoice(t *testing.T) {
	h := newHarness(t, "")
	h.search()
	_, slow := h.m.Update(keyMsg("enter")) // "a" is still downloading...
	h.press("down", "enter")               // ...when "b" is chosen.
	h.wantLoaded("b.webm")
	h.run(slow)
	h.wantLoaded("b.webm")
}

func TestAFinishEventFromBeforeSkippingDoesNotSkipTwice(t *testing.T) {
	h := newHarness(t, "")
	h.search()
	h.press("a", "down", "a", "down", "a")
	stale := statusMsg{player.Status{Finished: true}, nil, h.m.loadToken}
	h.press("n")
	h.send(stale)
	h.wantLoaded("a.webm", "b.webm")
}

func TestRemovingTheCurrentTrackMovesToTheNext(t *testing.T) {
	h := newHarness(t, "")
	h.search()
	h.press("a", "down", "a", "tab", "d")
	h.wantLoaded("a.webm", "b.webm")
	if ids := queueIDs(h.m); !slices.Equal(ids, []string{"b"}) {
		t.Fatalf("queue: %v", ids)
	}
}

func TestRemovingAnotherTrackLeavesPlaybackAlone(t *testing.T) {
	h := newHarness(t, "")
	h.search()
	h.press("a", "down", "a", "tab", "down", "d")
	h.wantLoaded("a.webm")
	if ids := queueIDs(h.m); !slices.Equal(ids, []string{"a"}) {
		t.Fatalf("queue: %v", ids)
	}
}

func TestRemovingTheLastTrackGoesIdle(t *testing.T) {
	h := newHarness(t, "")
	h.search()
	h.press("a", "tab", "d")
	if h.m.active || len(h.m.playlist.Tracks) != 0 {
		t.Fatal("should be empty and idle")
	}
	h.press("d", "enter") // Must not panic on an empty queue.
}

func TestAddAndRemoveOnlyWorkInTheirOwnList(t *testing.T) {
	h := newHarness(t, "")
	h.search()
	h.press("d")
	h.press("tab", "a")
	if len(h.m.playlist.Tracks) != 0 {
		t.Fatalf("queue: %v", queueIDs(h.m))
	}
}

func TestEnterInTheQueueJumpsToThatTrack(t *testing.T) {
	h := newHarness(t, "")
	h.search()
	h.press("a", "down", "a", "down", "a", "tab", "down", "down", "enter")
	h.wantLoaded("a.webm", "c.webm")
}

func TestPlaybackKeysReachThePlayer(t *testing.T) {
	h := newHarness(t, "")
	h.search()
	h.press("space", "right", "left", "+", "=", "-", "-", "-")
	if !h.player.paused {
		t.Error("should be paused")
	}
	if !slices.Equal(h.player.seeks, []float64{5, -5}) {
		t.Errorf("seeks: %v", h.player.seeks)
	}
	if h.player.volume != 95 {
		t.Errorf("volume: %d", h.player.volume)
	}
}

func TestTypingInTheSearchBoxDoesNotTriggerShortcuts(t *testing.T) {
	h := newHarness(t, "")
	h.press("n", "space", "q")
	if got := h.m.input.Value(); got != "n q" {
		t.Fatalf("typed %q", got)
	}
	if h.player.paused || h.quit {
		t.Fatal("shortcuts fired")
	}
}

func TestQuitKeys(t *testing.T) {
	h := newHarness(t, "")
	h.press("esc", "q")
	if !h.quit {
		t.Fatal("q should quit outside the search box")
	}
	h = newHarness(t, "")
	h.press("ctrl+c")
	if !h.quit {
		t.Fatal("ctrl+c should always quit")
	}
}

func TestFocusKeys(t *testing.T) {
	h := newHarness(t, "")
	h.press("tab")
	if h.m.focus != focusResults {
		t.Fatal("tab should go to the results")
	}
	h.press("tab")
	if h.m.focus != focusQueue {
		t.Fatal("tab should go to the queue")
	}
	h.press("tab")
	if h.m.focus != focusSearch {
		t.Fatal("tab should wrap to the search box")
	}
	h.press("shift+tab")
	if h.m.focus != focusQueue {
		t.Fatal("shift+tab should go back")
	}
	h.press("/")
	if h.m.focus != focusSearch || !h.m.input.Focused() {
		t.Fatal("/ should go to the search box")
	}
	if h.m.input.Value() != "" {
		t.Fatal("/ should not be typed")
	}
	h.press("esc")
	if h.m.focus != focusResults || h.m.input.Focused() {
		t.Fatal("esc should leave the search box")
	}
}

func TestEmptySearchDoesNothing(t *testing.T) {
	h := newHarness(t, "")
	h.press("space", "enter")
	if len(h.source.searches) != 0 {
		t.Fatalf("searched: %v", h.source.searches)
	}
}

func TestAnOlderSearchCannotOverwriteANewerOne(t *testing.T) {
	h := newHarness(t, "")
	h.press("x")
	_, slow := h.m.Update(keyMsg("enter"))
	h.press("y", "enter")
	h.source.tracks = testTracks[:1]
	h.run(slow)
	if got := h.m.resultsTitle; got != "Results for xy" {
		t.Fatalf("title: %q", got)
	}
}

func TestNoResultsSaysSoAndKeepsFocus(t *testing.T) {
	h := newHarness(t, "")
	h.source.tracks = nil
	h.search()
	if h.m.notice != "No results." {
		t.Fatalf("notice: %q", h.m.notice)
	}
	if h.m.focus != focusSearch {
		t.Fatal("focus should stay in the search box")
	}
}

func TestSearchErrorsAreShown(t *testing.T) {
	h := newHarness(t, "")
	h.source.err = errors.New("no internet")
	h.search()
	if !h.m.noticeIsError || h.m.notice != "no internet" {
		t.Fatalf("notice: %q", h.m.notice)
	}
	if h.m.resultsTitle != "Results" {
		t.Fatalf("title: %q", h.m.resultsTitle)
	}
}

func TestNoticesClearThemselves(t *testing.T) {
	h := newHarness(t, "")
	h.source.tracks = nil
	h.search()
	first := h.m.noticeSeq
	h.search()
	h.send(noticeExpiredMsg{first})
	if h.m.notice == "" {
		t.Fatal("an old timer should not clear a newer notice")
	}
	h.send(noticeExpiredMsg{h.m.noticeSeq})
	if h.m.notice != "" {
		t.Fatal("the notice should be gone")
	}
}

func TestALinkGoesStraightToTheQueue(t *testing.T) {
	h := newHarness(t, "https://youtu.be/a")
	h.run(h.m.Init())
	if ids := queueIDs(h.m); !slices.Equal(ids, []string{"a", "b"}) {
		t.Fatalf("queue: %v", ids)
	}
	h.wantLoaded("a.webm")
	if !strings.Contains(h.m.notice, "Added 2") {
		t.Fatalf("notice: %q", h.m.notice)
	}
}

func TestStartupWordsRunASearch(t *testing.T) {
	h := newHarness(t, "tum hi ho")
	h.run(h.m.Init())
	if !slices.Equal(h.source.searches, []string{"tum hi ho"}) {
		t.Fatalf("searches: %v", h.source.searches)
	}
	if h.m.focus != focusResults {
		t.Fatal("focus should be on the results")
	}
}

func TestTheAppExitsIfMpvDies(t *testing.T) {
	h := newHarness(t, "")
	h.player.gone = true
	h.send(h.m.statusCmd()())
	if !h.quit || h.m.Err() == nil {
		t.Fatalf("quit=%v err=%v", h.quit, h.m.Err())
	}
}

func TestASlowReplyFromMpvIsNotFatal(t *testing.T) {
	h := newHarness(t, "")
	h.player.flaky = true
	h.send(h.m.statusCmd()())
	if h.quit || h.m.Err() != nil {
		t.Fatal("a missed reply should be ignored")
	}
}

func TestAnUpdateIsAnnounced(t *testing.T) {
	h := newHarness(t, "")
	h.m.update = func() (string, error) { return "2026.11.03", nil }
	h.run(h.m.Init())
	if !strings.Contains(h.m.notice, "2026.11.03") {
		t.Fatalf("notice: %q", h.m.notice)
	}
}

func TestAFailedUpdateIsQuiet(t *testing.T) {
	h := newHarness(t, "")
	h.m.update = func() (string, error) { return "", errors.New("offline") }
	h.run(h.m.Init())
	if h.m.notice != "" {
		t.Fatalf("notice: %q", h.m.notice)
	}
}

func TestListScrollingKeepsTheCursorOnScreen(t *testing.T) {
	var l list
	rows := make([]string, 50)
	for i := range rows {
		rows[i] = fmt.Sprint("row ", i)
	}
	const height = 10
	l.setRows(rows, height)

	for range 15 {
		l.navigate("down", height)
	}
	if l.cursor != 15 || l.offset != 6 {
		t.Fatalf("cursor=%d offset=%d", l.cursor, l.offset)
	}
	l.navigate("end", height)
	if l.cursor != 49 || l.offset != 40 {
		t.Fatalf("cursor=%d offset=%d", l.cursor, l.offset)
	}
	l.navigate("pgdown", height)
	if l.cursor != 49 {
		t.Fatalf("cursor=%d", l.cursor)
	}
	l.navigate("pgup", height)
	if l.cursor != 39 || l.offset != 39 {
		t.Fatalf("cursor=%d offset=%d", l.cursor, l.offset)
	}
	l.navigate("home", height)
	if l.cursor != 0 || l.offset != 0 {
		t.Fatalf("cursor=%d offset=%d", l.cursor, l.offset)
	}
	if l.navigate("x", height) {
		t.Fatal("x is not a movement key")
	}
}

func TestListSurvivesShrinkingAndEmptying(t *testing.T) {
	var l list
	l.setRows([]string{"a", "b", "c", "d", "e"}, 3)
	l.navigate("end", 3)
	l.setRows([]string{"a"}, 3)
	if l.cursor != 0 || l.offset != 0 {
		t.Fatalf("cursor=%d offset=%d", l.cursor, l.offset)
	}
	l.setRows(nil, 3)
	if l.cursor != 0 || l.offset != 0 {
		t.Fatalf("cursor=%d offset=%d", l.cursor, l.offset)
	}
	l.navigate("down", 3)
	if lines := l.render(10, 3, true); len(lines) != 0 {
		t.Fatalf("got %v", lines)
	}
}

func TestThePlayingTrackIsMarkedInTheQueue(t *testing.T) {
	h := newHarness(t, "")
	h.search()
	h.press("a", "down", "a", "n")
	if !strings.HasPrefix(h.m.queue.rows[1], "▶ ") || strings.HasPrefix(h.m.queue.rows[0], "▶ ") {
		t.Fatalf("rows: %v", h.m.queue.rows)
	}
}

// wantExactSize checks the screen fills the terminal without spilling over. The
// lines are measured the way Bubble Tea's renderer measures them, which is what
// Filter keeps it doing.
func wantExactSize(t *testing.T, m *Model, width, height int) {
	t.Helper()
	lines := strings.Split(m.render(), "\n")
	if len(lines) != height {
		t.Fatalf("%dx%d: rendered %d lines", width, height, len(lines))
	}
	for i, line := range lines {
		if got := ansi.StringWidthWc(line); got != width {
			t.Fatalf("%dx%d: line %d is %d wide: %q", width, height, i, got, ansi.Strip(line))
		}
	}
}

func TestTheScreenFitsExactlyAtEverySize(t *testing.T) {
	long := strings.Repeat("A very long song title ", 12)
	hindi := "आशिकी 2 का गाना तुम ही हो | मिथुन का संगीत | आदित्य रॉय कपूर, श्रद्धा कपूर"
	malayalam := "മലരേ നിന്നെ കാണാതിരുന്നാൽ | പ്രേമം"
	wide := "日本語のとても長いタイトルの曲です 🎵🎵🎵"
	h := newHarness(t, "")
	h.source.tracks = []youtube.Track{
		{ID: "a", Title: long, Channel: long, Duration: 7200},
		{ID: "b", Title: hindi, Channel: "T-Series", Duration: 141},
		{ID: "c", Title: malayalam, Channel: "ചാനൽ", Duration: 300},
		{ID: "d", Title: wide, Channel: "チャンネル"},
		{ID: "e", Title: "", Channel: ""},
	}
	h.search()
	h.press("a", "down", "a", "down", "a", "down", "a", "down", "a")
	h.send(h.m.statusCmd()())

	for _, playing := range []int{0, 1, 2, 3, 4} {
		h.m.playlist.Index = playing
		for width := minWidth; width <= 200; width += 7 {
			for _, height := range []int{minHeight, 13, 24, 30, 60} {
				h.send(tea.WindowSizeMsg{Width: width, Height: height})
				wantExactSize(t, h.m, width, height)
			}
		}
	}
}

func TestTruncateKeepsIndicTextInsideTheWidth(t *testing.T) {
	malayalam := "മലരേ നിന്നെ കാണാതിരുന്നാൽ | പ്രേമം"
	for width := 1; width < 40; width++ {
		if got := ansi.StringWidthWc(truncate(malayalam, width)); got > width {
			t.Errorf("width %d: came out %d wide", width, got)
		}
	}
	if got := truncate("short", 10); got != "short" {
		t.Errorf("a line that fits should be left alone: %q", got)
	}
	if got := truncate("too long", 0); got != "" {
		t.Errorf("no room should mean no text: %q", got)
	}
}

func TestTheFilterKeepsBubbleTeaOffGraphemeMeasuring(t *testing.T) {
	for _, value := range []ansi.ModeSetting{ansi.ModeReset, ansi.ModeSet, ansi.ModePermanentlySet} {
		if got := Filter(nil, tea.ModeReportMsg{Mode: ansi.ModeUnicodeCore, Value: value}); got != nil {
			t.Errorf("value %v got through: %v", value, got)
		}
	}
	other := tea.ModeReportMsg{Mode: ansi.ModeSynchronizedOutput, Value: ansi.ModeSet}
	if Filter(nil, other) == nil {
		t.Error("other mode reports must still reach Bubble Tea")
	}
	if Filter(nil, keyMsg("a")) == nil {
		t.Error("key presses must still get through")
	}
}

// Terminals disagree by a few columns on how wide Indic text is, so anything
// drawn after a title would end up out of line on some of them.
func TestNothingIsDrawnToTheRightOfATitle(t *testing.T) {
	h := newHarness(t, "")
	h.search()
	h.press("a", "down", "a")
	h.send(h.m.statusCmd()())
	for i, line := range strings.Split(ansi.Strip(h.m.render()), "\n") {
		if strings.ContainsAny(line, "│╮╯") {
			t.Errorf("line %d has a border: %q", i, line)
		}
		if strings.HasPrefix(strings.TrimSpace(line), "──") {
			continue // A heading fills its whole row by design.
		}
		if strings.Contains(line, "Song [a]") && strings.Contains(line, "Song [b]") {
			t.Errorf("line %d has two titles side by side: %q", i, line)
		}
	}
}

func TestTheListsAreStackedWithResultsOnTop(t *testing.T) {
	h := newHarness(t, "")
	screen := ansi.Strip(h.m.render())
	search := strings.Index(screen, "── Search")
	results := strings.Index(screen, "── Results")
	queue := strings.Index(screen, "── Queue")
	now := strings.Index(screen, "── Now playing")
	if !(0 <= search && search < results && results < queue && queue < now) {
		t.Fatalf("wrong order:\n%s", screen)
	}
	for _, height := range []int{minHeight, 24, 30, 60} {
		h.send(tea.WindowSizeMsg{Width: 100, Height: height})
		if h.m.resultsHeight() < 1 || h.m.queueHeight() < 1 {
			t.Errorf("height %d: results %d, queue %d", height, h.m.resultsHeight(), h.m.queueHeight())
		}
		if h.m.resultsHeight() < h.m.queueHeight() {
			t.Errorf("height %d: the results should get more room", height)
		}
	}
}

func TestEachListScrollsByItsOwnHeight(t *testing.T) {
	h := newHarness(t, "")
	var many []youtube.Track
	for i := range 60 {
		many = append(many, youtube.Track{ID: fmt.Sprint(i), Title: fmt.Sprint("Song ", i)})
	}
	h.source.tracks = many
	h.search()
	for range 60 {
		h.press("a", "down")
	}
	h.press("tab", "end")
	if screen := ansi.Strip(h.m.render()); !strings.Contains(screen, "  Song 59") {
		t.Fatalf("the last queue row is off screen:\n%s", screen)
	}
	wantExactSize(t, h.m, 100, 30)
}

func TestALongTitleCannotPushTheProgressBarOutOfView(t *testing.T) {
	h := newHarness(t, "")
	h.source.tracks = []youtube.Track{{ID: "a", Title: strings.Repeat("A very long song title ", 12), Channel: "Artist"}}
	h.search()
	h.press("enter")
	h.send(h.m.statusCmd()())
	for _, width := range []int{minWidth, 50, 60, 100} {
		h.send(tea.WindowSizeMsg{Width: width, Height: 24})
		screen := ansi.Strip(h.m.render())
		if !strings.Contains(screen, "vol 100") {
			t.Fatalf("width %d:\n%s", width, screen)
		}
		if !strings.Contains(screen, "●") {
			t.Fatalf("width %d has no progress bar:\n%s", width, screen)
		}
	}
}

func TestAnHourLongTrackStillFitsTheNarrowestWindow(t *testing.T) {
	h := newHarness(t, "")
	h.search()
	h.press("enter")
	h.m.status.Position, h.m.status.Duration, h.m.status.Volume = 7199, 36000, 130
	h.send(tea.WindowSizeMsg{Width: minWidth, Height: 24})
	screen := ansi.Strip(h.m.render())
	if !strings.Contains(screen, "1:59:59 / 10:00:00  vol 130") || !strings.Contains(screen, "●") {
		t.Fatalf("the progress line was cut:\n%s", screen)
	}
}

func TestLongListsStayInsideTheirBox(t *testing.T) {
	h := newHarness(t, "")
	var many []youtube.Track
	for i := range 200 {
		many = append(many, youtube.Track{ID: fmt.Sprint(i), Title: fmt.Sprint("Song ", i)})
	}
	h.source.tracks = many
	h.search()
	h.press("end")
	wantExactSize(t, h.m, 100, 30)
	if screen := ansi.Strip(h.m.render()); !strings.Contains(screen, "Song 199") {
		t.Fatalf("the cursor row is off screen:\n%s", screen)
	}
	h.send(tea.WindowSizeMsg{Width: 100, Height: 14})
	if screen := ansi.Strip(h.m.render()); !strings.Contains(screen, "Song 199") {
		t.Fatalf("shrinking lost the cursor row:\n%s", screen)
	}
}

func TestTheScreenShowsWhatIsHappening(t *testing.T) {
	h := newHarness(t, "")
	if !strings.Contains(ansi.Strip(h.m.render()), "Nothing playing") {
		t.Fatal("should start idle")
	}
	h.search()
	_, slow := h.m.Update(keyMsg("enter"))
	if !strings.Contains(ansi.Strip(h.m.render()), "Loading Song [a]...") {
		t.Fatal("should say it's loading")
	}
	h.run(slow)
	h.send(h.m.statusCmd()())
	screen := ansi.Strip(h.m.render())
	for _, want := range []string{"▶ Song [a]", "Artist", "00:10 / 01:40", "vol 100"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("missing %q:\n%s", want, screen)
		}
	}
	h.press("space")
	h.send(h.m.statusCmd()())
	if !strings.Contains(ansi.Strip(h.m.render()), "|| Song [a]") {
		t.Fatal("should show the paused icon")
	}
}

func TestTheHelpLineFitsAStandardWindow(t *testing.T) {
	h := newHarness(t, "")
	h.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	lines := strings.Split(ansi.Strip(h.m.render()), "\n")
	help := lines[len(lines)-1]
	if strings.Contains(help, "…") || !strings.Contains(help, "q quit") {
		t.Fatalf("the help line was cut: %q", help)
	}
}

func TestATinyWindowAsksForMoreRoom(t *testing.T) {
	h := newHarness(t, "")
	h.send(tea.WindowSizeMsg{Width: 10, Height: 5})
	if !strings.Contains(h.m.render(), "too small") {
		t.Fatal("should say the window is too small")
	}
	if h.m.View().Cursor != nil {
		t.Fatal("no cursor should be placed")
	}
}

func TestTheCursorSitsInTheSearchBoxOnlyWhileItHasFocus(t *testing.T) {
	h := newHarness(t, "")
	h.press("h", "i")
	cursor := h.m.View().Cursor
	if cursor == nil {
		t.Fatal("expected a cursor")
	}
	prompt := ansi.StringWidth(h.m.input.Prompt)
	if cursor.Position.X != sidePadding+prompt+2 || cursor.Position.Y != 1 {
		t.Fatalf("cursor at %+v", cursor.Position)
	}
	h.press("esc")
	if h.m.View().Cursor != nil {
		t.Fatal("the cursor should be hidden")
	}
}

func TestFormatTime(t *testing.T) {
	for seconds, want := range map[float64]string{-1: "--:--", 0: "00:00", 65: "01:05", 3661: "1:01:01"} {
		if got := formatTime(seconds); got != want {
			t.Errorf("%v: got %s", seconds, got)
		}
	}
}

func TestProgressBarKeepsItsWidthAtEveryPosition(t *testing.T) {
	for _, position := range []float64{-1, 0, 50, 100, 500} {
		if got := ansi.StringWidth(progressBar(position, 100, 20)); got != 20 {
			t.Errorf("%v: width %d", position, got)
		}
	}
	if got := progressBar(0, 100, 5); got != "●────" {
		t.Errorf("got %s", got)
	}
	if got := progressBar(100, 100, 5); got != "━━━━●" {
		t.Errorf("got %s", got)
	}
	if got := progressBar(50, -1, 5); got != "●────" {
		t.Errorf("unknown duration: got %s", got)
	}
}

func TestIsURL(t *testing.T) {
	if !isURL("https://music.youtube.com/watch?v=x") || !isURL("http://youtu.be/x") {
		t.Error("should be URLs")
	}
	if isURL("tum hi ho") || isURL("httpsomething") {
		t.Error("should not be URLs")
	}
}
