# Development

## Container-first workflow

Podman or Docker is the only required development tool. The Makefile prefers Podman and falls back to Docker; override it with `CONTAINER_ENGINE=docker` when needed.

```sh
make test       # complete quality suite
make security   # govulncheck
make ci         # test plus security, exactly as Forgejo CI runs it
make build      # bin/elgatolight
make run        # minimal non-root runtime image
```

The development image pins the Go toolchain and includes Python plus the Home
Assistant custom-component test stack. `make test` checks formatting, vetting,
module integrity, shuffled Go tests, the race detector, coverage, Python
compilation and pytest, shell syntax, release versioning, every supported
cross-build, and the template-update contract.

Tests must be hermetic. The updater and installer suites use loopback or fake
HTTP clients, generated signing keys, temporary archives, and temporary
executables; they never contact Forgejo, Home Assistant, USB hardware, or an
installed binary.

Container-backed CI executes repository code with access to its container engine. Run pull-request jobs only on isolated, disposable runners; the release runner must additionally be trusted and restricted because it can read the signing secret.

Pull requests run the full suite and are the only automatic validation path outside `main`. Branch pushes do not start CI; open a pull request or use the manual workflow dispatch when validation is needed. A push to `main` runs the same test job once, then continues to release build and publication only after it succeeds.

## Distrobox

Distrobox is optional and uses the same development image:

```sh
make shell
make box-replace   # recreate the box after an image/toolchain change
```

Inside the box, use `make test-native`, `make build-native`, `make run-native`, or `make ci-native`. These targets are implementation details for the prepared environment; host workflows should use their container-backed names.

## Dependency maintenance

`renovate.json` covers the recorded Copier template, Go modules, Python
requirements, Docker base images and digests, pinned Forgejo actions, and pinned
Makefile tools. Public dependencies with published timestamps observe the
30-day age gate. Trusted Tmplt template and reusable Go module updates are
eligible immediately, including Forgejo pre-releases below `v1.0`; their
release timestamps remain available for auditing. Sources without timestamps
remain eligible instead of being held forever. Manual template maintenance uses
`make tmplt-check` and `make tmplt-update`.

The repository-local Renovate workflow runs every day at 05:17 and 17:17 UTC using the shared protected `RENOVATE_TOKEN` bot secret, one hour after Tmplt's own maintenance. Each run checks Tmplt alone first without the ordinary hourly PR limit, retires ordinary Renovate pull requests while the dedicated template update pull request is open, and performs the complete dependency scan after it merges. No repository path is excluded from that later scan. Manual dispatch starts an immediate check, and a configuration change on `main` refreshes the Dependency Dashboard automatically. The shared protected `RENOVATE_GH_TOKEN`, sourced from a GitHub classic personal access token with no selected scopes, is mapped to Renovate's `RENOVATE_GITHUB_COM_TOKEN` environment variable so Go dependency lookups can obtain public release dates without hitting GitHub's anonymous API limit. Docker Hub discovery is bounded to the newest 1,000 tags because deeper anonymous pagination is rejected by Docker Hub; updates discovered within that window retain their publish dates and the 30-day gate. Set the repository variable `RENOVATE_ENABLED=false` to pause it. Renovate applies template updates through Copier, synchronizes the reusable Go module, and opens ordinary pull requests; every proposal must pass its pull-request CI before merge. Manual updates remain available without Renovate.

Renovate opens one versioned pull request and generated commit per dependency. Digest, pin, patch, and minor updates become eligible for automatic merge only after Forgejo reports their checks successful. Renovate disables platform-native early merge, rebases a branch that has fallen behind `main`, and then uses a fast-forward-only merge; major updates always remain manual. Forgejo must allow fast-forward-only merges for the repository.

Before handing off a change:

```sh
make ci
git diff --check
git status --short
```
