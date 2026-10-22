#!/bin/sh
set -eu

REPOSITORY="admirable-oss/hive"
# HIVE_VERSION=v1.0.0 installs that release; HIVE_CHANNEL=preview installs
# the newest release, release candidates included.
RELEASES_URL="https://api.github.com/repos/${REPOSITORY}/releases"
DOWNLOAD_URL="https://github.com/${REPOSITORY}/releases/download"
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

# The release key, as `go run ./tools/release-sign allowed-signers <key>`
# prints it: only releases whose checksums.txt it signed are installed.
ALLOWED_SIGNERS='hive-release namespaces="hive-release" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAILHcqXlEfCE8NSz4faSoF7WWzUrakzdnGNT4fFEWlNm1'
# Releases published before SSH signatures existed. Any other release
# without one is refused, so removing the signature cannot downgrade the
# check.
UNSIGNED_RELEASES="v1.0.0-rc.1"

log() { printf '  > %s\n' "$1"; }
err() { printf '  ! %s\n' "$1" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || err "required command not found: $1"; }
fetch() {
    curl -fsSL --retry 3 --connect-timeout 10 --max-time 30 \
        -H 'Accept: application/vnd.github+json' \
        -H 'User-Agent: hive-installer' "$1"
}

# --- onboarding --------------------------------------------------------------
# After an install on a terminal: the bee says hello, then two questions
# (the theme, starting at login) answered with the arrow keys, Astro style.
# Without a terminal it prints one line; without stty it asks for numbers.

ESC="$(printf '\033')"
tty_saved=""

# restore_tty puts the terminal back as onboarding found it.
restore_tty() {
    if [ -n "$tty_saved" ]; then
        stty "$tty_saved" </dev/tty 2>/dev/null || true
        tty_saved=""
        printf '%s[?25h' "$ESC"
    fi
}

# colours sets the palette: the bee's amber, 24-bit when the terminal says
# it has it, else the nearest of the 256; nothing under NO_COLOR or a dumb
# terminal.
colours() {
    if [ -n "${NO_COLOR:-}" ] || [ "${TERM:-dumb}" = dumb ]; then
        c_reset=""; c_bold=""; c_dim=""; c_amber=""; c_green=""; c_chip=""
        bee_y=""; bee_yb=""; bee_w=""; bee_wb=""; bee_a=""; bee_d=""; bee_db=""
        return
    fi
    c_reset="${ESC}[0m"; c_bold="${ESC}[1m"; c_dim="${ESC}[2m"
    case "${COLORTERM:-}" in
        truecolor|24bit)
            y='2;245;185;66'; w='2;189;189;187'; a='2;155;155;161'; d='2;11;11;12'; g='2;93;200;121' ;;
        *)
            y='5;214'; w='5;250'; a='5;247'; d='5;232'; g='5;114' ;;
    esac
    c_amber="${ESC}[38;${y}m"; c_green="${ESC}[38;${g}m"
    c_chip="${ESC}[1;38;${d};48;${y}m"
    bee_y="${ESC}[38;${y}m"; bee_yb="${ESC}[48;${y}m"; bee_w="${ESC}[38;${w}m"; bee_wb="${ESC}[48;${w}m"
    bee_a="${ESC}[38;${a}m"; bee_d="${ESC}[38;${d}m"; bee_db="${ESC}[48;${d}m"
}

