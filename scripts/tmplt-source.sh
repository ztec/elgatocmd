#!/bin/sh

set -eu

source_url=${TMPLT_SOURCE_URL:-https://git2.riper.fr/ztec/tmplt.git}
source_web_url=${TMPLT_SOURCE_WEB_URL:-https://git2.riper.fr/ztec/tmplt}
answers_file=${TMPLT_ANSWERS_FILE:-.copier-answers.yml}
template_module=git2.riper.fr/ztec/tmplt

fail() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

read_answer() {
	key=$1
	sed -n "s/^${key}:[[:space:]]*['\"]\\{0,1\\}\\([^'\"]*\\)['\"]\\{0,1\\}[[:space:]]*$/\\1/p" "$answers_file" | sed -n '1p'
}

current_version() {
	[ -f "$answers_file" ] || fail "$answers_file is missing; this command only applies to a generated project"
	recorded_source=$(read_answer _src_path)
	[ "$recorded_source" = "$source_url" ] || fail "$answers_file does not reference the selected Tmplt source"
	current=$(read_answer _commit)
	printf '%s\n' "$current" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || fail "$answers_file has an unsupported Tmplt version: $current"
	printf '%s\n' "$current"
}

latest_version() {
	git ls-remote --tags --refs "$source_url" 'v*' |
		sed -n 's#.*refs/tags/\(v[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\)$#\1#p' |
		sort -V |
		tail -n 1
}

set_module_version() {
	version=$1
	printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || fail "unsupported Tmplt module version: $version"
	[ -f go.mod ] || fail 'go.mod is missing'
	temporary=go.mod.tmplt-update.$$
	if ! awk -v module="$template_module" -v version="$version" '
		$1 == module {
			count++
			$2 = version
		}
		{ print }
		END { if (count != 1) exit 1 }
	' go.mod >"$temporary"; then
		rm -f "$temporary"
		fail "go.mod must require $template_module exactly once"
	fi
	mv "$temporary" go.mod
}

case ${1:-check} in
	check)
		current=$(current_version)
		latest=$(latest_version)
		[ -n "$latest" ] || fail 'the selected Tmplt source has no compatible release'
		status=outdated
		[ "$current" != "$latest" ] || status=current
		printf 'current=%s\nlatest=%s\nstatus=%s\n' "$current" "$latest" "$status"
		if [ "$status" = outdated ]; then
			printf 'comparison=%s/compare/%s...%s\n' "${source_web_url%/}" "$current" "$latest"
		fi
		;;
	current)
		current_version
		;;
	latest)
		latest=$(latest_version)
		[ -n "$latest" ] || fail 'the selected Tmplt source has no compatible release'
		printf '%s\n' "$latest"
		;;
	set-module)
		[ "$#" -eq 2 ] || fail 'usage: scripts/tmplt-source.sh set-module VERSION'
		set_module_version "$2"
		;;
	*)
		fail 'usage: scripts/tmplt-source.sh [check|current|latest|set-module VERSION]'
		;;
esac
