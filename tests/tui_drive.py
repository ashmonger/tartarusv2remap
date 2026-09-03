#!/usr/bin/env python3
"""Drive the keypad editor through a pty and report what it did.

Bubble Tea needs a terminal, so the editor is exercised the way a person uses
it: one keystroke at a time, with a moment for each redraw.
"""

import os
import pty
import re
import select
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
BINARY = ROOT / "tartarus"
SOURCE_PROFILES = ROOT / "profiles"

ESC = "\x1b"
KEYS = {
    "up": ESC + "[A", "down": ESC + "[B", "right": ESC + "[C", "left": ESC + "[D",
    "enter": "\r", "tab": "\t",
}


def drive(script, profiles_dir, settle=0.35):
    """Run the editor, send each keystroke, and return everything it printed."""
    env = dict(os.environ, TARTARUS_PROFILES=str(profiles_dir), TERM="xterm-256color")
    primary, secondary = pty.openpty()
    proc = subprocess.Popen(
        [str(BINARY), "edit", "diablo4"],
        stdin=secondary, stdout=secondary, stderr=secondary, env=env, close_fds=True,
    )
    os.close(secondary)
    output = []

    def pump(seconds):
        deadline = time.time() + seconds
        while time.time() < deadline:
            ready, _, _ = select.select([primary], [], [], 0.05)
            if ready:
                try:
                    chunk = os.read(primary, 65536)
                except OSError:
                    return
                if not chunk:
                    return
                output.append(chunk.decode("utf-8", "replace"))

    pump(0.8)                       # let the first frame render
    for step in script:
        os.write(primary, KEYS.get(step, step).encode())
        pump(settle)
    pump(0.6)

    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait()
    os.close(primary)
    return "".join(output), proc.returncode


def strip_ansi(text):
    return re.sub(r"\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)", "", text)


def main():
    if not BINARY.exists():
        sys.exit(f"build it first: go build -o {BINARY} .")

    failures = 0

    def check(name, ok, detail=""):
        nonlocal failures
        print(("ok   " if ok else "FAIL ") + name + (f"  — {detail}" if detail and not ok else ""))
        if not ok:
            failures += 1

    # 1. It renders the keypad.
    with tempfile.TemporaryDirectory() as tmp:
        for f in SOURCE_PROFILES.glob("*.profile"):
            shutil.copy(f, tmp)
        screen, _ = drive(["q"], tmp)
        plain = strip_ansi(screen)
        check("renders the profile name", "Diablo 4" in plain)
        check("renders all 19 backlit keys", all(f"k{n:02d}" in plain for n in range(1, 20)))
        check("renders the thumb cluster", "thumb" in plain and "pad_up" in plain and "bar" in plain)
        check("shows an inherited binding", " esc " in plain or "esc" in plain)

    # 2. Binding a key by name persists to disk.
    with tempfile.TemporaryDirectory() as tmp:
        for f in SOURCE_PROFILES.glob("*.profile"):
            shutil.copy(f, tmp)
        target = Path(tmp) / "diablo4.profile"
        before = target.read_text()
        # row 3, column 2 is k12; bind it to f5, save, quit.
        drive(["down", "down", "right", "enter", "f", "5", "enter", "s", "q"], tmp)
        after = target.read_text()
        check("binding a key rewrites the profile", before != after)
        check("k12 is now f5", "k12 = f5" in after, after)
        check("other bindings survive", "k11 = q" in after and "bar = space" in after)
        check("inherited bindings are not duplicated", "pad_up" not in after)

    # 3. Unbinding writes an explicit off only where it overrides a parent.
    with tempfile.TemporaryDirectory() as tmp:
        for f in SOURCE_PROFILES.glob("*.profile"):
            shutil.copy(f, tmp)
        target = Path(tmp) / "diablo4.profile"
        # row 2, column 4 is k09, which default.profile binds to `a`.
        drive(["down", "right", "right", "right", "x", "s", "q"], tmp)
        after = target.read_text()
        check("unbinding an inherited key writes off", "k09 = off" in after, after)

    # 4. Quitting with unsaved changes is refused once.
    with tempfile.TemporaryDirectory() as tmp:
        for f in SOURCE_PROFILES.glob("*.profile"):
            shutil.copy(f, tmp)
        target = Path(tmp) / "diablo4.profile"
        before = target.read_text()
        screen, _ = drive(["down", "down", "right", "x", "q", "Q"], tmp)
        plain = strip_ansi(screen)
        check("warns before discarding edits", "unsaved changes" in plain)
        check("discarding leaves the file alone", target.read_text() == before)

    print()
    print("ALL TUI CHECKS PASSED" if not failures else f"{failures} TUI CHECK(S) FAILED")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
