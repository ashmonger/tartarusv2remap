#!/usr/bin/env python3
"""Remap a Razer Tartarus V2 keypad per game.

A profile names the physical keys of the keypad and says what each one sends.
This tool compiles a profile for a remapping backend and activates it.

Two backends are supported:

  keyd  (default)  a userspace daemon. Supports macros and unbinding.
  hwdb             udev's hardware database. One key to one key only.

Compiling a profile never needs privileges; only activating one does.
"""

from __future__ import annotations

import argparse
import configparser
import os
import shutil
import subprocess
import sys
from dataclasses import dataclass, field
from pathlib import Path

# ---------------------------------------------------------------------------
# Device layout
# ---------------------------------------------------------------------------

# USB vendor:product of the keypad. `lsusb` or `keyd monitor` confirms it.
DEFAULT_DEVICE = "1532:022b"

# The physical inputs of the keypad, in reading order.
#
#   label      the name a profile uses
#   stock      the key the keypad sends before remapping (what keyd matches on)
#   scancode   the HID scancode (what udev names its hwdb property after)
#
# Razer counts 19 backlit keys, a Hyperesponse thumb key, a spacebar actuator
# and an 8-way thumb pad. The pad reports diagonals as two cardinals at once,
# so only the four cardinals are bindable.
LAYOUT: list[tuple[str, str, str]] = [
    ("k01", "1", "7001e"),
    ("k02", "2", "7001f"),
    ("k03", "3", "70020"),
    ("k04", "4", "70021"),
    ("k05", "5", "70022"),
    ("k06", "tab", "7002b"),
    ("k07", "q", "70014"),
    ("k08", "w", "7001a"),
    ("k09", "e", "70008"),
    ("k10", "r", "70015"),
    ("k11", "capslock", "70039"),
    ("k12", "a", "70004"),
    ("k13", "s", "70016"),
    ("k14", "d", "70007"),
    ("k15", "f", "70009"),
    ("k16", "leftshift", "700e1"),
    ("k17", "z", "7001d"),
    ("k18", "x", "7001b"),
    ("k19", "c", "70006"),
    ("thumb", "leftalt", "700e2"),
    ("pad_up", "up", "70052"),
    ("pad_down", "down", "70051"),
    ("pad_left", "left", "70050"),
    ("pad_right", "right", "7004f"),
    ("bar", "space", "7002c"),
]

LABELS = [label for label, _, _ in LAYOUT]
STOCK = {label: stock for label, stock, _ in LAYOUT}
SCANCODE = {label: code for label, _, code in LAYOUT}

# Friendlier names a profile may use for the thumb cluster.
ALIASES = {
    "thumb_key": "thumb",
    "k20": "thumb",
    "spacebar": "bar",
    "thumb_bar": "bar",
    "pad_u": "pad_up",
    "pad_d": "pad_down",
    "pad_l": "pad_left",
    "pad_r": "pad_right",
}

GRID_ROWS = [LABELS[0:5], LABELS[5:10], LABELS[10:15], LABELS[15:19]]
PAD = ["pad_up", "pad_down", "pad_left", "pad_right"]

# ---------------------------------------------------------------------------
# Keyboard keys
# ---------------------------------------------------------------------------

# Every keyboard key the Linux input layer defines: codes 1-127 plus F13-F24,
# taken from linux/input-event-codes.h with the KEY_ prefix stripped. Codes are
# written out rather than counted, because the range has a hole at 84.
# Keys outside this range are mice, gamepads and media appliances, and are
# deliberately not offered.
_KEYBOARD_KEYS = """
    esc:1 1:2 2:3 3:4 4:5 5:6 6:7 7:8 8:9 9:10 0:11 minus:12 equal:13 backspace:14 tab:15
    q:16 w:17 e:18 r:19 t:20 y:21 u:22 i:23 o:24 p:25 leftbrace:26 rightbrace:27 enter:28
    leftctrl:29 a:30 s:31 d:32 f:33 g:34 h:35 j:36 k:37 l:38 semicolon:39 apostrophe:40
    grave:41 leftshift:42 backslash:43 z:44 x:45 c:46 v:47 b:48 n:49 m:50 comma:51 dot:52
    slash:53 rightshift:54 kpasterisk:55 leftalt:56 space:57 capslock:58 f1:59 f2:60 f3:61
    f4:62 f5:63 f6:64 f7:65 f8:66 f9:67 f10:68 numlock:69 scrolllock:70 kp7:71 kp8:72 kp9:73
    kpminus:74 kp4:75 kp5:76 kp6:77 kpplus:78 kp1:79 kp2:80 kp3:81 kp0:82 kpdot:83
    zenkakuhankaku:85 102nd:86 f11:87 f12:88 ro:89 katakana:90 hiragana:91 henkan:92
    katakanahiragana:93 muhenkan:94 kpjpcomma:95 kpenter:96 rightctrl:97 kpslash:98 sysrq:99
    rightalt:100 linefeed:101 home:102 up:103 pageup:104 left:105 right:106 end:107 down:108
    pagedown:109 insert:110 delete:111 macro:112 mute:113 volumedown:114 volumeup:115
    power:116 kpequal:117 kpplusminus:118 pause:119 scale:120 kpcomma:121 hangeul:122
    hanja:123 yen:124 leftmeta:125 rightmeta:126 compose:127 f13:183 f14:184 f15:185 f16:186
    f17:187 f18:188 f19:189 f20:190 f21:191 f22:192 f23:193 f24:194
"""

