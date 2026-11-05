"""The full-screen interface."""

from __future__ import annotations

import re
from pathlib import Path

from textual import on, work
from textual.app import App, ComposeResult
from textual.binding import Binding
from textual.containers import Horizontal
from textual.content import Content
from textual.widgets import Footer, Input, OptionList, Static
from textual.widgets.option_list import Option
from textual.worker import get_current_worker

from ytune.player import PlayerError, Status
from ytune.playlist import Playlist
from ytune.youtube import Track

TICK_SECONDS = 0.5
SEEK_SECONDS = 5
VOLUME_STEP = 5
BAR_RESERVED_COLUMNS = 34
MIN_BAR_WIDTH = 10


def is_url(text: str) -> bool:
    return re.match(r"https?://", text) is not None


def format_time(seconds: float | None) -> str:
    if seconds is None:
        return "--:--"
    minutes, secs = divmod(int(seconds), 60)
    hours, minutes = divmod(minutes, 60)
    return f"{hours}:{minutes:02}:{secs:02}" if hours else f"{minutes:02}:{secs:02}"


def progress_bar(position: float | None, duration: float | None, width: int) -> str:
    fraction = 0.0
    if position is not None and duration:
        fraction = min(max(position / duration, 0.0), 1.0)
    filled = round(fraction * (width - 1))
    return "━" * filled + "●" + "─" * (width - 1 - filled)


def track_line(track: Track) -> str:
    details = " · ".join(
        part for part in (track.channel, format_time(track.duration)) if part
    )
    return f"{track.title}  ({details})"


