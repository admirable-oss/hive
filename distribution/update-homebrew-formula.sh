#!/bin/sh
set -eu

tap_dir=${1:?usage: update-homebrew-formula.sh TAP_DIRECTORY RELEASE_TAG CHECKSUMS_FILE}
tag=${2:?usage: update-homebrew-formula.sh TAP_DIRECTORY RELEASE_TAG CHECKSUMS_FILE}
checksums=${3:?usage: update-homebrew-formula.sh TAP_DIRECTORY RELEASE_TAG CHECKSUMS_FILE}
version=${tag#v}

checksum_for() {
    archive=$1
    value=$(awk -v name="$archive" '$2 == name || $2 == "*" name { print $1; exit }' "$checksums")
    [ "${#value}" -eq 64 ] || { printf 'missing SHA-256 for %s\n' "$archive" >&2; exit 1; }
    case "$value" in *[!0123456789abcdefABCDEF]*) printf 'invalid SHA-256 for %s\n' "$archive" >&2; exit 1 ;; esac
    printf '%s' "$value"
}

mac_arm="hive_${version}_darwin_arm64.tar.gz"
mac_intel="hive_${version}_darwin_amd64.tar.gz"
linux_arm="hive_${version}_linux_arm64.tar.gz"
linux_intel="hive_${version}_linux_amd64.tar.gz"
mac_arm_sha=$(checksum_for "$mac_arm")
mac_intel_sha=$(checksum_for "$mac_intel")
linux_arm_sha=$(checksum_for "$linux_arm")
linux_intel_sha=$(checksum_for "$linux_intel")

formula_dir=${tap_dir%/}/Formula
mkdir -p "$formula_dir"
cat >"$formula_dir/hive-agents.rb" <<EOF
class HiveAgents < Formula
  desc "Coding agents that keep working when you close the terminal"
  homepage "https://hive.admir-saheta.com"
  version "$version"
  license "Apache-2.0"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/admirable-oss/hive/releases/download/$tag/$mac_arm"
      sha256 "$mac_arm_sha"
    else
      url "https://github.com/admirable-oss/hive/releases/download/$tag/$mac_intel"
      sha256 "$mac_intel_sha"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/admirable-oss/hive/releases/download/$tag/$linux_arm"
      sha256 "$linux_arm_sha"
    else
      url "https://github.com/admirable-oss/hive/releases/download/$tag/$linux_intel"
      sha256 "$linux_intel_sha"
    end
  end

  def install
    bin.install "hive"
  end

  test do
    system "#{bin}/hive", "version"
  end
end
EOF
