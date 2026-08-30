#!/bin/sh

set -eu

version_file=${VERSION_FILE:-}
if [ -n "$version_file" ]; then
	[ -s "$version_file" ] || { printf 'error: unreadable VERSION_FILE: %s\n' "$version_file" >&2; exit 2; }
	version=$(cat "$version_file")
else
	version=${VERSION:-${1:-}}
fi
dist_dir=${DIST_DIR:-dist}

version_lines=$(printf '%s\n' "$version" | wc -l | tr -d '[:space:]')
if [ "$version_lines" != 1 ] || ! printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*))?(-[0-9A-Za-z][0-9A-Za-z.-]*)?$'; then
	printf 'error: VERSION must use vMAJOR.MINOR with an optional PATCH component and prerelease suffix, got %s\n' "$version" >&2
	exit 2
fi
[ -f LICENSE ] || { printf '%s\n' 'error: LICENSE is required' >&2; exit 2; }
[ -n "${RELEASE_SIGNING_KEY:-}" ] || { printf '%s\n' 'error: RELEASE_SIGNING_KEY is required' >&2; exit 2; }

artifact_version=$(printf '%s' "$version" | sed 's/[^A-Za-z0-9._+-]/-/g')
mkdir -p "$dist_dir"
dist_dir=$(CDPATH= cd -- "$dist_dir" && pwd)
rm -f \
	"$dist_dir"/elgatolight-"$artifact_version"-*.tar.gz \
	"$dist_dir"/elgatolight-"$artifact_version"-*.zip \
	"$dist_dir"/elgatolight-"$artifact_version"-checksums.txt \
	"$dist_dir"/elgatolight-"$artifact_version"-checksums.txt.sig
stage_root=$(mktemp -d "$dist_dir/.elgatolight-release.XXXXXX")
trap 'rm -rf "$stage_root"' EXIT HUP INT TERM
licenses_dir="$stage_root/THIRD_PARTY_LICENSES"
sh ./scripts/package-licenses.sh "$licenses_dir"
source_date_epoch=${SOURCE_DATE_EPOCH:-0}
case $source_date_epoch in
	''|*[!0-9]*) printf 'error: SOURCE_DATE_EPOCH must be a Unix timestamp, got %s\n' "$source_date_epoch" >&2; exit 2 ;;
esac

signing_key_registry_api=https://git2.riper.fr/api/v1/repos/ztec/tmplt
keyring_directory=${RELEASE_SIGNING_KEYS_DIRECTORY:-$stage_root/central-release-keys}
if [ -z "${RELEASE_SIGNING_KEYS_DIRECTORY:-}" ]; then
	printf '%s\n' '[release] Loading the central signing-key registry from Tmplt main...'
	go run git2.riper.fr/ztec/tmplt/cmd/tmplt-release-sign fetch --api "$signing_key_registry_api" --directory "$keyring_directory"
fi
go run git2.riper.fr/ztec/tmplt/cmd/tmplt-release-sign keyring --directory "$keyring_directory" >/dev/null
signing_public_key_name=$(go run git2.riper.fr/ztec/tmplt/cmd/tmplt-release-sign match --directory "$keyring_directory")
ldflags="-s -w -X git2.riper.fr/ztec/elgatocmd/internal/buildinfo.Version=$version"

build_target() {
	goos=$1
	goarch=$2
	goarm=$3
	label=$4
	format=$5
	name="elgatolight-$artifact_version-$label"
	package_dir="$stage_root/$name"
	binary=elgatolight
	[ "$goos" != windows ] || binary=elgatolight.exe
	mkdir -p "$package_dir/docs"
	printf '[release] Building %s/%s%s...\n' "$goos" "$goarch" "${goarm:+ (GOARM=$goarm)}"
	if [ -n "$goarm" ]; then
		CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" GOARM="$goarm" \
			go build -buildvcs=false -trimpath -ldflags "$ldflags" -o "$package_dir/$binary" ./cmd/elgatolight
	else
		CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
			go build -buildvcs=false -trimpath -ldflags "$ldflags" -o "$package_dir/$binary" ./cmd/elgatolight
	fi
	cp README.md LICENSE "$package_dir/"
	cp docs/*.md "$package_dir/docs/"
	cp -R "$licenses_dir" "$package_dir/"
	find "$package_dir" -type d -exec chmod 0755 {} +
	find "$package_dir" -type f ! -name "$binary" -exec chmod 0644 {} +
	chmod 0755 "$package_dir/$binary"
	find "$package_dir" -exec touch -d "@$source_date_epoch" {} +
	case $format in
		tar.gz)
			tar --sort=name --mtime="@$source_date_epoch" --owner=0 --group=0 --numeric-owner \
				-C "$stage_root" -cf - "$name" | gzip -n >"$dist_dir/$name.tar.gz"
			;;
		zip)
			(cd "$stage_root" && zip -X -q -r "$dist_dir/$name.zip" "$name")
			;;
	esac
}

build_target linux amd64 '' linux-amd64 tar.gz
build_target linux arm64 '' linux-arm64 tar.gz
build_target linux arm 7 linux-armv7 tar.gz


native="$stage_root/elgatolight-$artifact_version-linux-amd64/elgatolight"
actual=$($native --version)
[ "$actual" = "$version" ] || { printf 'error: native binary reports %s, expected %s\n' "$actual" "$version" >&2; exit 1; }

checksums="$dist_dir/elgatolight-$artifact_version-checksums.txt"
(
	cd "$dist_dir"
	for artifact in elgatolight-"$artifact_version"-*.tar.gz elgatolight-"$artifact_version"-*.zip; do
		[ -f "$artifact" ] && sha256sum "$artifact"
	done
) | LC_ALL=C sort >"$checksums"

go run git2.riper.fr/ztec/tmplt/cmd/tmplt-release-sign sign --input "$checksums" --output "$checksums.sig"
go run git2.riper.fr/ztec/tmplt/cmd/tmplt-release-sign verify --public "$keyring_directory/$signing_public_key_name" --input "$checksums" --signature "$checksums.sig"
printf '[release] Complete: %s\n' "$dist_dir"
