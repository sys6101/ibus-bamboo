#!/bin/sh
# Installs a prebuilt ibus-bamboo from the release archive.
# Usage: sudo ./install.sh            install or update
#        sudo ./install.sh uninstall  remove the files this script installed
set -e

PREFIX=/usr
LIB_DIR=$PREFIX/lib/ibus-bamboo
DATA_DIR=$PREFIX/share/ibus-bamboo
COMPONENT=$PREFIX/share/ibus/component/bamboo.xml
DESKTOP=$PREFIX/share/applications/ibus-setup-Bamboo.desktop
HERE=$(cd "$(dirname "$0")" && pwd)

if [ "$(id -u)" -ne 0 ]; then
	echo "Hãy chạy bằng sudo: sudo $0 $*" >&2
	exit 1
fi

if [ "$1" = "uninstall" ]; then
	if dpkg -s ibus-bamboo >/dev/null 2>&1; then
		echo "Gói ibus-bamboo của apt đang cài: gỡ bằng 'sudo apt remove ibus-bamboo'." >&2
		exit 1
	fi
	rm -rf "$LIB_DIR" "$DATA_DIR"
	rm -f "$COMPONENT" "$DESKTOP"
	echo "Đã gỡ ibus-bamboo. Chạy 'ibus restart' (không cần sudo)."
	exit 0
fi

mkdir -p "$LIB_DIR" "$DATA_DIR" "$(dirname "$COMPONENT")" "$(dirname "$DESKTOP")"
# Replace the engine by renaming, so a running engine keeps its old file.
install -m 755 "$HERE/ibus-engine-bamboo" "$LIB_DIR/.ibus-engine-bamboo.new"
mv -f "$LIB_DIR/.ibus-engine-bamboo.new" "$LIB_DIR/ibus-engine-bamboo"
cp -R -f "$HERE/data" "$HERE/icons" "$DATA_DIR/"
cp -f "$HERE/data/bamboo.xml" "$COMPONENT"
cp -f "$HERE/data/ibus-setup-Bamboo.desktop" "$DESKTOP"

echo "Đã cài ibus-bamboo $(cat "$HERE/VERSION" 2>/dev/null)."
if dpkg -s ibus-bamboo >/dev/null 2>&1; then
	apt-mark hold ibus-bamboo >/dev/null
	echo "Gói ibus-bamboo của apt đã được giữ (apt-mark hold) để 'apt upgrade' không ghi đè bản này."
	echo "Muốn quay về bản của apt: sudo apt-mark unhold ibus-bamboo && sudo apt install --reinstall ibus-bamboo"
fi
echo "Chạy 'ibus restart' (không cần sudo), hoặc đăng xuất rồi đăng nhập lại."
