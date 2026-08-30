# Contributing

By contributing, you agree that your contribution is licensed under the [GNU General Public License v3.0 only](LICENSE).

This guide applies equally to humans and coding agents.

## Workflow

1. Read `AGENTS.md` and the focused document related to the change. Agents also follow their workspace instructions.
2. Run `make tmplt-check` before product work. Apply an available template update separately so infrastructure changes remain reviewable.
3. Keep one change focused. Test behavior, side effects, boundaries, and failure paths rather than trivial assignments.
4. Use hermetic tests. Never call live release services, consume credentials, or mutate a developer machine from the automated suite.
5. Run `make ci`. Podman or Docker is the only required development tool; `make shell` provides an optional Distrobox environment.
6. Update relevant user, contributor, and release documentation with the code.
7. Inspect `git diff --check`, the final diff, and untracked files before committing. Never commit release keys or generated artifacts.

## Commit titles

After the initial release, use this limited Conventional Commit form:

```text
type(optional-scope): short impactful subject
```

Allowed types are `feat`, `fix`, `perf`, `refactor`, `docs`, `test`, `build`, `ci`, `chore`, and `revert`. Keep the first line at 72 characters or fewer, make it specific, omit a final period, and use `!` before `:` only when it adds useful release-note context.

```text
feat(update): verify signed checksums
fix(release): preserve tags on retry
docs: clarify Windows installation
```

New Tmplt derivatives use one root `INIT` commit before their first release. Elgatocmd was already public when it adopted Tmplt, so its existing root commit and release tags are immutable. Never rewrite public history.

Integrate pull requests by rebasing and fast-forwarding, or squash them when one commit is clearer. Never create merge commits; keep public history straight.

Release notes are generated from commit titles and grouped by type, with a Forgejo comparison link to the previous release. Both provisional and final notes cover changes since the previous stable release; before one exists, they include `INIT`. Generated `fixup!` and `squash!` titles may appear provisionally but are omitted from final release notes. Do not add a repository changelog file.

## Go and shell quality

Go must pass `gofmt`, `go vet`, regular and race-enabled tests, coverage reporting, cross-compilation, and `govulncheck`. Prefer the standard library and justify every runtime dependency. Keep side effects explicit, propagate `context.Context` through network work, bound downloaded and expanded data, and preserve the last known-good executable on update failure.

Shell scripts use POSIX `sh` unless a workflow step explicitly selects Bash. Quote expansions, require HTTPS, use temporary directories with traps, and stage replacements atomically.
