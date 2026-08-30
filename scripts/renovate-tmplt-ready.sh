#!/bin/sh

set -eu

api=${TMPLT_SOURCE_API:-https://git2.riper.fr/api/v1/repos/ztec/tmplt}
tmp=$(mktemp -d "${TMPDIR:-/tmp}/tmplt-ready.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

fetch() {
	curl -fsSL --proto '=https,http' --proto-redir '=https' \
		--connect-timeout 15 --max-time 120 "$1" -o "$2"
}

branch=$tmp/main.json
releases=$tmp/releases.json
tag=$tmp/tag.json
fetch "${api%/}/branches/main" "$branch"
fetch "${api%/}/releases?draft=false&limit=50&page=1" "$releases"

main_sha=$(jq -er '.commit.id' "$branch")
release_tag=$(jq -er '[.[] | select(.draft == false)][0].tag_name' "$releases")
printf '%s\n' "$release_tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || {
	printf '%s\n' 'Tmplt source maintenance is pending: the latest release tag is invalid.' >&2
	exit 1
}
fetch "${api%/}/tags/$release_tag" "$tag"
release_sha=$(jq -er '.commit.sha' "$tag")
if [ "$main_sha" != "$release_sha" ]; then
	printf 'Tmplt source maintenance is pending: main %s has not been released (latest is %s at %s).\n' \
		"$main_sha" "$release_tag" "$release_sha" >&2
	exit 1
fi

page=1
while :; do
	pulls=$tmp/pulls-$page.json
	fetch "${api%/}/pulls?state=open&limit=50&page=$page" "$pulls"
	if jq -e '
		any(.[];
			(.head.ref | startswith("renovate/")) and
			((.body // "") | test("\\|[[:space:]]*(digest|pin|pinDigest|patch|minor)[[:space:]]*\\|"; "i"))
		)
	' "$pulls" >/dev/null; then
		printf '%s\n' 'Tmplt source maintenance is pending: a non-major dependency proposal must merge and release first.' >&2
		exit 1
	fi
	[ "$(jq 'length' "$pulls")" -lt 50 ] && break
	page=$((page + 1))
done

printf 'Tmplt source is ready at %s (%s).\n' "$release_tag" "$release_sha"
