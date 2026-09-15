#!/usr/bin/env bash
#
# Lays out the npm packages for a release under dist/npm/, using the binaries
# `make release` put in dist/. Nothing here is checked in: every package.json is
# generated so that the version lives in exactly one place — the git tag.
#
#   usage: scripts/npm-dist.sh v0.1.2
#
# Publish order matters. The platform packages have to be on the registry before
# the wrapper that depends on them; `make npm-publish` does them in that order.

set -euo pipefail

cd "$(dirname "$0")/.."

TAG=${1:?usage: scripts/npm-dist.sh <tag>}
VERSION=${TAG#v}   # npm versions are bare semver, no leading v

# The default VERSION is `git describe`, which on any commit past a tag looks
# like 0.1.1-3-gabc1234[-dirty]. npm would take that as a prerelease of 0.1.1
# and publish it — catch it here instead.
case $VERSION in
*-dirty | *[0-9]-g[0-9a-f]*)
	echo "npm-dist: \"$TAG\" is a git-describe version, not a release tag." >&2
	echo "  Name the version explicitly, keeping whichever target you ran:" >&2
	echo "    make npm-dist    VERSION=${TAG%%-*}" >&2
	echo "    make npm-publish VERSION=${TAG%%-*}" >&2
	echo "  A clean checkout of the tagged commit needs no VERSION at all." >&2
	exit 1
	;;
esac

if ! printf '%s' "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$'; then
	echo "npm-dist: \"$VERSION\" is not a version npm will accept" >&2
	exit 1
fi

DIST=dist
OUT=$DIST/npm
SCOPE=@digicli

# The wrapper is scoped because the bare name is unclaimable: npm strips
# punctuation before comparing names, so "digicli" collides with "digi-cli"
# (published 2022, abandoned) and the registry rejects it with a 403. Scoped
# names skip that check. The directory stays "digicli" — npm-publish.sh keys
# the publish order off it, and the installed command is still digicli either
# way, since that comes from "bin" and not from the package name.
WRAPPER=$SCOPE/cli

# go-target:npm-os:npm-cpu — one platform package each.
PLATFORMS=(
	"darwin-arm64:darwin:arm64"
	"darwin-amd64:darwin:x64"
	"linux-arm64:linux:arm64"
	"linux-amd64:linux:x64"
	"windows-arm64:win32:arm64"
	"windows-amd64:win32:x64"
)

rm -rf "$OUT"
mkdir -p "$OUT"

optional_deps=""

for entry in "${PLATFORMS[@]}"; do
	IFS=: read -r gotarget npmos npmcpu <<<"$entry"

	name="$npmos-$npmcpu"
	dir="$OUT/$name"
	src="$DIST/digicli-$gotarget"
	exe=digicli
	if [ "$npmos" = win32 ]; then
		src="$src.exe"
		exe=digicli.exe
	fi

	if [ ! -f "$src" ]; then
		echo "missing $src — run 'make release' first" >&2
		exit 1
	fi

	# No "bin" entry in these packages, deliberately: only the wrapper declares
	# one, so nothing competes for the name. npm keeps the mode bits from the
	# tarball, which is what leaves the binary executable.
	mkdir -p "$dir/bin"
	install -m 755 "$src" "$dir/bin/$exe"
	cp LICENSE "$dir/LICENSE"

	cat >"$dir/package.json" <<JSON
{
  "name": "$SCOPE/$name",
  "version": "$VERSION",
  "description": "The digicli binary for $npmos $npmcpu.",
  "homepage": "https://github.com/10txn/digicli",
  "repository": {
    "type": "git",
    "url": "git+https://github.com/10txn/digicli.git"
  },
  "license": "MIT",
  "author": "10txn",
  "os": ["$npmos"],
  "cpu": ["$npmcpu"],
  "files": ["bin", "LICENSE"],
  "preferUnplugged": true
}
JSON

	optional_deps="$optional_deps    \"$SCOPE/$name\": \"$VERSION\",
"
done

# Exact versions, not ranges: the wrapper and its binary are one release.
optional_deps=${optional_deps%,$'\n'}

# The wrapper users actually install.
mkdir -p "$OUT/digicli/bin"
install -m 755 npm/digicli/bin/digicli.js "$OUT/digicli/bin/digicli.js"
cp LICENSE "$OUT/digicli/LICENSE"
cp README.md "$OUT/digicli/README.md"

cat >"$OUT/digicli/package.json" <<JSON
{
  "name": "$WRAPPER",
  "version": "$VERSION",
  "description": "A local-first agentic coding assistant for the terminal.",
  "keywords": ["cli", "ai", "agent", "coding-assistant", "ollama", "tui", "local"],
  "homepage": "https://github.com/10txn/digicli#readme",
  "bugs": "https://github.com/10txn/digicli/issues",
  "repository": {
    "type": "git",
    "url": "git+https://github.com/10txn/digicli.git"
  },
  "license": "MIT",
  "author": "10txn",
  "bin": {
    "digicli": "bin/digicli.js"
  },
  "files": ["bin", "LICENSE", "README.md"],
  "engines": {
    "node": ">=18"
  },
  "optionalDependencies": {
$optional_deps
  }
}
JSON

echo "$OUT/ ready at $VERSION:"
ls -1 "$OUT"
