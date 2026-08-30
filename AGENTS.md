# Elgato Key Light Neo USB controller agent guidance

## Mission

Keep this project small, operational, portable within its declared platform scope, and straightforward for humans and agents to maintain.

## Required workflow

- Follow all applicable workspace instructions before repository work.
- Use Podman or Docker through `make test`, `make build`, `make run`, and `make ci`. Distrobox is optional through `make shell`.
- Keep tests hermetic. Never contact a live release service or consume credentials from automated tests.
- Never commit private signing material, public release keys, generated binaries, coverage output, or release archives.
- Do not add a `signing-key` Make target. Release signing uses the workspace secret and the active public keys fetched from [Tmplt `main`](https://git2.riper.fr/ztec/tmplt/src/branch/main/release-keys).
- Preserve `.copier-answers.yml`; it records the applied template release and project choices.
- Before project work, run `make tmplt-check`. Apply template releases separately with `make tmplt-update`, inspect the resulting diff, and resolve any `.rej` conflicts before committing.
- Keep the README short. Put detailed design, development, release, and template-update explanations under `docs/`.
- Do not add `CHANGELOG.md`; Forgejo release notes are generated from commit titles.
- This repository was publicly released before adopting Tmplt. Preserve its root commit and release tags, use the convention in `CONTRIBUTING.md`, and never rewrite public history.

Preserve the Linux USB controller, daemon, Home Assistant integration, setup flow, and HACS package as one tested product. Keep product behavior outside template-owned lifecycle code where practical.
