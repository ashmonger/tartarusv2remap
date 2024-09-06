#!/bin/bash

## List of keycodes
## https://hackage.haskell.org/package/evdev-2.3.1.1/docs/Evdev-Codes.html

set -uo pipefail

tempscript=$(mktemp)

mapfile -t games < <(ls "$HOME/.config/tartarus")
nbg=$(( ${#games[*]} - 1 ))

home=$HOME

echo "List of games"
for i in $(seq 0 1 ${nbg}); do
        echo "$i ${games[$i]}"
done

echo "Which game [0-${nbg}]?"
read -r game

cat > "$tempscript" << EOF
#!/bin/bash
cat "$home/.config/tartarus/${games[$game]}" >/etc/udev/hwdb.d/99-tartarus_v2.hwdb
systemd-hwdb update
udevadm trigger
EOF

if [ $EUID != 0 ]; then
    sudo bash "$tempscript"
    exit $?
fi