KEYBOARD_KEYS: dict[str, int] = {
    name: int(code) for name, _, code in (p.partition(":") for p in _KEYBOARD_KEYS.split())
}

# Short forms used only when drawing the keypad.
DISPLAY = {
    "leftshift": "LShft", "rightshift": "RShft", "leftctrl": "LCtrl",
    "rightctrl": "RCtrl", "leftalt": "LAlt", "rightalt": "RAlt",
    "leftmeta": "LMeta", "rightmeta": "RMeta", "capslock": "Caps",
    "space": "Space", "enter": "Enter", "backspace": "BkSp", "delete": "Del",
    "insert": "Ins", "pageup": "PgUp", "pagedown": "PgDn", "esc": "Esc",
    "tab": "Tab", "grave": "`", "minus": "-", "equal": "=", "comma": ",",
    "dot": ".", "slash": "/", "semicolon": ";", "apostrophe": "'",
    "leftbrace": "[", "rightbrace": "]", "backslash": "\\",
}

# keyd spells a few keys differently from the kernel. Anything not listed here
# is passed through and validated by `keyd check`, which is the real authority.
KEYD_RENAMES = {"leftctrl": "leftcontrol", "rightctrl": "rightcontrol"}

# Values a profile uses to say "this key sends nothing".
OFF_WORDS = {"off", "none", "-", "", "noop", "reserved", "unbound", "disabled"}

# ---------------------------------------------------------------------------
# Profiles
# ---------------------------------------------------------------------------

PROFILE_SUFFIX = ".profile"
STAMP = "# tartarus-profile:"
KEYD_PATH = Path("/etc/keyd/tartarus.conf")
HWDB_PATH = Path("/etc/udev/hwdb.d/99-tartarus_v2.hwdb")


class ProfileError(Exception):
    """A profile is missing, malformed, or asks for something impossible."""


@dataclass
class Profile:
    slug: str
    name: str
    device: str = DEFAULT_DEVICE
    passthrough_unbound: bool = False
    bindings: dict[str, str] = field(default_factory=dict)
    macros: dict[str, str] = field(default_factory=dict)
    sources: list[Path] = field(default_factory=list)


def profile_dirs(override: str | None) -> list[Path]:
    """Directories searched for profiles, most specific first."""
    if override:
        return [Path(override).expanduser()]
    dirs = []
    if env := os.environ.get("TARTARUS_PROFILES"):
        dirs.append(Path(env).expanduser())
    config = Path(os.environ.get("XDG_CONFIG_HOME", "~/.config")).expanduser()
    dirs.append(config / "tartarus" / "profiles")
    dirs.append(config / "tartarus")
    dirs.append(Path(__file__).resolve().parent.parent / "profiles")
    return dirs


def find_profile(slug: str, dirs: list[Path]) -> Path:
    for directory in dirs:
        path = directory / (slug + PROFILE_SUFFIX)
        if path.is_file():
            return path
    searched = ", ".join(str(d) for d in dirs)
    raise ProfileError(f"no profile named {slug!r} (searched: {searched})")


def list_profiles(dirs: list[Path]) -> list[str]:
    slugs: dict[str, None] = {}
    for directory in dirs:
        if directory.is_dir():
            for path in sorted(directory.glob("*" + PROFILE_SUFFIX)):
                slugs.setdefault(path.stem, None)
    return list(slugs)


