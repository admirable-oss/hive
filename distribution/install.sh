#!/bin/sh
set -eu

REPOSITORY="admirable-oss/hive"
# HIVE_VERSION=v1.0.0 installs that release; HIVE_CHANNEL=preview installs
# the newest release, release candidates included.
RELEASES_URL="https://api.github.com/repos/${REPOSITORY}/releases"
API_URL="${RELEASES_URL}/latest"
case "${HIVE_CHANNEL:-stable}" in
    stable) ;;
    preview) API_URL="${RELEASES_URL}?per_page=1" ;;
    *) printf '  ! HIVE_CHANNEL must be stable or preview\n' >&2; exit 1 ;;
esac
if [ -n "${HIVE_VERSION:-}" ]; then
    API_URL="${RELEASES_URL}/tags/${HIVE_VERSION}"
fi
INSTALL_DIR="${HIVE_INSTALL_DIR:-${HOME}/.local/bin}"

log() { printf '  > %s\n' "$1"; }
err() { printf '  ! %s\n' "$1" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || err "required command not found: $1"; }
fetch() {
    curl -fsSL --retry 3 --connect-timeout 10 --max-time 30 \
        -H 'Accept: application/vnd.github+json' \
        -H 'User-Agent: hive-installer' "$1"
}

onboard() {
    # curl | sh has stdin occupied by the script, so read answers from the
    # controlling terminal. Keep CI, redirected, and remote non-interactive
    # installs quiet and useful.
    [ -t 1 ] && [ -r /dev/tty ] || {
        printf '\nGet started with: hive\n'
        return
    }

    c_reset='\033[0m'; c_gold='\033[38;5;220m'; c_green='\033[38;5;114m'
    for bee_frame in '🐝·        ' '  🐝·      ' '    🐝·    ' '      🐝·  ' '        🐝'; do
        printf '\r  %b' "$c_gold"
        printf '%s' "$bee_frame"
        printf '%b' "$c_reset"
        sleep 0.08
    done
    printf '\r                      \r'
    printf '\n%b' "$c_gold"
    printf '      .     .\n'
    printf '     . .   . .       H I V E\n'
    printf '      (o)             agents keep working\n'
    printf '       |\n'
    printf '      / \\\n'
    printf '%b\n' "$c_reset"
    printf '  Hive keeps coding agents running in the background,\n'
    printf '  then brings their terminals together when you return.\n\n'
    printf '  Your agents stay alive when you close the terminal.\n'
    printf '  Work in parallel, pick up where you left off, and see\n'
    printf '  every agent from one terminal workspace.\n\n'

    config_home="${XDG_CONFIG_HOME:-${HOME}/.config}"
    config_file="${HIVE_CONFIG:-${config_home}/hive/config.toml}"
    if [ -e "$config_file" ]; then
        printf '\n  Existing config found; keeping it as-is.\n'
    else
        printf '\n  Pick a terminal theme:\n'
        printf '    1) Catppuccin Mocha (default)\n'
        printf '    2) Catppuccin Latte\n'
        printf '    3) Tokyo Night\n'
        printf '    4) Gruvbox\n'
        printf '    5) Nord\n'
        printf '    6) Match my terminal automatically\n'
        printf '  Choice [1]: '
        IFS= read -r choice </dev/tty || choice=1
        case "$choice" in
            2) theme=catppuccin-latte ;;
            3) theme=tokyo-night ;;
            4) theme=gruvbox ;;
            5) theme=nord ;;
            6) theme=auto ;;
            *) theme=catppuccin ;;
        esac
        mkdir -p "$(dirname "$config_file")"
        {
            printf '[theme]\nname = "%s"\n' "$theme"
        } >"$config_file"
        printf '\n  Saved %s theme to %s\n' "$theme" "$config_file"
    fi

    printf '\n  Start Hive automatically when you log in? [y/N] '
    IFS= read -r autostart </dev/tty || autostart=n
    case "$autostart" in
        y|Y|yes|YES)
            if "${INSTALL_DIR}/hive" daemon install; then
                printf '  %b✓%b Hive will start at login.\n' "$c_green" "$c_reset"
            else
                printf '  Could not install the login service. Run: hive daemon install\n'
            fi
            ;;
        *) printf '  You can enable this later with: hive daemon install\n' ;;
    esac

    printf '\n  Setup complete. Open Hive with: %bhive%b\n' "$c_gold" "$c_reset"
    printf '  Learn the basics: https://hive.admir-saheta.com/docs/getting-started/quick-start/\n\n'
}

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

    log "detecting the Hive release for ${os}/${arch}"
    if [ -n "${HIVE_CHANNEL:-}${HIVE_VERSION:-}" ]; then
        release="$(fetch "$API_URL")" || err "could not fetch the Hive release from GitHub"
    elif ! release="$(fetch "$API_URL" 2>/dev/null)"; then
        # Until the first stable release there is no latest one: take the
        # newest release candidate, unless a channel or version was asked for.
        release="$(fetch "${RELEASES_URL}?per_page=1")" || err "could not fetch the Hive release from GitHub"
        log "no stable release yet: installing the newest release candidate"
    fi
    # One field per line, whether GitHub sent the JSON indented or not.
    tag="$(printf '%s\n' "$release" | tr ',' '\n' | awk -F '"' '/"tag_name"[[:space:]]*:/ { print $4; exit }')"
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
    onboard
}

main "$@"
