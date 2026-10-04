#!/bin/sh
# Install env4ci from GitHub releases. No Go toolchain needed.
#
#   curl -sSfL https://raw.githubusercontent.com/bakhod1r/env4ci/main/install.sh | sh
#
# Environment:
#   ENV4CI_VERSION  release to install, e.g. v0.1.0 (default: latest)
#   BINDIR          install directory (default: /usr/local/bin if writable,
#                   else ~/.local/bin)
set -eu

REPO="bakhod1r/env4ci"

die() {
	echo "env4ci install: $*" >&2
	exit 1
}

need() {
	command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

need uname
need tar
if command -v curl >/dev/null 2>&1; then
	fetch() { curl -sSfL "$1" -o "$2"; }
	final_url() { curl -sSfLI -o /dev/null -w '%{url_effective}' "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -qO "$2" "$1"; }
	final_url() { wget -S --spider "$1" 2>&1 | sed -n 's/^ *[Ll]ocation: *//p' | tail -1 | tr -d '\r'; }
else
	die "curl or wget is required"
fi

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
linux | darwin) ;;
mingw* | msys* | cygwin*) die "on Windows download env4ci_<version>_windows_amd64.zip from https://github.com/$REPO/releases" ;;
*) die "unsupported OS: $os" ;;
esac

arch=$(uname -m)
case "$arch" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) die "unsupported architecture: $arch" ;;
esac

version=${ENV4CI_VERSION:-}
if [ -z "$version" ]; then
	version=$(final_url "https://github.com/$REPO/releases/latest")
	version=${version##*/}
	case "$version" in
	v*) ;;
	*) die "could not find the latest release" ;;
	esac
fi
num=${version#v}

archive="env4ci_${num}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$version"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "Downloading env4ci $version ($os/$arch)..."
fetch "$base/$archive" "$tmp/$archive" || die "download failed: $base/$archive"
fetch "$base/checksums.txt" "$tmp/checksums.txt" || die "download failed: checksums.txt"

want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || die "$archive is not listed in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/$archive" | awk '{ print $1 }')
elif command -v shasum >/dev/null 2>&1; then
	got=$(shasum -a 256 "$tmp/$archive" | awk '{ print $1 }')
else
	die "sha256sum or shasum is required to verify the download"
fi
[ "$got" = "$want" ] || die "checksum mismatch for $archive"

tar -xzf "$tmp/$archive" -C "$tmp" env4ci

bindir=${BINDIR:-}
if [ -z "$bindir" ]; then
	if [ -w /usr/local/bin ]; then
		bindir=/usr/local/bin
	else
		bindir="$HOME/.local/bin"
	fi
fi
mkdir -p "$bindir"
install -m 0755 "$tmp/env4ci" "$bindir/env4ci" 2>/dev/null || {
	cp "$tmp/env4ci" "$bindir/env4ci" && chmod 0755 "$bindir/env4ci"
} || die "cannot write to $bindir (set BINDIR or run with sudo)"

echo "Installed $("$bindir/env4ci" version) to $bindir/env4ci"
case ":$PATH:" in
*":$bindir:"*) ;;
*) echo "Note: $bindir is not on PATH; add it to your shell profile." ;;
esac