def canonical_label(raw: str) -> str:
    label = ALIASES.get(raw, raw)
    if label not in SCANCODE:
        raise ProfileError(f"unknown key {raw!r}; expected one of: {', '.join(LABELS)}")
    return label


def parse_bindings(section) -> dict[str, str]:
    """Turn a [keys] section into canonical label -> value."""
    bindings: dict[str, str] = {}
    for raw_label, raw_value in section.items():
        value = (raw_value or "").strip()
        if raw_label == "pad":
            parts = value.split()
            if len(parts) != 4:
                raise ProfileError(
                    f"pad takes 4 values (up down left right), got {len(parts)}: {raw_value!r}"
                )
            bindings.update(dict(zip(PAD, parts)))
            continue
        bindings[canonical_label(raw_label)] = value
    return bindings


def load_profile(slug: str, dirs: list[Path], _chain: tuple[str, ...] = ()) -> Profile:
    """Load a profile, layering it on top of any profile it extends."""
    if slug in _chain:
        raise ProfileError("profile inheritance loops: " + " -> ".join((*_chain, slug)))

    path = find_profile(slug, dirs)
    parser = configparser.ConfigParser(inline_comment_prefixes=("#", ";"), interpolation=None)
    parser.optionxform = str.lower
    try:
        parser.read(path, encoding="utf-8")
    except configparser.Error as exc:
        raise ProfileError(f"{path}: {exc}") from exc

    meta = parser["profile"] if parser.has_section("profile") else {}
    parent = (meta.get("extends") or "").strip()

    if parent:
        profile = load_profile(parent, dirs, (*_chain, slug))
        profile.slug = slug
    else:
        profile = Profile(slug=slug, name=slug)

    profile.name = (meta.get("name") or profile.name or slug).strip()
    if device := (meta.get("device") or "").strip():
        profile.device = device.lower()
    unbound = (meta.get("unbound") or "").strip().lower()
    if unbound:
        if unbound not in {"off", "passthrough"}:
            raise ProfileError(f"{path}: unbound must be 'off' or 'passthrough', got {unbound!r}")
        profile.passthrough_unbound = unbound == "passthrough"

    if parser.has_section("macros"):
        profile.macros.update({k: (v or "").strip() for k, v in parser["macros"].items()})
    if parser.has_section("keys"):
        try:
            profile.bindings.update(parse_bindings(parser["keys"]))
        except ProfileError as exc:
            raise ProfileError(f"{path}: {exc}") from exc

    profile.sources.append(path)
    return profile


# ---------------------------------------------------------------------------
# Values: plain keys, unbinds, and macros
# ---------------------------------------------------------------------------

def resolve(profile: Profile, label: str, value: str) -> str:
    """Expand a @macro reference. Everything else is returned unchanged."""
    if value.startswith("@"):
        name = value[1:].lower()
        if name not in profile.macros:
            known = ", ".join(sorted(profile.macros)) or "none defined"
            raise ProfileError(f"{label}: no macro named {name!r} ([macros] has: {known})")
        return f"macro({profile.macros[name]})"
    return value


def is_off(value: str) -> bool:
    return value.strip().lower() in OFF_WORDS


def is_plain_key(value: str) -> bool:
    """True when the value is a single key name, remappable by any backend."""
    return value.lower() in KEYBOARD_KEYS


def describe_value(value: str) -> str:
    if is_off(value):
        return "unbound"
    if is_plain_key(value):
        return "key"
    return "macro"


# ---------------------------------------------------------------------------
# Backends
# ---------------------------------------------------------------------------

def modalias(device: str) -> str:
    """udev's device match string for a vendor:product pair."""
    try:
        vendor, product = device.split(":")
        int(vendor, 16), int(product, 16)
    except ValueError as exc:
        raise ProfileError(f"device must look like 1532:022b, got {device!r}") from exc
    return f"evdev:input:b0003v{vendor.upper()}p{product.upper()}*"


def header(profile: Profile, backend: str) -> list[str]:
    return [
        f"{STAMP} {profile.slug}",
        f"# {profile.name}",
        f"# Generated by tartarus ({backend} backend). Edit the profile, not this file.",
        "",
    ]


