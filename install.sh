#!/bin/sh

set -eu

repository_api=${ELGATOLIGHT_RELEASE_API:-https://git2.riper.fr/api/v1/repos/ztec/elgatocmd}

fail() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

fetch() {
	url=$1
	destination=$2
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$url" -o "$destination"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO "$destination" "$url"
	else
		fail 'curl or wget is required'
	fi
}

asset_urls() {
	sed 's/"browser_download_url"[[:space:]]*:[[:space:]]*"/\
URL=/g' "$1" | sed -n 's/^URL=\([^"]*\)".*/\1/p'
}

os_name=${ELGATOLIGHT_OS:-$(uname -s)}
machine=${ELGATOLIGHT_ARCH:-$(uname -m)}

case $os_name in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) fail "unsupported operating system: $os_name" ;;
esac

case $machine in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	armv7 | armv7l | armhf)
		[ "$os" = linux ] || fail "unsupported architecture on $os: $machine"
		arch=armv7
		;;
	*) fail "unsupported architecture: $machine" ;;
esac

target=$os-$arch
archive_extension=tar.gz
binary_name=elgatolight

if [ "$(id -u 2>/dev/null || printf 1)" = 0 ]; then
	default_install_dir=/usr/local/bin
else
	default_install_dir=${HOME:?HOME is required}/.local/bin
fi

install_dir=${ELGATOLIGHT_INSTALL_DIR:-}
if [ -z "$install_dir" ]; then
	install_dir=$default_install_dir
	if [ -t 2 ] && [ -r /dev/tty ]; then
		printf 'Install elgatolight in which directory? [%s] ' "$default_install_dir" >/dev/tty
		IFS= read -r answer </dev/tty || answer=
		[ -z "$answer" ] || install_dir=$answer
	fi
fi

case $install_dir in
	'~') install_dir=${HOME:?HOME is required} ;;
	'~/'*) install_dir=${HOME:?HOME is required}/${install_dir#'~/'} ;;
esac

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/elgatolight-install.XXXXXX")
cleanup() {
	rm -rf "$tmp_dir"
}
trap cleanup EXIT HUP INT TERM

release_json=$tmp_dir/release.json
if fetch "$repository_api/releases/latest" "$release_json" 2>/dev/null; then
	release_channel='stable release'
else
	printf '%s\n' 'No stable release found; selecting the latest pre-release.'
	fetch "$repository_api/releases?pre-release=true&draft=false&limit=1" "$release_json" \
		|| fail 'could not find a stable release or pre-release'
	release_channel='pre-release'
fi

archive_suffix=-$target.$archive_extension
archive_url=$(asset_urls "$release_json" | awk -v suffix="$archive_suffix" '
	length($0) >= length(suffix) && substr($0, length($0) - length(suffix) + 1) == suffix { print; exit }
')
checksum_url=$(asset_urls "$release_json" | awk '
	length($0) >= 14 && substr($0, length($0) - 13) == "-checksums.txt" { print; exit }
')

[ -n "$archive_url" ] || fail "the $release_channel has no $target archive"
[ -n "$checksum_url" ] || fail "the $release_channel has no checksum file"

archive=$tmp_dir/elgatolight.$archive_extension
checksums=$tmp_dir/checksums.txt
printf 'Downloading %s for %s...\n' "$release_channel" "$target"
fetch "$archive_url" "$archive"
fetch "$checksum_url" "$checksums"

expected=$(awk -v suffix="$archive_suffix" '
	length($2) >= length(suffix) && substr($2, length($2) - length(suffix) + 1) == suffix { print $1; exit }
' "$checksums")
[ -n "$expected" ] || fail "the checksum file does not contain the $target archive"

if command -v sha256sum >/dev/null 2>&1; then
	actual=$(sha256sum "$archive" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
	actual=$(shasum -a 256 "$archive" | awk '{print $1}')
elif command -v openssl >/dev/null 2>&1; then
	actual=$(openssl dgst -sha256 "$archive" | awk '{print $NF}')
else
	fail 'sha256sum, shasum, or openssl is required to verify the download'
fi
[ "$actual" = "$expected" ] || fail 'download checksum verification failed'

unpack_dir=$tmp_dir/unpack
mkdir -p "$unpack_dir"
tar -xzf "$archive" -C "$unpack_dir"

binary=$(find "$unpack_dir" -type f -name "$binary_name" -print | sed -n '1p')
[ -n "$binary" ] || fail "the archive does not contain $binary_name"

mkdir -p "$install_dir" || fail "cannot create install directory: $install_dir"
destination=$install_dir/$binary_name
if command -v install >/dev/null 2>&1; then
	install -m 0755 "$binary" "$destination" || fail "cannot install to $destination"
else
	cp "$binary" "$destination" || fail "cannot install to $destination"
	chmod 0755 "$destination"
fi

version=$($destination --version 2>/dev/null || printf unknown)
printf 'Installed elgatolight %s at %s\n' "$version" "$destination"
printf '\nConfigure it with one of these commands:\n'
printf '  %s setup\n' "$destination"
printf '    User service: starts when you log in. Only the USB rule uses sudo.\n\n'
printf '  sudo %s setup\n' "$destination"
printf '    System service: starts at boot and runs as root.\n\n'
printf '  %s setup --scope none\n' "$destination"
printf '    Command-line only: installs USB access without a daemon service.\n'
