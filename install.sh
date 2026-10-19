#!/bin/sh
# tgtui installer: an unofficial Telegram client for the terminal.
#
#   curl -fsSL https://raw.githubusercontent.com/raccoon-overlord-dev/tgtui/main/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- --uninstall
#
# Environment:
#   TGTUI_INSTALL_DIR   where to put the binary (default ~/.local/bin)
#   TGTUI_REPO          GitHub owner/repo to download from
#   TGTUI_RELEASE_URL   base URL of the release files (default: the repo's latest GitHub release)
set -eu

REPO="${TGTUI_REPO:-raccoon-overlord-dev/tgtui}"
DIR="${TGTUI_INSTALL_DIR:-$HOME/.local/bin}"
BASE="${TGTUI_RELEASE_URL:-https://github.com/$REPO/releases/latest/download}"

say() { printf '%s\n' "$*"; }
die() { printf 'tgtui install: %s\n' "$*" >&2; exit 1; }

fetch() { # url dest
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$1" -o "$2"
	elif command -v wget >/dev/null 2>&1; then
		wget -q "$1" -O "$2"
	else
		die "need curl or wget"
	fi
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		die "need sha256sum or shasum"
	fi
}

uninstall() {
	rm -f "$DIR/tgtui"
	say "removed $DIR/tgtui"
	cfg="${XDG_CONFIG_HOME:-$HOME/.config}/tgtui"
	data="${XDG_DATA_HOME:-$HOME/.local/share}/tgtui"
	cache="${XDG_CACHE_HOME:-$HOME/.cache}/tgtui"
	[ -d "$cfg" ] || [ -d "$data" ] || exit 0
	printf 'Also remove the settings (%s) and the local session and messages (%s)? [y/N] ' "$cfg" "$data"
	ans=""
	{ read -r ans </dev/tty; } 2>/dev/null || true # stdin is the script when piped; no tty = keep
	case "$ans" in
	y | Y | yes)
		rm -rf "$cfg" "$data" "$cache"
		for account in db-key db-key-test; do # the database keys in the OS keychain
			if command -v secret-tool >/dev/null 2>&1; then
				secret-tool clear service tgtui username "$account" 2>/dev/null || true
			elif command -v security >/dev/null 2>&1; then
				security delete-generic-password -s tgtui -a "$account" >/dev/null 2>&1 || true
			fi
		done
		say "removed $cfg and $data"
		say "the session stays listed in Telegram › Settings › Devices until you end it there"
		;;
	*) say "kept $cfg and $data" ;;
	esac
	exit 0
}

if [ $# -gt 0 ]; then
	[ "$1" = "--uninstall" ] || die "unknown argument: $1 (only --uninstall)"
	uninstall
fi

case $(uname -s) in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) die "unsupported OS $(uname -s): tgtui runs on Linux and macOS (use WSL on Windows)" ;;
esac
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) die "unsupported CPU $(uname -m)" ;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

name="tgtui_${os}_${arch}.tar.gz"
fetch "$BASE/checksums.txt" "$tmp/checksums.txt" || die "download failed: $BASE/checksums.txt"
want=$(grep " $name\$" "$tmp/checksums.txt" | cut -d' ' -f1)
[ -n "$want" ] || die "no $os/$arch build in this release yet"
say "downloading $name"
fetch "$BASE/$name" "$tmp/$name" || die "download failed: $BASE/$name"
[ "$(sha256 "$tmp/$name")" = "$want" ] || die "checksum mismatch for $name: download corrupted or tampered with"
tar -xzf "$tmp/$name" -C "$tmp" tgtui

mkdir -p "$DIR"
cp "$tmp/tgtui" "$DIR/.tgtui.new"
chmod 755 "$DIR/.tgtui.new"
mv -f "$DIR/.tgtui.new" "$DIR/tgtui" # atomic, and safe while an old tgtui is running
say "installed $("$DIR/tgtui" --version) to $DIR/tgtui"

case ":$PATH:" in
*":$DIR:"*) ;;
*)
	say ""
	say "$DIR is not on your PATH. Add it with:"
	case "${SHELL##*/}" in
	fish) say "  fish_add_path $DIR" ;;
	zsh) say "  echo 'export PATH=\"$DIR:\$PATH\"' >> ~/.zshrc" ;;
	*) say "  echo 'export PATH=\"$DIR:\$PATH\"' >> ~/.bashrc" ;;
	esac
	say "then open a new terminal."
	;;
esac
say "run: tgtui"
