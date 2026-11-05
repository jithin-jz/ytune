"""Command-line entry point."""

from __future__ import annotations

import argparse
import sys
import tempfile
from pathlib import Path

from ytune import __version__
from ytune.app import YtuneApp
from ytune.player import Mpv, PlayerError
from ytune.youtube import YouTube, find_js_runtime


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        prog="ytune", description="A full-screen terminal music player for YouTube."
    )
    parser.add_argument("query", nargs="*", help="search words or a YouTube link")
    parser.add_argument("--version", action="version", version=f"ytune {__version__}")
    args = parser.parse_args(argv)

    if find_js_runtime() is None:
        print(
            "ytune needs Node.js, Deno or Bun: YouTube blocks audio downloads "
            "without a JavaScript runtime.",
            file=sys.stderr,
        )
        return 1

    try:
        player = Mpv()
    except PlayerError as error:
        print(f"{error} See the README for install steps.", file=sys.stderr)
        return 1

    with tempfile.TemporaryDirectory(
        prefix="ytune-", ignore_cleanup_errors=True
    ) as cache_dir:
        try:
            YtuneApp(player, YouTube(Path(cache_dir)), " ".join(args.query)).run()
        finally:
            player.close()  # Before the cache goes, so no file is still open.
    return 0
