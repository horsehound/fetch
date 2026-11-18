#!/bin/sh
# Installs the Fetch hub on a Linux machine with systemd (a Proxmox LXC, a
# VPS, a NAS), or updates one that is already there. Run it as root:
#
#   curl -fsSL https://raw.githubusercontent.com/horsehound/fetch/main/hub/install.sh | sh
#
# It installs the newest release to /usr/local/bin/fetch (checked against the
# release's SHA256SUMS), sets up the fetch-hub service if it isn't there yet,
# restarts it if it is, and turns on a daily update check. Settings, all
# optional, as environment variables:
#
#   FETCH_AUTO_UPDATE=0     no daily update check (update with: fetch update)
#   FETCH_REPO=you/fetch    install from your fork's releases
set -eu

repo=${FETCH_REPO:-horsehound/fetch}
auto=${FETCH_AUTO_UPDATE:-1}

fail() {
	echo "fetch install: $*" >&2
	exit 1
}

[ "$(id -u)" = 0 ] || fail "run this as root (for example: curl -fsSL ... | sudo sh)"
command -v systemctl >/dev/null 2>&1 || fail "this needs systemd. Without it, run the hub with Docker (see the README)."
for tool in curl sha256sum install; do
	command -v "$tool" >/dev/null 2>&1 || fail "$tool is missing; install it first"
done
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) fail "no hub build for $(uname -m) yet" ;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
releases=${FETCH_RELEASES_URL:-"https://github.com/$repo/releases/latest/download"} # overrides for testing
files=${FETCH_FILES_URL:-"https://raw.githubusercontent.com/$repo/main/hub"}

echo "Downloading the newest release of $repo for linux-$arch"
curl -fsSL -o "$tmp/fetch" "$releases/fetch-linux-$arch"
curl -fsSL -o "$tmp/SHA256SUMS" "$releases/SHA256SUMS"
sum=$(awk -v name="fetch-linux-$arch" '$2 == name || $2 == "*" name { print $1 }' "$tmp/SHA256SUMS")
[ -n "$sum" ] || fail "the release lists no checksum for fetch-linux-$arch"
echo "$sum  $tmp/fetch" | sha256sum -c - >/dev/null || fail "the download doesn't match the release's checksum; nothing was changed"
chmod 755 "$tmp/fetch"
new=$("$tmp/fetch" version) || fail "the downloaded hub doesn't run on this machine"

old=""
[ -x /usr/local/bin/fetch ] && old=$(/usr/local/bin/fetch version 2>/dev/null || true)
install -m 755 "$tmp/fetch" /usr/local/bin/fetch

# The hub's own service is only written the first time, so changes you made
# to it (another port, another data folder) stay. The update units are ours.
fresh=0
if [ ! -f /etc/systemd/system/fetch-hub.service ]; then
	curl -fsSL -o /etc/systemd/system/fetch-hub.service "$files/fetch-hub.service"
	fresh=1
fi
curl -fsSL -o /etc/systemd/system/fetch-hub-update.service "$files/fetch-hub-update.service"
curl -fsSL -o /etc/systemd/system/fetch-hub-update.timer "$files/fetch-hub-update.timer"
systemctl daemon-reload

if [ "$auto" = 0 ]; then
	systemctl disable --now fetch-hub-update.timer >/dev/null 2>&1 || true
	updates="off: update with  fetch update"
else
	systemctl enable --now fetch-hub-update.timer >/dev/null 2>&1
	updates="daily, automatic (turn off: systemctl disable --now fetch-hub-update.timer)"
fi

if [ "$fresh" = 1 ]; then
	systemctl enable --now fetch-hub
	sleep 2
	echo
	echo "Fetch hub $new is running."
	journalctl -u fetch-hub --no-pager -o cat | grep -E '^ +(open|token) ' | tail -n 2 || true
	echo
	echo "Set up your first device with this address and token (see \"Pair your devices\" in the README)."
else
	systemctl restart fetch-hub
	if [ -n "$old" ] && [ "$old" != "$new" ]; then
		echo "Fetch hub updated: $old -> $new"
	else
		echo "Fetch hub $new reinstalled and restarted."
	fi
fi
echo "Updates: $updates"