# bee prints the mascot (internal/tui/bee) with its wings in frame $1, one
# letter per cell: y ▀ and v ▄ amber, F solid amber, a ▄ antenna, h antenna
# on amber, e an eye, s a stripe, d dark, U solid wing, L ▄ wing, . blank.
bee() {
    case "$1" in
        0) w0='.LU UL.'; w1='UUL LUU'; w2='UUU UUU'; w3='... ...' ;;
        2|4) w0='... ...'; w1='.UL LU.'; w2='UUL LUU'; w3='... ...' ;;
        3) w0='... ...'; w1='... ...'; w2='.LU UL.'; w3='UU. .UU' ;;
        *) w0='... ...'; w1='UUL LUU'; w2='UUU UUU'; w3='... ...' ;;
    esac
    printf '%s\n' \
        "${w0% *}ya......ay${w0#* }" \
        "${w1% *}.vhvvvvhv.${w1#* }" \
        "${w2% *}FFeFFFFeFF${w2#* }" \
        "${w3% *}FFsFFFFsFF${w3#* }" \
        ".vvFssssssssFvv." \
        "....FdsddsdF...." \
        "....y.y..y.y...." |
    sed -e "s/y/${bee_y}▀${c_reset}/g" -e "s/v/${bee_y}▄${c_reset}/g" \
        -e "s/F/${bee_y}${bee_yb}▀${c_reset}/g" -e "s/a/${bee_a}▄${c_reset}/g" \
        -e "s/h/${bee_a}${bee_yb}▀${c_reset}/g" -e "s/e/${bee_y}${bee_db}▀${c_reset}/g" \
        -e "s/s/${bee_d}${bee_yb}▀${c_reset}/g" -e "s/d/${bee_d}▀${c_reset}/g" \
        -e "s/U/${bee_w}${bee_wb}▀${c_reset}/g" -e "s/L/${bee_w}▄${c_reset}/g" \
        -e 's/\./ /g'
}

# repeat prints $2 $1 times.
repeat() {
    i=0
    while [ "$i" -lt "$1" ]; do printf '%s' "$2"; i=$((i + 1)); done
}

# say draws the bee beside a speech bubble holding $1 (its title) and the
# first $3 characters of $2, wings in frame $4. Both texts are ASCII: the
# bubble is sized by their lengths.
say() {
    title="$1"; text="$2"; shown="$3"
    inner=${#text}
    [ ${#title} -gt "$inner" ] && inner=${#title}
    typed=""
    [ "$shown" -gt 0 ] && typed="$(printf '%s' "$text" | cut -c "1-$shown")"
    caret=""; caret_w=0
    if [ "$shown" -lt ${#text} ]; then caret="${c_amber}▍${c_reset}"; caret_w=1; fi
    edge="$(repeat $((inner + 2)) '─')"
    pad_title="$(repeat $((inner - ${#title})) ' ')"
    pad_text="$(repeat $((inner - ${#typed} - caret_w)) ' ')"
    n=0
    bee "$4" | while IFS= read -r line; do
        case "$n" in
            1) side="  ${c_dim} ╭${edge}╮${c_reset}" ;;
            2) side="  ${c_dim} │ ${c_reset}${c_amber}${c_bold}hive${c_reset}${c_dim}${title#hive}${pad_title} │${c_reset}" ;;
            3) side="  ${c_dim}─┤ ${c_reset}${typed}${caret}${pad_text}${c_dim} │${c_reset}" ;;
            4) side="  ${c_dim} ╰${edge}╯${c_reset}" ;;
            *) side="" ;;
        esac
        printf '\r%s  %s%s%s\n' "${ESC}[2K" "$line" "$side" ""
        n=$((n + 1))
    done
}