def render_keyd(profile: Profile) -> str:
    lines = [
        f"{STAMP} {profile.slug}",
        f"# {profile.name}",
        "# Generated by tartarus (keyd backend). Edit the profile, not this file.",
        "#",
        "# The left-hand side is the key the keypad sends before remapping:",
        "#   k01=1  k02=2  k03=3  k04=4  k05=5",
        "#   k06=tab  k07=q  k08=w  k09=e  k10=r",
        "#   k11=capslock  k12=a  k13=s  k14=d  k15=f",
        "#   k16=leftshift  k17=z  k18=x  k19=c",
        "#   thumb=leftalt  pad=up/down/left/right  bar=space",
        "",
        "[ids]",
        profile.device,
        "",
        "[main]",
    ]
    # No trailing comments: not every keyd version strips them from a value.
    for label, scancode in [(i, None) for i in LABELS]:
        value = profile.bindings.get(label)
        if value is None:
            if profile.passthrough_unbound:
                continue
            target = "noop"
        else:
            value = resolve(profile, label, value)
            target = "noop" if is_off(value) else keyd_name(value)
        lines.append(f"{STOCK[label]} = {target}")
    lines.append("")
    return "\n".join(lines)


def keyd_name(value: str) -> str:
    lowered = value.lower()
    return KEYD_RENAMES.get(lowered, value)


def render_hwdb(profile: Profile) -> str:
    lines = header(profile, "hwdb")
    lines.append(modalias(profile.device))
    for label in LABELS:
        value = profile.bindings.get(label)
        if value is None:
            if profile.passthrough_unbound:
                continue
            target = "reserved"
        else:
            value = resolve(profile, label, value)
            if is_off(value):
                target = "reserved"
            elif is_plain_key(value):
                target = value.lower()
            else:
                raise ProfileError(
                    f"{label} = {value}: the hwdb backend remaps one key to one key and "
                    "cannot run macros. Use the keyd backend for this profile."
                )
        lines.append(f" KEYBOARD_KEY_{SCANCODE[label]}={target}")
    lines.append("")
    return "\n".join(lines)


@dataclass
class Backend:
    name: str
    path: Path
    render: object
    activate: object
    deactivate: object


def run(cmd: list[str], dry_run: bool) -> None:
    if dry_run:
        print("would run:", " ".join(cmd))
        return
    subprocess.run(cmd, check=True)


def missing_ok(dry_run: bool, message: str) -> bool:
    """A dry run reports a missing tool; a real run refuses to continue."""
    if not dry_run:
        raise ProfileError(message)
    print(f"note: {message} (dry run, continuing)", file=sys.stderr)
    return False


def keyd_activate(dry_run: bool) -> None:
    if HWDB_PATH.exists():
        print(
            f"warning: {HWDB_PATH} is still installed. It remaps the keypad before keyd "
            "sees it, so the two will compose. Run `tartarus off --backend hwdb` first.",
            file=sys.stderr,
        )
    if not shutil.which("keyd") and not missing_ok(
        dry_run, "keyd is not installed; install it or use --backend hwdb"
    ):
        return
    run(["keyd", "check", str(KEYD_PATH)], dry_run)
    run(["keyd", "reload"], dry_run)


def keyd_deactivate(dry_run: bool) -> None:
    if shutil.which("keyd"):
        run(["keyd", "reload"], dry_run)


def hwdb_activate(dry_run: bool) -> None:
    """Rebuild and reapply the hardware database.

    The rebuild is not optional: udev reads only the compiled hwdb.bin, so
    reloading and retriggering without it reapplies the previous mapping.
    """
    if shutil.which("systemd-hwdb"):
        run(["systemd-hwdb", "update", "--strict"], dry_run)
    elif shutil.which("udevadm"):
        run(["udevadm", "hwdb", "--update"], dry_run)
    elif not missing_ok(
        dry_run, "neither systemd-hwdb nor udevadm found; cannot rebuild the database"
    ):
        return
    run(["udevadm", "control", "--reload"], dry_run)
    run(["udevadm", "trigger", "--action=change", "--subsystem-match=input"], dry_run)


BACKENDS = {
    "keyd": Backend("keyd", KEYD_PATH, render_keyd, keyd_activate, keyd_deactivate),
    "hwdb": Backend("hwdb", HWDB_PATH, render_hwdb, hwdb_activate, hwdb_activate),
}


# ---------------------------------------------------------------------------
# Drawing the keypad
# ---------------------------------------------------------------------------

CELL = 8


