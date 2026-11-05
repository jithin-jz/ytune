//go:build e2e

package ui

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/jithin-jz/ytune/internal/player"
	"github.com/jithin-jz/ytune/internal/setup"
	"github.com/jithin-jz/ytune/internal/youtube"
)

// TestEndToEnd drives the real interface against real YouTube and a real mpv,
// with sound turned off. It needs an internet connection, and it installs the
// helper programs if they're missing, so point YTUNE_HOME at a spare folder:
//
//	YTUNE_HOME=/some/folder go test -tags e2e -run TestEndToEnd -timeout 10m ./internal/ui/
func TestEndToEnd(t *testing.T) {
	ctx := context.Background()
	tools, err := setup.Ensure(ctx, strings.NewReader("y\n"), os.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	mpv, err := player.Start(tools.Mpv, "--ao=null")
	if err != nil {
		t.Fatal(err)
	}
	defer mpv.Close()

	cacheDir := t.TempDir()
	source := youtube.New(ctx, tools.YtDlp, tools.JSRuntime, cacheDir)
	m := New(mpv, source, "", nil)
	m.after = func(time.Duration, tea.Msg) tea.Cmd { return nil }
	h := &harness{t: t, m: m}
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})

	poll := func() player.Status {
		h.send(m.statusCmd()())
		return m.status
	}
	waitFor := func(what string, timeout time.Duration, check func(player.Status) bool) player.Status {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			if status := poll(); check(status) {
				return status
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s\nstatus: %+v\nnotice: %q", what, m.status, m.notice)
		return player.Status{}
	}
	playing := func(s player.Status) bool { return !m.loading && s.Position > 0.3 }
	typeInto := func(text string) {
		h.press("/")
		m.input.SetValue(text)
		h.press("enter")
	}

	// Search.
	started := time.Now()
	h.press("/")
	for _, r := range "imagine dragons believer" {
		if r == ' ' {
			h.press("space")
		} else {
			h.press(string(r))
		}
	}
	h.press("enter")
	t.Logf("search took %v", time.Since(started).Round(time.Millisecond))
	if len(m.tracks) < 5 {
		t.Fatalf("got %d results, notice %q", len(m.tracks), m.notice)
	}
	if m.focus != focusResults {
		t.Fatal("focus should move to the results")
	}

	// Playback controls.
	started = time.Now()
	h.press("enter")
	status := waitFor("the first track", 60*time.Second, playing)
	t.Logf("first track started after %v", time.Since(started).Round(time.Millisecond))
	if status.Duration < 30 {
		t.Fatalf("suspicious duration: %+v", status)
	}

	h.press("space")
	paused := waitFor("pause", 5*time.Second, func(s player.Status) bool { return s.Paused })
	time.Sleep(500 * time.Millisecond)
	if poll().Position-paused.Position > 0.1 {
		t.Fatal("position moved while paused")
	}
	h.press("space")
	waitFor("resume", 5*time.Second, func(s player.Status) bool { return !s.Paused })

	before := poll().Position
	h.press("right")
	waitFor("seek", 5*time.Second, func(s player.Status) bool { return s.Position >= before+4 })

	volume := poll().Volume
	h.press("-")
	waitFor("volume", 5*time.Second, func(s player.Status) bool { return s.Volume == volume-volumeStep })

	// The queue, downloading ahead, and rolling into the next track.
	h.press("down", "a")
	second, ok := m.playlist.Upcoming()
	if !ok {
		t.Fatal("nothing was queued")
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil || len(entries) < 2 {
		t.Fatalf("the next track should already be downloaded: %v %v", entries, err)
	}

	status = poll()
	mpv.Seek(status.Duration - status.Position - 1.5)
	waitFor("the next track to start by itself", 30*time.Second, func(s player.Status) bool {
		current, _ := m.playlist.Current()
		return current == second && playing(s)
	})

	h.press("b")
	waitFor("going back", 15*time.Second, func(s player.Status) bool { return m.playlist.Index == 0 && playing(s) })
	started = time.Now()
	h.press("n")
	waitFor("going forward", 15*time.Second, func(s player.Status) bool { return m.playlist.Index == 1 && playing(s) })
	t.Logf("a downloaded track started after %v", time.Since(started).Round(time.Millisecond))

	// A YouTube Music link that also names a playlist adds just that track.
	count := len(m.playlist.Tracks)
	id := m.tracks[2].ID
	typeInto(fmt.Sprintf("https://music.youtube.com/watch?v=%s&list=RDAMVM%s", id, id))
	if got := len(m.playlist.Tracks); got != count+1 {
		t.Fatalf("queue went from %d to %d, notice %q", count, got, m.notice)
	}

	// A real playlist link adds all of its tracks, and they play.
	found, err := source.Resolve("https://www.youtube.com/results?sp=EgIQAw%3D%3D&search_query=" + url.QueryEscape("imagine dragons playlist"))
	if err != nil || len(found) == 0 {
		t.Fatalf("couldn't find a playlist to test with: %v", err)
	}
	count = len(m.playlist.Tracks)
	typeInto("https://www.youtube.com/playlist?list=" + found[0].ID)
	added := len(m.playlist.Tracks) - count
	t.Logf("the playlist added %d tracks", added)
	if added < 2 {
		t.Fatalf("expected several tracks, got %d, notice %q", added, m.notice)
	}
	h.press("tab", "tab")
	m.queue.cursor = count
	h.press("enter")
	waitFor("a track from the playlist", 60*time.Second, playing)

	// A bad link is reported and leaves playback alone.
	count = len(m.playlist.Tracks)
	typeInto("https://www.youtube.com/watch?v=xxxxxxxxxxx")
	if !m.noticeIsError || len(m.playlist.Tracks) != count {
		t.Fatalf("notice %q, queue %d -> %d", m.notice, count, len(m.playlist.Tracks))
	}
	t.Logf("a bad link says: %s", m.notice)
	waitFor("playback to continue", 5*time.Second, playing)
}
