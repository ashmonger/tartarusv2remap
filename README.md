# tartarusv2remap

Per-game key remapping for the **Razer Tartarus V2** keypad on Linux.

Profiles name the keypad's physical keys rather than HID scancodes, and are compiled
into a [keyd](https://github.com/rvaiya/keyd) configuration and applied without
rebuilding the udev hardware database.

```ini
# profiles/diablo4.profile
[profile]
name = Diablo 4
extends = default

[keys]
k11 = q
k12 = 1
k16 = c            # character sheet
thumb = e          # inventory
bar = space        # evade
```

## Install

Grab a `.deb` from the CI artifacts, or build one:

```bash
make deb
sudo apt install ./build/tartarus-remap_0.1.0-1_amd64.deb
```

That installs `/usr/bin/tartarus` and read-only profiles under
`/usr/share/tartarus/profiles`. Applying a profile needs `keyd`:

```bash
sudo apt install keyd
```

To run from a checkout instead:

```bash
make
export TARTARUS_PROFILES="$PWD/profiles"
./tartarus list
```

## Use

```bash
tartarus new eldenring          # create a profile and start binding
tartarus edit diablo4           # the keypad editor
tartarus list                   # profiles, active one marked
tartarus apply diablo4          # activate (asks for a password once)
tartarus status                 # what is active
tartarus off                    # back to the stock layout
tartarus keys shift             # search the 138 bindable keyboard keys
tartarus --dry-run apply hd2    # exactly what apply would write and run
```

### Migrating off the udev mapping

If a `.map` was ever applied through the old script, removing
`/etc/udev/hwdb.d/99-tartarus_v2.hwdb` is not enough. udev's keyboard builtin applies a
mapping by writing the device's in-kernel keycode table, and only ever sets the keys the
mapping names — nothing resets them. The keypad keeps that table until the input device is
re-created.

That has to be done before using keyd, because **keyd matches on what the device sends**.
While the kernel still holds an hwdb remap, a profile keyed on the stock key names cannot
match.

```bash
sudo rm /etc/udev/hwdb.d/99-tartarus_v2.hwdb
sudo systemd-hwdb update
# then unplug and replug the keypad, or rebind it in place:
for d in /sys/bus/usb/devices/*/idProduct; do
  dev=$(dirname "$d")
  [ "$(cat "$d")" = 022b ] && [ "$(cat "$dev/idVendor")" = 1532 ] || continue
  port=$(basename "$dev")
  echo "$port" | sudo tee /sys/bus/usb/drivers/usb/unbind; sleep 1
  echo "$port" | sudo tee /sys/bus/usb/drivers/usb/bind
done
```

`sudo python3 tests/verify_layout.py --stock` confirms it worked: all 25 inputs should
report their stock keys.

### keyd versions

`[ids]` is written as a bare `1532:022b`, which every keyd version accepts. The keypad
exposes several interfaces under that one id — two keyboard interfaces, only one of which
emits its keys, plus a mouse interface carrying the scroll wheel. If your keyd supports a
narrower syntax you can set it per profile:

```ini
[profile]
device = k:1532:022b          # keyboards only
device = 1532:022b:fab63040   # one specific interface, keyd 2.5 and later
```

`keyd monitor` prints the three-part ids for the machine it runs on. Whatever `device`
says is passed through unchanged.

`tartarus apply` runs `keyd check` before loading a config, but that subcommand was added
part-way through keyd's history; where it is missing, the step is skipped with a note and
a failed `keyd reload` still restores the previous config.

Only `apply` and `off` need root, and they re-run under `sudo` after the profile has
compiled, so a mistake never costs a password prompt. `--dry-run` never escalates.

### The editor

`tartarus edit` draws the keypad. The cursor is a double-bordered cap, bound keys are
highlighted, unbound keys are dim.

```
╔═════════╗╭─────────╮╭─────────╮╭─────────╮╭─────────╮
║   k01   ║│   k02   ││   k03   ││   k04   ││   k05   │
║   esc   ║│    ·    ││    ·    ││    ·    ││    ·    │
╚═════════╝╰─────────╯╰─────────╯╰─────────╯╰─────────╯
```

| Key | Does |
|---|---|
| arrows / `hjkl` | move around the keypad |
| `enter` | bind — type to filter the 138 keyboard keys, or type a macro |
| `c` | capture — press the key this cap should send |
| `x` | unbind |
| `s` | save |
| `a` | save and apply |
| `tab` | next profile |
| `q` | quit |

Capture cannot see bare modifiers: `capslock`, `leftshift` and `leftalt` never reach a
terminal application, so those must be typed.

## Writing a profile

Profiles are searched for in this order, so your own copy always shadows a packaged one:

1. `$TARTARUS_PROFILES`
2. `~/.config/tartarus/profiles`
3. `/etc/tartarus/profiles`
4. `/usr/share/tartarus/profiles` (shipped by the package)

`tartarus new` writes to `--profiles` if given, else `$TARTARUS_PROFILES` if set, else
`~/.config/tartarus/profiles`. Where a new profile lands never depends on your current
directory, and the packaged profiles are never edited in place. A profile created this
way still resolves `extends = default` against the packaged `default.profile`.

Key names, in reading order:

```
k01 k02 k03 k04 k05        the 19 backlit keys
k06 k07 k08 k09 k10
k11 k12 k13 k14 k15
k16 k17 k18 k19

thumb                      Hyperesponse thumb key
bar                        spacebar actuator
pad_up pad_down pad_left pad_right    8-way thumb pad (cardinals only)
pad = w s a d              shorthand for all four
```

- A key you do not name **sends nothing**. Use `off` to drop a binding inherited
  through `extends`.
- **Never use `0` to disable a key.** `0` is the digit zero (`KEY_0`, keycode 11), so it
  types a zero rather than doing nothing. This is the bug the old `.map` files had.
- `tartarus keys [PATTERN]` lists every valid right-hand side.

### Macros

```ini
[macros]
count = 1 100ms 2 100ms 3

[keys]
k02 = C-c                  # chord: ctrl held while c is pressed
k03 = macro(C-x 50ms v)    # inline sequence
k04 = @count               # named macro, inherited like a binding
```

`keyd(1)` documents the full action syntax. Whatever you write is validated by
`keyd check` before it is loaded, and the previous configuration is restored if it is
rejected.

## Migrating an old `.map`

```bash
python3 tests/oracle/tartarus.py import ~/.config/tartarus/mygame.map \
  > profiles/mygame.profile
```

It reports every `=0` and empty value it converts to a real unbind.

## Development

```bash
make            # build
make check      # the full offline suite: no keypad, no root, no keyd
make deb        # build the package
```

`tests/run.sh` runs unit tests, drives the editor through a real pty, verifies the
generated key table still matches `linux/input-event-codes.h`, and cross-checks the
generated keyd config byte-for-byte against the reference implementation in
`tests/oracle/tartarus.py`.

### Checking against the real keypad

`tests/verify_layout.py` is a manual check, not part of `make check`: it needs root and
the keypad plugged in.

```bash
sudo python3 tests/verify_layout.py           # compare against the installed mapping
sudo python3 tests/verify_layout.py --stock   # compare against the stock layout
sudo python3 tests/verify_layout.py --report   # just record what each key sends
```

It walks the 25 inputs, records the keycode each physically emits, and compares against
whatever is installed — a keyd config, a udev hwdb file, or the stock layout. It is
read-only, and grabs the keypad while running so its keys do not type into your terminal.

This is how the layout table was confirmed: all 25 inputs matched the mapping that was
applied at the time. It is also how to confirm that `tartarus apply` worked — after
applying a profile, keys the profile leaves unbound should come back `silent` rather than
sending the digit zero.

## Layout

```
cmd/tartarus/        the tool
profiles/            profiles shipped by the package
packaging/deb/       Debian packaging
tests/               offline test suite
tests/oracle/        reference implementation, used only to cross-check output
bin/tartarus.sh      the original shell script (superseded, kept for now)
config/tartarus/     the original .map files (superseded, kept for now)
```

## Status

The tool is not yet validated against real hardware. `keyd check` and `keyd reload` are
invoked but have not been observed succeeding on a keypad, and the original shell script
and `.map` files are deliberately left in place until it has been.

## License

MIT. See `LICENSE`.
