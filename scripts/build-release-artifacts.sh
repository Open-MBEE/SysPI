#!/usr/bin/env bash
# Builds the release archives of syspi for every published platform.
#
#   scripts/build-release-artifacts.sh <tag> [<dist directory>]
#
# Writes <dist>/syspi-<os>-<arch>.tar.gz (.zip for Windows), each holding syspi[.exe] and
# LICENSE, and <dist>/SHA256SUMS.txt over them. Builds are static (CGO_ENABLED=0, -trimpath)
# and stamped with the tag; the archives are reproducible for a given SOURCE_DATE_EPOCH
# (default: the commit date of HEAD). The release workflow runs this on the tag; a release
# rehearsal runs it locally with any tag name.
set -euo pipefail

tag=${1:?usage: build-release-artifacts.sh <tag> [<dist directory>]}
dist=${2:-dist}
platforms="linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64 windows-arm64"

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
export SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git log -1 --format=%ct 2>/dev/null || date +%s)}"
export CGO_ENABLED=0

# The archives are written by scripts/release-archive (archive/tar, archive/zip): no tar or
# zip on the host, so the same bytes come out of Linux, macOS and Git for Windows.
tools=$(mktemp -d)
trap 'rm -rf "$tools"' EXIT
archiver="$tools/release-archive$(go env GOEXE)"
go build -o "$archiver" ./scripts/release-archive
if command -v sha256sum >/dev/null 2>&1; then sum_cmd="sha256sum"; else sum_cmd="shasum -a 256"; fi

rm -rf "$dist"
mkdir -p "$dist"
dist=$(cd "$dist" && pwd)
host="$(go env GOOS)-$(go env GOARCH)"

for platform in $platforms; do
	os=${platform%-*}
	arch=${platform#*-}
	exe=""
	[[ $os != windows ]] || exe=.exe
	stage="$dist/stage-$platform"
	mkdir -p "$stage"
	GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.Version=$tag" -o "$stage/syspi$exe" ./cmd/syspi
	cp LICENSE "$stage/LICENSE"

	# Every build must carry the tag (Go does not record -ldflags in the build info);
	# the host's build is also run.
	LC_ALL=C grep -q -a -F -- "syspi $tag" "$stage/syspi$exe" || LC_ALL=C grep -q -a -F -- "$tag" "$stage/syspi$exe" ||
		{ echo "build-release-artifacts: $platform build is not stamped with $tag" >&2; exit 1; }
	if [[ $platform == "$host" ]]; then
		reported=$("$stage/syspi$exe" version)
		[[ $reported == "syspi $tag" ]] || { echo "build-release-artifacts: host build reports '$reported', not 'syspi $tag'" >&2; exit 1; }
	fi

	archive="syspi-$platform.tar.gz"
	[[ $os != windows ]] || archive="syspi-$platform.zip"
	(cd "$stage" && "$archiver" -out "../$archive" -mtime "$SOURCE_DATE_EPOCH" "syspi$exe" LICENSE)
	rm -rf "$stage"
	echo "built syspi-$platform"
done

(cd "$dist" && $sum_cmd syspi-* | LC_ALL=C sort -k2 >SHA256SUMS.txt)
echo "wrote $dist/SHA256SUMS.txt:"
cat "$dist/SHA256SUMS.txt"
