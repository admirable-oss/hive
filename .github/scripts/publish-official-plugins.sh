#!/usr/bin/env bash
# Publishes the official plugins: tags each release immutably (v<version>)
# and records the pinned commit plus the SHA-256 of the full-repository
# archive the registry publishes. Release records are append-only: a
# published version's commit and checksum never change.
set -euo pipefail

records="registry/official-releases.json"
now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

[ -f "$records" ] || printf '{"schema":1,"plugins":[]}\n' > "$records"

for manifest in plugins/official/*/hive-plugin.toml; do
  pkg="$(dirname "$manifest")"
  id="$(sed -n 's/^id *= *"\(.*\)"/\1/p' "$manifest" | head -n1)"
  version="$(sed -n 's/^version *= *"\(.*\)"/\1/p' "$manifest" | head -n1)"
  if [ -z "$id" ] || [ -z "$version" ]; then
    echo "::error::cannot read id and version from $manifest" >&2
    exit 1
  fi
  ref="v$version"
  # Tags point to the commit that introduced this plugin version. This lets
  # unrelated repository changes leave already-published versions intact.
  commit="$(git log -n1 --format=%H -- "$manifest")"
  if [ -z "$commit" ]; then
    echo "::error::cannot find the commit that introduced $id@$version" >&2
    exit 1
  fi

  if git rev-parse -q --verify "refs/tags/$ref" >/dev/null; then
    tagged="$(git rev-list -n1 "$ref")"
    if [ "$tagged" != "$commit" ]; then
      echo "::error::tag $ref already points at $tagged, not $commit; official releases are immutable" >&2
      exit 1
    fi
  else
    git tag "$ref" "$commit"
    git push origin "refs/tags/$ref"
  fi

  checksum="sha256:$(git archive --format=tar "$commit" | sha256sum | cut -d' ' -f1)"

  existing="$(jq -r --arg id "$id" --arg version "$version" \
    '.plugins[]? | select(.id == $id) | .releases[]? | select(.version == $version) | [.commit, .checksum] | @tsv' \
    "$records")"
  if [ -n "$existing" ] && [ "$existing" != "$(printf '%s\t%s' "$commit" "$checksum")" ]; then
    echo "::error::release $id@$version is already recorded with different source metadata; releases are immutable" >&2
    exit 1
  fi

  tmp="$(mktemp)"
  jq --arg id "$id" --arg subdir "$pkg" --arg version "$version" --arg ref "$ref" \
     --arg commit "$commit" --arg checksum "$checksum" --arg published "$now" '
    def release: {"version": $version, "ref": $ref, "commit": $commit,
                  "checksum": $checksum, "published_at": $published};
    if any(.plugins[]; .id == $id) then
      .plugins |= map(if .id == $id then
        (if any(.releases[]; .version == $version) then .
         else .releases += [release] end)
      else . end)
    else .plugins += [{"id": $id, "subdir": $subdir, "releases": [release]}] end
    | .plugins |= sort_by(.id)' "$records" > "$tmp"
  mv "$tmp" "$records"
  echo "recorded $id@$ref at $commit ($checksum)"
done
