#!/bin/sh

set -eu

version_file=${VERSION_FILE:-}
if [ -n "$version_file" ]; then
	if [ ! -s "$version_file" ]; then
		printf 'error: VERSION_FILE must name a non-empty readable file: %s\n' "$version_file" >&2
		exit 2
	fi
	version=$(cat "$version_file")
else
	version=${VERSION:-${1:-}}
fi
dist_dir=${DIST_DIR:-dist}

if [ -z "$version" ]; then
	printf '%s\n' 'error: VERSION must contain the exact non-empty release tag' >&2
	exit 2
fi

# Git tags may contain path separators or punctuation that is inconvenient in
# an archive filename. Keep the exact tag inside the binary and Forgejo release,
# and use this stable filesystem-safe form only for artifact names.
artifact_version=$(printf '%s' "$version" | sed 's/[^A-Za-z0-9._+-]/-/g')

printf '[release] Packaging exact tag %s...\n' "$version"

mkdir -p "$dist_dir"
stage_root=$(mktemp -d "$dist_dir/.elgatolight-release.XXXXXX")
trap 'rm -rf "$stage_root"' EXIT HUP INT TERM

ldflags="-s -w -X elgatolight/internal/buildinfo.Version=$version"
checksums="$dist_dir/elgatolight-$artifact_version-checksums.txt"
rm -f "$checksums"

build_target() {
	goos=$1
	goarch=$2
	goarm=$3
	label=$4

	name="elgatolight-$artifact_version-$label"
	package_dir="$stage_root/$name"
	mkdir -p "$package_dir"

	printf '[release] Building %s/%s%s...\n' "$goos" "$goarch" "${goarm:+ (GOARM=$goarm)}"
	if [ -n "$goarm" ]; then
		CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" GOARM="$goarm" \
			go build -trimpath -ldflags "$ldflags" -o "$package_dir/elgatolight" ./cmd/elgatolight
	else
		CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
			go build -trimpath -ldflags "$ldflags" -o "$package_dir/elgatolight" ./cmd/elgatolight
	fi
	cp README.md "$package_dir/"
	cp LICENSE "$package_dir/"
	mkdir -p "$package_dir/docs"
	cp docs/technical.md "$package_dir/docs/"

	artifact="$dist_dir/$name.tar.gz"
	rm -f "$artifact"
	tar -C "$stage_root" -czf "$artifact" "$name"
	(cd "$dist_dir" && sha256sum "$(basename "$artifact")") >>"$checksums"
}

build_target linux amd64 "" linux-amd64
build_target linux arm64 "" linux-arm64
build_target linux arm 7 linux-armv7

native="$stage_root/elgatolight-$artifact_version-linux-amd64/elgatolight"
actual=$("$native" --version)
if [ "$actual" != "$version" ]; then
	printf 'error: native binary reports version %s, expected %s\n' "$actual" "$version" >&2
	exit 1
fi

printf '[release] Complete: %s\n' "$dist_dir"
