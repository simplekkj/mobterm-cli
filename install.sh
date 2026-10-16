#!/bin/sh
# Install only the official Release binary; never execute downloaded content.
set -eu
umask 077

fail() { printf 'mobterm-cli install: %s\n' "$*" >&2; exit 1; }
stable_version() {
    printf '%s\n' "$1" | awk '
        NR == 1 { valid = /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/ }
        END { exit !(NR == 1 && valid) }'
}

version=
case $# in
    0) ;;
    2) [ "$1" = --version ] || fail 'Usage: sh install.sh [--version vX.Y.Z]'
       version=$2
       stable_version "$version" || fail 'Version must be a stable vX.Y.Z tag.' ;;
    *) fail 'Usage: sh install.sh [--version vX.Y.Z]' ;;
esac

for tool in curl uname awk tar mktemp mkdir rmdir cat chmod mv rm; do
    command -v "$tool" >/dev/null 2>&1 || fail "Required tool unavailable: $tool"
done
if command -v sha256sum >/dev/null 2>&1; then
    sha_tool=sha256sum
elif command -v shasum >/dev/null 2>&1; then
    sha_tool=shasum
else
    fail 'SHA-256 verification requires sha256sum or shasum.'
fi
case $(uname -s) in
    Linux) system=linux ;;
    Darwin) system=darwin ;;
    *) fail 'Unsupported system; only Linux and macOS are supported.' ;;
esac
case $(uname -m) in
    x86_64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) fail 'Unsupported architecture; only amd64 and arm64 are supported.' ;;
esac
case ${HOME:-} in /*) ;; *) fail 'HOME must be an absolute directory path.' ;; esac
case ${TMPDIR:-/tmp} in /*) ;; *) fail 'TMPDIR must be an absolute directory path.' ;; esac

repo=https://github.com/simplekkj/mobterm-cli
if [ -z "$version" ]; then
    # GitHub's latest endpoint excludes draft and prerelease Releases. Read only
    # the effective URL, not HTML/JSON or shell code from a response body.
    latest=$(curl -q -fsSL --proto '=https' --proto-redir '=https' \
        --connect-timeout 15 --max-time 60 --max-redirs 5 \
        -o /dev/null -w '%{url_effective}' "$repo/releases/latest") ||
        fail 'No published stable Release available, or GitHub is unreachable.'
    case $latest in
        "$repo/releases/tag/"*) version=${latest#"$repo/releases/tag/"} ;;
        *) fail 'Latest Release did not resolve to the expected repository.' ;;
    esac
    stable_version "$version" || fail 'Latest Release is not a stable vX.Y.Z tag.'
fi

work=
staged=
created_local=0
created_bin=0
committed=0
defer_signals=0
interrupted=0
local_dir=$HOME/.local
install_dir=$local_dir/bin
warn_cleanup() { printf 'mobterm-cli install: warning: %s\n' "$1" >&2 || :; }
cleanup() {
    status=$?
    trap - 0
    trap '' HUP INT TERM
    if [ -n "$staged" ] && ! rm -f "$staged"; then
        warn_cleanup 'Cannot remove staged executable; inspect installation directory.'
    fi
    if [ -n "$work" ] && ! rm -rf "$work"; then
        warn_cleanup 'Cannot remove private download directory; inspect TMPDIR.'
    fi
    if [ "$committed" = 0 ]; then
        if [ "$created_bin" = 1 ]; then
            if [ -L "$local_dir" ] || [ -L "$install_dir" ] || ! rmdir "$install_dir" 2>/dev/null; then
                warn_cleanup 'Cannot remove newly created bin directory; retained for manual inspection.'
            fi
        fi
        if [ "$created_local" = 1 ]; then
            if [ -L "$local_dir" ] || ! rmdir "$local_dir" 2>/dev/null; then
                warn_cleanup 'Cannot remove newly created .local directory; retained for manual inspection.'
            fi
        fi
    fi
    exit "$status"
}
on_signal() {
    interrupted=1
    [ "$defer_signals" = 1 ] || exit 1
}
trap cleanup 0
trap on_signal HUP INT TERM
defer_signals=1
work=$(mktemp -d "${TMPDIR:-/tmp}/mobterm-cli-install.XXXXXXXX") || fail 'Cannot create temporary directory.'
defer_signals=0
[ "$interrupted" = 0 ] || exit 1
asset=mobterm-cli_${version}_${system}_${arch}.tar.gz
base=$repo/releases/download/$version
for file in checksums.txt "$asset"; do
    curl -q -fsSL --proto '=https' --proto-redir '=https' \
        --connect-timeout 15 --max-time 300 --max-redirs 5 \
        -o "$work/$file" "$base/$file" || fail "Release download failed: $file"
done
expected=$(awk -v name="$asset" '
    { filename = $2; sub(/^\*/, "", filename) }
    filename == name {
        count++
        if (NF != 2 || length($1) != 64 || $1 ~ /[^0-9a-fA-F]/) invalid = 1
        hash = tolower($1)
    }
    END { if (count != 1 || invalid) exit 1; print hash }
