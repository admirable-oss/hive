#!/bin/sh
# Writes the hive-desktop cask (`brew install --cask admirable-oss/tap/hive-desktop`)
# for a release, from its signed desktop-checksums.txt.
set -eu

tap_dir=${1:?usage: update-homebrew-desktop-cask.sh TAP_DIRECTORY RELEASE_TAG DESKTOP_CHECKSUMS_FILE}
tag=${2:?usage: update-homebrew-desktop-cask.sh TAP_DIRECTORY RELEASE_TAG DESKTOP_CHECKSUMS_FILE}
checksums=${3:?usage: update-homebrew-desktop-cask.sh TAP_DIRECTORY RELEASE_TAG DESKTOP_CHECKSUMS_FILE}
version=${tag#v}

zip="hive-desktop_${version}_darwin_universal.zip"
sha=$(awk -v name="$zip" '$2 == name || $2 == "*" name { print $1; exit }' "$checksums")
[ "${#sha}" -eq 64 ] || { printf 'missing SHA-256 for %s\n' "$zip" >&2; exit 1; }
case "$sha" in *[!0123456789abcdefABCDEF]*) printf 'invalid SHA-256 for %s\n' "$zip" >&2; exit 1 ;; esac

cask_dir=${tap_dir%/}/Casks
mkdir -p "$cask_dir"
cat >"$cask_dir/hive-desktop.rb" <<EOF
cask "hive-desktop" do
  version "$version"
  sha256 "$sha"

  url "https://github.com/admirable-oss/hive/releases/download/$tag/$zip"
  name "Hive"
  desc "Coding agents that keep working when you close the window"
  homepage "https://hive.admir-saheta.com"

  # The app updates itself, from signed releases only.
  auto_updates true
  depends_on macos: ">= :ventura"

  app "hive-desktop.app"

  zap trash: [
    "~/Library/Application Support/Hive/desktop.json",
    "~/Library/Application Support/Hive/updates",
  ]
end
EOF
