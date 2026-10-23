#!/bin/sh
# SPDX-License-Identifier: Unlicense OR MIT
# Install Haiku build packages and prepare komarugif/go-haiku without needing Go.
set -eu

usage() {
    cat <<'HELP'
Usage: sh cmd/haiku-setup.sh [--go-dir PATH] [--no-profile]

Install git, golang and ffmpeg6_tools with pkgman, then build go-haiku
(branch golang-1.27-haiku). Default directory: $HOME/go-haiku.
A compatible existing toolchain is reused; it is not updated automatically.
--go-dir PATH   Use a different source/toolchain directory.
--no-profile    Do not add the toolchain to ~/config/settings/profile.
-h, --help     Show this help.

GOROOT_BOOTSTRAP can select a bootstrap Go other than Haiku's package.
The script does not build the messenger. Afterwards run cmd/haiku-build.
HELP
}

fail() { printf 'haiku-setup: %s\n' "$*" >&2; exit 1; }
say() { printf 'haiku-setup: %s\n' "$*"; }

[ -n "${HOME:-}" ] || fail 'HOME is not set'
go_dir=$HOME/go-haiku
write_profile=yes
while [ "$#" -gt 0 ]; do
    case "$1" in
        --go-dir)
            [ "$#" -ge 2 ] || fail '--go-dir needs a path'
            go_dir=$2
            shift 2
            ;;
        --no-profile) write_profile=no; shift ;;
        -h|--help) usage; exit 0 ;;
        *) usage >&2; fail "unknown argument: $1" ;;
    esac
done

