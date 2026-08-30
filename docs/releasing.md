# Releasing

## Policy

Versions use `vMAJOR.MINOR`; manual prereleases append a suffix such as `v0.2-rc.1`.

- Every successful push to `main` calculates and publishes the next minor version, beginning at `v0.1`.
- Every version below `v1.0` is a prerelease. A version suffix or a Forgejo release initially marked as a prerelease also keeps the release in that state.
- A version tag already attached to the commit is reused, making a failed job safe to retry.
- Pushing a valid version tag starts a release for that exact commit.
- Publishing a release starts the same validation and signed build; an initial unsigned release is replaced once, preserving its prerelease state.
- After validation, existing version-release titles and prerelease flags are normalized to this policy without changing their notes or assets.
- Major versions are deliberate manual tags; automation never increments `MAJOR`.
- Stable releases receive a generated, type-grouped changelog from Conventional Commit titles.
- Provisional and final changelogs cover every change since the previous stable Forgejo release. Before the first stable release, they start at the repository root and include `INIT`. Provisional notes retain `fixup!` and `squash!` titles; final stable notes omit them.
- Every release after the first links to Forgejo's comparison view from the previous release tag, exposing its commits and complete diff.
- The repository never contains a `CHANGELOG` file.

Branch CI and release validation both execute `make ci` in the pinned development container. Release publication starts only after that suite and the non-root runtime smoke test pass.

## Signing setup

`RELEASE_SIGNING_KEY` is a protected workspace-level Forgejo Actions secret inherited by projects. Do not create a repository-level copy.

[Tmplt's `release-keys/` on `main`](https://git2.riper.fr/ztec/tmplt/src/branch/main/release-keys) must remain publicly readable so installers and self-update can verify releases without Forgejo credentials. This repository deliberately has neither that directory nor a signing-key Make target.

Every project build fetches the central key registry from the latest Tmplt `main` and refuses to sign unless the global secret's public half matches an active key. It signs the exact checksum file and verifies the signature before upload. Public keys are not copied into project repositories, release assets, or binaries. Install and self-update fetch the same central registry, try every active key, and separately retain revoked keys for diagnosis.

If a signature matches a `-revoked.pub` key, installation or update is refused with the key name and instructions to wait for a newer release signed by an active key. `self-update --force` never bypasses signature verification. The Go self-updater has no external cryptographic dependency.

The shell installer uses OpenSSL for Ed25519. When suitable OpenSSL support is unavailable, an interactive terminal receives a detailed checksum-only warning and must confirm before installation continues. Automation must opt in explicitly with `--skip-signature-verification` or `ELGATOLIGHT_SKIP_SIGNATURE_VERIFICATION=1`. In that mode SHA-256 is still verified and reported, but authenticity is not established: an attacker able to replace a binary can also replace its checksum.

Because public keys are fetched from the same Forgejo instance as releases, this model detects corrupt or mismatched assets but does not protect a client if Forgejo repository/tag data itself is maliciously replaced. Protect repository history, release tags, the workspace secret, and the release runner together.

## Rotation and revocation

Key generation and rotation happen only in Tmplt. The workspace operator adds the new dated public key there, replaces the global secret, publishes replacement releases, and then renames an old file to `NAME-revoked.pub`. That rename immediately prevents install and self-update from trusting releases signed by the revoked key because they always consult the latest Tmplt `main`.

## Local rehearsal

A clean Git commit, Podman or Docker, and an operator-supplied in-memory private key are required. Avoid shell history and unset the value immediately afterwards:

```sh
read -r -s RELEASE_SIGNING_KEY
export RELEASE_SIGNING_KEY
make release VERSION=v0.1
unset RELEASE_SIGNING_KEY
```

The output under `dist/` contains:

- Linux tarballs for amd64, arm64, and armv7

- SHA-256 checksums and their Ed25519 signature

Archives include the binary, short README, GPL-3.0-only license, focused docs, and the verbatim licenses and notices of compiled Go dependencies. Tar ordering, ownership, timestamps, and gzip headers are normalized from the release commit for reproducible tarballs.

## Forgejo behavior

`.forgejo/workflows/test.yaml` tests pull requests without release jobs. `.forgejo/workflows/test-and-release.yaml` runs on `main`, tags, and published releases; its single successful test feeds the build and publication jobs, so a main push never starts a separate test workflow. A main-push job creates or reuses an annotated version tag; a direct tag push uses the existing tag. A manually published release is replaced only when explicitly published without the expected signed assets. A partial automated upload for the same verified tag and commit is rebuilt and replaced on retry. Once every expected signed asset exists, release publication is immutable and idempotent, so follow-up tag or release events cannot overwrite or duplicate it.

Protect `main`, release tags, the workspace signing secret, and the release runner. Correct a bad public release with a new commit and version—never replace its assets silently.
