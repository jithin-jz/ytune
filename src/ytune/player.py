"""Drives a headless mpv process over its JSON IPC channel."""

from __future__ import annotations

import json
import os
import shutil
import socket
import subprocess
import tempfile
import threading
import time
import uuid
from dataclasses import dataclass
from pathlib import Path

CONNECT_TIMEOUT = 5.0
QUIT_TIMEOUT = 3.0


class PlayerError(RuntimeError):
    """mpv is missing or stopped responding."""


@dataclass(frozen=True)
class Status:
    position: float | None = None
    duration: float | None = None
    paused: bool = False
    volume: int = 100


def find_mpv() -> Path | None:
    found = shutil.which("mpv")
    if not found:
        return None
    path = Path(found)
    # On Windows `mpv` resolves to mpv.com, a console wrapper around mpv.exe.
    # Run the real binary so that stopping the process stops the audio.
    if path.suffix.lower() == ".com" and path.with_suffix(".exe").exists():
        return path.with_suffix(".exe")
    return path


class _WindowsPipe:
    def __init__(self, address: str) -> None:
        self._pipe = open(address, "r+b", buffering=0)

    def read(self) -> bytes:
        return self._pipe.read(4096)

    def write(self, data: bytes) -> None:
        self._pipe.write(data)

    def close(self) -> None:
        self._pipe.close()


class _UnixSocket:
    def __init__(self, address: str) -> None:
        self._socket = socket.socket(socket.AF_UNIX)
        self._socket.connect(address)

    def read(self) -> bytes:
        return self._socket.recv(4096)

    def write(self, data: bytes) -> None:
        self._socket.sendall(data)

    def close(self) -> None:
        self._socket.close()


class Mpv:
    def __init__(self, extra_args: tuple[str, ...] = ()) -> None:
        binary = find_mpv()
        if binary is None:
            raise PlayerError("mpv was not found on your PATH.")
        name = f"ytune-{uuid.uuid4().hex}"
        if os.name == "nt":
            address, transport = rf"\\.\pipe\{name}", _WindowsPipe
        else:
            address = str(Path(tempfile.gettempdir()) / f"{name}.sock")
            transport = _UnixSocket
        self._process = subprocess.Popen(
            [
                str(binary),
                "--idle=yes",
                "--no-video",
                "--no-terminal",
                f"--input-ipc-server={address}",
                *extra_args,
            ],
            stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        self._channel = self._connect(transport, address)
        self._lock = threading.Lock()
        self._buffer = b""
        self._request_id = 0
        self._finished = False

    def _connect(self, transport, address: str):
        deadline = time.monotonic() + CONNECT_TIMEOUT
        while True:
            try:
                return transport(address)
            except OSError as error:
                if self._process.poll() is not None or time.monotonic() > deadline:
                    self._process.kill()
                    raise PlayerError("Could not connect to mpv.") from error
                time.sleep(0.05)

    def _read_message(self) -> dict:
        while b"\n" not in self._buffer:
            data = self._channel.read()
            if not data:
                raise PlayerError("mpv closed the connection.")
            self._buffer += data
        line, self._buffer = self._buffer.split(b"\n", 1)
        return json.loads(line)

    def _command(self, *args) -> dict:
        with self._lock:
            self._request_id += 1
            payload = {"command": list(args), "request_id": self._request_id}
            try:
                self._channel.write(json.dumps(payload).encode() + b"\n")
                while True:
                    message = self._read_message()
                    # A replaced or stopped file ends with reason "stop", which
                    # must not count as the track finishing on its own.
                    if (
                        message.get("event") == "end-file"
                        and message.get("reason") in ("eof", "error")
                    ):
                        self._finished = True
                    if message.get("request_id") == self._request_id:
                        return message
            except (OSError, ValueError) as error:
                raise PlayerError("Lost contact with mpv.") from error

    def _get(self, name: str, default=None):
        reply = self._command("get_property", name)
        return reply["data"] if reply.get("error") == "success" else default

    def load(self, path: Path) -> None:
        self._finished = False
        self._command("loadfile", str(path), "replace")
        self._command("set_property", "pause", False)

    def stop(self) -> None:
        self._command("stop")
        self._finished = False

    def toggle_pause(self) -> None:
        self._command("cycle", "pause")

    def seek(self, seconds: float) -> None:
        self._command("seek", seconds, "relative")

    def change_volume(self, delta: int) -> None:
        self._command("add", "volume", delta)

    def status(self) -> Status:
        return Status(
            position=self._get("time-pos"),
            duration=self._get("duration"),
            paused=bool(self._get("pause", False)),
            volume=round(self._get("volume", 100)),
        )

    def consume_finished(self) -> bool:
        """True once for each track that played to its end or failed to play."""
        self._command("get_version")  # Drains any end-of-file event waiting.
        finished, self._finished = self._finished, False
        return finished

    def close(self) -> None:
        try:
            self._channel.write(b'{"command": ["quit"]}\n')
        except (OSError, ValueError):
            pass
        try:
            self._process.wait(timeout=QUIT_TIMEOUT)
        except subprocess.TimeoutExpired:
            self._process.kill()
        self._channel.close()