[ "$(uname -s)" = Haiku ] || fail 'run this script on Haiku'
[ "$(uname -m)" = x86_64 ] || fail 'this setup supports Haiku x86_64 only'
[ -n "$go_dir" ] || fail 'the Go directory must not be empty'
case "$go_dir" in
    /*) ;;
    *) go_dir=$(pwd)/$go_dir ;;
esac
# Canonicalize the parent so that GOROOT and profile paths are stable.
parent=$(dirname "$go_dir")
mkdir -p "$parent"
parent=$(CDPATH= cd "$parent" && pwd -P)
go_dir=$parent/$(basename "$go_dir")
if [ -d "$go_dir" ]; then
    go_dir=$(CDPATH= cd "$go_dir" && pwd -P)
fi
[ "$go_dir" != / ] && [ "$go_dir" != "$HOME" ] || fail 'choose a dedicated Go directory'

repo=https://github.com/komarugif/go-haiku
branch=golang-1.27-haiku
bootstrap=${GOROOT_BOOTSTRAP:-/boot/system/develop/lib/go}
clone_tmp=
cleanup() {
    if [ -n "$clone_tmp" ]; then
        rm -rf "$clone_tmp"
    fi
}
trap cleanup 0
trap 'exit 130' INT
trap 'exit 143' TERM

command -v pkgman >/dev/null 2>&1 || fail 'pkgman is not available'
say 'installing build packages: git, golang, ffmpeg6_tools'
# Avoid repository refreshes on repeat runs when every required system
# package is already present. Missing packages still refresh normally.
all_installed=yes
for package in git golang ffmpeg6_tools; do
    found=no
    for file in /boot/system/packages/"$package"-[0-9]*.hpkg; do
        if [ -f "$file" ]; then
            found=yes
            break
        fi
    done
    if [ "$found" = no ]; then
        all_installed=no
    fi
done
if [ "$all_installed" = yes ]; then
    pkgman install -y -R git golang ffmpeg6_tools
else
    pkgman install -y git golang ffmpeg6_tools
fi
[ -x "$bootstrap/bin/go" ] || fail "bootstrap Go not found at $bootstrap/bin/go"
command -v git >/dev/null 2>&1 || fail 'git is not available after package installation'
command -v gcc >/dev/null 2>&1 || fail 'the Haiku C compiler (gcc) is not available'
command -v bash >/dev/null 2>&1 || fail 'bash is required by Go make.bash'

if [ ! -e "$go_dir" ]; then
    clone_tmp=$(mktemp -d "$parent/.komarugram-go-source.XXXXXX")
    say "cloning $repo ($branch)"
    git clone --depth 1 --single-branch --branch "$branch" "$repo" "$clone_tmp"
    [ ! -e "$go_dir" ] || fail "Go directory appeared during cloning: $go_dir"
    mv "$clone_tmp" "$go_dir"
    clone_tmp=
fi
[ -d "$go_dir/.git" ] || fail "$go_dir is not a Git checkout; choose another --go-dir"
origin=$(git -C "$go_dir" remote get-url origin)
case "$origin" in
    "$repo"|"$repo.git"|git@github.com:komarugif/go-haiku.git) ;;
    *) fail "refusing to change a checkout from $origin; choose another --go-dir" ;;
esac
[ "$(git -C "$go_dir" branch --show-current)" = "$branch" ] || fail "expected branch $branch; choose another --go-dir"
[ -z "$(git -C "$go_dir" status --porcelain)" ] || fail "Go sources have local changes; choose another --go-dir"
[ -f "$go_dir/src/make.bash" ] || fail 'Go sources are incomplete; choose another --go-dir'
[ -f "$go_dir/src/cmd/vendor/golang.org/x/sys/unix/syscall_haiku.go" ] || fail 'Go sources lack the Haiku patches'
commit=$(git -C "$go_dir" rev-parse HEAD)
version=$(sed -n '1p' "$go_dir/VERSION")
expected="go version $version haiku/amd64"

# Never inherit another Go's GOROOT or a caller's cross-compilation target.
toolchain() (
    unset GOROOT GOOS GOARCH GOHOSTOS GOHOSTARCH
    GOENV=off GOWORK=off GOTOOLCHAIN=local "$go_dir/bin/go" "$@"
)

if [ -x "$go_dir/bin/go" ]; then
    actual=$(toolchain version) || fail 'existing Go does not run; choose a fresh --go-dir'
    [ "$actual" = "$expected" ] || fail "existing Go is $actual; expected $expected; choose a fresh --go-dir"
    if [ -f "$go_dir/pkg/.komarugram-setup-commit" ]; then
        [ "$(cat "$go_dir/pkg/.komarugram-setup-commit")" = "$commit" ] || fail 'Go sources changed since setup; choose a fresh --go-dir'
    fi
    say "reusing $actual in $go_dir"
else
    say "building $version from $commit with bootstrap Go at $bootstrap (about 10 minutes)"
    attempt=1
    while :; do
        say "bootstrap attempt $attempt of 3"
        if (
            cd "$go_dir/src"
            unset GOROOT GOOS GOARCH GOHOSTOS GOHOSTARCH GOFLAGS
            GOMAXPROCS=1 GODEBUG=asyncpreemptoff=1 GOROOT_BOOTSTRAP="$bootstrap" \
                GOENV=off GOWORK=off GOTOOLCHAIN=local CGO_ENABLED=1 ./make.bash
        ); then
            break
        fi
        [ "$attempt" -lt 3 ] || fail "Go bootstrap failed after 3 attempts; sources remain in $go_dir. See docs/BUILD_HAIKU.md for clean-rebuild steps, or choose a fresh --go-dir"
        say 'bootstrap failed; retrying without resetting the checkout (Haiku bootstrap Go can be unstable)'
        attempt=$((attempt + 1))
    done
    [ "$(toolchain version)" = "$expected" ] || fail 'the built Go has an unexpected version or target'
    printf '%s\n' "$commit" > "$go_dir/pkg/.komarugram-setup-commit"
fi
[ "$(toolchain env GOROOT)" = "$go_dir" ] || fail 'Go resolves a different GOROOT'
for file in bin/gofmt pkg/tool/haiku_amd64/asm pkg/tool/haiku_amd64/compile pkg/tool/haiku_amd64/link pkg/tool/haiku_amd64/cgo; do
    [ -x "$go_dir/$file" ] || fail "incomplete Go toolchain: $file is missing; choose a fresh --go-dir"
done

# Quote a literal path for a profile without evaluating its contents.
quote_sh() {
    rest=$1
    printf "'"
    while :; do
        case "$rest" in
            *"'"*)
                prefix=${rest%%"'"*}
                printf '%s' "$prefix"
                printf '%s' "'\''"
                rest=${rest#*"'"}
                ;;
            *) break ;;
        esac
    done
    printf "%s'" "$rest"
}

if [ "$go_dir" = "$HOME/go-haiku" ]; then
    profile_line='export PATH=$HOME/go-haiku/bin:$PATH'
else
    quoted_bin=$(quote_sh "$go_dir/bin")
    profile_line="export PATH=$quoted_bin:\$PATH"
fi
if [ "$write_profile" = yes ]; then
    profile=$HOME/config/settings/profile
    mkdir -p "$(dirname "$profile")"
    if [ ! -f "$profile" ] || ! grep -F -x "$profile_line" "$profile" >/dev/null 2>&1; then
        printf '\n%s\n' "$profile_line" >> "$profile"
        say "added PATH to $profile"
    else
        say "PATH is already present in $profile"
    fi
fi
say "ready: $(toolchain version)"
printf '\nFor this Terminal (a child script cannot change its PATH), run:\n%s\n' "$profile_line"
printf '\nThen, from KomaruGram, run:\ngo run -ldflags=-linkmode=internal ./cmd/haiku-build -o "$HOME/apps/KomaruGram"\n'
