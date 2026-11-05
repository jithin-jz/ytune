"""YouTube access through yt-dlp: search, playlist expansion and audio fetching."""

from __future__ import annotations

import shutil
import threading
from dataclasses import dataclass
from pathlib import Path

from yt_dlp import YoutubeDL
from yt_dlp.utils import DownloadError

# yt-dlp only auto-detects deno. Without a JS runtime YouTube's audio URLs
# return HTTP 403, so name whichever supported runtime is installed.
JS_RUNTIMES = ("deno", "node", "bun")

# Leftovers from an interrupted download, never playable audio.
PARTIAL_SUFFIXES = {".part", ".ytdl", ".temp"}


class FetchError(RuntimeError):
    """A search or download failed; the message is safe to show the user."""


@dataclass(frozen=True)
class Track:
    id: str
    title: str
    channel: str = ""
    duration: int | None = None

    @property
    def url(self) -> str:
        return f"https://www.youtube.com/watch?v={self.id}"

    @classmethod
    def from_entry(cls, entry: dict) -> Track:
        duration = entry.get("duration")
        return cls(
            id=entry["id"],
            title=entry.get("title") or entry["id"],
            channel=entry.get("channel") or entry.get("uploader") or "",
            duration=int(duration) if duration else None,
        )


class _SilentLogger:
    """Keeps yt-dlp from printing over the full-screen interface."""

    def debug(self, msg: str) -> None:
        pass

    info = warning = error = debug


def find_js_runtime() -> str | None:
    return next((name for name in JS_RUNTIMES if shutil.which(name)), None)


def _clean_error(error: Exception) -> str:
    lines = str(error).strip().splitlines() or ["Unknown error"]
    return lines[0].removeprefix("ERROR: ")


class YouTube:
    """Finds tracks and downloads their audio into ``cache_dir``."""

    def __init__(self, cache_dir: Path) -> None:
        self.cache_dir = cache_dir
        self._locks: dict[str, threading.Lock] = {}
        self._locks_guard = threading.Lock()

    def _options(self, **extra) -> dict:
        options = {
            "logger": _SilentLogger(),
            "quiet": True,
            "no_warnings": True,
            "noprogress": True,
            "no_color": True,
            "noplaylist": True,
        }
        runtime = find_js_runtime()
        if runtime:
            options["js_runtimes"] = {runtime: {}}
        return options | extra

    def _info(self, target: str, **options) -> dict:
        try:
            with YoutubeDL(self._options(**options)) as ydl:
                return ydl.extract_info(target, download=False)
        except DownloadError as error:
            raise FetchError(_clean_error(error)) from error

    def search(self, query: str, limit: int = 20) -> list[Track]:
        info = self._info(f"ytsearch{limit}:{query}", extract_flat=True)
        return _tracks(info)

    def resolve(self, url: str) -> list[Track]:
        """Return the tracks behind a video or playlist link."""
        return _tracks(self._info(url, extract_flat="in_playlist"))

    def cached(self, track: Track) -> Path | None:
        for path in self.cache_dir.glob(f"{track.id}.*"):
            if path.suffix not in PARTIAL_SUFFIXES:
                return path
        return None

    def fetch(self, track: Track) -> Path:
        """Return a local audio file for ``track``, downloading it if needed."""
        # Playback and prefetch can ask for the same track at the same time.
        with self._lock_for(track.id):
            path = self.cached(track)
            if path:
                return path
            options = self._options(
                format="bestaudio/best",
                outtmpl=str(self.cache_dir / f"{track.id}.%(ext)s"),
            )
            try:
                with YoutubeDL(options) as ydl:
                    ydl.download([track.url])
            except DownloadError as error:
                raise FetchError(_clean_error(error)) from error
            path = self.cached(track)
            if not path:
                raise FetchError(f"No audio downloaded for {track.title}")
            return path

    def _lock_for(self, track_id: str) -> threading.Lock:
        with self._locks_guard:
            return self._locks.setdefault(track_id, threading.Lock())


def _tracks(info: dict | None) -> list[Track]:
    if not info:
        return []
    entries = info.get("entries")
    if entries is None:
        return [Track.from_entry(info)]
    # Deleted and private videos come back as empty placeholders.
    return [Track.from_entry(entry) for entry in entries if entry and entry.get("id")]
