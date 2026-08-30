#!/bin/sh

set -eu

version=${1:-}
source_prerelease=${2:-false}

if ! printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*))?(-[0-9A-Za-z][0-9A-Za-z.-]*)?$'; then
	printf 'error: release version must use vMAJOR.MINOR with an optional PATCH component and prerelease suffix\n' >&2
	exit 2
fi
case $source_prerelease in
	true|false) ;;
	*) printf 'error: source prerelease state must be true or false\n' >&2; exit 2 ;;
esac

prerelease=$source_prerelease
case $version in
	v0.*|*-*) prerelease=true ;;
esac

printf 'title=%s\n' "$version"
printf 'prerelease=%s\n' "$prerelease"
