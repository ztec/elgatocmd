#!/bin/sh

set -eu

answers_file=.copier-answers.yml
source_url=https://git2.riper.fr/ztec/tmplt.git

fail() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

read_answer() {
	key=$1
	sed -n "s/^${key}:[[:space:]]*['\"]\\{0,1\\}\\([^'\"]*\\)['\"]\\{0,1\\}[[:space:]]*$/\\1/p" "$answers_file" | sed -n '1p'
}

target=$(read_answer _commit)
recorded_source=$(read_answer _src_path)
printf '%s\n' "$target" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || fail "unsupported Tmplt version: $target"
[ "$recorded_source" = "$source_url" ] || fail 'the answers file references an unexpected Tmplt source'

old_source=$(git show "HEAD:$answers_file" | sed -n 's/^_src_path:[[:space:]]*//p' | sed -n '1p')
old_version=$(git show "HEAD:$answers_file" | sed -n 's/^_commit:[[:space:]]*//p' | sed -n '1p')
[ "$old_source" = "$source_url" ] || fail 'the committed answers file references an unexpected Tmplt source'
[ "$old_version" != "$target" ] || fail 'Renovate did not change the recorded Tmplt version'

old_answers=$(git show "HEAD:$answers_file" | sed '/^_commit:[[:space:]]*/d')
new_answers=$(sed '/^_commit:[[:space:]]*/d' "$answers_file")
[ "$old_answers" = "$new_answers" ] || fail 'Renovate changed more than the recorded Tmplt version'
[ -z "$(git diff --cached --name-only)" ] || fail 'Renovate staged unexpected files before template application'
[ -z "$(git ls-files --others --exclude-standard)" ] || fail 'Renovate created unexpected files before template application'
for path in $(git diff --name-only); do
	case $path in
	"$answers_file"|go.mod|go.sum) ;;
	*) fail "Renovate changed an unexpected file before template application: $path" ;;
	esac
done
git restore --worktree -- "$answers_file" go.mod go.sum
[ -z "$(git status --porcelain --untracked-files=all)" ] || fail 'could not restore a clean tree before template application'

python_version=$(sed -n 's#^FROM docker.io/library/python:\([0-9][0-9.]*\)-.*#\1#p' tools/copier/Containerfile)
copier_version=$(sed -n 's/^RUN pip install --no-cache-dir copier==\([0-9][0-9.]*\)$/\1/p' tools/copier/Containerfile)
go_version=$(sed -n 's/^go \([0-9][0-9.]*\)$/\1/p' go.mod)
[ -n "$python_version" ] || fail 'cannot determine the pinned Python version'
[ -n "$copier_version" ] || fail 'cannot determine the pinned Copier version'
[ -n "$go_version" ] || fail 'cannot determine the pinned Go version'

install-tool python "$python_version"
install-tool copier "$copier_version"
install-tool golang "$go_version"
copier update --vcs-ref "$target" --defaults --skip-answered --conflict rej .
./scripts/tmplt-source.sh set-module "$target"
go mod tidy
./scripts/tmplt-update-validate.sh