def cell(profile: Profile, label: str) -> str:
    value = profile.bindings.get(label)
    if value is None or is_off(value):
        text = "=" if profile.passthrough_unbound and value is None else "·"
    elif value.startswith("@"):
        text = value
    elif is_plain_key(value):
        text = DISPLAY.get(value.lower(), value)
    else:
        text = "macro"
    if len(text) > CELL:
        text = text[: CELL - 1] + "…"
    return text.center(CELL)


def render_layout(profile: Profile) -> str:
    """Draw the keypad as it physically is: three rows of five, then four."""
    dash = "─" * CELL

    def row(labels):
        return "  │" + "│".join(cell(profile, label) for label in labels) + "│"

    def labelled(labels):
        return "  " + " ".join(label for label in labels)

    out = [f"Razer Tartarus V2 — {profile.name}  [{profile.slug}]", ""]
    out.append("  ┌" + "┬".join([dash] * 5) + "┐")
    for index, labels in enumerate(GRID_ROWS[:3]):
        out.append(row(labels) + labelled(labels))
        if index < 2:
            out.append("  ├" + "┼".join([dash] * 5) + "┤")
    # Row four is one key shorter, so the grid closes over the fifth column.
    out.append("  ├" + "┼".join([dash] * 4) + "┤" + dash + "┘")
    out.append(row(GRID_ROWS[3]) + labelled(GRID_ROWS[3]))
    out.append("  └" + "┴".join([dash] * 4) + "┘")
    out.append("")

    def shown(label):
        return cell(profile, label).strip() or "·"

    out.append(f"  thumb key   {shown('thumb')}")
    out.append(f"  spacebar    {shown('bar')}")
    out.append(
        f"  thumb pad   ↑ {shown('pad_up')}   ↓ {shown('pad_down')}"
        f"   ← {shown('pad_left')}   → {shown('pad_right')}"
    )
    out.append("")

    bound = [
        (label, value, resolve(profile, label, value))
        for label in LABELS
        if (value := profile.bindings.get(label)) is not None and not is_off(value)
    ]
    if bound:
        out.append(f"  {len(bound)} of {len(LABELS)} inputs bound")
        width = max(len(value) for _, value, _ in bound)
        for label, value, resolved in bound:
            expansion = f"  -> {resolved}" if resolved != value else ""
            out.append(f"    {label:<10} {value:<{width}}  {describe_value(resolved)}{expansion}")
    return "\n".join(out)


# ---------------------------------------------------------------------------
# Activating
# ---------------------------------------------------------------------------

def require_root(argv: list[str], dry_run: bool = False) -> None:
    """Re-run under sudo. Called only after the profile compiled successfully."""
    if dry_run or os.geteuid() == 0:
        return
    if not shutil.which("sudo"):
        raise ProfileError("this command needs root and sudo is not available")
    os.execvp("sudo", ["sudo", sys.executable, os.path.abspath(__file__), *argv])


def install(backend: Backend, content: str, dry_run: bool) -> None:
    """Write the config and activate it, restoring the old one if it is rejected."""
    previous = backend.path.read_bytes() if backend.path.exists() else None
    if dry_run:
        print(f"would write {backend.path}:\n")
        print(content)
    else:
        backend.path.parent.mkdir(parents=True, exist_ok=True)
        backend.path.write_text(content, encoding="utf-8")
    try:
        backend.activate(dry_run)
    except subprocess.CalledProcessError:
        if not dry_run:
            if previous is None:
                backend.path.unlink(missing_ok=True)
            else:
                backend.path.write_bytes(previous)
            backend.activate(False)
        raise ProfileError(
            f"{backend.name} rejected the configuration; the previous one was restored"
        )


def active_profile(backend: Backend) -> str | None:
    if not backend.path.exists():
        return None
    for line in backend.path.read_text(encoding="utf-8", errors="replace").splitlines():
        if line.startswith(STAMP):
            return line[len(STAMP) :].strip() or "unknown"
    return "unknown"


# ---------------------------------------------------------------------------
# Importing old .map files
# ---------------------------------------------------------------------------

