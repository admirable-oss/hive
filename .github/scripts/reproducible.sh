#!/bin/sh
# Builds the release archives twice, from two copies of the repository made
# at different times (so every file's mtime differs), and fails unless both
# builds are byte for byte the same. Run by CI (`make reproducible`).
set -eu
goreleaser=${GORELEASER:-goreleaser}
src=$(pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
for n in 1 2; do
  mkdir "$tmp/$n"
  # A copy without times (rsync -r, not -a): new mtimes, and nothing from
  # earlier builds (excludes anchored at the top, so cmd/hive stays).
  rsync -r --links --exclude=/dist --exclude=/bin --exclude=/hive "$src/" "$tmp/$n/"
  [ "$n" = 1 ] && sleep 2
  # Name snapshots after the last hive release (an annotated tag), not a
  # plugin's lightweight tag.
  (cd "$tmp/$n" && GORELEASER_CURRENT_TAG=$(git describe --abbrev=0 2>/dev/null || echo v0.0.0) "$goreleaser" release --snapshot --clean --skip=sign,sbom >/dev/null)
done
if ! diff -u "$tmp/1/dist/checksums.txt" "$tmp/2/dist/checksums.txt"; then
  echo "::error::two builds of the same commit differ" >&2
  exit 1
fi
echo "reproducible: $(wc -l < "$tmp/1/dist/checksums.txt" | tr -d ' ') artifacts identical across two builds"
