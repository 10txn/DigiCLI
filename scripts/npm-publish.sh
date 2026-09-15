#!/usr/bin/env bash
#
# Publishes what scripts/npm-dist.sh laid out in dist/npm.
#
#   usage: scripts/npm-publish.sh [dist-tag]
#
# Platform packages go first and the wrapper last, because the wrapper depends
# on them by exact version: publish it first and every `npm i -g @digicli/cli` in
# between fails to resolve.
#
# Safe to re-run. With 2FA on, npm asks for a one-time password per package —
# seven prompts, and abandoning halfway leaves some versions published. Each
# package is checked against the registry first and skipped if that exact
# version is already up, so a second run finishes the job rather than dying on
# the first EPUBLISHCONFLICT.

set -euo pipefail

cd "$(dirname "$0")/.."

TAG=${1:-latest}
OUT=dist/npm

[ -d "$OUT" ] || { echo "no $OUT — run 'make npm-dist' first" >&2; exit 1; }

published=0
skipped=0

publish_one() {
	dir=$1
	name=$(node -p "require('./$dir/package.json').name")
	version=$(node -p "require('./$dir/package.json').version")

	if npm view "$name@$version" version >/dev/null 2>&1; then
		echo "==> $name@$version is already published, skipping"
		skipped=$((skipped + 1))
		return
	fi

	echo "==> publishing $name@$version"
	(cd "$dir" && npm publish --access public --tag "$TAG")
	published=$((published + 1))
}

for dir in "$OUT"/*/; do
	dir=${dir%/}
	if [ "$(basename "$dir")" = digicli ]; then
		continue
	fi
	publish_one "$dir"
done

# The wrapper last.
publish_one "$OUT/digicli"

echo
echo "published $published, skipped $skipped"
if [ "$published" -gt 0 ]; then
	echo "verify with: npm i -g @digicli/cli && digicli --version"
fi
