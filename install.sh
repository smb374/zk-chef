#!/bin/sh
# Installs the zk-chef binary from a GitHub release, then runs `zk-chef install`.
# Env: ZK_CHEF_BASE_URL, ZK_CHEF_BIN_DIR, ZK_CHEF_INSTALL_ARGS.
set -eu

BASE_URL=${ZK_CHEF_BASE_URL:-https://github.com/smb374/zk-chef/releases/latest/download}
BIN_DIR=${ZK_CHEF_BIN_DIR:-$HOME/.local/bin}

case $(uname -s) in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) echo "unsupported OS" >&2; exit 1 ;;
esac
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) echo "unsupported arch" >&2; exit 1 ;;
esac
asset=zk-chef_${os}_${arch}

fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$1"
	else
		wget -qO- "$1"
	fi
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1"
	else
		shasum -a 256 "$1"
	fi
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fetch "$BASE_URL/checksums.txt" >"$tmp/checksums.txt"
fetch "$BASE_URL/$asset" >"$tmp/$asset"

want=$(awk -v n="$asset" '$2 == n || $2 == "*" n { print $1; exit }' "$tmp/checksums.txt")
[ -n "$want" ] || { echo "no checksum listed for $asset" >&2; exit 1; }
got=$(sha256 "$tmp/$asset" | awk '{ print $1 }')
[ "$got" = "$want" ] || { echo "checksum mismatch for $asset" >&2; exit 1; }

mkdir -p "$BIN_DIR"
install -m 0755 "$tmp/$asset" "$BIN_DIR/.zk-chef.new"
mv -f "$BIN_DIR/.zk-chef.new" "$BIN_DIR/zk-chef"

case ":$PATH:" in
*":$BIN_DIR:"*) ;;
*) echo "add $BIN_DIR to your PATH" >&2 ;;
esac

set -f
rc=0
"$BIN_DIR/zk-chef" install ${ZK_CHEF_INSTALL_ARGS:-} || rc=$?
exit "$rc"
