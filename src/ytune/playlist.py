"""The play queue: an ordered list of tracks and a position in it."""

from __future__ import annotations

from ytune.youtube import Track


class Playlist:
    def __init__(self) -> None:
        self.tracks: list[Track] = []
        self.index = -1

    @property
    def current(self) -> Track | None:
        if 0 <= self.index < len(self.tracks):
            return self.tracks[self.index]
        return None

    @property
    def upcoming(self) -> Track | None:
        following = self.index + 1
        return self.tracks[following] if following < len(self.tracks) else None

    def add(self, *tracks: Track) -> None:
        self.tracks.extend(tracks)

    def play_now(self, track: Track) -> None:
        """Put ``track`` right after the current one and move to it."""
        self.index += 1
        self.tracks.insert(self.index, track)

    def jump(self, index: int) -> Track:
        self.index = index
        return self.tracks[index]

    def advance(self) -> Track | None:
        if self.upcoming is None:
            return None
        self.index += 1
        return self.tracks[self.index]

    def back(self) -> Track | None:
        if self.index <= 0:
            return None
        self.index -= 1
        return self.tracks[self.index]

    def remove(self, index: int) -> bool:
        """Remove a track. Returns True if it was the current one.

        After removing the current track the position sits just before its
        old slot, so ``advance`` moves on to whatever followed it.
        """
        was_current = index == self.index
        del self.tracks[index]
        if index <= self.index:
            self.index -= 1
        return was_current
