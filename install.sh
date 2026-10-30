#!/bin/sh
# Installs syspi, the SysML Package Installer, from a GitHub release.
#
# Linux and macOS (and Git Bash or MSYS2 on Windows; install.ps1 is the native
# Windows installer):
#
#   curl -fsSL https://raw.githubusercontent.com/Open-MBEE/SysPI/main/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/Open-MBEE/SysPI/main/install.sh | sh -s -- --version v0.1.0
#
# The release's archive (syspi-<os>-<arch>.tar.gz, .zip on Windows) is downloaded
# with its SHA256SUMS.txt manifest, checked against the manifest before anything is
# installed, and the installed binary is run once to confirm it reports the release
# it came from. Downloads go through curl or wget, which never set macOS's
# quarantine attribute, so Gatekeeper does not object.
#
# Run with --help for the options.
#
# Needs: curl or wget; sha256sum, shasum or openssl; and tar (unzip or a
# zip-capable tar for the Windows archive).
set -eu

REPO_URL="https://github.com/Open-MBEE/SysPI"
DEFAULT_BASE_URL="$REPO_URL/releases"

# The identity the checksum manifest is signed with: the release workflow, run on the tag.
RELEASE_OIDC_ISSUER="https://token.actions.githubusercontent.com"
RELEASE_IDENTITY_REGEXP='^https://github\.com/Open-MBEE/SysPI/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'

version="${SYSPI_VERSION:-latest}"
prefix="${SYSPI_PREFIX:-}"
bin_dir="${SYSPI_BIN_DIR:-}"
base_url="${SYSPI_DOWNLOAD_BASE:-$DEFAULT_BASE_URL}"
target_os=""
target_arch=""
verify_signature=false
dry_run=false

usage() {
	# A literal, not read back from $0: piped through `sh` there is no file to read.
	cat <<'USAGE'
usage: install.sh [--version <tag>] [--prefix <dir>|--bin-dir <dir>]
                  [--os <os> --arch <arch>] [--base-url <url>] [--verify-signature] [--dry-run]

Installs syspi, the SysML Package Installer, from a GitHub release, checking the
download against the release's SHA256SUMS.txt.

Options (each has an environment variable, so a piped `sh` can be configured):

  --version <tag>      release tag (v0.1.0) or `latest` (default)     [SYSPI_VERSION]
  --prefix <dir>       install under <dir>/bin; default /usr/local when
                       writable, else ~/.local                         [SYSPI_PREFIX]
  --bin-dir <dir>      install the binary into <dir> (overrides --prefix)
                                                                       [SYSPI_BIN_DIR]
  --os <os>, --arch <arch>
                       stage another platform's build instead of this machine's
                       (linux|darwin|windows, amd64|arm64); it is not run
  --base-url <url>     where the releases are, for a mirror; default
                       https://github.com/Open-MBEE/SysPI/releases    [SYSPI_DOWNLOAD_BASE]
  --verify-signature   also verify SHA256SUMS.txt's cosign bundle against the
                       identity that signs the releases (needs cosign on PATH)
  --dry-run            print what would be installed and from where, then exit
  -h, --help           this text
USAGE
}

info() { printf '%s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
fail() {
	printf 'install.sh: %s\n' "$*" >&2
	exit 1
}

need_value() { # <option> <count of remaining args>
	[ "$2" -ge 2 ] || fail "$1 needs a value (try --help)"
}

while [ $# -gt 0 ]; do
	case "$1" in
	--version)
		need_value "$1" $#
		version=$2
		shift 2
		;;
	--version=*) version="${1#*=}"; shift ;;
	--prefix)
		need_value "$1" $#
		prefix=$2
		shift 2
		;;
	--prefix=*) prefix="${1#*=}"; shift ;;
	--bin-dir)
		need_value "$1" $#
		bin_dir=$2
		shift 2
		;;
	--bin-dir=*) bin_dir="${1#*=}"; shift ;;
	--os)
		need_value "$1" $#
		target_os=$2
		shift 2
		;;
	--os=*) target_os="${1#*=}"; shift ;;
	--arch)
		need_value "$1" $#
		target_arch=$2
		shift 2
		;;
	--arch=*) target_arch="${1#*=}"; shift ;;
	--base-url)
		need_value "$1" $#
		base_url=$2
		shift 2
		;;
	--base-url=*) base_url="${1#*=}"; shift ;;
	--verify-signature) verify_signature=true; shift ;;
	--dry-run) dry_run=true; shift ;;
	-h | --help)
		usage
		exit 0
		;;
	*) fail "unknown option '$1' (try --help)" ;;
	esac
done

# --- which version ----------------------------------------------------------

case "$version" in
latest) ;;
v[0-9]*.[0-9]*.[0-9]*) ;;
[0-9]*.[0-9]*.[0-9]*) version="v$version" ;;
*) fail "--version must be a release tag like v0.1.0 or 'latest', not '$version'" ;;
esac

