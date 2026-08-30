# Security

## Supported code

Security fixes target the current `main` branch and the latest stable release.

## Reporting a vulnerability

Do not open a public issue for a vulnerability. Use the private security-reporting channel on the canonical Forgejo repository. Include the affected version, operating system and architecture, a minimal reproduction, and expected impact; never include real credentials or private data.

## Release trust

Release archives are covered by SHA-256 checksums, and the checksum file is signed with Ed25519. The private seed exists only as the protected workspace-level Forgejo Actions secret `RELEASE_SIGNING_KEY`.

Public keys exist only in [Tmplt's central `release-keys/`](https://git2.riper.fr/ztec/tmplt/src/branch/main/release-keys). Active files use dated names; files ending in `-revoked.pub` are rejected. Every project release build fetches the latest Tmplt `main` and refuses a private key that does not match an active file. Install and self-update fetch that same central status, then verify the project-local release signature and archive checksum before replacement. No public key is committed here, copied into release assets, or embedded in executables.

The shell installer uses OpenSSL and normally verifies the signature before trusting the checksum; `elgatolight self-update` performs the Ed25519 check internally. A signature made by a revoked key produces an explicit refusal and recovery instruction. If OpenSSL support is unavailable, installer users may explicitly accept checksum-only installation interactively or with `--skip-signature-verification`; the installer warns that this detects corruption but does not authenticate the download and states when the checksum passes. Self-update has no signature bypass.

Because both project release data and the central Tmplt public keys come from Forgejo, protect immutable tags and repository integrity: this design does not defend a client against a Forgejo server that maliciously replaces both.

## Development safety

- Tests use loopback HTTP fixtures and temporary directories only.
- Downloads have protocol, redirect, response-size, archive-size, path, and file-count checks.
- Failed verification never replaces the installed executable.
- Container images run the released binary as an unprivileged user.
- Dependency and vulnerability updates must pass the same containerized suite as other changes.
