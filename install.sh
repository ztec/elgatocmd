#!/bin/sh

set -eu
umask 077

api=${ELGATOLIGHT_RELEASE_API:-https://git2.riper.fr/api/v1/repos/ztec/elgatocmd}
signing_keys_api=${RELEASE_SIGNING_KEYS_API:-https://git2.riper.fr/api/v1/repos/ztec/tmplt}
signing_keys_repository=${RELEASE_SIGNING_KEYS_REPOSITORY_URL:-https://git2.riper.fr/ztec/tmplt}
binary=elgatolight

fail() { printf 'error: %s\n' "$*" >&2; exit 1; }
signature_verification=required
case ${ELGATOLIGHT_SKIP_SIGNATURE_VERIFICATION:-0} in
	1|true|yes) signature_verification=skipped ;;
	0|false|no|'') ;;
	*) fail 'ELGATOLIGHT_SKIP_SIGNATURE_VERIFICATION must be 1, true, yes, 0, false, or no' ;;
esac
while [ "$#" -gt 0 ]; do
	case $1 in
		--skip-signature-verification) signature_verification=skipped ;;
		--help)
			printf '%s\n' 'usage: install.sh [--skip-signature-verification]'
			exit 0
			;;
		*) fail "unknown option: $1" ;;
	esac
	shift
done
warn_signature_bypass() {
	printf '%s\n' \
		'WARNING: Ed25519 signature verification is disabled.' \
		'The SHA-256 checksum passed. That detects accidental corruption,' \
		'but it cannot prove authenticity because an attacker could replace both the archive and checksum.' >&2
}
confirm_signature_bypass() {
	printf '%s\n' \
		'OpenSSL with Ed25519 support is unavailable, so the release signature cannot be verified.' \
		'The SHA-256 checksum passed, but checksum-only installation does not authenticate the download:' \
		'an attacker could replace both the archive and its checksum.' >&2
	if ! (exec 3<>/dev/tty) 2>/dev/null; then
		fail 'cannot ask for confirmation without a terminal; rerun with --skip-signature-verification for an explicit non-interactive bypass'
	fi
	exec 3<>/dev/tty
	printf '%s' 'Continue with checksum-only installation? [y/N] ' >&3
	answer=
	IFS= read -r answer <&3 || true
	exec 3>&-
	case $answer in y|Y|yes|YES|Yes) signature_verification=skipped ;; *) fail 'installation canceled because the signature cannot be verified' ;; esac
}
fetch() {
	case $1 in https://*|http://127.0.0.1:*|http://localhost:*) ;; *) fail "unsafe download URL: $1" ;; esac
	quiet=${3:-false}
	case $quiet in true|false) ;; *) fail 'internal fetch quiet flag must be true or false' ;; esac
	if command -v curl >/dev/null 2>&1; then
		if [ "$quiet" = true ]; then
			curl -fsSL --proto '=https,http' --proto-redir '=https' --connect-timeout 15 --max-time 300 "$1" -o "$2" 2>/dev/null
		else
			curl -fsSL --proto '=https,http' --proto-redir '=https' --connect-timeout 15 --max-time 300 "$1" -o "$2"
		fi
	elif command -v wget >/dev/null 2>&1; then
		if [ "$quiet" = true ]; then
			wget -q --https-only --timeout=30 --tries=2 "$1" -O "$2" 2>/dev/null
		else
			wget -q --https-only --timeout=30 --tries=2 "$1" -O "$2"
		fi
	else
		fail 'curl or GNU wget is required'
	fi
}
asset_urls() {
	sed 's/"browser_download_url"[[:space:]]*:[[:space:]]*"/\
URL=/g' "$1" | sed -n 's/^URL=\([^"]*\)".*/\1/p'
}
public_key_names() {
	grep -o '"name"[[:space:]]*:[[:space:]]*"[^"]*\.pub"' "$1" |
		sed -n 's/.*"\([^"]*\.pub\)"$/\1/p'
}
verify_with_key() {
	key_file=$1
	key_der=$tmp/public-key.der
	key_pem=$tmp/public-key.pem
	printf '\060\052\060\005\006\003\053\145\160\003\041\000' >"$key_der"
	tr -d '[:space:]' <"$key_file" | openssl base64 -d -A >>"$key_der" 2>/dev/null || return 1
	[ "$(wc -c <"$key_der" | tr -d '[:space:]')" = 44 ] || return 1
	openssl pkey -pubin -inform DER -in "$key_der" -out "$key_pem" >/dev/null 2>&1 || return 1
	openssl pkeyutl -verify -pubin -inkey "$key_pem" -rawin \
		-in "$checksums" -sigfile "$signature_binary" >/dev/null 2>&1
}

