#!/bin/sh
set -eu

REPOSITORY="admirable-oss/hive"
API_URL="https://api.github.com/repos/${REPOSITORY}/releases/latest"
INSTALL_DIR="${HIVE_INSTALL_DIR:-${HOME}/.local/bin}"

log() { printf '  > %s\n' "$1"; }
err() { printf '  ! %s\n' "$1" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || err "required command not found: $1"; }

main() {
    need curl
    need tar
    need awk
    need install

    case "$(uname -s)" in
        Darwin) os=darwin ;;
        Linux) os=linux ;;
        *) err "unsupported operating system: $(uname -s)" ;;
    esac
    case "$(uname -m)" in
        x86_64|amd64) arch=amd64 ;;
        arm64|aarch64) arch=arm64 ;;
        *) err "unsupported architecture: $(uname -m)" ;;
    esac

    log "detecting latest Hive release for ${os}/${arch}"
    release="$(curl -fsSL --retry 3 --connect-timeout 10 --max-time 30 \
        -H 'Accept: application/vnd.github+json' \
        -H 'User-Agent: hive-installer' "$API_URL")" \
        || err "could not fetch the latest Hive release from GitHub"
    tag="$(printf '%s\n' "$release" | awk -F '"' '/"tag_name"[[:space:]]*:/ { print $4; exit }')"
    [ -n "$tag" ] || err "GitHub did not return a latest release tag"
    version="${tag#v}"
    archive="hive_${version}_${os}_${arch}.tar.gz"
    base_url="https://github.com/${REPOSITORY}/releases/download/${tag}"

    tmp="$(mktemp -d)" || err "could not create a temporary directory"
    trap 'rm -rf "$tmp"' EXIT HUP INT TERM
    curl -fsSL --retry 3 --connect-timeout 10 --max-time 120 \
        "${base_url}/${archive}" -o "${tmp}/${archive}" \
        || err "could not download ${archive}"
    curl -fsSL --retry 3 --connect-timeout 10 --max-time 30 \
        "${base_url}/checksums.txt" -o "${tmp}/checksums.txt" \
        || err "could not download the release checksums"

    expected="$(awk -v name="$archive" '$2 == name || $2 == "*" name { print $1; exit }' "${tmp}/checksums.txt")"
    [ "${#expected}" -eq 64 ] || err "release checksum is missing for ${archive}"
    case "$expected" in *[!0123456789abcdefABCDEF]*) err "invalid release checksum for ${archive}" ;; esac

    if command -v sha256sum >/dev/null 2>&1; then
        actual="$(sha256sum "${tmp}/${archive}" | awk '{ print $1 }')"
    elif command -v shasum >/dev/null 2>&1; then
        actual="$(shasum -a 256 "${tmp}/${archive}" | awk '{ print $1 }')"
    elif command -v openssl >/dev/null 2>&1; then
        actual="$(openssl dgst -sha256 "${tmp}/${archive}" | awk '{ print $NF }')"
    else
        err "SHA-256 verification requires sha256sum, shasum, or openssl"
    fi
    [ "$(printf '%s' "$actual" | tr '[:upper:]' '[:lower:]')" = \
      "$(printf '%s' "$expected" | tr '[:upper:]' '[:lower:]')" ] \
        || err "checksum verification failed for ${archive}"

    tar -xzf "${tmp}/${archive}" -C "$tmp" hive \
        || err "could not extract the Hive binary from ${archive}"
    mkdir -p "$INSTALL_DIR"
    install -m 755 "${tmp}/hive" "${INSTALL_DIR}/hive"
    log "installed Hive ${tag} to ${INSTALL_DIR}/hive"
    case ":${PATH}:" in
        *":${INSTALL_DIR}:"*) ;;
        *) printf 'Add this directory to your PATH: %s\n' "$INSTALL_DIR" ;;
    esac
}

main "$@"
