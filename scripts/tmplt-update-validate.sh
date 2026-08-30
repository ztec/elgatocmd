#!/bin/sh

set -eu

fail() {
	printf 'error: %s\n' "$*" >&2
	exit 2
}

answers_file=.copier-answers.yml
[ -f "$answers_file" ] || fail "$answers_file is missing"
[ -f go.mod ] || fail 'go.mod is missing'
[ -f Makefile ] || fail 'Makefile is missing'

version=$(sed -n 's/^_commit:[[:space:]]*//p' "$answers_file" | sed -n '1p' | tr -d "'\"")
printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || fail 'the recorded Tmplt version is missing or invalid'

rejection=$(find . -path './.git' -prune -o -name '*.rej' -print -quit)
[ -z "$rejection" ] || fail "Copier left a conflict at $rejection; resolve it before testing"
[ ! -d release-keys ] || fail 'generated projects must not contain a release-keys directory'
! grep -Eq '^[[:space:]]*signing-key[[:space:]]*:' Makefile || fail 'generated projects must not contain the signing-key target'
awk -v version="$version" '$1 == "git2.riper.fr/ztec/tmplt" && $2 == version { found++ } END { exit found == 1 ? 0 : 1 }' go.mod ||
	fail "go.mod must require git2.riper.fr/ztec/tmplt $version exactly once"
