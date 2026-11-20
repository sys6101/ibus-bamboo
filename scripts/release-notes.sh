#!/bin/sh
# Prints the release notes: the commits since the previous release, grouped
# by type, then how to install.
# Usage: scripts/release-notes.sh PREVIOUS_TAG REVISION VERSION COMMIT
# PREVIOUS_TAG may be empty for the first release.
set -eu
PREV=$1
REV=$2
VERSION=$3
COMMIT=$4
RANGE=$REV
[ -n "$PREV" ] && RANGE=$PREV..$REV

section() {
	# $1 title, $2 regexp of the commit types
	lines=$(git log --no-merges --format='%s (%h)' "$RANGE" | grep -E "^($2)(\([^)]*\))?!?: " |
		sed -E 's/^[a-z]+(\([^)]*\))?!?: /- /' || true)
	[ -n "$lines" ] && printf '## %s\n%s\n\n' "$1" "$lines"
	return 0
}

section "Sửa lỗi" "fix"
section "Tính năng" "feat"
section "Cải tiến" "perf|refactor"
section "Tài liệu" "docs"

ENGINE=ibus-engine-bamboo-$VERSION-$COMMIT-x86_64
FENCE='```'
cat <<EOF
## Cài đặt
Xem [README](https://github.com/sys6101/ibus-bamboo#cài-bản-build-sẵn-copy-file). Chỉ thay file engine khi đã cài:

${FENCE}sh
wget -O ibus-engine-bamboo-linux-amd64 https://github.com/sys6101/ibus-bamboo/releases/download/$VERSION/$ENGINE
ibus exit
sudo install -m 755 ibus-engine-bamboo-linux-amd64 /usr/lib/ibus-bamboo/ibus-engine-bamboo
sudo apt-mark hold ibus-bamboo
ibus-daemon -drx
${FENCE}

Cần glibc ≥ 2.34 (Ubuntu 22.04+), x86_64. Kiểm tra file tải về bằng \`sha256sum -c SHA256SUMS\`.
EOF