class YtuneApp(App[None]):
    TITLE = "ytune"
    ENABLE_COMMAND_PALETTE = False

    CSS = """
    #search { margin: 0 1; }
    #lists { height: 1fr; }
    #results { width: 3fr; }
    #queue { width: 2fr; }
    OptionList {
        height: 100%;
        border: round $surface-lighten-2;
        border-title-color: $text-muted;
        background: $background;
        text-wrap: nowrap;
        text-overflow: ellipsis;
    }
    OptionList:focus { border: round $accent; border-title-color: $text; }
    #now {
        height: 4;
        padding: 0 1;
        border: round $surface-lighten-2;
        border-title-color: $text-muted;
        text-wrap: nowrap;
        text-overflow: ellipsis;
    }
    """

    BINDINGS = [
        Binding("slash", "focus_search", "Search"),
        Binding("space", "toggle_pause", "Pause"),
        Binding("a", "enqueue", "Add to queue"),
        Binding("d", "remove", "Remove"),
        Binding("n", "next", "Next"),
        Binding("b", "previous", "Back"),
        Binding("left", f"seek(-{SEEK_SECONDS})", "Seek", show=False),
        Binding("right", f"seek({SEEK_SECONDS})", "Seek", show=False),
        Binding("minus", f"volume(-{VOLUME_STEP})", "Vol-"),
        Binding("plus,equals_sign", f"volume({VOLUME_STEP})", "Vol+"),
        Binding("escape", "focus_results", "Results", show=False),
        Binding("q", "quit", "Quit"),
    ]

    def __init__(self, player, source, startup: str = "") -> None:
        super().__init__()
        self.player = player
        self.source = source
        self.startup = startup
        self.playlist = Playlist()
        self.results: list[Track] = []
        self.active = False  # A track is playing or being fetched.
        self.loading = False
        self.load_token = 0  # Lets a finished fetch notice it was superseded.

    def compose(self) -> ComposeResult:
        yield Input(placeholder="Search YouTube, or paste a link", id="search")
        with Horizontal(id="lists"):
            yield OptionList(id="results")
            yield OptionList(id="queue")
        yield Static(id="now")
        yield Footer()

    def on_mount(self) -> None:
        self.query_one("#results").border_title = "Results"
        self.query_one("#queue").border_title = "Queue"
        self.query_one("#now").border_title = "Now playing"
        self.query_one("#search").focus()
        self.set_interval(TICK_SECONDS, self._tick)
        self._render_now(Status())
        if self.startup:
            self.query_one("#search", Input).value = self.startup
            self._submit(self.startup)

    # --- Searching ---

    @on(Input.Submitted, "#search")
    def _search_submitted(self, event: Input.Submitted) -> None:
        self._submit(event.value.strip())

    def _submit(self, text: str) -> None:
        if not text:
            return
        if is_url(text):
            self._open_link(text)
        else:
            self.query_one("#results").border_title = "Searching..."
            self._search(text)

    @work(thread=True, exclusive=True, group="search", exit_on_error=False)
    def _search(self, query: str) -> None:
        worker = get_current_worker()
        try:
            tracks = self.source.search(query)
        except Exception as error:
            self.call_from_thread(self._report, error)
            return
        if not worker.is_cancelled:
            self.call_from_thread(self._show_results, query, tracks)

    def _show_results(self, query: str, tracks: list[Track]) -> None:
        self.results = tracks
        results = self.query_one("#results", OptionList)
        results.border_title = Content(f"Results for {query}")
        results.clear_options()
        results.add_options(Option(Content(track_line(t))) for t in tracks)
        if tracks:
            results.highlighted = 0
            results.focus()
        else:
            self.notify("No results.", severity="warning")

    @work(thread=True, group="search", exit_on_error=False)
    def _open_link(self, url: str) -> None:
        try:
            tracks = self.source.resolve(url)
        except Exception as error:
            self.call_from_thread(self._report, error)
            return
        self.call_from_thread(self._enqueue_tracks, tracks)

    def _enqueue_tracks(self, tracks: list[Track]) -> None:
        if not tracks:
            self.notify("Nothing to play at that link.", severity="warning")
            return
        self.playlist.add(*tracks)
        self._queue_changed()
        self.notify(f"Added {len(tracks)} to the queue.", markup=False)

    def _report(self, error: Exception) -> None:
        self.query_one("#results").border_title = "Results"
        self.notify(str(error), severity="error", markup=False)

    # --- Picking tracks ---

    @on(OptionList.OptionSelected, "#results")
    def _result_chosen(self, event: OptionList.OptionSelected) -> None:
        self.playlist.play_now(self.results[event.option_index])
        self._start_current()

    @on(OptionList.OptionSelected, "#queue")
    def _queue_entry_chosen(self, event: OptionList.OptionSelected) -> None:
        self.playlist.jump(event.option_index)
        self._start_current()

    def action_enqueue(self) -> None:
        index = self.query_one("#results", OptionList).highlighted
        if index is None or self.focused is not self.query_one("#results"):
            return
        self.playlist.add(self.results[index])
        self._queue_changed()

    def action_remove(self) -> None:
        queue = self.query_one("#queue", OptionList)
        if self.focused is not queue or queue.highlighted is None:
            return
        if self.playlist.remove(queue.highlighted):
            self._advance()
        else:
            self._refresh_queue()

    def _queue_changed(self) -> None:
        self._refresh_queue()
        if not self.active:
            self._advance()
        elif not self.loading and self.playlist.upcoming:
            self._prefetch(self.playlist.upcoming)

    def _refresh_queue(self) -> None:
        queue = self.query_one("#queue", OptionList)
        highlighted = queue.highlighted
        queue.clear_options()
        queue.add_options(
            Option(Content(("▶ " if i == self.playlist.index else "  ") + track.title))
            for i, track in enumerate(self.playlist.tracks)
        )
        if self.playlist.tracks:
            last = len(self.playlist.tracks) - 1
            queue.highlighted = min(highlighted or 0, last)

    # --- Playback ---

    def _advance(self) -> None:
        if self.playlist.advance():
            self._start_current()
        else:
            self._stop()

    def _stop(self) -> None:
        self.load_token += 1
        self.active = self.loading = False
        self.player.stop()
        self._refresh_queue()

    def _start_current(self) -> None:
        self.load_token += 1
        self.active = self.loading = True
        self.player.stop()
        self._refresh_queue()
        self._load(self.playlist.current, self.load_token)

    @work(thread=True, group="load", exit_on_error=False)
    def _load(self, track: Track, token: int) -> None:
        try:
            path = self.source.fetch(track)
        except Exception as error:
            self.call_from_thread(self._load_failed, token, error)
            return
        self.call_from_thread(self._loaded, token, path)

    def _loaded(self, token: int, path: Path) -> None:
        if token != self.load_token:
            return
        self.loading = False
        self.player.load(path)
        if self.playlist.upcoming:
            self._prefetch(self.playlist.upcoming)

    def _load_failed(self, token: int, error: Exception) -> None:
        if token != self.load_token:
            return
        self.notify(str(error), severity="error", markup=False)
        self._advance()

    @work(thread=True, group="prefetch", exit_on_error=False)
    def _prefetch(self, track: Track) -> None:
        try:
            self.source.fetch(track)
        except Exception:
            pass  # Reported if and when the track is actually played.

    def _tick(self) -> None:
        try:
            if self.player.consume_finished():
                self._advance()
            status = self.player.status()
        except PlayerError as error:
            self.exit(message=str(error))
            return
        self._render_now(status)

    def _render_now(self, status: Status) -> None:
        now = self.query_one("#now", Static)
        track = self.playlist.current if self.active else None
        if track is None:
            now.update(Content("Nothing playing. Press / to search."))
            return
        if self.loading:
            now.update(Content(f"Loading {track.title}..."))
            return
        icon = "⏸" if status.paused else "▶"
        width = max(now.size.width - BAR_RESERVED_COLUMNS, MIN_BAR_WIDTH)
        bar = progress_bar(status.position, status.duration, width)
        times = f"{format_time(status.position)} / {format_time(status.duration)}"
        byline = f"  ·  {track.channel}" if track.channel else ""
        now.update(
            Content(f"{icon} {track.title}{byline}\n{bar}  {times}  vol {status.volume}")
        )

    # --- Keys ---

    def action_focus_search(self) -> None:
        self.query_one("#search").focus()

    def action_focus_results(self) -> None:
        self.query_one("#results").focus()

    def action_toggle_pause(self) -> None:
        self.player.toggle_pause()

    def action_seek(self, seconds: int) -> None:
        self.player.seek(seconds)

    def action_volume(self, delta: int) -> None:
        self.player.change_volume(delta)

    def action_next(self) -> None:
        if self.playlist.upcoming:
            self._advance()

    def action_previous(self) -> None:
        if self.playlist.back():
            self._start_current()