case ${ELGATOLIGHT_OS:-$(uname -s)} in
	Linux) os=linux ;;

	*) fail 'install.sh supports Linux' ;;

esac
case ${ELGATOLIGHT_ARCH:-$(uname -m)} in
	x86_64|amd64) arch=amd64 ;;
	aarch64|arm64) arch=arm64 ;;
	armv7|armv7l|armhf) [ "$os" = linux ] || fail 'armv7 is supported only on Linux'; arch=armv7 ;;
	*) fail "unsupported architecture: ${ELGATOLIGHT_ARCH:-$(uname -m)}" ;;
esac

if [ "$(id -u 2>/dev/null || printf 1)" = 0 ]; then
	default_dir=/usr/local/bin
else
	default_dir=${HOME:?HOME is required}/.local/bin
fi
install_dir=${ELGATOLIGHT_INSTALL_DIR:-$default_dir}
if [ "$signature_verification" = required ]; then
	if ! command -v openssl >/dev/null 2>&1 || ! openssl pkeyutl -help 2>&1 | grep -q -- '-rawin'; then
		signature_verification=unavailable
	fi
fi
tmp=$(mktemp -d "${TMPDIR:-/tmp}/elgatolight-install.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

release=$tmp/release.json
if ! fetch "${api%/}/releases/latest" "$release" true; then
	printf '%s\n' 'No stable release found; selecting the latest pre-release.'
	fetch "${api%/}/releases?pre-release=true&draft=false&limit=1" "$release"
fi
tag=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$release" | sed -n '1p')
[ -n "$tag" ] || fail 'release response has no tag'
archive_url=$(asset_urls "$release" | awk -v suffix="-$os-$arch.tar.gz" 'substr($0, length($0)-length(suffix)+1)==suffix { print; exit }')
checksum_url=$(asset_urls "$release" | awk 'substr($0, length($0)-13)=="-checksums.txt" { print; exit }')
signature_url=$(asset_urls "$release" | awk -v suffix="-checksums.txt.sig" 'substr($0, length($0)-length(suffix)+1)==suffix { print; exit }')
[ -n "$archive_url" ] && [ -n "$checksum_url" ] || fail "release has no $os-$arch archive or checksum file"
if [ "$signature_verification" = required ]; then
	[ -n "$signature_url" ] || fail 'release has no checksum signature'
fi

archive=$tmp/archive.tar.gz
checksums=$tmp/checksums.txt
signature=$tmp/checksums.txt.sig
signature_binary=$tmp/checksums.sig.bin
fetch "$archive_url" "$archive"
fetch "$checksum_url" "$checksums"
if [ "$signature_verification" = required ]; then
	fetch "$signature_url" "$signature"
	tr -d '[:space:]' <"$signature" | openssl base64 -d -A >"$signature_binary" 2>/dev/null || fail 'release signature is not valid base64'
	[ "$(wc -c <"$signature_binary" | tr -d '[:space:]')" = 64 ] || fail 'release signature is not valid Ed25519 data'

	key_index=$tmp/release-keys.json
	fetch "${signing_keys_api%/}/contents/release-keys?ref=main" "$key_index"
	key_names=$(public_key_names "$key_index" || true)
	[ -n "$key_names" ] || fail 'central Tmplt main contains no release public keys'
	mkdir "$tmp/active-keys" "$tmp/revoked-keys"
	key_count=0
	for key_name in $key_names; do
		printf '%s\n' "$key_name" | grep -Eq '^[0-9]{4}-[0-9]{2}-[0-9]{2}(-([2-9]|[1-9][0-9]+))?(-revoked)?\.pub$' || fail "invalid release public-key filename: $key_name"
		key_count=$((key_count + 1))
		[ "$key_count" -le 128 ] || fail 'central Tmplt main contains too many release public keys'
		case $key_name in
			*-revoked.pub) key_destination=$tmp/revoked-keys/$key_name ;;
			*) key_destination=$tmp/active-keys/$key_name ;;
		esac
		[ ! -e "$key_destination" ] || fail "duplicate release public key: $key_name"
		fetch "${signing_keys_repository%/}/raw/branch/main/release-keys/$key_name" "$key_destination"
	done

	verified=false
	for key_file in "$tmp"/active-keys/*.pub; do
		[ -e "$key_file" ] || continue
		if verify_with_key "$key_file"; then verified=true; break; fi
	done
	if [ "$verified" != true ]; then
		for key_file in "$tmp"/revoked-keys/*.pub; do
			[ -e "$key_file" ] || continue
			if verify_with_key "$key_file"; then
				fail "release $tag was signed with revoked key ${key_file##*/}; installation refused. Wait for a newer release signed with an active key, then run the installer again"
			fi
		done
		fail 'release signature did not match any active key from central Tmplt main; installation refused. Retry after a trusted release is published'
	fi
	printf 'Signature verified for %s using an active key from central Tmplt main.\n' "$tag"
