#!/usr/bin/env python3
"""Check what the Razer Tartarus V2 physically sends, key by key.

Walks you through the keypad's 25 inputs and records the keycode each one
emits, then compares that against whatever mapping is currently installed —
a udev hwdb file, a keyd config, or the keypad's stock layout if neither.

    sudo python3 verify_layout.py              # compare against what is installed
    sudo python3 verify_layout.py --stock      # compare against the stock layout
    sudo python3 verify_layout.py --report     # just record, compare nothing

Needs root, because /dev/input/event* is root:input. Read-only: it never
writes to the device or to any config. It takes an exclusive grab on the
keypad while running so its keys do not type into your terminal; the grab is
always released on exit. Your main keyboard is untouched, so Ctrl-C works.

A key that emits nothing within the per-key timeout is recorded as "silent",
which is the correct result for a genuinely unbound key.
"""

import argparse
import fcntl
import os
import re
import select
import struct
import sys
import time

VERSION = "3"
VENDOR, PRODUCT = "1532", "022b"
EVIOCGRAB = 0x40044590
EV_KEY = 0x01
EVENT_SIZE = struct.calcsize("llHHi")

HWDB_PATH = "/etc/udev/hwdb.d/99-tartarus_v2.hwdb"
KEYD_PATH = "/etc/keyd/tartarus.conf"

# label, stock key name, stock keycode, HID scancode, where to find it
WALK = [
    ("k01", "1", 2, "7001e", "top row, 1st from left"),
    ("k02", "2", 3, "7001f", "top row, 2nd"),
    ("k03", "3", 4, "70020", "top row, 3rd"),
    ("k04", "4", 5, "70021", "top row, 4th"),
    ("k05", "5", 6, "70022", "top row, 5th (rightmost)"),
    ("k06", "tab", 15, "7002b", "second row, 1st from left"),
    ("k07", "q", 16, "70014", "second row, 2nd"),
    ("k08", "w", 17, "7001a", "second row, 3rd"),
    ("k09", "e", 18, "70008", "second row, 4th"),
    ("k10", "r", 19, "70015", "second row, 5th"),
    ("k11", "capslock", 58, "70039", "third row, 1st from left"),
    ("k12", "a", 30, "70004", "third row, 2nd"),
    ("k13", "s", 31, "70016", "third row, 3rd"),
    ("k14", "d", 32, "70007", "third row, 4th"),
    ("k15", "f", 33, "70009", "third row, 5th"),
    ("k16", "leftshift", 42, "700e1", "bottom row, 1st from left"),
    ("k17", "z", 44, "7001d", "bottom row, 2nd"),
    ("k18", "x", 45, "7001b", "bottom row, 3rd"),
    ("k19", "c", 46, "70006", "bottom row, 4th (last)"),
    ("thumb", "leftalt", 56, "700e2", "round thumb button, above the thumb pad"),
    ("pad_up", "up", 103, "70052", "thumb pad: push UP"),
    ("pad_down", "down", 108, "70051", "thumb pad: push DOWN"),
    ("pad_left", "left", 105, "70050", "thumb pad: push LEFT"),
    ("pad_right", "right", 106, "7004f", "thumb pad: push RIGHT"),
    ("bar", "space", 57, "7002c", "the big bar under your thumb"),
]

SILENT = "silent"
UNBOUND_VALUES = {"reserved", "noop", "unknown", "off", ""}


def load_key_names():
    """code -> name, and name -> code, from the kernel header when present."""
    by_code, by_name = {}, {}
    header = "/usr/include/linux/input-event-codes.h"
    if os.path.exists(header):
        for line in open(header):
            m = re.match(r"#define (?:KEY|BTN)_([A-Z0-9_]+)\s+(?:0x)?([0-9a-fA-F]+)", line)
            if not m:
                continue
            try:
                code = int(m.group(2), 0)
            except ValueError:
                continue
            name = m.group(1).lower()
            by_code.setdefault(code, name)
            by_name.setdefault(name, code)
    # Enough to run without the header installed.
    for label, name, code, _, _ in WALK:
        by_code.setdefault(code, name)
        by_name.setdefault(name, code)
    return by_code, by_name


BY_CODE, BY_NAME = load_key_names()


def name_of(code):
    if code is None:
        return SILENT
    return BY_CODE.get(code, f"code {code}")


def parse_hwdb(path):
    """scancode -> key name, from a udev hwdb file."""
    out = {}
    for line in open(path):
        s = line.strip()
        if s.startswith("KEYBOARD_KEY_") and "=" in s:
            prop, _, value = s.partition("=")
            out[prop.replace("KEYBOARD_KEY_", "").lower()] = value.strip().lower()
    return out


