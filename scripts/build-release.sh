#!/bin/sh

set -eu

version=${VERSION:-${1:-}}
dist_dir=${DIST_DIR:-dist}

if ! printf '%s\n' "$version" | grep -Eq '^[0-9]+\.[0-9]+$'; then
	printf 'error: VERSION must match MAJOR.MINOR exactly (for example 1.2); got %s\n' "${version:-<empty>}" >&2
	exit 2
fi

mkdir -p "$dist_dir"
stage_root=$(mktemp -d "$dist_dir/.elgatolight-release.XXXXXX")
trap 'rm -rf "$stage_root"' EXIT HUP INT TERM

ldflags="-s -w -X elgatolight/internal/buildinfo.Version=$version"
checksums="$dist_dir/elgatolight-$version-checksums.txt"
rm -f "$checksums"

build_target() {
	goos=$1
	goarch=$2
	goarm=$3
	label=$4
	format=$5
	extension=$6

	name="elgatolight-$version-$label"
	package_dir="$stage_root/$name"
	mkdir -p "$package_dir"

	printf '[release] Building %s/%s%s...\n' "$goos" "$goarch" "${goarm:+ (GOARM=$goarm)}"
	if [ -n "$goarm" ]; then
		CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" GOARM="$goarm" \
			go build -trimpath -ldflags "$ldflags" -o "$package_dir/elgatolight$extension" ./cmd/elgatolight
	else
		CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
			go build -trimpath -ldflags "$ldflags" -o "$package_dir/elgatolight$extension" ./cmd/elgatolight
	fi
	cp README.md "$package_dir/"
	mkdir -p "$package_dir/docs"
	cp docs/technical.md "$package_dir/docs/"

	if [ "$format" = zip ]; then
		artifact="$dist_dir/$name.zip"
		rm -f "$artifact"
		(cd "$stage_root" && zip -qr "../$(basename "$artifact")" "$name")
	else
		artifact="$dist_dir/$name.tar.gz"
		rm -f "$artifact"
		tar -C "$stage_root" -czf "$artifact" "$name"
	fi
	(cd "$dist_dir" && sha256sum "$(basename "$artifact")") >>"$checksums"
}

build_target linux amd64 "" linux-amd64 tar ""
build_target linux arm64 "" linux-arm64 tar ""
build_target linux arm 7 linux-armv7 tar ""
build_target windows amd64 "" windows-amd64 zip .exe
build_target windows arm64 "" windows-arm64 zip .exe
build_target darwin amd64 "" darwin-amd64 tar ""
build_target darwin arm64 "" darwin-arm64 tar ""

native="$stage_root/elgatolight-$version-linux-amd64/elgatolight"
actual=$("$native" --version)
if [ "$actual" != "$version" ]; then
	printf 'error: native binary reports version %s, expected %s\n' "$actual" "$version" >&2
	exit 1
fi

printf '[release] Complete: %s\n' "$dist_dir"