# greet types $2 into the bubble while the bee flaps, then leaves it at
# rest. Without colours it is two lines of text.
greet() {
    if [ -z "$c_reset" ]; then
        printf '  %s\n  %s\n' "$1" "$2"
        return
    fi
    printf '%s[?25l' "$ESC"
    shown=0; frame=0
    while :; do
        say "$1" "$2" "$shown" $((frame / 2 % 6))
        [ "$shown" -ge ${#2} ] && [ "$frame" -ge 12 ] && break
        if [ "$shown" -lt ${#2} ]; then shown=$((shown + 2)); fi
        [ "$shown" -gt ${#2} ] && shown=${#2}
        frame=$((frame + 1))
        sleep 0.03
        printf '%s[7A' "$ESC"
    done
    printf '%s[7A' "$ESC"
    say "$1" "$2" ${#2} 1
    printf '%s[?25h' "$ESC"
}

# chip is a step's label, as Astro marks its steps.
chip() { printf '  %s %-6s %s ' "$c_chip" "$1" "$c_reset"; }

# choose asks $1 (the step) / $2 (the question) with options "value|label|hint"
# and sets choice to the picked value: arrows or j/k move, a digit jumps,
# Enter picks. The first option is the default.
choose() {
    step="$1"; question="$2"; shift 2
    count=$#
    chip "$step"; printf '%s\n' "${c_bold}${question}${c_reset}"
    if ! tty_saved="$(stty -g </dev/tty 2>/dev/null)" || ! command -v dd >/dev/null 2>&1; then
        tty_saved=""
        i=1
        for o in "$@"; do
            label="${o#*|}"; printf '           %d) %s\n' "$i" "${label%%|*}"; i=$((i + 1))
        done
        printf '           Choice [1]: '
        IFS= read -r pick </dev/tty || pick=1
        case "$pick" in ''|*[!0-9]*) pick=1 ;; esac
        [ "$pick" -ge 1 ] && [ "$pick" -le "$count" ] || pick=1
    else
        stty -icanon -echo min 1 time 0 </dev/tty
        printf '%s[?25l' "$ESC"
        pick=1
        while :; do
            i=1
            for o in "$@"; do
                label="${o#*|}"; hint="${label#*|}"; label="${label%%|*}"
                if [ "$i" -eq "$pick" ]; then
                    printf '\r%s           %s●%s %s%s%s  %s%s%s\n' "${ESC}[2K" "$c_amber" "$c_reset" "$c_bold" "$label" "$c_reset" "$c_dim" "$hint" "$c_reset"
                else
                    printf '\r%s           %s○ %s%s\n' "${ESC}[2K" "$c_dim" "$label" "$c_reset"
                fi
                i=$((i + 1))
            done
            printf '\r%s           %s↑/↓ to move · enter to pick%s' "${ESC}[2K" "$c_dim" "$c_reset"
            key="$(dd bs=1 count=1 2>/dev/null </dev/tty)"
            case "$key" in
                "$ESC")
                    case "$(dd bs=1 count=2 2>/dev/null </dev/tty)" in
                        '[A'|'OA') [ "$pick" -gt 1 ] && pick=$((pick - 1)) ;;
                        '[B'|'OB') [ "$pick" -lt "$count" ] && pick=$((pick + 1)) ;;
                    esac ;;
                k|K) [ "$pick" -gt 1 ] && pick=$((pick - 1)) ;;
                j|J|' ') [ "$pick" -lt "$count" ] && pick=$((pick + 1)) ;;
                [1-9]) [ "$key" -le "$count" ] && pick="$key" ;;
                ''|"$(printf '\r')") break ;;
            esac
            printf '\r%s[%dA' "$ESC" "$count"
        done
        restore_tty
        # Fold the question into one line with the answer.
        printf '\r%s[%dA%s[J' "$ESC" $((count + 1)) "$ESC"
    fi
    picked=""; i=1
    for o in "$@"; do
        [ "$i" -eq "$pick" ] && picked="$o"
        i=$((i + 1))
    done
    choice="${picked%%|*}"
    label="${picked#*|}"; label="${label%%|*}"
    chip "$step"; printf '%s%s%s %s✔%s\n' "$c_reset" "$label" "$c_reset" "$c_green" "$c_reset"
}

