#!/bin/sh

set -eu

version=${1:-}
target=${2:-HEAD}
mode=${3:-final}
repository_url=${4:-}
previous_stable=${5:-auto}
if ! printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*))?(-[0-9A-Za-z][0-9A-Za-z.-]*)?$'; then
	printf 'error: release-notes requires vMAJOR.MINOR with an optional PATCH component\n' >&2
	exit 2
fi
case $mode in
	final) heading='##' ;;
	provisional) heading='###' ;;
	*) printf 'error: release-notes mode must be final or provisional\n' >&2; exit 2 ;;
esac
case $repository_url in
	''|https://*) ;;
	*) printf 'error: release-notes repository URL must use HTTPS\n' >&2; exit 2 ;;
esac

release_pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*))?(-[0-9A-Za-z][0-9A-Za-z.-]*)?$'
previous_release=$(git tag --merged "$target" --list 'v*' --sort=-version:refname \
	| grep -E "$release_pattern" \
	| grep -Fvx "$version" \
	| head -n 1 || true)
case $previous_stable in
	auto)
	previous_changelog=$(git tag --merged "$target" --list 'v*' --sort=-version:refname \
		| grep -E '^v([1-9][0-9]*)\.(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*))?$' \
		| grep -Fvx "$version" \
		| head -n 1 || true)
	;;
	-)
		previous_changelog=
		;;
	*)
		if ! printf '%s\n' "$previous_stable" | grep -Eq '^v([1-9][0-9]*)\.(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*))?$'; then
			printf 'error: previous stable release must be an unsuffixed version tag, -, or auto\n' >&2
			exit 2
		fi
		if [ "$previous_stable" = "$version" ] \
			|| ! git rev-parse --verify --quiet "$previous_stable^{commit}" >/dev/null \
			|| ! git merge-base --is-ancestor "$previous_stable^{commit}" "$target"; then
			printf 'error: previous stable release %s is not an earlier ancestor of %s\n' "$previous_stable" "$target" >&2
			exit 2
		fi
		previous_changelog=$previous_stable
		;;
esac
if [ -n "$previous_changelog" ]; then
	range=$previous_changelog..$target
else
	range=$target
fi

notes_root=$(mktemp -d)
trap 'rm -rf "$notes_root"' EXIT HUP INT TERM
for group in feat fix perf refactor docs test build ci chore revert other; do
	: >"$notes_root/$group"
done

git log --reverse --format=%s "$range" | while IFS= read -r subject; do
	[ -n "$subject" ] || continue
	case $mode:$subject in final:fixup\!*|final:squash\!*) continue ;; esac
	type=$(printf '%s\n' "$subject" | sed -n 's/^\([a-z][a-z]*\)[^:]*: .*/\1/p')
	text=$(printf '%s\n' "$subject" | sed 's/^[^:]*: //')
	case $type in
		feat|fix|perf|refactor|docs|test|build|ci|chore|revert) group=$type ;;
		*) group=other; text=$subject ;;
	esac
	printf -- '- %s\n' "$text" >>"$notes_root/$group"
done

printf '# %s\n\n' "$version"
if [ -n "$repository_url" ] && [ -n "$previous_release" ]; then
	printf '[Compare changes](%s/compare/%s...%s)\n' "${repository_url%/}" "$previous_release" "$version"
fi
if [ "$mode" = provisional ]; then
	printf '\n## Provisional changelog\n'
fi

emit_group() {
	group=$1
	title=$2
	[ -s "$notes_root/$group" ] || return 0
	printf '\n%s %s\n\n' "$heading" "$title"
	cat "$notes_root/$group"
}

emit_group feat Features
emit_group fix Fixes
emit_group perf Performance
emit_group refactor Refactoring
emit_group docs Documentation
emit_group test Tests
emit_group build Build
emit_group ci CI
emit_group chore Maintenance
emit_group revert Reverts
emit_group other 'Other changes'