def parse_keyd(path):
    """stock key name -> target, from a keyd config's [main] section."""
    out, section = {}, ""
    for line in open(path):
        s = line.split("#", 1)[0].strip()
        if not s:
            continue
        if s.startswith("[") and s.endswith("]"):
            section = s[1:-1].lower()
            continue
        if section == "main" and "=" in s:
            key, _, value = s.partition("=")
            out[key.strip().lower()] = value.strip().lower()
    return out


def build_expectations(mode):
    """(description, {label: expected code or None}) for the chosen comparison."""
    if mode != "stock":
        if os.path.exists(KEYD_PATH):
            keyd = parse_keyd(KEYD_PATH)
            exp = {}
            for label, stock, _, _, _ in WALK:
                target = keyd.get(stock)
                if target is None:
                    exp[label] = None
                elif target in UNBOUND_VALUES:
                    exp[label] = None
                else:
                    exp[label] = BY_NAME.get(target, -1)
            return f"the keyd config in {KEYD_PATH}", exp
        if os.path.exists(HWDB_PATH):
            hwdb = parse_hwdb(HWDB_PATH)
            exp = {}
            for label, _, _, scancode, _ in WALK:
                target = hwdb.get(scancode)
                if target is None:
                    exp[label] = None
                elif target in UNBOUND_VALUES:
                    exp[label] = None
                else:
                    exp[label] = BY_NAME.get(target, -1)
            return f"the udev mapping in {HWDB_PATH}", exp
    return "the keypad's stock layout", {l: c for l, _, c, _, _ in WALK}


def keypad_devices():
    """(path, name) for each keypad event device that can report keys."""
    found = []
    for block in open("/proc/bus/input/devices").read().split("\n\n"):
        if f"Vendor={VENDOR}" not in block or f"Product={PRODUCT}" not in block:
            continue
        if "B: KEY=" not in block:
            continue
        handlers = re.search(r"H: Handlers=(.*)", block)
        name = re.search(r'N: Name="([^"]*)"', block)
        if not handlers:
            continue
        for token in handlers.group(1).split():
            if token.startswith("event"):
                found.append((f"/dev/input/{token}", name.group(1) if name else token))
    return found


