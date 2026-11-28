#!/bin/sh
# Install only the official Release binary; never execute downloaded content.
set -eu
umask 077

phase=before_replace
code=metadata
request_id=
fail() {
    printf 'mobterm-cli install: %s\n' "$1" >&2
    if [ -n "$request_id" ]; then
        printf 'MOBTERM_CLI_INSTALL_ERROR_V1 %s %s %s\n' "$request_id" "$phase" "$code" >&2
    fi
    exit 1
}
usage() { fail 'Usage: sh install.sh [--version vX.Y.Z [--expected-release-id ID]] [--request-id UUID]'; }

version=
expected_id=
while [ "$#" -gt 0 ]; do
    [ "$#" -ge 2 ] && [ -n "$2" ] || usage
    case $1 in
        --version)
            [ -z "$version" ] || usage
            case $2 in *[!v0-9.]*) usage ;; esac
            LC_ALL=C awk -v tag="$2" 'BEGIN { exit !(tag ~ /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/) }' || usage
            version=$2 ;;
        --expected-release-id)
            [ -z "$expected_id" ] || usage
            case $2 in 0*|*[!0-9]*) usage ;; esac
            expected_id=$2 ;;
        --request-id)
            [ -z "$request_id" ] || usage
            case $2 in *[!a-fA-F0-9-]*) usage ;; esac
            case $2 in ????????-????-????-????-????????????)
                hex_parts=$(printf '%s' "$2" | tr -d '-')
                [ "${#hex_parts}" = 32 ] || usage ;;
                *) usage ;;
            esac
            request_id=$2 ;;
        *) usage ;;
    esac
    shift 2
done
[ -z "$expected_id" ] || [ -n "$version" ] || usage

code=dependency
for tool in curl uname awk tar mktemp mkdir rmdir cat chmod mv rm wc tr cmp date od; do
    command -v "$tool" >/dev/null 2>&1 || fail "Required tool unavailable: $tool"
done
if command -v sha256sum >/dev/null 2>&1; then
    sha_tool=sha256sum
elif command -v shasum >/dev/null 2>&1; then
    sha_tool=shasum
else
    fail 'SHA-256 verification requires sha256sum or shasum.'