def import_map(path: Path) -> str:
    """Convert a hand-written hwdb .map file into a profile."""
    by_scancode = {code: label for label, _, code in LAYOUT}
    bindings: dict[str, str] = {}
    device = ""
    for line in path.read_text(encoding="utf-8").splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        if stripped.startswith("evdev:"):
            device = stripped
            continue
        if "=" not in stripped:
            continue
        prop, _, value = stripped.partition("=")
        scancode = prop.strip().removeprefix("KEYBOARD_KEY_")
        label = by_scancode.get(scancode)
        if label is None:
            print(f"note: {path.name}: skipping unknown scancode {scancode}", file=sys.stderr)
            continue
        value = value.strip().lower()
        if value in {"0", ""}:
            # `0` is the digit zero (KEY_0), and an empty value is dropped by
            # udev. Both were written meaning "unbound".
            print(
                f"note: {path.name}: {label}={value!r} read as unbound "
                f"({'it actually sends the digit 0' if value else 'udev ignores it'})",
                file=sys.stderr,
            )
            value = "off"
        bindings[label] = value

    out = ["[profile]", f"name = {path.stem}"]
    if device:
        parts = device.split("v")[-1]
        vendor, _, product = parts.partition("p")
        out.append(f"device = {vendor.lower()}:{product.rstrip('*').lower()}")
    out += ["", "[keys]"]
    for label in LABELS:
        value = bindings.get(label)
        if value and value != "off":
            out.append(f"{label} = {value}")
    out.append("")
    return "\n".join(out)


# ---------------------------------------------------------------------------
# Commands
# ---------------------------------------------------------------------------

def choose_profile(slugs: list[str]) -> str:
    if not slugs:
        raise ProfileError("no profiles found")
    for index, slug in enumerate(slugs):
        print(f"  {index}  {slug}")
    answer = input(f"Which profile [0-{len(slugs) - 1}]? ").strip()
    if not answer.isdigit() or int(answer) >= len(slugs):
        raise ProfileError(f"{answer!r} is not one of the listed numbers")
    return slugs[int(answer)]


def backend_for(args) -> Backend:
    return BACKENDS[args.backend]


def cmd_list(args) -> int:
    dirs = profile_dirs(args.profiles)
    slugs = list_profiles(dirs)
    if not slugs:
        raise ProfileError("no profiles found in: " + ", ".join(str(d) for d in dirs))
    active = active_profile(backend_for(args))
    for slug in slugs:
        profile = load_profile(slug, dirs)
        mark = "*" if slug == active else " "
        print(f"{mark} {slug:<16} {profile.name}")
    return 0


def cmd_show(args) -> int:
    dirs = profile_dirs(args.profiles)
    slug = args.profile or active_profile(backend_for(args)) or choose_profile(list_profiles(dirs))
    print(render_layout(load_profile(slug, dirs)))
    return 0


def cmd_compile(args) -> int:
    profile = load_profile(args.profile, profile_dirs(args.profiles))
    warn_unknown(profile)
    sys.stdout.write(backend_for(args).render(profile))
    return 0


def cmd_apply(args) -> int:
    dirs = profile_dirs(args.profiles)
    slug = args.profile or choose_profile(list_profiles(dirs))
    profile = load_profile(slug, dirs)
    backend = backend_for(args)
    if warn_unknown(profile) and not args.allow_unknown_keys:
        raise ProfileError("refusing to apply; re-run with --allow-unknown-keys to override")
    content = backend.render(profile)
    require_root(["apply", slug, *common_flags(args)], args.dry_run)
    install(backend, content, args.dry_run)
    print(f"active profile: {slug} ({profile.name}) via {backend.name}")
    return 0


def cmd_off(args) -> int:
    backend = backend_for(args)
    require_root(["off", *common_flags(args)], args.dry_run)
    if not backend.path.exists():
        print(f"no {backend.name} mapping installed; nothing to do")
        return 0
    if args.dry_run:
        print(f"would remove {backend.path}")
    else:
        backend.path.unlink()
    backend.deactivate(args.dry_run)
    print(f"{backend.name} mapping removed; the keypad is back to its stock layout")
    return 0


def cmd_status(args) -> int:
    for name, backend in BACKENDS.items():
        active = active_profile(backend)
        if active is None:
            print(f"{name:<6} no mapping installed")
        else:
            print(f"{name:<6} active profile: {active}  ({backend.path})")
    return 0


def cmd_check(args) -> int:
    dirs = profile_dirs(args.profiles)
    backend = backend_for(args)
    failures = 0
    for slug in list_profiles(dirs):
        try:
            profile = load_profile(slug, dirs)
            backend.render(profile)
        except ProfileError as exc:
            print(f"FAIL {slug}: {exc}", file=sys.stderr)
            failures += 1
            continue
        unknown = unknown_key_names(profile)
        print(f"{'WARN' if unknown else 'ok  '} {slug:<16} {len(profile.bindings)} bindings")
        for label, value in unknown:
            print(f"       {label} = {value}  (not a keyboard key; treated as a macro)",
                  file=sys.stderr)
    return 1 if failures else 0


