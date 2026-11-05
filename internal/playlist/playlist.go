// Package playlist holds the play queue: tracks and a position in them.
package playlist

import (
	"slices"

	"github.com/jithin-jz/ytune/internal/youtube"
)

// Playlist is an ordered list of tracks with a current position.
type Playlist struct {
	Tracks []youtube.Track
	Index  int // Position of the current track, or -1 before the first.
}

// New returns an empty playlist.
func New() *Playlist {
	return &Playlist{Index: -1}
}

// Current returns the track at the current position.
func (p *Playlist) Current() (youtube.Track, bool) {
	if p.Index >= 0 && p.Index < len(p.Tracks) {
		return p.Tracks[p.Index], true
	}
	return youtube.Track{}, false
}

// Upcoming returns the track after the current one.
func (p *Playlist) Upcoming() (youtube.Track, bool) {
	if next := p.Index + 1; next < len(p.Tracks) {
		return p.Tracks[next], true
	}
	return youtube.Track{}, false
}

// Add puts tracks at the end.
func (p *Playlist) Add(tracks ...youtube.Track) {
	p.Tracks = append(p.Tracks, tracks...)
}

// PlayNow puts track right after the current one and moves to it.
func (p *Playlist) PlayNow(track youtube.Track) {
	p.Index++
	p.Tracks = slices.Insert(p.Tracks, p.Index, track)
}

// Jump moves to the track at index.
func (p *Playlist) Jump(index int) {
	p.Index = index
}

// Advance moves to the next track, reporting whether there was one.
func (p *Playlist) Advance() bool {
	if _, ok := p.Upcoming(); !ok {
		return false
	}
	p.Index++
	return true
}

// Back moves to the previous track, reporting whether there was one.
func (p *Playlist) Back() bool {
	if p.Index <= 0 {
		return false
	}
	p.Index--
	return true
}

// Remove deletes the track at index and reports whether it was the current one.
// After removing the current track the position sits just before its old slot,
// so Advance moves on to whatever followed it.
func (p *Playlist) Remove(index int) bool {
	wasCurrent := index == p.Index
	p.Tracks = slices.Delete(p.Tracks, index, index+1)
	if index <= p.Index {
		p.Index--
	}
	return wasCurrent
}
