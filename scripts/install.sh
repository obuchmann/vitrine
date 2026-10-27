#!/bin/sh
# Installs vitrine on Linux, macOS and WSL from the GitHub releases:
#
#   curl -fsSL https://github.com/obuchmann/vitrine/releases/latest/download/install.sh | sh
#
# No root needed. Environment:
#   VITRINE_VERSION       a release, e.g. v0.1.0 (default: the latest)
#   VITRINE_INSTALL_DIR   where the binary goes (default: ~/.local/bin)
#   VITRINE_DOWNLOAD_URL  where the release files are, e.g. for tests
set -eu

repo=obuchmann/vitrine
dir=${VITRINE_INSTALL_DIR:-$HOME/.local/bin}
if [ -n "${VITRINE_DOWNLOAD_URL:-}" ]; then
	download=$VITRINE_DOWNLOAD_URL
elif [ -n "${VITRINE_VERSION:-}" ]; then
	download=https://github.com/$repo/releases/download/$VITRINE_VERSION
else
	download=https://github.com/$repo/releases/latest/download
fi

fail() {
	echo "vitrine install: $*" >&2
	exit 1
}

case $(uname -s) in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) fail "unsupported system $(uname -s). On Windows, use install.ps1 in PowerShell." ;;
esac
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) fail "unsupported CPU $(uname -m)" ;;
esac

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -q -O "$2" "$1"; }
else
	fail "curl or wget is needed"
fi
if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
elif command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
else
	fail "sha256sum or shasum is needed to check the download"
fi

archive=vitrine_${os}_${arch}.tar.gz
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $download/$archive"
fetch "$download/$archive" "$tmp/$archive" || fail "download failed: $download/$archive"
fetch "$download/checksums.txt" "$tmp/checksums.txt" || fail "download failed: $download/checksums.txt"
want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || fail "$archive is not in checksums.txt"
[ "$(sha256 "$tmp/$archive")" = "$want" ] || fail "checksum mismatch for $archive"

tar -xzf "$tmp/$archive" -C "$tmp" vitrine
mkdir -p "$dir"
# Replace by renaming, which also works while vitrine runs.
cp "$tmp/vitrine" "$dir/.vitrine.new"
chmod 755 "$dir/.vitrine.new"
mv -f "$dir/.vitrine.new" "$dir/vitrine"

echo "Installed vitrine $("$dir/vitrine" version) to $dir/vitrine"
case ":$PATH:" in
*":$dir:"*) ;;
*) echo "Add $dir to your PATH, e.g. in ~/.profile: export PATH=\"$dir:\$PATH\"" ;;
esac
cat <<'EOF'

Next, register vitrine with your agent, e.g. for Claude Code:
  claude mcp add --scope user vitrine -- vitrine mcp
Other agents: https://github.com/obuchmann/vitrine
If vitrine was already running, restart it: "vitrine doctor" tells you how.
EOF
