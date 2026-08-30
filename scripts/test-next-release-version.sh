#!/bin/sh

set -eu

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

git -C "$test_root" init --quiet
git -C "$test_root" config user.name release-test
git -C "$test_root" config user.email release-test@example.invalid

commit() {
	printf '%s\n' "$1" >"$test_root/value"
	git -C "$test_root" add value
	git -C "$test_root" commit --quiet -m "$1"
}

assert_version() {
	want=$1
	components=${2:-2}
	got=$(cd "$test_root" && VERSION_COMPONENTS=$components "$repository_root/scripts/next-release-version.sh" HEAD)
	[ "$got" = "$want" ] || {
		printf 'next version = %s, want %s\n' "$got" "$want" >&2
		exit 1
	}
}

commit INIT
git -C "$test_root" tag v0.1-rc.1
git -C "$test_root" tag v1.2.03
git -C "$test_root" tag v01.1
assert_version v0.1
git -C "$test_root" tag --annotate v0.1 --message 'release v0.1'
assert_version v0.1

commit 'feat: add one thing'
assert_version v0.2
git -C "$test_root" tag v0.2
assert_version v0.2

commit 'fix: correct one thing'
git -C "$test_root" tag --annotate v1.0 --message 'major release'
assert_version v1.0

commit 'docs: explain one thing'
assert_version v1.1
assert_version v1.1.0 3

git -C "$test_root" tag v1.1.0
assert_version v1.1.0 3

commit 'test: exercise another release'
assert_version v1.2 2
assert_version v1.2.0 3

if (cd "$test_root" && VERSION_COMPONENTS=4 "$repository_root/scripts/next-release-version.sh" HEAD >/dev/null 2>&1); then
	printf 'next version accepted an invalid component count\n' >&2
	exit 1
fi
