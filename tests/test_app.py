"""Drives the real interface against fake mpv and YouTube backends."""

import asyncio
import threading
from pathlib import Path

from textual.widgets import OptionList

from ytune.app import YtuneApp, format_time, is_url, progress_bar
from ytune.player import PlayerError, Status
from ytune.youtube import FetchError, Track

TRACKS = [Track(c, f"Song [{c}]", "Artist", 100) for c in "abc"]


class FakePlayer:
    def __init__(self):
        self.loaded: list[str] = []
        self.finished = False
        self.paused = False
        self.seeks: list[int] = []
        self.volume = 100
        self.gone = False

    def load(self, path):
        self.loaded.append(Path(path).name)

    def stop(self):
        pass

    def toggle_pause(self):
        self.paused = not self.paused

    def seek(self, seconds):
        self.seeks.append(seconds)

    def change_volume(self, delta):
        self.volume += delta

    def status(self):
        return Status(10.0, 100.0, self.paused, self.volume)

    def consume_finished(self):
        if self.gone:
            raise PlayerError("mpv went away")
        finished, self.finished = self.finished, False
        return finished


class FakeSource:
    def __init__(self, failing=(), block=None):
        self.failing = set(failing)
        self.block = block or {}

    def search(self, query):
        return TRACKS

    def resolve(self, url):
        return TRACKS[:2]

    def fetch(self, track):
        if track.id in self.block:
            self.block[track.id].wait(5)
        if track.id in self.failing:
            raise FetchError(f"{track.id} is unavailable")
        return Path(f"{track.id}.webm")


def run(scenario, source=None, startup=""):
    player = FakePlayer()
    app = YtuneApp(player, source or FakeSource(), startup)

    async def main():
        async with app.run_test(size=(100, 30)) as pilot:
            await scenario(app, player, pilot)

    asyncio.run(main())


async def settle(app, pilot):
    await app.workers.wait_for_complete()
    await pilot.pause()


async def search(app, pilot):
    await pilot.click("#search")
    await pilot.press(*"song", "enter")
    await settle(app, pilot)


def test_search_fills_results_and_moves_focus_to_them():
    async def scenario(app, player, pilot):
        await search(app, pilot)
        results = app.query_one("#results", OptionList)
        assert results.option_count == 3
        assert app.focused is results

    run(scenario)


def test_enter_plays_the_highlighted_result():
    async def scenario(app, player, pilot):
        await search(app, pilot)
        await pilot.press("down", "enter")
        await settle(app, pilot)
        assert player.loaded == ["b.webm"]

    run(scenario)


def test_adding_to_an_idle_player_starts_playing():
    async def scenario(app, player, pilot):
        await search(app, pilot)
        await pilot.press("a")
        await settle(app, pilot)
        assert player.loaded == ["a.webm"]

    run(scenario)


def test_queue_plays_on_when_a_track_finishes():
    async def scenario(app, player, pilot):
        await search(app, pilot)
        await pilot.press("a", "down", "a")
        await settle(app, pilot)
        assert player.loaded == ["a.webm"]
        player.finished = True
        app._tick()
        await settle(app, pilot)
        assert player.loaded == ["a.webm", "b.webm"]

    run(scenario)


def test_last_track_finishing_returns_to_idle():
    async def scenario(app, player, pilot):
        await search(app, pilot)
        await pilot.press("a")
        await settle(app, pilot)
        player.finished = True
        app._tick()
        await settle(app, pilot)
        assert app.active is False

    run(scenario)


def test_next_and_back_move_through_the_queue():
    async def scenario(app, player, pilot):
        await search(app, pilot)
        await pilot.press("a", "down", "a", "n")
        await settle(app, pilot)
        await pilot.press("b")
        await settle(app, pilot)
        assert player.loaded == ["a.webm", "b.webm", "a.webm"]

    run(scenario)


def test_track_that_fails_is_skipped():
    async def scenario(app, player, pilot):
        await search(app, pilot)
        await pilot.press("a", "down", "a")
        await settle(app, pilot)
        assert player.loaded == ["b.webm"]

    run(scenario, FakeSource(failing={"a"}))