fi
code=unsupported
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
code=storage
case ${HOME:-} in /*) ;; *) fail 'HOME must be an absolute directory path.' ;; esac
case ${TMPDIR:-/tmp} in /*) ;; *) fail 'TMPDIR must be an absolute directory path.' ;; esac

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
# Curl resolves relative Location values for redirect_url. Each hop gets its
# own request only after the scheme and entire authority have been checked.
fetch() {
    kind=$1 url=$2 output=$3 limit=$4
    case $kind in
        asset) accept=application/octet-stream; budget=300 ;;
        *) accept=application/vnd.github+json; budget=60 ;;
    esac
    deadline=$(( $(date +%s) + budget ))
    hops=0
    while :; do
        code=source_mismatch
        case $url in *[[:space:][:cntrl:]]*|*\\*|*'#'*) fail 'Invalid release URL.' ;; esac
        case $url in https://*/*) ;; *) fail 'Expected a GitHub HTTPS URL.' ;; esac
        authority=${url#https://}
        authority=${authority%%/*}
        case $kind:${authority%:443} in
            repository:api.github.com|release:api.github.com|asset:api.github.com|asset:github.com|\
            asset:release-assets.githubusercontent.com|asset:objects.githubusercontent.com) ;;
            *) fail 'Unexpected release source.' ;;
        esac
        code=network
        remaining=$((deadline - $(date +%s)))
        [ "$remaining" -gt 0 ] || fail 'GitHub request timed out.'
        # Older curl versions need a file cap for responses of unknown length.
        # Leave room for an excess byte so a partial write at the hard limit
        # is distinguishable from an interrupted, in-budget download.
        curl_status=0
        (ulimit -f "$((limit / 512 + 1))" &&
            exec curl -q -s --globoff --proto '=https' --connect-timeout 15 \
                --max-time "$remaining" --max-filesize "$limit" \
                -H "Accept: $accept" -D "$work/headers" -o "$output" \
                -w '%{http_code}\n%{redirect_url}\n' -- "$url") > "$work/response" 2>/dev/null || curl_status=$?
        size=0
        if [ -f "$output" ]; then
            size=$(wc -c < "$output") || fail 'Cannot read downloaded file.'
        fi
        if [ "$curl_status" = 63 ] || [ "$size" -gt "$limit" ]; then
            case $kind in asset) code=integrity ;; *) code=metadata ;; esac
            fail 'GitHub response exceeds the size limit.'
        fi
        [ "$curl_status" = 0 ] || fail 'Cannot complete GitHub request.'
        {
            IFS= read -r status && IFS= read -r redirect || fail 'Invalid GitHub response.'
            if IFS= read -r extra; then code=source_mismatch; fail 'Invalid redirect.'; fi
        } < "$work/response"
        case $status in
            200) return ;;
            301|302|303|307|308)
                code=source_mismatch
                [ "$hops" -lt 5 ] && [ -n "$redirect" ] || fail 'Invalid redirect chain.'
                hops=$((hops + 1))
                url=$redirect ;;
            404)
                case $kind in
                    repository) code=source_mismatch; fail 'Cannot locate the official repository.' ;;
                    release) return 4 ;;
                    *) fail 'Cannot download the selected release asset.' ;;
                esac ;;
            429) code=rate_limited; fail 'GitHub rate limit reached.' ;;
            403)
                if LC_ALL=C awk 'tolower($0) ~ /^(x-ratelimit-remaining:[ \t]*0[ \t\r]*$|retry-after:)/ { found=1 } END { exit !found }' "$work/headers"; then
                    code=rate_limited; fail 'GitHub rate limit reached.'
                fi
                if [ "${authority%:443}" = api.github.com ] && [ "$size" -gt 0 ] && [ "$size" -le 8192 ] &&
                    read_metadata rate_limit "$output"; then
                    code=rate_limited; fail 'GitHub rate limit reached.'
                fi
                fail 'GitHub refused the request.' ;;
            *) fail 'GitHub request failed.' ;;
        esac
    done
}

# Read only the repository, release and asset fields used below. The parser
# consumes the complete JSON value, including nested fields it does not use.
# IDs are kept as strings throughout; awk numbers cannot represent every ID.
nul_free() {
    # Some POSIX awk versions silently discard the rest of a line at a NUL.
    LC_ALL=C tr -d '\000' < "$1" > "$work/nonul" && cmp -s "$work/nonul" "$1"
}
read_metadata() {
    nul_free "$2" || return 2
    LC_ALL=C awk -v mode="$1" -v repo="$repo_name" -v requested="$version" \
        -v expected="$expected_id" -v platform="${system}_${arch}" '
    BEGIN {
        hex = "0123456789abcdef"
        for (i=1; i<256; i++) byte[sprintf("%c", i)] = i
    }
    { doc = doc $0 "\n" }
    function bad() { exit 2 }
    function peek() { return substr(doc, pos, 1) }
    function white() { while (peek() ~ /^[ \t\r\n]$/) pos++ }
    function take(c) { if (peek()!=c) bad(); pos++ }
    function hex4(    s,i,n,d) {
        s=tolower(substr(doc,pos,4)); if (length(s)!=4) bad()
        for (i=1;i<=4;i++) {
            d=index(hex,substr(s,i,1)); if (!d) bad()
            n=n*16+d-1
        }
        pos+=4; return n
    }
    function utf8(n) {
        # Canonical spelling for controls and backslash keeps object keys
        # distinct even on an awk that cannot store NUL in a string.
        if (n<32 || n==92) return sprintf("\\u%04x",n)
        if (n<128) return sprintf("%c",n)
        if (n<2048) return sprintf("%c%c",192+int(n/64),128+n%64)
        if (n<65536) return sprintf("%c%c%c",224+int(n/4096),128+int(n/64)%64,128+n%64)
        return sprintf("%c%c%c%c",240+int(n/262144),128+int(n/4096)%64,128+int(n/64)%64,128+n%64)
    }
    function json_string(    start,out,c,n,lo,len,b,i) {
        take("\""); start=pos
        while (pos<=length(doc)) {
            c=peek(); pos++
            if (c=="\"") return out substr(doc,start,pos-start-1)
            if (c=="\\") {
                out=out substr(doc,start,pos-start-1)
                c=peek(); pos++
                if (c=="u") {
                    n=hex4()
                    if (n>=55296 && n<=56319) {
                        if (substr(doc,pos,2)!="\\u") bad()
                        pos+=2; lo=hex4(); if (lo<56320 || lo>57343) bad()
                        n=65536+(n-55296)*1024+lo-56320
                    } else if (n>=56320 && n<=57343) bad()
                } else if (c=="b") n=8
                else if (c=="f") n=12
                else if (c=="n") n=10
                else if (c=="r") n=13
                else if (c=="t") n=9
                else if (c=="\\" || c=="\"" || c=="/") n=byte[c]
                else bad()
                out=out utf8(n); start=pos
            } else {
                n=byte[c]
                if (n<32) bad()
                if (n>=128) {
                    if (n<194 || n>244) bad()
                    len=(n<224 ? 1 : (n<240 ? 2 : 3))
                    for (i=1;i<=len;i++) {
                        b=byte[peek()]; pos++
                        if (b<128 || b>191 || (i==1 && ((n==224 && b<160) || (n==237 && b>=160) || (n==240 && b<144) || (n==244 && b>=144)))) bad()
                    }
                }
            }
        }
        bad()
    }
    function number(    start,s) {
        start=pos
        while (peek() ~ /^[0-9eE+.-]$/) pos++
        s=substr(doc,start,pos-start)
        if (s !~ /^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$/) bad()
        return s
    }
    function value(depth,scope,    c,key,child,obj,start,typ,s) {
        if (depth>64) bad()
        white(); c=peek()
        if (c=="{") {
            typ="object"; obj=++objects; pos++; white()
            if (peek()!="}") {
                while (1) {
                    key=json_string()
                    if (seen[obj SUBSEP key]++) bad()
                    white(); take(":")
                    child=""
                    if (scope=="root" && (key=="id" || (mode=="repository" && key=="full_name") || (mode=="release" && key ~ /^(tag_name|draft|prerelease|url|assets)$/))) child="root." key
                    if (scope=="root" && mode=="rate_limit" && key=="message") child="root.message"
                    if (scope=="asset" && key ~ /^(id|name|url|digest)$/) child="asset." key
                    value(depth+1,child)
                    white(); if (peek()!=",") break
                    pos++; white()
                }
            }
            take("}")
        } else if (c=="[") {
            typ="array"; pos++; white()
            if (peek()!="]") {
                while (1) {
                    child=""
                    if (scope=="root.assets") { assets++; child="asset" }
                    value(depth+1,child)
                    white(); if (peek()!=",") break
                    pos++; white()
                }
            }
            take("]")
        } else if (c=="\"") { typ="string"; s=json_string() }
        else if (substr(doc,pos,4)=="true" || substr(doc,pos,4)=="null") {
            s=substr(doc,pos,4); typ=s; pos+=4
        } else if (substr(doc,pos,5)=="false") { s="false"; typ=s; pos+=5 }
        else { typ="number"; s=number() }
        if (scope=="root" || scope=="asset") { if (typ!="object") bad() }
        else if (scope ~ /^root\./) { kind[scope]=typ; field[scope]=s }
        else if (scope ~ /^asset\./) { kind[assets,scope]=typ; field[assets,scope]=s }
    }
    function required(key,t) { if (kind[key]!=t) bad(); return field[key] }
    function id(key,    s) {
        s=required(key,"number")
        if (s !~ /^[1-9][0-9]*$/ || length(s)>19 || (length(s)==19 && "i"s>"i9223372036854775807")) bad()
        return s
    }
    END {
        pos=1; value(0,"root"); white(); if (pos<=length(doc)) bad()
        if (mode=="rate_limit") {
            message=tolower(required("root.message","string"))
            exit !(message ~ /^you have (exceeded|triggered) a secondary rate limit([ .]|$)/)
        }
        rid=id("root.id")
        if (mode=="repository") {
            if ("i" rid!="i1416491919") exit 3
            full=required("root.full_name","string")
            if (full !~ /^[A-Za-z0-9-]+\/[A-Za-z0-9._-]+$/ || full ~ /\/\.\.?$/) bad()
            print full; exit
        }
        api="https://api.github.com/repos/" repo "/releases/"
        if (required("root.url","string")!=api rid) exit 3
        tag=required("root.tag_name","string")
        if (kind["root.draft"] !~ /^(true|false)$/ || kind["root.prerelease"] !~ /^(true|false)$/) bad()
        if (kind["root.draft"]=="true" || kind["root.prerelease"]=="true" || tag !~ /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/) exit 4
        if ((requested!="" && tag!=requested) || (expected!="" && "i"rid!="i"expected)) exit 3
        required("root.assets","array")
        package="mobterm-cli_" tag "_" platform ".tar.gz"
        for (i=1;i<=assets;i++) {
            aid=id(i SUBSEP "asset.id")
            name=required(i SUBSEP "asset.name","string")
            url=required(i SUBSEP "asset.url","string")
            if (length(name)==0 || usedname["n"name]++ || usedid["i"aid]++) bad()
            if (url!=api "assets/" aid) exit 3
            digest="-"; key=i SUBSEP "asset.digest"
            if (key in kind && kind[key]!="null") {
                d=required(key,"string")
                if (length(d)!=71 || d !~ /^sha256:[0-9a-f]+$/) bad()
                digest=substr(d,8)
            }
            if (name==package || name=="checksums.txt") { urls[name]=url; hashes[name]=digest }
        }
        if (!(package in urls) || !("checksums.txt" in urls)) bad()
        print tag; print urls["checksums.txt"]; print hashes["checksums.txt"]
        print urls[package]; print hashes[package]
    }' "$2"
}

resolve_repo() {
    fetch repository "https://api.github.com/repos/$1" "$work/metadata" 262144
    if read_metadata repository "$work/metadata" > "$work/repository"; then
        IFS= read -r current_name < "$work/repository"
    else
        parse_status=$?; code=metadata
        if [ "$parse_status" = 3 ]; then code=source_mismatch; fail 'Repository ID does not match the official source.'; fi
        fail 'Invalid GitHub repository metadata.'
    fi
}
repo_name=
attempt=1
while :; do
    resolve_repo mobterm/mobterm-cli
    repo_name=$current_name
    release_path=latest
    if [ -n "$version" ]; then release_path=tags/$version; fi
    if fetch release "https://api.github.com/repos/$repo_name/releases/$release_path" "$work/metadata" 262144; then
        if read_metadata release "$work/metadata" > "$work/selection"; then result=0; else result=$?; fi
    else
        result=$?
    fi
    # The old name can be reused between lookup and release selection.
    # Recheck the exact name used for this metadata response, including 404s.
    resolve_repo "$repo_name"
    if [ "$current_name" != "$repo_name" ] && [ "$attempt" = 1 ]; then attempt=2; continue; fi
    code=source_mismatch
    [ "$current_name" = "$repo_name" ] || fail 'Repository location changed during release selection.'
    case $result in
        0) break ;;
        3) fail 'Release source or confirmed version does not match.' ;;
        4) code=release_unavailable; fail 'The selected stable release is unavailable.' ;;
        *) code=metadata; fail 'Invalid GitHub release metadata.' ;;
    esac
done
{
    IFS= read -r version && IFS= read -r checks_url && IFS= read -r checks_digest &&
        IFS= read -r archive_url && IFS= read -r archive_digest
} < "$work/selection" || fail 'Cannot read verified release selection.'
asset=mobterm-cli_${version}_${system}_${arch}.tar.gz

sha256() {
    if [ "$sha_tool" = sha256sum ]; then
        sha256sum < "$1"
    else
        shasum -a 256 < "$1"
    fi
}
verify_digest() {
    code=integrity
    hash=$(sha256 "$1") || fail 'Cannot calculate SHA-256.'
    hash=${hash%% *}
    [ "$2" = - ] || [ "$hash" = "$2" ] || fail 'Release asset digest mismatch.'
}
fetch asset "$checks_url" "$work/checksums.txt" 16384
verify_digest "$work/checksums.txt" "$checks_digest"
fetch asset "$archive_url" "$work/$asset" 67108864
verify_digest "$work/$asset" "$archive_digest"

nul_free "$work/checksums.txt" || fail 'Invalid checksum file.'
expected=$(awk -v name="$asset" '
    { filename = $2; sub(/^\*/, "", filename) }
    filename == name {
        count++
        if (NF != 2 || length($1) != 64 || $1 ~ /[^0-9a-fA-F]/) invalid = 1
        hash = tolower($1)
    }
    END { if (count != 1 || invalid) exit 1; print hash }
