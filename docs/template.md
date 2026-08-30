# Template updates

This project was generated from [Tmplt](https://git2.riper.fr/ztec/tmplt). It owns its Git history, workflows, releases, and product decisions; the template is only an update source.

## Recorded contract

`.copier-answers.yml` records the applied Tmplt release and these project choices:

- display name, description, binary name, and Go module path;
- Forgejo owner, repository name, web origin, SSH host, and SSH port;
- configuration environment prefix and copyright holder;
- release platform scope (`all` or `linux`);
- two- or three-component release versions.

The normal product version is `MAJOR.MINOR`. Selecting three components makes all version parsing, automatic releases, tags, notes, builds, installation, and self-update accept `MAJOR.MINOR.PATCH` instead. These numbers identify ordered releases; they do not promise any particular compatibility policy.

Do not delete or hand-regenerate the answers file. A deliberate answer change may be edited there and applied with the next update, then reviewed like any other template change.

## Manual update

```sh
make tmplt-check
make tmplt-update
```

`tmplt-check` reports `current`, `latest`, and `status`, plus a Forgejo comparison link when an update is available. `tmplt-update` requires a clean Git worktree, renders the latest tagged Tmplt release with Copier, synchronizes the reusable Tmplt module, rejects conflicts, inherited release keys, or a signing-key target, normalizes Go dependencies, and runs the full containerized CI suite. It leaves the update uncommitted for review. Manual updates, Renovate updates, and ordinary CI enforce this same contract.

Copier preserves project changes where it can. When both the project and template changed the same lines, it leaves `.rej` files and stops before dependency normalization. Resolve the conflicts, remove the rejection files, run `make deps-tidy` and `make ci`, then commit the reviewed result. If the update is unsuitable, restore the clean pre-update tree with your normal Git workflow.

The updater is built from the pinned `tools/copier/Containerfile`; neither Copier, Python, nor Go is required on the host.

## Shared Go packages

Self-update and release verification come from `git2.riper.fr/ztec/tmplt`. The required module version follows the same Tmplt tag as `.copier-answers.yml`, while a small local adapter supplies this project's binary name and endpoints. `go.mod` and `go.sum` otherwise belong to the derived project: Copier does not overwrite them during updates. Go's module version selection carries newer shared requirements such as Cobra and Viper from Tmplt while the normal dependency tidy preserves project-specific requirements and permits their independent maintenance.

Release public keys are different: they are intentionally not versioned into this repository or executable. Release builds, the installer, and self-update always fetch active and revoked key status from [the latest Tmplt `main`](https://git2.riper.fr/ztec/tmplt/src/branch/main/release-keys).

## Renovate

The local `.forgejo/workflows/renovate.yaml` runs the official pinned Renovate image at 05:17 and 17:17 UTC every day using the shared protected `RENOVATE_TOKEN` bot secret. Tmplt runs its own maintenance one hour earlier, giving its merged changes time to become a release before this repository checks for them. The shared protected `RENOVATE_GH_TOKEN`, sourced from a GitHub classic personal access token with no selected scopes, is mapped to Renovate's `RENOVATE_GITHUB_COM_TOKEN` environment variable and provides public GitHub metadata without anonymous API limits. Manual dispatch runs an immediate check, and changes to the Renovate configuration on `main` refresh the Dependency Dashboard automatically. Set the repository variable `RENOVATE_ENABLED=false` to pause it.

Each run checks the recorded Tmplt version first. If no template pull request is open, the workflow removes an obsolete `renovate/tmplt-template` bot branch before asking Renovate to create the current proposal. If it produces or finds the dedicated template update pull request, Renovate retires ordinary dependency pull requests and waits for the template update to pass CI and merge. Before the next run performs a complete scan, it verifies that Tmplt `main` has a matching release and no non-major Tmplt dependency proposal remains open. This prevents duplicate template-owned proposals while source maintenance is still being tested or released; major source proposals do not block the project. A previously closed Tmplt proposal is recreated while the update remains applicable; use `RENOVATE_ENABLED=false` to pause maintenance deliberately. No repository path is excluded from that scan, including paths originally rendered by Tmplt. Renovate renders the same tagged Copier update from a clean tree, synchronizes the reusable Go module, normalizes `go.sum`, and opens an ordinary pull request. Pull-request CI remains the acceptance gate. Manual `make tmplt-update` stays available and does not depend on Renovate.

Configure Forgejo to allow fast-forward-only merges and disable merge commits. Renovate opens one versioned pull request and commit per external dependency. Non-major maintenance automatically fast-forwards only after successful checks; major updates remain open for manual review. Rebase and squash may remain available for human integration while repository history stays free of merge commits.

## Existing release history

Fresh Tmplt derivatives use one root commit titled `INIT` before their first release. Elgatocmd already had public releases when Tmplt was adopted, so that initialization rule is not applied retroactively. Preserve its existing root commit and tags, use the Conventional Commit policy for new work, and never rewrite public history.
