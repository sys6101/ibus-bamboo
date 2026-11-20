#!/bin/sh
# Builds the prebuilt release files into OUT_DIR. The engine reports
# VERSION-COMMIT with -version.
#
#   ibus-bamboo-VERSION-COMMIT-x86_64.tar.gz  engine, data and install.sh
#   ibus-engine-bamboo-VERSION-COMMIT-x86_64  the engine alone
#   SHA256SUMS
# Run from the source tree on Ubuntu 22.04 (glibc 2.34, the oldest the
# release supports) with golang-go, libgtk-3-dev, libx11-dev, libxtst-dev and
# pkg-config installed.
# Usage: scripts/build-release.sh VERSION COMMIT OUT_DIR
set -eu
VERSION=$1
COMMIT=$2
OUT=$3
NAME=$VERSION-$COMMIT-x86_64
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# Some tests save the configuration, which needs its directory.
mkdir -p "${XDG_CONFIG_HOME:-$HOME/.config}/ibus-bamboo"
go test -mod=vendor -count=1 ./...
CGO_ENABLED=1 go build -mod=vendor -trimpath -buildvcs=false \
	-ldflags "-w -s -X main.Version=$VERSION-$COMMIT" -o "$WORK/ibus-engine-bamboo" .

PKG=$WORK/pkg/ibus-bamboo
mkdir -p "$PKG" "$OUT"
cp -r data icons LICENSE "$PKG/"
rm -f "$PKG/data/bamboo-freebsd.xml"
cp "$WORK/ibus-engine-bamboo" "$PKG/"
cp scripts/release-install.sh "$PKG/install.sh"
chmod 755 "$PKG/install.sh"
echo "$VERSION-$COMMIT" >"$PKG/VERSION"

tar -C "$WORK/pkg" -czf "$OUT/ibus-bamboo-$NAME.tar.gz" ibus-bamboo
cp "$WORK/ibus-engine-bamboo" "$OUT/ibus-engine-bamboo-$NAME"
(cd "$OUT" && sha256sum "ibus-bamboo-$NAME.tar.gz" "ibus-engine-bamboo-$NAME" >SHA256SUMS)
ls -l "$OUT"
