#!/bin/sh
# Install macdoc from a GitHub release, verifying its sha256 first.
#   MACDOC_VERSION=v0.1.0  pick a release (default: latest)
#   MACDOC_PREFIX=DIR      install into DIR/bin (default: ~/.local)
# Never uses sudo. Refuses to install on a checksum mismatch.
set -eu

REPO="sagnik26/macdoc"
VERSION="${MACDOC_VERSION:-latest}"
PREFIX="${MACDOC_PREFIX:-$HOME/.local}"

die() { printf 'macdoc install: %s\n' "$1" >&2; exit 1; }

[ "$(uname -s)" = "Darwin" ] || die "macdoc supports macOS only"
case "$(uname -m)" in
  arm64) arch=arm64 ;;
  x86_64) arch=amd64 ;;
  *) die "unsupported architecture: $(uname -m)" ;;
esac

if [ "$VERSION" = "latest" ]; then
  base="https://github.com/$REPO/releases/latest/download"
  tag=$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") || die "cannot resolve latest release"
  tag=${tag##*/}
else
  tag=$VERSION
  base="https://github.com/$REPO/releases/download/$tag"
fi
ver=${tag#v}
archive="macdoc_${ver}_darwin_${arch}.tar.gz"

tmp=$(mktemp -d) || die "cannot make a temporary directory"
trap 'rm -rf "$tmp"' EXIT INT TERM

printf 'Downloading %s (%s)\n' "$archive" "$tag"
curl -fsSL -o "$tmp/$archive" "$base/$archive" || die "download failed: $base/$archive"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" || die "download failed: checksums.txt"

want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || die "no checksum listed for $archive"
got=$(shasum -a 256 "$tmp/$archive" | awk '{ print $1 }')
[ "$want" = "$got" ] || die "checksum mismatch for $archive (expected $want, got $got); nothing installed"
printf 'sha256 verified\n'

mkdir "$tmp/x"
tar -xzf "$tmp/$archive" -C "$tmp/x" macdoc || die "archive has no macdoc binary"
mkdir -p "$PREFIX/bin" || die "cannot create $PREFIX/bin"
cp "$tmp/x/macdoc" "$PREFIX/bin/macdoc.new" && chmod 755 "$PREFIX/bin/macdoc.new" \
  && mv -f "$PREFIX/bin/macdoc.new" "$PREFIX/bin/macdoc" || die "cannot write to $PREFIX/bin"

printf 'Installed %s/bin/macdoc\n' "$PREFIX"
case ":$PATH:" in
  *":$PREFIX/bin:"*) ;;
  *) printf 'Add %s/bin to your PATH.\n' "$PREFIX" ;;
esac
printf 'Not notarized yet: if macOS blocks it, run: xattr -d com.apple.quarantine %s/bin/macdoc\n' "$PREFIX"
