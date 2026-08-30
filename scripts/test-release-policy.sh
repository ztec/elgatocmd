#!/bin/sh

set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

assert_policy() {
	version=$1
	source_prerelease=$2
	want_prerelease=$3
	policy=$($repository_root/scripts/release-policy.sh "$version" "$source_prerelease")
	title=$(printf '%s\n' "$policy" | sed -n 's/^title=//p')
	prerelease=$(printf '%s\n' "$policy" | sed -n 's/^prerelease=//p')
	[ "$title" = "$version" ] || {
		printf 'release title = %s, want %s\n' "$title" "$version" >&2
		exit 1
	}
	[ "$prerelease" = "$want_prerelease" ] || {
		printf '%s prerelease = %s, want %s\n' "$version" "$prerelease" "$want_prerelease" >&2
		exit 1
	}
}

assert_policy v0.1 false true
assert_policy v0.99 false true
assert_policy v0.99.0 false true
assert_policy v1.0 false false
assert_policy v1.0.0 false false
assert_policy v2.4 false false
assert_policy v1.2-rc.1 false true
assert_policy v1.2.0-rc.1 false true
assert_policy v2.4 true true

if "$repository_root/scripts/release-policy.sh" invalid false >/dev/null 2>&1; then
	printf 'release policy accepted an invalid version\n' >&2
	exit 1
fi
if "$repository_root/scripts/release-policy.sh" v1.0 invalid >/dev/null 2>&1; then
	printf 'release policy accepted an invalid source prerelease state\n' >&2
	exit 1
fi