' "$work/checksums.txt") || fail 'Missing, invalid or duplicate SHA-256 record.'
[ "$hash" = "$expected" ] || fail 'Release SHA-256 mismatch; existing binary preserved.'

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
(ulimit -f 131072 && exec tar -xOzf "$work/$asset" mobterm-cli) > "$work/mobterm-cli" || fail 'Cannot read release executable.'
[ -s "$work/mobterm-cli" ] || fail 'Release executable is empty.'

# Inspect the packaged executable without running it. Both supported 64-bit
# architectures use little-endian ELF or Mach-O headers.
od -A n -N 20 -t u1 "$work/mobterm-cli" > "$work/header" || fail 'Cannot read executable header.'
LC_ALL=C awk -v os="$system" -v arch="$arch" '
    { for (i=1; i<=NF; i++) h[++n]=$i }
    END {
        if (n!=20) exit 1
        if (os=="linux") {
            machine=(arch=="amd64" ? 62 : 183)
            exit !(h[1]==127 && h[2]==69 && h[3]==76 && h[4]==70 && h[5]==2 && h[6]==1 && h[7]==1 &&
                   (h[17]==2 || h[17]==3) && h[18]==0 && h[19]==machine && h[20]==0)
        }
        cpu=(arch=="amd64" ? 7 : 12)
        exit !(h[1]==207 && h[2]==250 && h[3]==237 && h[4]==254 && h[5]==cpu && h[6]==0 && h[7]==0 && h[8]==1 &&
               h[13]==2 && h[14]==0 && h[15]==0 && h[16]==0)
    }' "$work/header" || fail 'Release executable platform mismatch.'
code=storage
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
phase=unknown
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