onboard() {
    # curl | sh has stdin occupied by the script, so read answers from the
    # controlling terminal. Keep CI, redirected, and remote non-interactive
    # installs quiet and useful.
    [ -t 1 ] && [ -r /dev/tty ] || {
        printf '\nGet started with: hive\n'
        return
    }
    colours
    printf '\n'
    greet "hive ${tag}" "Welcome! Let's get your agents buzzing."
    printf '\n'

    config_home="${XDG_CONFIG_HOME:-${HOME}/.config}"
    config_file="${HIVE_CONFIG:-${config_home}/hive/config.toml}"
    if [ -e "$config_file" ]; then
        chip theme; printf '%skeeping your config: %s%s\n' "$c_dim" "$config_file" "$c_reset"
    else
        choose theme "Pick your colours" \
            "auto|Hive|amber on graphite, light on a light terminal (the desktop app's)" \
            "catppuccin|Catppuccin Mocha|" \
            "catppuccin-latte|Catppuccin Latte|" \
            "tokyo-night|Tokyo Night|" \
            "gruvbox|Gruvbox|" \
            "nord|Nord|" \
            "terminal|My terminal's colours|whatever scheme your terminal has"
        mkdir -p "$(dirname "$config_file")"
        printf '[theme]\nname = "%s"\n' "$choice" >"$config_file"
    fi

    choose login "Start Hive when you log in?" \
        "no|Not now|you can turn it on later with: hive daemon install" \
        "yes|Yes|agents come back after a restart"
    if [ "$choice" = yes ]; then
        if "${INSTALL_DIR}/hive" daemon install >/dev/null 2>&1; then
            chip login; printf '%sHive starts when you log in%s\n' "$c_dim" "$c_reset"
        else
            chip login; printf 'could not install the login service; run: hive daemon install\n'
        fi
    fi

    printf '\n'
    chip next; printf '%sYou are all set.%s Open Hive with %s%shive%s\n' "$c_bold" "$c_reset" "$c_amber" "$c_bold" "$c_reset"
    printf '           %sthe basics: https://hive.admir-saheta.com/docs/getting-started/quick-start/%s\n\n' "$c_dim" "$c_reset"
}

# unverifiable stops the install when the signature cannot be checked here,
# unless HIVE_ALLOW_UNVERIFIED=1 accepts the checksum alone.
unverifiable() {
    if [ "${HIVE_ALLOW_UNVERIFIED:-}" = 1 ]; then
        printf '  ! %s; installing with the checksum only (HIVE_ALLOW_UNVERIFIED=1)\n' "$1" >&2
        return
    fi
    err "$1, so the release signature cannot be checked. Install OpenSSH 8.1 or later, or set HIVE_ALLOW_UNVERIFIED=1 to rely on the checksum alone"
}

# verify_signature checks checksums.txt against the release key's SSH
# signature with `ssh-keygen -Y verify`. A signature that does not verify
# always stops the install.
verify_signature() {
    case " ${UNSIGNED_RELEASES} " in
        *" ${tag} "*)
            printf '  ! %s was published before release signatures for installers; checking its checksum only\n' "$tag" >&2
            return ;;
    esac
    curl -fsSL --retry 3 --connect-timeout 10 --max-time 30 \
        "${base_url}/checksums.txt.sshsig" -o "${tmp}/checksums.txt.sshsig" \
        || err "${tag} has no checksums.txt.sshsig; refusing an unsigned release"
    command -v ssh-keygen >/dev/null 2>&1 || { unverifiable "ssh-keygen (OpenSSH) is not installed"; return; }
    printf '%s\n' "$ALLOWED_SIGNERS" > "${tmp}/allowed_signers"
    if ssh-keygen -Y verify -f "${tmp}/allowed_signers" -I hive-release -n hive-release \
        -s "${tmp}/checksums.txt.sshsig" < "${tmp}/checksums.txt" >/dev/null 2>&1; then
        log "verified the release signature"
        return
    fi
    # OpenSSH before 8.1 has no -Y: that is a missing tool, not a bad signature.
    if ssh-keygen -Y verify -f /dev/null -I x -n x -s /dev/null </dev/null 2>&1 | grep -Eqi 'illegal option|unknown option|invalid option'; then
        unverifiable "this ssh-keygen is older than OpenSSH 8.1"
        return
    fi
    err "the signature of ${tag}'s checksums.txt does not verify with the hive release key; not installing it"
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
    base_url="${DOWNLOAD_URL}/${tag}"

    tmp="$(mktemp -d)" || err "could not create a temporary directory"
    trap 'rm -rf "$tmp"; restore_tty' EXIT
    trap 'rm -rf "$tmp"; restore_tty; exit 130' HUP INT TERM
    curl -fsSL --retry 3 --connect-timeout 10 --max-time 120 \
        "${base_url}/${archive}" -o "${tmp}/${archive}" \
        || err "could not download ${archive}"
    curl -fsSL --retry 3 --connect-timeout 10 --max-time 30 \
        "${base_url}/checksums.txt" -o "${tmp}/checksums.txt" \
        || err "could not download the release checksums"
    verify_signature

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
