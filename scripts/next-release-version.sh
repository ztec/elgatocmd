#!/bin/sh

set -eu

target=${1:-HEAD}
target_commit=$(git rev-parse --verify "$target^{commit}")
version_components=${VERSION_COMPONENTS:-2}
case $version_components in
	2) wanted_pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' ;;
	3) wanted_pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' ;;
	*) printf 'error: VERSION_COMPONENTS must be 2 or 3\n' >&2; exit 2 ;;
esac
version_pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*))?$'

# Reuse a stable tag already attached to this commit so retries are idempotent.
for candidate in $(git tag --points-at "$target_commit" --list 'v*' --sort=-version:refname); do
	if printf '%s\n' "$candidate" | grep -Eq "$wanted_pattern"; then
		printf '%s\n' "$candidate"
		exit 0
	fi
done

latest=$(git tag --list 'v*' --sort=-version:refname | grep -E "$version_pattern" | head -n 1 || true)
if [ -z "$latest" ]; then
	if [ "$version_components" = 3 ]; then printf '%s\n' 'v0.1.0'; else printf '%s\n' 'v0.1'; fi
	exit 0
fi

numbers=${latest#v}
major=${numbers%%.*}
rest=${numbers#*.}
minor=${rest%%.*}
if [ "$version_components" = 3 ]; then
	printf 'v%s.%s.0\n' "$major" "$((minor + 1))"
else
	printf 'v%s.%s\n' "$major" "$((minor + 1))"
fi
