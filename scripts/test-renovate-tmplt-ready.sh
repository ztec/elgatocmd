#!/bin/sh

set -eu

repository_root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
readiness=$repository_root/scripts/renovate-tmplt-ready.sh
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
fake_bin=$test_root/bin
mkdir -p "$fake_bin"

cat >"$fake_bin/curl" <<'EOF'
#!/bin/sh
set -eu
url=
output=
while [ "$#" -gt 0 ]; do
	case $1 in
		--proto|--proto-redir|--connect-timeout|--max-time) shift 2 ;;
		-o) output=$2; shift 2 ;;
		-*) shift ;;
		*) url=$1; shift ;;
	esac
done
case $url in
	*/branches/main) cp "$FAKE_BRANCH" "$output" ;;
	*/releases?*) cp "$FAKE_RELEASES" "$output" ;;
	*/tags/v0.2.0) cp "$FAKE_TAG" "$output" ;;
	*/pulls?*) cp "$FAKE_PULLS" "$output" ;;
	*) printf 'unexpected URL: %s\n' "$url" >&2; exit 22 ;;
esac
EOF
chmod +x "$fake_bin/curl"

branch=$test_root/branch.json
releases=$test_root/releases.json
tag=$test_root/tag.json
pulls=$test_root/pulls.json
printf '%s\n' '[{"tag_name":"v0.2.0","draft":false}]' >"$releases"
printf '%s\n' '{"commit":{"sha":"released"}}' >"$tag"
printf '%s\n' '{"commit":{"id":"released"}}' >"$branch"
printf '%s\n' '[{"head":{"ref":"renovate/checkout-7.x"},"body":"| Package | Update |\\n| checkout | major |"}]' >"$pulls"

run_readiness() {
	PATH="$fake_bin:$PATH" \
	FAKE_BRANCH="$branch" FAKE_RELEASES="$releases" FAKE_TAG="$tag" FAKE_PULLS="$pulls" \
	TMPLT_SOURCE_API=http://127.0.0.1:1/tmplt "$readiness" 2>&1
}

output=$(run_readiness) || {
	printf 'released Tmplt source was not ready:\n%s\n' "$output" >&2
	exit 1
}
printf '%s\n' "$output" | grep -Fq 'Tmplt source is ready at v0.2.0 (released).'

printf '%s\n' '[{"head":{"ref":"renovate/cobra-1.x"},"body":"| Package | Update |\\n| cobra | minor |"}]' >"$pulls"
if output=$(run_readiness); then
	printf '%s\n' 'readiness accepted pending non-major source maintenance' >&2
	exit 1
fi
printf '%s\n' "$output" | grep -Fq 'a non-major dependency proposal must merge and release first'

printf '%s\n' '[]' >"$pulls"
printf '%s\n' '{"commit":{"id":"unreleased"}}' >"$branch"
if output=$(run_readiness); then
	printf '%s\n' 'readiness accepted an unreleased source main commit' >&2
	exit 1
fi
printf '%s\n' "$output" | grep -Fq 'has not been released'

printf '%s\n' '[renovate] Tmplt source readiness tests passed.'
