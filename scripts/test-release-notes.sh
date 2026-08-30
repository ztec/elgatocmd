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

commit INIT
git -C "$test_root" tag v0.1
commit 'feat: add useful output'
git -C "$test_root" tag v0.2
commit 'fix(update): preserve the installed binary'
commit 'docs: explain release trust'
commit 'fixup! feat: add useful output'

provisional=$(cd "$test_root" && "$repository_root/scripts/release-notes.sh" v0.3 HEAD provisional https://forge.example/acme/tool -)
for required in '# v0.3' '[Compare changes](https://forge.example/acme/tool/compare/v0.2...v0.3)' '## Provisional changelog' '### Features' '- add useful output' '### Fixes' '- preserve the installed binary' '### Documentation' '- explain release trust' '- INIT' '- fixup! feat: add useful output'; do
	printf '%s\n' "$provisional" | grep -Fq -- "$required" || {
		printf 'provisional release notes do not contain %s\n%s\n' "$required" "$provisional" >&2
		exit 1
	}
done
if printf '%s\n' "$provisional" | grep -Fq 'changelog generation is reserved'; then
	printf 'provisional release notes contain the obsolete generic note\n%s\n' "$provisional" >&2
	exit 1
fi

final=$(cd "$test_root" && "$repository_root/scripts/release-notes.sh" v1.0 HEAD final https://forge.example/acme/tool)
for required in '# v1.0' '[Compare changes](https://forge.example/acme/tool/compare/v0.2...v1.0)' '## Features' '## Fixes' '## Documentation' '- INIT'; do
	printf '%s\n' "$final" | grep -Fq -- "$required" || {
		printf 'final release notes do not contain %s\n%s\n' "$required" "$final" >&2
		exit 1
	}
done
if printf '%s\n' "$final" | grep -Fq 'fixup!'; then
	printf 'final release notes contain a fixup commit\n%s\n' "$final" >&2
	exit 1
fi

git -C "$test_root" tag v1.0
commit 'feat: add post-stable behavior'
git -C "$test_root" tag v1.1
commit 'fix: repair the release candidate'

since_stable=$(cd "$test_root" && "$repository_root/scripts/release-notes.sh" v1.2 HEAD provisional https://forge.example/acme/tool v1.0)
for required in '# v1.2' '[Compare changes](https://forge.example/acme/tool/compare/v1.1...v1.2)' '- add post-stable behavior' '- repair the release candidate'; do
	printf '%s\n' "$since_stable" | grep -Fq -- "$required" || {
		printf 'stable-based provisional notes do not contain %s\n%s\n' "$required" "$since_stable" >&2
		exit 1
	}
done

three_component=$(cd "$test_root" && "$repository_root/scripts/release-notes.sh" v1.2.0 HEAD provisional https://forge.example/acme/tool v1.0)
printf '%s\n' "$three_component" | grep -Fq '# v1.2.0' || {
	printf 'release notes rejected a three-component version\n%s\n' "$three_component" >&2
	exit 1
}
for forbidden in '- INIT' '- add useful output'; do
	if printf '%s\n' "$since_stable" | grep -Fq -- "$forbidden"; then
		printf 'stable-based provisional notes contain pre-baseline change %s\n%s\n' "$forbidden" "$since_stable" >&2
		exit 1
	fi
done

if (cd "$test_root" && "$repository_root/scripts/release-notes.sh" v1.2 HEAD provisional https://forge.example/acme/tool v1.2 >/dev/null 2>&1); then
	printf 'release notes accepted the current version as its stable baseline\n' >&2
	exit 1
fi
for forbidden in 'Signed release for' 'attached checksums' 'Elgato Key Light Neo USB controller main' 'public-key manifest'; do
	if printf '%s\n%s\n%s\n' "$provisional" "$final" "$since_stable" | grep -Fq "$forbidden"; then
		printf 'release notes contain documentation-only trust guidance %s\n%s\n%s\n%s\n' "$forbidden" "$provisional" "$final" "$since_stable" >&2
		exit 1
	fi
done