base_url="${base_url%/}"

# --- the platform -----------------------------------------------------------

host_os=""
host_arch=""
uname_s=$(uname -s 2>/dev/null || echo unknown)
uname_m=$(uname -m 2>/dev/null || echo unknown)
case "$uname_s" in
Linux) host_os=linux ;;
Darwin) host_os=darwin ;;
MINGW* | MSYS* | CYGWIN* | Windows_NT) host_os=windows ;;
*) ;;
esac
case "$uname_m" in
x86_64 | amd64) host_arch=amd64 ;;
aarch64 | arm64) host_arch=arm64 ;;
*) ;;
esac
# A shell under Rosetta reports x86_64; the native build is the one to install.
if [ "$host_os" = darwin ] && [ "$host_arch" = amd64 ] &&
	[ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
	host_arch=arm64
fi

if [ -z "$target_os" ]; then
	target_os="$host_os"
	[ -n "$target_os" ] || fail "unsupported operating system '$uname_s': releases are built for Linux, macOS and Windows; see $REPO_URL#install for building from source"
fi
if [ -z "$target_arch" ]; then
	target_arch="$host_arch"
	[ -n "$target_arch" ] || fail "unsupported architecture '$uname_m': releases are built for amd64 and arm64; 'go install github.com/Open-MBEE/SysPI/cmd/syspi@latest' builds one for this machine"
fi
case "$target_os" in
linux | darwin | windows) ;;
*) fail "--os must be linux, darwin or windows, not '$target_os'" ;;
esac
case "$target_arch" in
amd64 | arm64) ;;
*) fail "--arch must be amd64 or arm64, not '$target_arch'" ;;
esac
platform="$target_os-$target_arch"
is_host_platform=false
[ "$target_os" != "$host_os" ] || [ "$target_arch" != "$host_arch" ] || is_host_platform=true

exe=""
[ "$target_os" != windows ] || exe=".exe"
archive="syspi-$platform.tar.gz"
[ "$target_os" != windows ] || archive="syspi-$platform.zip"

# --- where it goes ----------------------------------------------------------

if [ -z "$bin_dir" ]; then
	if [ -z "$prefix" ]; then
		if [ -w /usr/local/bin ] || { [ ! -e /usr/local/bin ] && [ -w /usr/local ]; }; then
			prefix=/usr/local
		else
			prefix="$HOME/.local"
		fi
	fi
	bin_dir="$prefix/bin"
fi

# --- tools on this machine --------------------------------------------------

have() { command -v "$1" >/dev/null 2>&1; }

downloader=""
if have curl; then
	downloader=curl
elif have wget; then
	downloader=wget
else
	fail "curl or wget is needed to download the release"
fi

# Only https is followed when the releases are fetched over https; a plain-http
# mirror is left to its own policy.
curl_proto=""
case "$base_url" in https://*) curl_proto="--proto =https --proto-redir =https" ;; *) ;; esac

download() { # <url> <destination>
	if [ "$downloader" = curl ]; then
		# shellcheck disable=SC2086  # curl_proto is a list of options
		curl -fsSL $curl_proto --retry 3 -o "$2" "$1"
	else
		wget -q -O "$2" "$1"
	fi
}

sha256_of() { # <file>: the hex digest
	if have sha256sum; then
		sha256sum "$1" | cut -d' ' -f1
	elif have shasum; then
		shasum -a 256 "$1" | cut -d' ' -f1
	elif have openssl; then
		openssl dgst -sha256 "$1" | sed 's/.*= //'
	else
		fail "sha256sum, shasum or openssl is needed to verify the download"
	fi
}
have sha256sum || have shasum || have openssl || fail "sha256sum, shasum or openssl is needed to verify the download"

if [ "$verify_signature" = true ]; then
	have cosign || fail "--verify-signature needs cosign on PATH (https://docs.sigstore.dev/cosign/system_config/installation/)"
fi
if [ "$target_os" = windows ]; then
	have unzip || have tar || fail "unzip or tar is needed to extract $archive"
else
	have tar || fail "tar is needed to extract $archive"
fi

# --- which release ----------------------------------------------------------

# Resolves `latest` to its tag through the redirect GitHub serves for it, so the
# release is named before anything is downloaded; a mirror without the redirect
# still works through the `latest/download/` path.
resolve_latest() {
	location=""
	if [ "$downloader" = curl ]; then
		# shellcheck disable=SC2086
		location=$(curl -fsSLI $curl_proto -o /dev/null -w '%{url_effective}' "$base_url/latest" 2>/dev/null) || location=""
	else
		location=$(wget -q --spider --server-response "$base_url/latest" 2>&1 | sed -n 's/^ *[Ll]ocation: *//p' | tail -n 1 | tr -d '\r') || location=""
	fi
	case "$location" in
	*/tag/*) printf '%s\n' "${location##*/tag/}" ;;
	*) return 1 ;;
	esac
}