def cmd_keys(args) -> int:
    pattern = (args.pattern or "").lower()
    matches = [(n, c) for n, c in KEYBOARD_KEYS.items() if pattern in n]
    if not matches:
        raise ProfileError(f"no keyboard key matches {args.pattern!r}")
    for name, code in sorted(matches, key=lambda item: item[1]):
        print(f"{name:<20} {code}")
    print(f"\n{len(matches)} of {len(KEYBOARD_KEYS)} keyboard keys", file=sys.stderr)
    return 0


def cmd_import(args) -> int:
    sys.stdout.write(import_map(Path(args.file)))
    return 0


def unknown_key_names(profile: Profile) -> list[tuple[str, str]]:
    """Bindings that are neither a keyboard key, an unbind, nor a macro."""
    out = []
    for label, value in sorted(profile.bindings.items()):
        resolved = resolve(profile, label, value)
        if is_off(resolved) or is_plain_key(resolved):
            continue
        if any(ch in resolved for ch in "(+-") or resolved.startswith("macro"):
            continue  # a keyd action or modifier chord; keyd check validates it
        out.append((label, value))
    return out


def warn_unknown(profile: Profile) -> bool:
    unknown = unknown_key_names(profile)
    for label, value in unknown:
        print(f"warning: {label} = {value} is not a keyboard key", file=sys.stderr)
    return bool(unknown)


def common_flags(args) -> list[str]:
    flags = ["--backend", args.backend]
    if args.profiles:
        flags += ["--profiles", str(Path(args.profiles).expanduser().resolve())]
    if getattr(args, "dry_run", False):
        flags.append("--dry-run")
    if getattr(args, "allow_unknown_keys", False):
        flags.append("--allow-unknown-keys")
    return flags


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="tartarus", description="Remap a Razer Tartarus V2 keypad per game."
    )
    parser.add_argument("--profiles", metavar="DIR", help="directory holding *.profile files")
    parser.add_argument(
        "--backend", choices=sorted(BACKENDS), default="keyd",
        help="remapping backend (default: keyd; hwdb cannot run macros)",
    )
    sub = parser.add_subparsers(dest="command")

    def add(name, func, help_text):
        sp = sub.add_parser(name, help=help_text)
        sp.set_defaults(func=func)
        return sp

    add("list", cmd_list, "list profiles, marking the active one")
    add("show", cmd_show, "draw the keypad with its bindings").add_argument(
        "profile", nargs="?", help="defaults to the active profile"
    )
    add("compile", cmd_compile, "print the backend config (no privileges needed)").add_argument(
        "profile"
    )
    apply_parser = add("apply", cmd_apply, "install and activate a profile")
    apply_parser.add_argument("profile", nargs="?", help="asked for if omitted")
    apply_parser.add_argument("--dry-run", action="store_true", help="show what would change")
    apply_parser.add_argument(
        "--allow-unknown-keys", action="store_true", help="apply despite unknown key names"
    )
    off_parser = add("off", cmd_off, "remove the mapping, restore the stock layout")
    off_parser.add_argument("--dry-run", action="store_true", help="show what would change")

    add("status", cmd_status, "print the active profile for each backend")
    add("check", cmd_check, "validate every profile")
    add("keys", cmd_keys, "list the keyboard keys a profile may use").add_argument(
        "pattern", nargs="?", help="only show keys whose name contains this"
    )
    add("import", cmd_import, "convert an old .map file into a profile").add_argument(
        "file", help="path to a hand-written hwdb .map"
    )
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    if not getattr(args, "command", None):
        parser.print_help()
        return 2
    for flag, default in (("dry_run", False), ("allow_unknown_keys", False), ("profile", None)):
        if not hasattr(args, flag):
            setattr(args, flag, default)
    try:
        return args.func(args)
    except ProfileError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1
    except PermissionError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1
    except subprocess.CalledProcessError as exc:
        print(f"error: {' '.join(exc.cmd)} failed with status {exc.returncode}", file=sys.stderr)
        return exc.returncode
    except KeyboardInterrupt:
        return 130


if __name__ == "__main__":
    sys.exit(main())
