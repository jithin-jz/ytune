from ytune.playlist import Playlist
from ytune.youtube import Track

A, B, C = Track("a", "A"), Track("b", "B"), Track("c", "C")


def filled(index: int = 0) -> Playlist:
    playlist = Playlist()
    playlist.add(A, B, C)
    playlist.index = index
    return playlist


def test_empty_playlist_has_nothing_to_play():
    playlist = Playlist()
    assert playlist.current is None
    assert playlist.advance() is None
    assert playlist.back() is None


def test_advance_walks_forward_and_stops_at_the_end():
    playlist = filled()
    assert playlist.advance() == B
    assert playlist.advance() == C
    assert playlist.advance() is None
    assert playlist.current == C


def test_back_stops_at_the_first_track():
    playlist = filled(1)
    assert playlist.back() == A
    assert playlist.back() is None


def test_play_now_keeps_the_rest_of_the_queue():
    playlist = filled(0)
    new = Track("n", "N")
    playlist.play_now(new)
    assert playlist.current == new
    assert playlist.tracks == [A, new, B, C]


def test_play_now_on_an_empty_playlist():
    playlist = Playlist()
    playlist.play_now(A)
    assert playlist.current == A


def test_removing_an_earlier_track_keeps_the_current_one():
    playlist = filled(2)
    assert playlist.remove(0) is False
    assert playlist.current == C


def test_removing_a_later_track_keeps_the_current_one():
    playlist = filled(0)
    assert playlist.remove(2) is False
    assert playlist.current == A


def test_removing_the_current_track_advances_to_what_followed_it():
    playlist = filled(1)
    assert playlist.remove(1) is True
    assert playlist.advance() == C


def test_removing_the_only_track_leaves_nothing_to_advance_to():
    playlist = Playlist()
    playlist.add(A)
    playlist.index = 0
    assert playlist.remove(0) is True
    assert playlist.advance() is None
