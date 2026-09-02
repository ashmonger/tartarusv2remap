#!/bin/bash
# Every check runs offline: no keypad, no root, no keyd daemon.
set -uo pipefail
cd "$(dirname "$0")/.."
export GOTMPDIR="${GOTMPDIR:-$(mktemp -d)}"
export TARTARUS_PROFILES="$PWD/profiles"
fail=0
step() { printf '\n== %s\n' "$1"; }
ok() { [ "$1" -eq 0 ] && echo "  ok" || { echo "  FAILED"; fail=1; }; }

step "gofmt"
[ -z "$(gofmt -l cmd)" ]; ok $?

step "go vet"
go vet ./... ; ok $?

step "unit tests"
go test ./... ; ok $?

step "build"
CGO_ENABLED=0 go build -trimpath -o tartarus ./cmd/tartarus ; ok $?

step "the generated key table still matches the kernel header"
./tests/verify_keys.py ; ok $?

step "the editor renders and edits (driven through a pty)"
python3 tests/tui_drive.py ; ok $?

step "keyd output matches the reference implementation byte for byte"
same=0
for p in default diablo4 hd2 nms phasmophobia shapeofdreams macros-example; do
  python3 tests/oracle/tartarus.py --profiles "$TARTARUS_PROFILES" compile "$p" > "$GOTMPDIR/ref" 2>/dev/null
  ./tartarus compile "$p" > "$GOTMPDIR/got" 2>/dev/null
  cmp -s "$GOTMPDIR/ref" "$GOTMPDIR/got" || { echo "  differs: $p"; same=1; }
done
ok $same

step "new creates a profile, with flags on either side of the command"
scratch=$(mktemp -d)
cp profiles/default.profile profiles/diablo4.profile "$scratch/"
neworder=0
TARTARUS_PROFILES="$scratch" ./tartarus new after --from diablo4 --name "Flags After" --no-edit >/dev/null 2>&1 || neworder=1
grep -q 'name = Flags After' "$scratch/after.profile" || { echo "  --name after the command ignored"; neworder=1; }
grep -q 'k11 = q' "$scratch/after.profile" || { echo "  --from after the command ignored"; neworder=1; }
TARTARUS_PROFILES="$scratch" ./tartarus --no-edit --name "Flags Before" new before >/dev/null 2>&1 || neworder=1
grep -q 'name = Flags Before' "$scratch/before.profile" || { echo "  --name before the command ignored"; neworder=1; }
TARTARUS_PROFILES="$scratch" ./tartarus check >/dev/null 2>&1 || { echo "  new profiles do not validate"; neworder=1; }
rm -rf "$scratch"
ok $neworder

step "every shipped profile compiles to 25 bindings"
shipped=0
for f in profiles/*.profile; do
  p=$(basename "$f" .profile)
  [ "$(./tartarus compile "$p" | grep -c ' = ')" = 25 ] || { echo "  $p"; shipped=1; }
done
ok $shipped

step "a dry run needs no privileges"
./tartarus --dry-run apply diablo4 >/dev/null 2>&1 ; ok $?
./tartarus --dry-run off >/dev/null 2>&1 ; ok $?

step "--help exits 0"
./tartarus --help >/dev/null 2>&1 ; ok $?

printf '\n%s\n' "$([ $fail -eq 0 ] && echo 'ALL CHECKS PASSED' || echo 'SOME CHECKS FAILED')"
exit $fail
