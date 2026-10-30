#!/usr/bin/env bash
# Runs install.sh (and install.ps1 when pwsh is on PATH) against a fake release served from
# this machine, so the installers are tested without the network or a published release.
#
#   scripts/install-test.sh
#
# The fake releases are real syspi builds (stamped with the fake tags) packaged by
# build-release-artifacts.sh, served by install-test-server.py with GitHub's
# /releases/latest redirect. Needs: Go, python3 and what install.sh needs.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
server_pid=""
cleanup() {
	[[ -z "$server_pid" ]] || kill "$server_pid" 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT

host="$(go env GOOS)-$(go env GOARCH)"
# A platform the fake releases publish but this machine cannot run.
foreign=linux-arm64
[[ $host != linux-arm64 ]] || foreign=linux-amd64

good=v9.9.1
older=v9.9.0
mismatch=v9.9.2    # its binaries report another version
tampered=v9.9.3    # its manifest names a digest the archive does not have
repeated=v9.9.4    # its manifest repeats the host's line verbatim
conflicting=v9.9.5 # its manifest gives the host's archive a second digest
partial=v9.9.6     # its manifest omits the foreign platform

# release <tag> [<version the binaries report>]
release() {
	local tag=$1 stamp=${2:-$1} dir="$work/site/releases/download/$1"
	SOURCE_DATE_EPOCH=1700000000 "$root/scripts/build-release-artifacts.sh" "$stamp" "$work/build-$tag" >/dev/null
	mkdir -p "$dir"
	mv "$work/build-$tag"/* "$dir"
	rmdir "$work/build-$tag"
}

release "$good"
release "$older"
release "$mismatch" "$good"
release "$tampered"
release "$repeated"
release "$conflicting"
release "$partial"

manifest() { printf '%s/site/releases/download/%s/SHA256SUMS.txt\n' "$work" "$1"; }
sed -i.bak "s/^[0-9a-f]\{8\}\(.*syspi-$host\.tar\.gz\)$/00000000\1/" "$(manifest "$tampered")" && rm "$(manifest "$tampered").bak"
line=$(grep "  syspi-$host\.tar\.gz\$" "$(manifest "$repeated")")
printf '%s\n' "$line" >>"$(manifest "$repeated")"
printf '%s  syspi-%s.tar.gz\n' "$(printf '1%.0s' $(seq 64))" "$host" >>"$(manifest "$conflicting")"
sed -i.bak "/syspi-$foreign\.tar\.gz/d" "$(manifest "$partial")" && rm "$(manifest "$partial").bak"

# A mirror that serves latest/download/ but has no /latest redirect to a tag.
mkdir -p "$work/site/mirror/latest"
cp -R "$work/site/releases/download/$good" "$work/site/mirror/latest/download"

python3 "$root/scripts/install-test-server.py" "$work/site" "$good" >"$work/port" &
server_pid=$!
for _ in $(seq 50); do
	[[ -s "$work/port" ]] && break
	sleep 0.1
done
[[ -s "$work/port" ]] || { echo "install-test: the release server did not start" >&2; exit 1; }
base="http://127.0.0.1:$(cat "$work/port")/releases"
mirror="http://127.0.0.1:$(cat "$work/port")/mirror"

# --- the cases ---------------------------------------------------------------

failures=0
n=0
out=""
status=0
prefix=""

# run <args...>: install.sh with a fresh prefix; @P@ in the arguments stands for it.
run() {
	local arg args=()
	n=$((n + 1))
	prefix="$work/install-$n"
	for arg in "$@"; do args+=("${arg//@P@/$prefix}"); done
	set +e
	out=$(cd "$work" && sh "$root/install.sh" --base-url "$base" "${args[@]}" 2>&1)
	status=$?
	set -e
}

ok() { # <name> <args...>: must succeed
	local name=$1
	shift
	run "$@"
	if [[ "$status" -ne 0 ]]; then
		echo "FAIL $name: exit $status" >&2 && printf '%s\n' "$out" >&2
		failures=$((failures + 1))
		return 1
	fi
	echo "ok   $name"
}

fails() { # <name> <message> <args...>: must fail, saying <message>
	local name=$1 message=$2
	shift 2
	run "$@"
	if [[ "$status" -eq 0 ]] || [[ "$out" != *"$message"* ]]; then
		echo "FAIL $name: exit $status, expected a failure mentioning '$message'" >&2 && printf '%s\n' "$out" >&2
		failures=$((failures + 1))
		return 1
	fi
	echo "ok   $name"
}

said() { [[ "$out" == *"$1"* ]] || { echo "FAIL: expected output to mention '$1'" >&2; printf '%s\n' "$out" >&2; failures=$((failures + 1)); }; }
unsaid() { [[ "$out" != *"$1"* ]] || { echo "FAIL: expected output not to mention '$1'" >&2; printf '%s\n' "$out" >&2; failures=$((failures + 1)); }; }
installed() { [[ -x "$prefix/$1" ]] || { echo "FAIL: $prefix/$1 missing or not executable" >&2; failures=$((failures + 1)); }; }
absent() { [[ ! -e "$prefix/$1" ]] || { echo "FAIL: $prefix/$1 should not exist" >&2; failures=$((failures + 1)); }; }
reports() { # <binary> <tag>
	local got
	got=$("$1" version 2>&1) || true
	[[ $got == "syspi $2" ]] || { echo "FAIL: $1 reports '$got', not 'syspi $2'" >&2; failures=$((failures + 1)); }
}

if ok "release by tag" --version "$good" --prefix @P@; then
	installed bin/syspi
	said "syspi-$host.tar.gz verified"
	said "syspi $good"
	reports "$prefix/bin/syspi" "$good"
fi
if ok "release by bare version" --version "${older#v}" --prefix @P@; then
	reports "$prefix/bin/syspi" "$older"
fi
if ok "latest through the redirect" --prefix @P@; then
	said "Installing syspi ($good)"
	reports "$prefix/bin/syspi" "$good"
fi
if ok "latest on a mirror without the redirect" --base-url "$mirror" --prefix @P@; then
	said "Installing syspi (the latest release)"
	reports "$prefix/bin/syspi" "$good"
fi
if ok "--bin-dir" --version "$good" --bin-dir @P@/tools; then
	installed tools/syspi
	absent bin
fi
if ok "dry run" --version "$good" --prefix @P@ --dry-run; then
	said "Dry run: nothing installed."
	said "$base/download/$good/syspi-$host.tar.gz"
	absent bin/syspi
fi
if ok "foreign platform is staged, not run" --version "$good" --prefix @P@ --os "${foreign%-*}" --arch "${foreign#*-}"; then
	installed bin/syspi
	said "staged for $foreign"
	unsaid "syspi $good"
fi
if ok "Windows archive" --version "$good" --prefix @P@ --os windows --arch amd64; then
	installed bin/syspi.exe
	said "syspi-windows-amd64.zip verified"
fi
if ok "repeated manifest line" --version "$repeated" --prefix @P@; then
	reports "$prefix/bin/syspi" "$repeated"
fi
if ok "environment variables" --prefix @P@ --version latest; then :; fi
n=$((n + 1)); prefix="$work/install-$n"
set +e
out=$(cd "$work" && SYSPI_VERSION="$older" SYSPI_PREFIX="$prefix" SYSPI_DOWNLOAD_BASE="$base" sh "$root/install.sh" 2>&1)
status=$?
set -e
if [[ $status -eq 0 ]]; then echo "ok   SYSPI_* environment"; reports "$prefix/bin/syspi" "$older"; else echo "FAIL SYSPI_* environment: exit $status" >&2; printf '%s\n' "$out" >&2; failures=$((failures + 1)); fi

fails "tampered archive" "does not match SHA256SUMS.txt" --version "$tampered" --prefix @P@
absent bin/syspi
fails "conflicting digests" "more than one digest" --version "$conflicting" --prefix @P@
absent bin/syspi
fails "binary reporting another version" "reports 'syspi $good', not $mismatch" --version "$mismatch" --prefix @P@
fails "platform the release lacks" "does not list syspi-$foreign.tar.gz" --version "$partial" --prefix @P@ --os "${foreign%-*}" --arch "${foreign#*-}"
absent bin/syspi
fails "unknown release" "could not download" --version v0.0.0-none --prefix @P@
fails "bad version" "--version must be a release tag" --version nightly --prefix @P@
fails "bad os" "--os must be" --version "$good" --prefix @P@ --os plan9
fails "unknown option" "unknown option" --version "$good" --prefix @P@ --tools sysml
fails "unwritable prefix" "cannot create" --version "$good" --prefix /proc/syspi-nowhere

if command -v pwsh >/dev/null 2>&1; then
	if pwsh -NoProfile -File "$root/scripts/install-test.ps1" -BaseUrl "$base" -Mirror "$mirror" -Good "$good" -Older "$older" -Mismatch "$mismatch" -Tampered "$tampered" -Partial "$partial" -Foreign "$foreign"; then
		echo "ok   install.ps1 suite"
	else
		echo "FAIL install.ps1 suite" >&2
		failures=$((failures + 1))
	fi
else
	echo "skip install.ps1 suite: pwsh not on PATH"
fi

if [[ $failures -gt 0 ]]; then
	echo "install-test: $failures failure(s)" >&2
	exit 1
fi
echo "install-test: all cases passed"
