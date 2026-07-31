#!/bin/sh
set -eu

project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
bin_dir="$project_dir/bin"
binary=herdr-source-control
version_file="$project_dir/VERSION"

fail() {
    printf '%s\n' "herdr-source-control: $*" >&2
    exit 1
}

[ "$(uname -s)" = Linux ] || fail "only Linux is supported"

case $(uname -m) in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) fail "unsupported architecture: $(uname -m) (supported: amd64, arm64)" ;;
esac

[ -f "$version_file" ] || fail "VERSION is missing"
[ "$(wc -l < "$version_file" | tr -d ' ')" = 1 ] || fail "VERSION must contain exactly one line"
version=$(LC_ALL=C tr -d '\n' < "$version_file")
case "$version" in
    ''|*[!0-9.]*) fail "VERSION is invalid" ;;
esac

asset="${binary}_${version}_linux_${arch}.tar.gz"
release_url="https://github.com/mariotmc/herdr-source-control/releases/download/v${version}"

mkdir -p "$bin_dir"
temp_dir="$bin_dir/.install-$$"
new_binary="$bin_dir/$binary.new"
mkdir "$temp_dir" || fail "could not create installation directory"

cleanup() {
    rm -rf "$temp_dir"
    rm -f "$new_binary"
}
trap cleanup 0
trap 'exit 1' HUP INT TERM

archive="$temp_dir/$asset"
checksums="$temp_dir/checksums.txt"

download_archive() {
    url=$1
    output=$2

    if command -v curl >/dev/null 2>&1; then
        if status=$(curl -fL --silent --show-error --output "$output" --write-out '%{http_code}' "$url"); then
            return 0
        fi
        [ "$status" = 404 ] && return 44
        fail "could not download $asset"
    elif command -v wget >/dev/null 2>&1; then
        log="$temp_dir/wget-archive.log"
        if wget --server-response -O "$output" "$url" 2>"$log"; then
            return 0
        fi
        if LC_ALL=C grep -Eq 'HTTP/[0-9.]+ 404([[:space:]]|$)' "$log"; then
            return 44
        fi
        fail "could not download $asset"
    else
        fail "curl or wget is required"
    fi
}

download_file() {
    url=$1
    output=$2

    if command -v curl >/dev/null 2>&1; then
        curl -fL --silent --show-error --output "$output" "$url" || return 1
    elif command -v wget >/dev/null 2>&1; then
        wget -q -O "$output" "$url" || return 1
    else
        return 1
    fi
}

go_is_compatible() {
    command -v go >/dev/null 2>&1 || return 1
    go_version=$(go env GOVERSION 2>/dev/null) || return 1
    go_version=${go_version#go}
    go_major=${go_version%%.*}
    go_rest=${go_version#*.}
    go_minor=${go_rest%%.*}

    case "$go_major:$go_minor" in
        *[!0-9:]*|:|*:) return 1 ;;
    esac
    [ "$go_major" -gt 1 ] || { [ "$go_major" -eq 1 ] && [ "$go_minor" -ge 25 ]; }
}

install_from_source() {
    go_is_compatible || fail "release asset was not found; source fallback requires Go 1.25 or newer"
    printf '%s\n' "Release asset not found; building herdr-source-control from source." >&2
    (
        cd "$project_dir"
        go build -trimpath -o "$new_binary" ./cmd/herdr-source-control
    ) || fail "source build failed"
    chmod 0755 "$new_binary"
    mv -f "$new_binary" "$bin_dir/$binary"
}

archive_result=0
download_archive "$release_url/$asset" "$archive" || archive_result=$?
if [ "$archive_result" -eq 44 ]; then
    install_from_source
    exit 0
fi
[ "$archive_result" -eq 0 ] || fail "could not download $asset"

download_file "$release_url/checksums.txt" "$checksums" || fail "could not download checksums.txt"

expected=
matches=0
while IFS= read -r line || [ -n "$line" ]; do
    digest=${line%%  *}
    if [ "$line" = "$digest  $asset" ] && [ "${#digest}" -eq 64 ]; then
        case "$digest" in
            *[!0-9a-f]*) ;;
            *) expected=$digest; matches=$((matches + 1)) ;;
        esac
    fi
done < "$checksums"
[ "$matches" -eq 1 ] || fail "checksums.txt must contain exactly one valid checksum for $asset"

if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "$archive")
    actual=${actual%% *}
elif command -v shasum >/dev/null 2>&1; then
    actual=$(shasum -a 256 "$archive")
    actual=${actual%% *}
else
    fail "sha256sum or shasum is required"
fi
[ "$actual" = "$expected" ] || fail "checksum verification failed for $asset"

command -v tar >/dev/null 2>&1 || fail "tar is required"
members=$(tar --quoting-style=escape -tzf "$archive") || fail "could not safely inspect $asset (GNU tar is required)"
[ "$members" = "$binary" ] || fail "archive must contain exactly one file named $binary"
details=$(tar --quoting-style=escape -tvzf "$archive") || fail "could not inspect $asset"
case "$details" in
    -*) ;;
    *) fail "archive member $binary must be a regular file" ;;
esac

extract_dir="$temp_dir/extract"
mkdir "$extract_dir"
tar -xzf "$archive" -C "$extract_dir" -- "$binary" || fail "could not extract $asset"
[ -f "$extract_dir/$binary" ] && [ ! -L "$extract_dir/$binary" ] ||
    fail "archive member $binary must be a regular file"

cp "$extract_dir/$binary" "$new_binary"
chmod 0755 "$new_binary"
mv -f "$new_binary" "$bin_dir/$binary"
