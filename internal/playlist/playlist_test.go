package playlist

import (
	"slices"
	"testing"

	"github.com/jithin-jz/ytune/internal/youtube"
)

var (
	a = youtube.Track{ID: "a"}
	b = youtube.Track{ID: "b"}
	c = youtube.Track{ID: "c"}
)

func filled(index int) *Playlist {
	p := New()
	p.Add(a, b, c)
	p.Index = index
	return p
}

func current(t *testing.T, p *Playlist) youtube.Track {
	t.Helper()
	track, ok := p.Current()
	if !ok {
		t.Fatal("expected a current track")
	}
	return track
}

func TestEmptyPlaylistHasNothingToPlay(t *testing.T) {
	p := New()
	if _, ok := p.Current(); ok {
		t.Error("Current should be empty")
	}
	if p.Advance() {
		t.Error("Advance should fail")
	}
	if p.Back() {
		t.Error("Back should fail")
	}
}

func TestAdvanceWalksForwardAndStopsAtTheEnd(t *testing.T) {
	p := filled(0)
	if !p.Advance() || current(t, p) != b {
		t.Fatal("expected b")
	}
	if !p.Advance() || current(t, p) != c {
		t.Fatal("expected c")
	}
	if p.Advance() {
		t.Fatal("expected to stop at the end")
	}
	if current(t, p) != c {
		t.Fatal("position should stay on the last track")
	}
}

func TestBackStopsAtTheFirstTrack(t *testing.T) {
	p := filled(1)
	if !p.Back() || current(t, p) != a {
		t.Fatal("expected a")
	}
	if p.Back() {
		t.Fatal("expected to stop at the start")
	}
}

func TestPlayNowKeepsTheRestOfTheQueue(t *testing.T) {
	p := filled(0)
	added := youtube.Track{ID: "new"}
	p.PlayNow(added)
	if current(t, p) != added {
		t.Fatal("expected the new track to be current")
	}
	if !slices.Equal(p.Tracks, []youtube.Track{a, added, b, c}) {
		t.Fatalf("wrong order: %v", p.Tracks)
	}
}

func TestPlayNowOnAnEmptyPlaylist(t *testing.T) {
	p := New()
	p.PlayNow(a)
	if current(t, p) != a {
		t.Fatal("expected a")
	}
}

func TestRemovingAnEarlierTrackKeepsTheCurrentOne(t *testing.T) {
	p := filled(2)
	if p.Remove(0) {
		t.Fatal("should not report the current track")
	}
	if current(t, p) != c {
		t.Fatal("expected c to stay current")
	}
}

func TestRemovingALaterTrackKeepsTheCurrentOne(t *testing.T) {
	p := filled(0)
	if p.Remove(2) {
		t.Fatal("should not report the current track")
	}
	if current(t, p) != a {
		t.Fatal("expected a to stay current")
	}
}

func TestRemovingTheCurrentTrackAdvancesToWhatFollowedIt(t *testing.T) {
	p := filled(1)
	if !p.Remove(1) {
		t.Fatal("should report the current track")
	}
	if !p.Advance() || current(t, p) != c {
		t.Fatal("expected c next")
	}
}

func TestRemovingTheOnlyTrackLeavesNothingToAdvanceTo(t *testing.T) {
	p := New()
	p.Add(a)
	p.Index = 0
	if !p.Remove(0) {
		t.Fatal("should report the current track")
	}
	if p.Advance() {
		t.Fatal("nothing should follow")
	}
}
