#!/usr/bin/env python3
"""Check the generated key table in layout.go against the kernel header.

The table is committed rather than generated at build time, so this guards
against it drifting from linux/input-event-codes.h. Skips when the header is
not installed.
"""

import re
import sys
from pathlib import Path

HEADER = Path("/usr/include/linux/input-event-codes.h")
LAYOUT = Path(__file__).resolve().parent.parent / "cmd" / "tartarus" / "layout.go"


def main():
    if not HEADER.exists():
        print(f"skipped: {HEADER} is not installed (install linux-libc-dev to check)")
        return 0

    truth = {}
    for line in HEADER.read_text().splitlines():
        m = re.match(r"#define KEY_([A-Z0-9_]+)\s+(\d+)", line)
        if m:
            name, code = m.group(1).lower(), int(m.group(2))
            # Keyboard keys only: codes 1-127 plus F13-F24.
            if (1 <= code <= 127) or (183 <= code <= 194):
                truth.setdefault(code, name)

    text = LAYOUT.read_text()
    # Scope the search to the key-table constant. Reading the whole file would
    # also pick up unrelated colon-separated values such as the device id.
    start = text.find("const keyboardKeys")
    if start < 0:
        sys.exit("could not find the keyboardKeys constant in layout.go")
    end = text.find("\n\n", start)
    block = text[start : end if end > 0 else len(text)]
    pairs = {}
    for chunk in re.findall(r'"([^"]*)"', block):
        for pair in chunk.split():
            name, sep, code = pair.partition(":")
            if sep and code.isdigit():
                pairs[name] = int(code)
    if not pairs:
        sys.exit("the keyboardKeys constant held no name:code pairs")

    problems = []
    for name, code in pairs.items():
        if truth.get(code) != name:
            problems.append(f"  layout.go says {name} = {code}, header says {code} = {truth.get(code)}")
    for code, name in truth.items():
        if name not in pairs:
            problems.append(f"  missing from layout.go: {name} = {code}")

    if problems:
        print(f"key table disagrees with {HEADER}:")
        print("\n".join(sorted(problems)[:20]))
        return 1
    print(f"key table matches {HEADER}: {len(pairs)} keyboard keys")
    return 0


if __name__ == "__main__":
    sys.exit(main())
