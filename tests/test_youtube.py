from ytune import youtube
from ytune.youtube import Track, YouTube, _tracks


def test_entry_falls_back_to_uploader_and_rounds_duration():
    track = Track.from_entry({"id": "x", "title": "T", "uploader": "U", "duration": 61.7})
    assert track == Track("x", "T", "U", 61)


def test_entry_without_title_uses_its_id():
    assert Track.from_entry({"id": "x"}).title == "x"


def test_tracks_skips_deleted_playlist_entries():
    info = {"entries": [{"id": "a"}, None, {"title": "[Deleted video]"}]}
    assert [t.id for t in _tracks(info)] == ["a"]


def test_tracks_handles_a_single_video():
    assert [t.id for t in _tracks({"id": "a", "title": "A"})] == ["a"]


def test_js_runtime_prefers_the_first_one_installed(monkeypatch):
    installed = {"node", "bun"}
    monkeypatch.setattr(
        youtube.shutil, "which", lambda name: name if name in installed else None
    )
    assert youtube.find_js_runtime() == "node"


def test_js_runtime_missing(monkeypatch):
    monkeypatch.setattr(youtube.shutil, "which", lambda name: None)
    assert youtube.find_js_runtime() is None


def test_fetch_reuses_a_cached_file_without_downloading(tmp_path):
    audio = tmp_path / "abc.webm"
    audio.write_bytes(b"audio")
    assert YouTube(tmp_path).fetch(Track("abc", "T")) == audio


def test_interrupted_download_is_not_treated_as_cached(tmp_path):
    (tmp_path / "abc.webm.part").write_bytes(b"partial")
    assert YouTube(tmp_path).cached(Track("abc", "T")) is None