' "$work/checksums.txt") || fail 'Missing, invalid or duplicate SHA-256 record.'
if [ "$sha_tool" = sha256sum ]; then
    actual=$(sha256sum "$work/$asset") || fail 'SHA-256 calculation failed.'
else
    actual=$(shasum -a 256 "$work/$asset") || fail 'SHA-256 calculation failed.'
fi
actual=${actual%% *}
[ "$actual" = "$expected" ] || fail 'Release SHA-256 mismatch; existing binary preserved.'

# Follow the existing package/upgrader layout. Listing all entries before
# streaming just the binary avoids extracting paths or links onto the filesystem.
tar -tzf "$work/$asset" > "$work/names" || fail 'Corrupt release archive.'
awk '
    !/^(mobterm-cli|LICENSE|README\.md|THIRD_PARTY\.md|build-info\.json)$/ { invalid = 1 }
    { if (seen[$0]++) invalid = 1 }
    END { exit (invalid || seen["mobterm-cli"] != 1) }
' "$work/names" || fail 'Unsafe archive paths or missing/duplicate mobterm-cli.'
tar -tvzf "$work/$asset" > "$work/types" || fail 'Corrupt release archive.'
awk 'substr($0, 1, 1) != "-" { invalid = 1 } END { exit (NR == 0 || invalid) }' \
    "$work/types" || fail 'Release archive must contain only regular files.'
tar -xOzf "$work/$asset" mobterm-cli > "$work/mobterm-cli" || fail 'Cannot read release executable.'
[ -s "$work/mobterm-cli" ] || fail 'Release executable is empty.'

destination=$install_dir/mobterm-cli
check_directory() {
    [ ! -L "$1" ] || fail 'Refusing a symlink installation directory.'
    if [ -e "$1" ]; then
        [ -d "$1" ] || fail 'Installation path must be a directory.'
    fi
}
prepare_directory() {
    check_directory "$1"
    if [ ! -d "$1" ]; then
        # Defer a caught signal until the successful mkdir has been recorded.
        defer_signals=1
        if mkdir "$1"; then
            case $2 in
                local) created_local=1 ;;
                bin) created_bin=1 ;;
            esac
        else
            if [ -e "$1" ] || [ -L "$1" ]; then
                warn_cleanup 'Installation directory appeared during failed creation; retained for manual inspection.'
            fi
            fail 'Cannot create installation directory.'
        fi
        defer_signals=0
        [ "$interrupted" = 0 ] || exit 1
    fi
}
check_destination() {
    [ ! -L "$destination" ] || fail 'Refusing a symlink installation target.'
    if [ -e "$destination" ]; then
        [ -f "$destination" ] && [ -w "$destination" ] || fail 'Installation target must be a writable regular file.'
    fi
}
check_directory "$local_dir"
check_directory "$install_dir"
check_destination
prepare_directory "$local_dir" local
prepare_directory "$install_dir" bin
[ -d "$install_dir" ] && [ -w "$install_dir" ] || fail 'Installation directory is not writable.'
defer_signals=1
staged=$(mktemp "$install_dir/.mobterm-cli-install.XXXXXXXX") || fail 'Cannot stage executable in installation directory.'
defer_signals=0
[ "$interrupted" = 0 ] || exit 1
cat "$work/mobterm-cli" > "$staged" || fail 'Cannot write staged executable.'
chmod 0755 "$staged" || fail 'Cannot set executable permissions.'
check_destination
# Record a successful move before honoring a signal arriving during that move.
defer_signals=1
mv -f "$staged" "$destination" || fail 'Cannot atomically install executable.'
committed=1
staged=
defer_signals=0
[ "$interrupted" = 0 ] || exit 1

printf 'Installed mobterm-cli %s (%s/%s)\nPath: %s\n' "$version" "$system" "$arch" "$destination"
printf 'Check version: "%s" version\n' "$destination"
case :${PATH:-}: in
    *:"$install_dir":*) ;;
    *) printf 'PATH does not include %s. Add it to your interactive and non-interactive SSH PATH, or use the absolute path above.\n' "$install_dir" ;;
esac
printf 'Continue pairing and Agent hook configuration in the corresponding Mobterm App connection.\n'
