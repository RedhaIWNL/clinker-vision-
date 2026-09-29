#!/usr/bin/env bash
# Publish a version: label the current commit v<date> (v2026.09.30, then v2026.09.30.2 the same day)
# and push the label. GitHub then tests and builds the images (.github/workflows/release.yml);
# the plant server installs it with the Update icon (deploy/server/cv-update).
#   tools/release.sh            (committed and pushed work only)
set -euo pipefail
cd "$(dirname "$0")/.."

[ -z "$(git status --porcelain --untracked-files=no)" ] || { echo "Commit your changes first."; exit 1; }
git fetch --quiet --tags origin
branch="$(git rev-parse --abbrev-ref HEAD)"
[ "$(git rev-parse HEAD)" = "$(git rev-parse "origin/$branch" 2>/dev/null)" ] || { echo "Push $branch first."; exit 1; }

base="v$(date +%Y.%m.%d)"; tag="$base"; n=2
while git rev-parse -q --verify "refs/tags/$tag" >/dev/null; do tag="$base.$n"; n=$((n + 1)); done

git tag -a "$tag" -m "Release $tag ($(git log -1 --format=%s))"
git push --quiet origin "$tag"
echo "Published $tag. Build: https://github.com/RedhaIWNL/clinker-vision-/actions"
