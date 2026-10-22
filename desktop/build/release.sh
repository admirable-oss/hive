#!/usr/bin/env bash
# Builds the desktop app's release packages for the system it runs on, with
# the hive CLI of the same release bundled, signed with the platform's code
# signing, under the names the app's updater looks for
# (internal/updater.Asset):
#
#   macOS    hive-desktop_<v>_darwin_universal.zip    (Developer ID, notarized)
#   Windows  hive-desktop_<v>_windows_<arch>_setup.exe (Authenticode)
#   Linux    hive-desktop_<v>_linux_<arch>.AppImage, .deb and .rpm (PGP)
#
#   desktop/build/release.sh v1.2.0          # into desktop/dist
#
# The release workflow runs it on each platform once the CLI is published;
# desktop-checksums.txt and its signatures are made from all of them after.
#
# Environment:
#   HIVE_RELEASE_PUBLIC_KEY   built into the app, which verifies updates with
#                             it; also verifies the bundled CLI's release.
#   HIVE_CLI                  a hive binary to bundle instead of the
#                             release's (local builds; macOS: universal).
#   HIVE_DESKTOP_UNSIGNED=1   skip code signing (local builds). Without it a
#                             missing signing secret is an error.
#   macOS:   MACOS_CERTIFICATE (base64 .p12), MACOS_CERTIFICATE_PASSWORD,
#            MACOS_SIGNING_IDENTITY, APPLE_ID, APPLE_TEAM_ID,
#            APPLE_APP_PASSWORD (an app-specific password, for notarytool).
#   Windows: WINDOWS_CERTIFICATE (base64 .pfx), WINDOWS_CERTIFICATE_PASSWORD.
#   Linux:   LINUX_SIGNING_KEY (an armored PGP private key),
#            LINUX_SIGNING_PASSPHRASE.
set -euo pipefail