fi

archive_name=${archive_url##*/}
archive_name=${archive_name%%\?*}
expected=$(awk -v name="$archive_name" '$2==name || $2=="*"name { print $1; exit }' "$checksums")
[ -n "$expected" ] || fail "checksums do not contain $archive_name"
if command -v sha256sum >/dev/null 2>&1; then
	actual=$(sha256sum "$archive" | awk '{print $1}')
else
	actual=$(shasum -a 256 "$archive" | awk '{print $1}')
fi
[ "$(printf '%s' "$actual" | tr A-F a-f)" = "$(printf '%s' "$expected" | tr A-F a-f)" ] || fail 'checksum verification failed'
printf 'Checksum verified for %s.\n' "$archive_name"
case $signature_verification in
	unavailable) confirm_signature_bypass ;;
	skipped) warn_signature_bypass ;;
esac

member=$(tar -tzf "$archive" | awk -F/ '$NF=="elgatolight" { count++; value=$0 } END { if (count==1) print value }')
[ -n "$member" ] || fail 'archive must contain exactly one elgatolight binary'
tar -xOzf "$archive" "$member" >"$tmp/elgatolight"
chmod 0755 "$tmp/elgatolight"
[ "$($tmp/elgatolight --version 2>/dev/null)" = "$tag" ] || fail 'downloaded binary version does not match the release'

umask 022
mkdir -p "$install_dir"
staged=$(mktemp "$install_dir/.elgatolight-install.XXXXXX")
install -m 0755 "$tmp/elgatolight" "$staged"
mv -f "$staged" "$install_dir/$binary"
printf 'Installed Elgato Key Light Neo USB controller %s at %s/%s\n' "$tag" "$install_dir" "$binary"
printf '\nConfigure it with one of these commands:\n'
printf '  %s/%s setup\n' "$install_dir" "$binary"
printf '    User service: starts when you log in. Only the USB rule uses sudo.\n\n'
printf '  sudo %s/%s setup\n' "$install_dir" "$binary"
printf '    System service: starts at boot and runs as root.\n\n'
printf '  %s/%s setup --scope none\n' "$install_dir" "$binary"
printf '    Command-line only: installs USB access without a daemon service.\n'
