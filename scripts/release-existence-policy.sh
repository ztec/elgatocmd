#!/bin/sh

set -eu

fail() {
	printf 'error: %s\n' "$*" >&2
	exit 2
}

[ "$#" -eq 4 ] || fail 'usage: release-existence-policy.sh EVENT SIGNATURE EXPECTED_ASSETS ACTUAL_ASSETS'
event=$1
signature=$2
expected_assets=$3
actual_assets=$4

case $event in push|release) ;; *) fail "unsupported release event: $event" ;; esac
[ -s "$expected_assets" ] || fail 'expected release asset list is empty'
[ -f "$actual_assets" ] || fail 'actual release asset list is missing'

validate_assets() {
	list=$1
	label=$2
	while IFS= read -r asset || [ -n "$asset" ]; do
		case $asset in ''|*/*|.|..) fail "$label contains an invalid asset name: $asset" ;; esac
	done <"$list"
	duplicates=$(LC_ALL=C sort "$list" | uniq -d | sed -n '1p')
	[ -z "$duplicates" ] || fail "$label contains duplicate asset $duplicates"
}

validate_assets "$expected_assets" expected-assets
validate_assets "$actual_assets" actual-assets
grep -Fxq "$signature" "$expected_assets" || fail 'the checksum signature is not part of the expected release assets'

complete=true
while IFS= read -r asset || [ -n "$asset" ]; do
	if ! grep -Fxq "$asset" "$actual_assets"; then
		complete=false
		break
	fi
done <"$expected_assets"

managed=false
if grep -Fxq "$signature" "$actual_assets"; then
	managed=true
fi

if [ "$complete" = true ]; then
	printf '%s\n' 'exists=true' 'replace=false' 'reason=complete'
elif [ "$managed" = true ]; then
	printf '%s\n' 'exists=false' 'replace=true' 'reason=incomplete-managed'
elif [ "$event" = release ]; then
	printf '%s\n' 'exists=false' 'replace=true' 'reason=unsigned-release-event'
else
	printf '%s\n' 'exists=true' 'replace=false' 'reason=unsigned-preserved'
fi
