#!/bin/sh
# Usage: build.sh OUTDIR VERSION
# Builds the four release binaries and checksums.txt into OUTDIR.
set -eu

[ $# -eq 2 ] || { echo "usage: build.sh OUTDIR VERSION" >&2; exit 2; }
mkdir -p "$1"
OUT=$(cd "$1" && pwd)
VERSION=$2
cd "$(dirname "$0")/../.."

for os in linux darwin; do
	for arch in amd64 arm64; do
		CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
			-ldflags "-s -w -X github.com/smb374/zk-chef/internal/version.Version=$VERSION" \
			-o "$OUT/zk-chef_${os}_${arch}" .
	done
done

cd "$OUT"
if command -v sha256sum >/dev/null 2>&1; then
	sha256sum zk-chef_linux_amd64 zk-chef_linux_arm64 zk-chef_darwin_amd64 zk-chef_darwin_arm64 >checksums.txt
else
	shasum -a 256 zk-chef_linux_amd64 zk-chef_linux_arm64 zk-chef_darwin_amd64 zk-chef_darwin_arm64 >checksums.txt
fi
