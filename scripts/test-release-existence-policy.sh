#!/bin/sh

set -eu

repository_root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
policy=$repository_root/scripts/release-existence-policy.sh
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

expected=$test_root/expected
actual=$test_root/actual
signature=elgatolight-v0.1-checksums.txt.sig
printf '%s\n' \
	elgatolight-v0.1-linux-amd64.tar.gz \
	elgatolight-v0.1-checksums.txt \
	"$signature" >"$expected"

assert_policy() {
	event=$1
	want=$2
	shift 2
	printf '%s\n' "$@" >"$actual"
	got=$($policy "$event" "$signature" "$expected" "$actual")
	[ "$got" = "$want" ] || {
		printf 'policy for %s returned:\n%s\nexpected:\n%s\n' "$event" "$got" "$want" >&2
		exit 1
	}
}

assert_policy push 'exists=true
replace=false
reason=complete' \
	elgatolight-v0.1-linux-amd64.tar.gz elgatolight-v0.1-checksums.txt "$signature"

assert_policy push 'exists=false
replace=true
reason=incomplete-managed' \
	elgatolight-v0.1-checksums.txt "$signature"

assert_policy release 'exists=false
replace=true
reason=unsigned-release-event' \
	elgatolight-v0.1-linux-amd64.tar.gz elgatolight-v0.1-checksums.txt

assert_policy push 'exists=true
replace=false
reason=unsigned-preserved' \
	elgatolight-v0.1-linux-amd64.tar.gz elgatolight-v0.1-checksums.txt

printf '%s\n' "$signature" "$signature" >"$actual"
if "$policy" push "$signature" "$expected" "$actual" >/dev/null 2>&1; then
	printf '%s\n' 'release existence policy accepted duplicate remote assets' >&2
	exit 1
fi

printf '%s\n' '[release] Existing-release policy tests passed.'