tag=${1:?usage: release.sh TAG}
version=${tag#v}
core=${version%%-*} # 1.2.0 of 1.2.0-rc.1: what Info.plist and NSIS accept
desktop=$(cd "$(dirname "$0")/.." && pwd)
root=$(dirname "$desktop")
out=${OUT:-$desktop/dist}
goos=$(go env GOOS)
goarch=$(go env GOARCH)
unsigned=${HIVE_DESKTOP_UNSIGNED:-}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

die() {
	echo "release.sh: $*" >&2
	exit 1
}
need() {
	for v in "$@"; do
		[ -n "${!v:-}" ] || die "$v is not set (HIVE_DESKTOP_UNSIGNED=1 builds without signing)"
	done
}
sha256() {
	if command -v sha256sum >/dev/null; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

[ -n "${HIVE_RELEASE_PUBLIC_KEY:-}" ] || [ -n "$unsigned" ] || die "HIVE_RELEASE_PUBLIC_KEY is not set: the app could not verify its updates"
mkdir -p "$out"

# cli fetches the release's hive for an architecture after checking
# checksums.txt's signature and the archive's checksum, and prints its path.
cli() {
	local arch=$1 ext=tar.gz exe=hive
	[ "$goos" = windows ] && ext=zip exe=hive.exe
	local archive="hive_${version}_${goos}_${arch}.${ext}" dir="$work/cli-$arch"
	mkdir -p "$dir"
	gh release download "$tag" -R admirable-oss/hive -D "$dir" -p checksums.txt -p checksums.txt.sig -p "$archive" >&2
	(cd "$root" && go run ./tools/release-sign verify "$HIVE_RELEASE_PUBLIC_KEY" "$dir/checksums.txt") >&2
	local want
	want=$(awk -v n="$archive" '$2 == n { print $1 }' "$dir/checksums.txt")
	[ -n "$want" ] && [ "$(sha256 "$dir/$archive")" = "$want" ] || die "$archive does not match checksums.txt"
	if [ "$ext" = zip ]; then
		(cd "$dir" && if command -v unzip >/dev/null; then unzip -q "$archive" "$exe"; else 7z x -y "$archive" "$exe" >/dev/null; fi)
	else
		tar -xzf "$dir/$archive" -C "$dir" "$exe"
	fi
	echo "$dir/$exe"
}

build() {
	(cd "$desktop" && wails3 task "$@" VERSION="$version" RELEASE_KEY="${HIVE_RELEASE_PUBLIC_KEY:-}")
}

darwin() {
	local hive=${HIVE_CLI:-}
	if [ -z "$hive" ]; then
		hive="$work/hive"
		lipo -create -output "$hive" "$(cli arm64)" "$(cli amd64)"
	fi
	build darwin:package:universal HIVE_CLI="$hive"
	local app="$desktop/bin/hive-desktop.app" zip="$out/hive-desktop_${version}_darwin_universal.zip"
	plutil -replace CFBundleShortVersionString -string "$version" "$app/Contents/Info.plist"
	plutil -replace CFBundleVersion -string "$core" "$app/Contents/Info.plist"

	if [ -n "$unsigned" ]; then
		codesign --force --sign - "$app/Contents/Resources/hive"
		codesign --force --sign - "$app"
	else
		need MACOS_CERTIFICATE MACOS_CERTIFICATE_PASSWORD MACOS_SIGNING_IDENTITY APPLE_ID APPLE_TEAM_ID APPLE_APP_PASSWORD
		local keychain="$work/signing.keychain-db" pass
		pass=$(openssl rand -hex 16)
		echo "$MACOS_CERTIFICATE" | base64 --decode >"$work/cert.p12"
		security create-keychain -p "$pass" "$keychain"
		security set-keychain-settings -lut 3600 "$keychain"
		security unlock-keychain -p "$pass" "$keychain"
		security import "$work/cert.p12" -k "$keychain" -P "$MACOS_CERTIFICATE_PASSWORD" -T /usr/bin/codesign
		security set-key-partition-list -S apple-tool:,apple: -s -k "$pass" "$keychain" >/dev/null
		local chains=("$keychain") chain
		while IFS= read -r chain; do
			chain=${chain//\"/}
			chains+=("${chain#"${chain%%[![:space:]]*}"}")
		done < <(security list-keychains -d user)
		security list-keychains -d user -s "${chains[@]}"
		# Inside out: the bundled CLI, then the bundle (the hardened runtime
		# is what notarization requires).
		local sign=(codesign --force --options runtime --timestamp --keychain "$keychain" --sign "$MACOS_SIGNING_IDENTITY")
		"${sign[@]}" "$app/Contents/Resources/hive"
		"${sign[@]}" "$app"
		codesign --verify --deep --strict --verbose=2 "$app"
		ditto -c -k --norsrc --noextattr --noacl --keepParent "$app" "$work/notarize.zip"
		xcrun notarytool submit "$work/notarize.zip" --apple-id "$APPLE_ID" --team-id "$APPLE_TEAM_ID" --password "$APPLE_APP_PASSWORD" --wait
		xcrun stapler staple "$app"
		spctl --assess --type execute --verbose "$app"
	fi
	codesign --verify --deep --strict "$app"
	# Without extended attributes: as AppleDouble files (._*) they are not
	# part of the signed bundle, and the updater skips them.
	rm -f "$zip"
	ditto -c -k --norsrc --noextattr --noacl --keepParent "$app" "$zip"
}

windows() {
	local hive=${HIVE_CLI:-}
	[ -n "$hive" ] || hive=$(cli "$goarch")
	build windows:build ARCH="$goarch"
	local exe="$desktop/bin/hive-desktop.exe" signtool=""
	if [ -z "$unsigned" ]; then
		need WINDOWS_CERTIFICATE WINDOWS_CERTIFICATE_PASSWORD
		echo "$WINDOWS_CERTIFICATE" | base64 --decode >"$work/cert.pfx"
		signtool=$(find "/c/Program Files (x86)/Windows Kits/10/bin" -path '*/x64/signtool.exe' | sort -V | tail -1)
		[ -x "$signtool" ] || die "no signtool.exe in the Windows SDK"
	fi
	winsign() {
		[ -n "$signtool" ] || return 0
		"$signtool" sign //f "$(cygpath -w "$work/cert.pfx")" //p "$WINDOWS_CERTIFICATE_PASSWORD" \
			//fd sha256 //tr http://timestamp.digicert.com //td sha256 "$(cygpath -w "$1")"
		"$signtool" verify //pa "$(cygpath -w "$1")"
	}
	# The app is signed before the installer packs it. The bundled hive.exe
	# stays the release's own bytes, which `hive version --verify` checks.
	winsign "$exe"
	# A per-user install: updating it needs no administrator (the updater
	# runs this installer silently into the same folder).
	local flag=AMD64
	[ "$goarch" = arm64 ] && flag=ARM64
	(
		cd "$desktop/build/windows/nsis"
		wails3 generate webview2bootstrapper -dir .
		makensis -DWAILS_INSTALL_SCOPE=user -DREQUEST_EXECUTION_LEVEL=user -DINFO_PRODUCTVERSION="$core" \
			-DARG_WAILS_${flag}_BINARY="$(cygpath -w "$exe")" -DARG_HIVE_CLI="$(cygpath -w "$hive")" project.nsi
	)
	local setup="$out/hive-desktop_${version}_windows_${goarch}_setup.exe"
	mv "$desktop/bin/hive-desktop-${goarch}-installer.exe" "$setup"
	winsign "$setup"
}

linux() {
	# The Linux packages do not bundle hive: install it beside the app
	# (docs/desktop.md). An AppImage runs from a mount that goes away when
	# the app quits, and a daemon started from it would go with it.
	local key=""
	if [ -z "$unsigned" ]; then
		need LINUX_SIGNING_KEY LINUX_SIGNING_PASSPHRASE
		key="$work/signing.asc"
		printf '%s\n' "$LINUX_SIGNING_KEY" >"$key"
	fi
	build linux:build
	build linux:create:appimage
	# nfpm reads the version and the signing key from the environment
	# (build/linux/nfpm/nfpm.yaml).
	(
		cd "$desktop"
		export HIVE_DESKTOP_VERSION="$version" NFPM_SIGNING_KEY_FILE="$key" NFPM_PASSPHRASE="${LINUX_SIGNING_PASSPHRASE:-}"
		rm -f bin/*.deb bin/*.rpm
		wails3 tool package -name hive-desktop -format deb -config ./build/linux/nfpm/nfpm.yaml -out bin
		wails3 tool package -name hive-desktop -format rpm -config ./build/linux/nfpm/nfpm.yaml -out bin
	)
	local base="$out/hive-desktop_${version}_linux_${goarch}"
	mv "$desktop"/bin/hive-desktop-*.AppImage "$base.AppImage"
	mv "$desktop"/bin/*.deb "$base.deb"
	mv "$desktop"/bin/*.rpm "$base.rpm"
	chmod 755 "$base.AppImage"
}

case "$goos" in
darwin) darwin ;;
windows) windows ;;
linux) linux ;;
*) die "no desktop packages for $goos" ;;
esac
ls -l "$out"