def wait_for_press(fds, timeout):
    """Return (code, fd) for the next key down, or None if nothing arrives."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        ready, _, _ = select.select(fds, [], [], 0.2)
        for fd in ready:
            data = os.read(fd, EVENT_SIZE * 64)
            for off in range(0, len(data) - EVENT_SIZE + 1, EVENT_SIZE):
                _, _, etype, code, value = struct.unpack("llHHi", data[off : off + EVENT_SIZE])
                if etype == EV_KEY and value == 1:
                    drain(fds)
                    return code, fd
    return None


def drain(fds):
    """Swallow the release and any repeats so the next prompt starts clean."""
    end = time.time() + 0.35
    while time.time() < end:
        ready, _, _ = select.select(fds, [], [], 0.05)
        for fd in ready:
            try:
                os.read(fd, EVENT_SIZE * 64)
            except OSError:
                pass


def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--stock", action="store_true",
                    help="compare against the stock layout even if a mapping is installed")
    ap.add_argument("--report", action="store_true",
                    help="only record what each key sends; compare nothing")
    ap.add_argument("--timeout", type=float, default=8.0,
                    help="seconds to wait per key before recording it as silent (default 8)")
    args = ap.parse_args()

    if os.geteuid() != 0:
        sys.exit(f"this needs root to read /dev/input/event*:  sudo python3 {sys.argv[0]}")

    devices = keypad_devices()
    if not devices:
        sys.exit(f"no Razer Tartarus V2 ({VENDOR}:{PRODUCT}) found. Is it plugged in?")

    mode = "stock" if args.stock else "installed"
    what, expected = build_expectations(mode)

    installed = [p for p in (KEYD_PATH, HWDB_PATH) if os.path.exists(p)]
    if args.stock and installed:
        print("REFUSING: --stock compares against an unmapped keypad, but a mapping")
        print("is installed, so the keypad is not at stock:")
        for path in installed:
            print(f"  {path}")
        print()
        print("Every key would report a mismatch, which would say nothing about the")
        print("layout table. Either drop --stock to compare against what is installed,")
        print("or remove the mapping first:")
        if HWDB_PATH in installed:
            print(f"  sudo rm {HWDB_PATH}")
            print("  sudo systemd-hwdb update")
            print("  sudo udevadm trigger --action=change --subsystem-match=input")
        if KEYD_PATH in installed:
            print("  tartarus off")
        sys.exit(1)

    print(f"Razer Tartarus V2 layout check (script v{VERSION})")
    print()
    print("installed mappings:")
    for path in (KEYD_PATH, HWDB_PATH):
        print(f"  {'present' if os.path.exists(path) else 'absent ':<8} {path}")
    if not args.report:
        print(f"\ncomparing against {what}")
    print()

    opened = []
    for path, name in devices:
        try:
            opened.append((os.open(path, os.O_RDONLY), path, name))
        except OSError as exc:
            print(f"note: cannot open {path}: {exc}", file=sys.stderr)
    if not opened:
        sys.exit("found the keypad but could not open any of its event devices")

    print(f"watching {len(opened)} device(s):")
    for _, path, name in opened:
        print(f"  {path}  {name}")
    print()
    print(f"Press each key as prompted. A key that stays quiet for {args.timeout:.0f}s is")
    print("recorded as silent, which is correct for an unbound key.")
    print("The keypad is grabbed, so its keys will not type here. Ctrl-C aborts.")
    print()

    fds = [fd for fd, _, _ in opened]
    path_of = {fd: path for fd, path, _ in opened}
    grabbed, results = [], []
    try:
        for fd, path, _ in opened:
            try:
                fcntl.ioctl(fd, EVIOCGRAB, 1)
                grabbed.append(fd)
            except OSError as exc:
                print(f"note: could not grab {path}: {exc}", file=sys.stderr)

        for label, _, _, _, where in WALK:
            print(f"  {label:<10} press: {where:<42}", end="", flush=True)
            got = wait_for_press(fds, args.timeout)
            code, source = (None, None) if got is None else (got[0], path_of[got[1]])
            verdict = ""
            if not args.report:
                want = expected.get(label, -1)
                verdict = "  ok" if code == want else f"  DIFFERS (expected {name_of(want)})"
            print(f"  -> {name_of(code):<12}{verdict}")
            results.append((label, code, source))
    except KeyboardInterrupt:
        print("\naborted")
    finally:
        for fd in grabbed:
            try:
                fcntl.ioctl(fd, EVIOCGRAB, 0)
            except OSError:
                pass
        for fd, _, _ in opened:
            os.close(fd)

    report(results, expected, what, args)


def report(results, expected, what, args):
    print()
    if not results:
        print("nothing recorded")
        return

    print("what each key sends:")
    for label, code, _ in results:
        print(f"  {label:<10} {name_of(code)}")

    if not args.report:
        bad = [(l, c) for l, c, _ in results if c != expected.get(l, -1)]
        print()
        print(f"{len(results) - len(bad)}/{len(results)} match {what}")
        if bad:
            print()
            print("DIFFERENCES:")
            for label, code in bad:
                print(f"  {label:<10} expected {name_of(expected.get(label, -1)):<12} "
                      f"got {name_of(code)}")

    sources = {s for _, _, s in results if s}
    print()
    print("keys arrived from:")
    for source in sorted(sources):
        n = sum(1 for _, _, s in results if s == source)
        print(f"  {source}  ({n} presses)")
    if len(sources) > 1:
        print("  more than one interface emits keys; keyd should claim all of them")

    # A stock comparison that matches nothing, with no mapping installed, means
    # the kernel is still holding a keymap a previous mapping wrote.
    if not args.report and not args.stock:
        pass
    if args.stock and results:
        matched = sum(1 for l, c, _ in results if c == expected.get(l, -1))
        if matched == 0 and not any(os.path.exists(p) for p in (KEYD_PATH, HWDB_PATH)):
            print()
            print("NOTE: no mapping is installed, yet nothing matches the stock layout.")
            print("The kernel still holds the keycode table a previous hwdb mapping wrote.")
            print("udev's keyboard builtin only ever sets the keys a mapping names; removing")
            print("the file resets nothing, so the device keeps that table until it is")
            print("re-created. Unplug and replug the keypad, or rebind it without the cable:")
            print()
            print("  for d in /sys/bus/usb/devices/*/idProduct; do \\")
            print("    dev=$(dirname \"$d\"); \\")
            print("    [ \"$(cat \"$d\")\" = 022b ] && [ \"$(cat \"$dev/idVendor\")\" = 1532 ] || continue; \\")
            print("    port=$(basename \"$dev\"); \\")
            print("    echo \"$port\" | sudo tee /sys/bus/usb/drivers/usb/unbind; sleep 1; \\")
            print("    echo \"$port\" | sudo tee /sys/bus/usb/drivers/usb/bind; \\")
            print("  done")
            print()
            print("This matters before using keyd: keyd matches on what the device sends,")
            print("so a config keyed on the stock names cannot match a device still")
            print("carrying an hwdb remap.")

    zeros = [l for l, c, _ in results if c == 11]
    if zeros:
        print()
        print(f"emitted the digit zero: {', '.join(zeros)}")
        print("  `=0` in an hwdb file is KEY_0, not 'unbound'. These keys type a 0.")

    print()
    print("Paste this whole output back to Claude.")


if __name__ == "__main__":
    main()