def test_slow_fetch_cannot_replace_a_newer_choice():
    release = threading.Event()

    async def scenario(app, player, pilot):
        await search(app, pilot)
        await pilot.press("enter")  # "a" hangs in fetch.
        await pilot.press("down", "enter")  # "b" is chosen meanwhile.
        await pilot.pause(0.2)
        release.set()
        await settle(app, pilot)
        assert player.loaded == ["b.webm"]

    run(scenario, FakeSource(block={"a": release}))


def test_removing_the_current_track_moves_to_the_next():
    async def scenario(app, player, pilot):
        await search(app, pilot)
        await pilot.press("a", "down", "a")
        await settle(app, pilot)
        app.query_one("#queue").focus()
        await pilot.pause()
        await pilot.press("d")
        await settle(app, pilot)
        assert player.loaded == ["a.webm", "b.webm"]
        assert [t.id for t in app.playlist.tracks] == ["b"]

    run(scenario)


def test_playback_keys_reach_the_player():
    async def scenario(app, player, pilot):
        await search(app, pilot)
        await pilot.press("space", "right", "left", "plus", "minus", "minus")
        assert player.paused is True
        assert player.seeks == [5, -5]
        assert player.volume == 95

    run(scenario)


def test_typing_in_the_search_box_does_not_trigger_shortcuts():
    async def scenario(app, player, pilot):
        await pilot.click("#search")
        await pilot.press("n", "space", "q")
        assert app.query_one("#search").value == "n q"
        assert player.paused is False

    run(scenario)


def test_link_goes_straight_to_the_queue():
    async def scenario(app, player, pilot):
        await settle(app, pilot)
        assert [t.id for t in app.playlist.tracks] == ["a", "b"]
        assert player.loaded == ["a.webm"]

    run(scenario, startup="https://youtu.be/a")


def test_startup_words_run_a_search():
    async def scenario(app, player, pilot):
        await settle(app, pilot)
        assert app.query_one("#results", OptionList).option_count == 3

    run(scenario, startup="tum hi ho")


def test_titles_with_brackets_are_shown_literally():
    async def scenario(app, player, pilot):
        await search(app, pilot)
        await pilot.press("enter")
        await settle(app, pilot)
        app._tick()
        await pilot.pause()
        assert "Song [a]" in str(app.query_one("#now").content)

    run(scenario)


def test_long_title_cannot_push_the_progress_bar_out_of_view():
    long_track = Track("a", "A very long song title " * 12, "Artist", 100)

    class LongTitles(FakeSource):
        def search(self, query):
            return [long_track]

    player = FakePlayer()
    app = YtuneApp(player, LongTitles())

    async def main():
        async with app.run_test(size=(60, 24)) as pilot:
            await search(app, pilot)
            await pilot.press("enter")
            await settle(app, pilot)
            app._tick()
            await pilot.pause()
            now = app.query_one("#now")
            rows = [
                "".join(segment.text for segment in now.render_line(y))
                for y in range(now.size.height)
            ]
            assert any("vol 100" in row for row in rows), rows

    asyncio.run(main())


def test_app_exits_if_mpv_dies():
    async def scenario(app, player, pilot):
        player.gone = True
        app._tick()
        await pilot.pause()
        assert app.return_code is not None or not app.is_running

    run(scenario)


def test_is_url():
    assert is_url("https://music.youtube.com/watch?v=x")
    assert not is_url("tum hi ho")


def test_format_time():
    assert format_time(None) == "--:--"
    assert format_time(65) == "01:05"
    assert format_time(3661) == "1:01:01"


def test_progress_bar_keeps_its_width_at_every_position():
    for position in (None, 0, 50, 100, 500):
        assert len(progress_bar(position, 100, 20)) == 20
    assert progress_bar(0, 100, 5) == "●────"
    assert progress_bar(100, 100, 5) == "━━━━●"