tag="$version"
asset_dir=""
if [ "$version" = latest ]; then
	if tag=$(resolve_latest); then
		asset_dir="$base_url/download/$tag"
	else
		tag=""
		asset_dir="$base_url/latest/download"
	fi
else
	asset_dir="$base_url/download/$version"
fi
asset_url() { printf '%s/%s\n' "$asset_dir" "$1"; }
release_name="${tag:-the latest release}"

# --- the manifest -----------------------------------------------------------

work=$(mktemp -d 2>/dev/null || mktemp -d -t syspi)
trap 'rm -rf "$work"' EXIT INT TERM

fetch() { # <asset>: into the work directory
	download "$(asset_url "$1")" "$work/$1" ||
		fail "could not download $(asset_url "$1"); is $release_name a published release with a $platform build? See $base_url"
}

# Fetches SHA256SUMS.txt and, when asked, checks its signature before anything
# is read from it.
fetch_manifest() {
	fetch SHA256SUMS.txt
	[ "$verify_signature" = true ] || return 0
	fetch SHA256SUMS.txt.bundle
	cosign verify-blob "$work/SHA256SUMS.txt" --bundle "$work/SHA256SUMS.txt.bundle" \
		--certificate-oidc-issuer "$RELEASE_OIDC_ISSUER" \
		--certificate-identity-regexp "$RELEASE_IDENTITY_REGEXP" >/dev/null 2>&1 ||
		fail "SHA256SUMS.txt is not signed by the SysPI release workflow"
	info "  signature of SHA256SUMS.txt verified"
}

# manifest_digest <asset>: its digest in the manifest, empty when unlisted. A line
# repeated verbatim counts once, so two lines of output mean two different digests.
manifest_digest() {
	awk -v name="$1" '{ n = $2; sub(/^\*/, "", n); if (n == name && !seen[$1]++) print $1 }' "$work/SHA256SUMS.txt"
}

info "Installing syspi ($release_name) for $platform"
info "  binary:   $bin_dir/syspi$exe"
info "  from:     $(asset_url "$archive")"
if [ "$dry_run" = true ]; then
	info "Dry run: nothing installed."
	exit 0
fi

# --- download and verify ----------------------------------------------------

info "Downloading..."
fetch_manifest
fetch "$archive"

newline='
'
expected=$(manifest_digest "$archive")
[ -n "$expected" ] || fail "SHA256SUMS.txt of $release_name does not list $archive; the release has no $platform build"
case $expected in
*"$newline"*) fail "SHA256SUMS.txt of $release_name lists $archive with more than one digest ($(printf '%s' "$expected" | tr '\n' ' ')); the manifest is corrupt or tampered with" ;;
*) ;;
esac
actual=$(sha256_of "$work/$archive")
[ "$actual" = "$expected" ] || fail "$archive does not match SHA256SUMS.txt (expected $expected, got $actual); the download is corrupt or tampered with"
info "  $archive verified"

# --- install ----------------------------------------------------------------

mkdir -p "$work/unpacked"
if [ "$target_os" = windows ]; then
	if have unzip; then
		unzip -q "$work/$archive" -d "$work/unpacked"
	else
		tar -xf "$work/$archive" -C "$work/unpacked"
	fi
else
	tar -xzf "$work/$archive" -C "$work/unpacked"
fi
source="$work/unpacked/syspi$exe"
[ -f "$source" ] || fail "$archive has no syspi$exe"

mkdir -p "$bin_dir" 2>/dev/null || fail "cannot create $bin_dir; pass --prefix or --bin-dir for a writable location, or run with sudo"
[ -w "$bin_dir" ] || fail "cannot write to $bin_dir; pass --prefix or --bin-dir for a writable location, or run with sudo"
destination="$bin_dir/syspi$exe"
cp "$source" "$destination.tmp.$$" && chmod 0755 "$destination.tmp.$$" && mv -f "$destination.tmp.$$" "$destination"

# --- check ------------------------------------------------------------------

if [ "$is_host_platform" = true ]; then
	reported=$("$destination" version 2>&1) || fail "$destination does not run: $reported"
	reported=$(printf '%s\n' "$reported" | head -n 1)
	if [ -n "$tag" ]; then
		case " $reported " in
		*" $tag "*) ;;
		*) fail "$destination reports '$reported', not $tag" ;;
		esac
	fi
	info "  $reported"
else
	info "  staged for $platform, not run on this $host_os-$host_arch machine"
fi

info "Installed: $destination"
case ":$PATH:" in
*":$bin_dir:"*) ;;
*)
	warn "$bin_dir is not on PATH; add it, e.g. in your shell profile:"
	warn "  export PATH=\"$bin_dir:\$PATH\""
	;;
esac
found=$(command -v syspi 2>/dev/null || true)
if [ -n "$found" ] && [ "$found" != "$destination" ] && [ "$found" != "$bin_dir/syspi" ]; then
	warn "'syspi' on PATH is $found, which comes before $destination"
fi
