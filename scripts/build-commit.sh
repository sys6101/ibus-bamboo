#!/bin/sh
# Prints the short hash of the last commit that can change the build, the one
# the release files are named after. Commits that only touch documentation,
# the CI configuration or the Nix files do not count, so they do not trigger
# a new build. Needs the history (git clone without --depth).
# Usage: scripts/build-commit.sh [REVISION]
set -e
cd "$(dirname "$0")/.."
git log -1 --format=%h "${1:-HEAD}" -- . \
	':(exclude)*.md' \
	':(exclude)**/*.md' \
	':(exclude).gitignore' \
	':(exclude)**/LICENSE*' \
	':(exclude)docs/**' \
	':(exclude).github/**' \
	':(exclude)flake.nix' \
	':(exclude)flake.lock' \
	':(exclude)nix/**' \
	':(exclude)scripts/build-commit.sh' \
	':(exclude)scripts/release-notes.sh'
