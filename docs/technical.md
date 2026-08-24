# Elgato USB Light technical reference

This document covers installation, configuration, Home Assistant, development,
releases, and the USB protocol. Start with the [README](../README.md) for the
shortest path to a working light.

## Overview and compatibility

The repository contains two parts:

- a Go CLI and daemon that communicate with Elgato Key Light Neo devices over
  Linux `hidraw`;
- a Python custom integration that exposes those lights natively in Home
  Assistant.

```text
Elgato USB light <-- hidraw --> elgatolight daemon == OAuth WebSocket ==> Home Assistant
```

USB light control and release builds support Linux only.

The controller supports power, brightness, color temperature, the two stored
presets, multiple lights, hotplug, and physical control updates. A USB serial is
used as the stable light ID and survives reconnects and `/dev/hidrawN` changes.
Hardware without a serial receives a visibly marked path-based ID.

Preset buttons I and II store brightness and temperature. Hold a button for
three seconds to save the current settings; the CLI and Home Assistant scenes
always recall the values currently stored in that slot.

## Installation and USB access

The root `install.sh` script selects the latest stable Forgejo release, falling
back to the latest non-draft pre-release. It validates Linux, detects the CPU,
verifies the archive against the published SHA-256 file, and asks for an
install directory. Defaults are `~/.local/bin` for a regular user and
`/usr/local/bin` for root.

Set `ELGATOLIGHT_INSTALL_DIR` for a noninteractive destination. The canonical
and GitHub-mirror installer commands are in the [README](../README.md#cli).

The release binary embeds its udev rule and systemd units. `setup` installs USB
access and optionally configures a daemon service:

| Command | Result |
| --- | --- |
| `elgatolight setup` | User service; starts at login and uses sudo only for the USB rule. |
| `sudo elgatolight setup` | System service; starts at boot and runs as root. |
| `elgatolight setup --scope none` | USB access only; no daemon service; uses sudo only for the USB rule. |

For Home Assistant, setup asks for its externally reachable URL, completes OAuth
authorization, and writes the service configuration. Every interactive value
has a flag equivalent:

```sh
elgatolight setup \
  --ha-url https://homeassistant.example.test \
  --credentials /home/example/.local/state/elgatolight/credentials.json \
  --yes
```

Noninteractive service setup requires `--ha-url` and `--yes`. CLI-only setup
requires `--scope none`. Global `--oauth-callback` and
`--insecure-skip-tls-verify` flags also apply during setup.

Setup is idempotent: it retains matching credentials, updates its managed udev
rule, and refreshes the selected service. It refuses to replace unrelated
administrator-owned files or links. Unplug and reconnect the light after the
first USB-rule installation.

### Updating the binary

```sh
elgatolight self-update
```

Self-update uses the same release selection and checksum verification as the
installer, verifies the downloaded binary's embedded version, and replaces the
executable atomically. It skips an exact version match unless `--force` is used.
Use sudo for a system-owned executable, then restart the daemon service.

## CLI and configuration

Run `elgatolight --help` or `elgatolight COMMAND --help` for the generated
reference. Common workflows are:

| Purpose | Commands |
| --- | --- |
| Inspect | `status`, `info`, `info --json`, `list` |
| Control | `on`, `off`, `toggle`, `brightness 30`, `temperature 4500` |
| Presets | `presets`, `preset 1`, `preset 2` |
| Monitor | `watch --interval 200ms`, `log --interval 200ms` |
| Home Assistant | `setup`, `pair`, `daemon`, `auth status`, `auth revoke` |
| Maintenance | `self-update`, `completion bash` |

Cobra persistent flags—including `--json`, `--light`, `--device`, `--timeout`,
`--config`, `--ha-url`, and `--credentials`—may appear before or after a
subcommand.

With one light, control commands select it automatically. Read commands include
all lights. With several lights, select a target by the stable ID shown by
`info`:

```sh
elgatolight --light A7BTB0000000ZZ brightness 30
```

`--device /dev/hidrawN` is available for diagnostics. `watch` continuously
redraws an in-place terminal tree. `log` is designed for automation and emits
one compact JSON object per line: an initial snapshot followed by state and
hotplug changes.

Viper resolves configuration in this order: command-line flags, environment,
config file, then defaults. Environment variables use the `ELGATOLIGHT_`
prefix, for example:

```sh
ELGATOLIGHT_JSON=true elgatolight info
ELGATOLIGHT_LIGHT=A7BTB0000000ZZ elgatolight brightness 30
ELGATOLIGHT_HOME_ASSISTANT_URL=https://homeassistant.example.test elgatolight daemon
```

The default config is `config.*` under `$XDG_CONFIG_HOME/elgatolight`, normally
`~/.config/elgatolight`. Viper-supported formats such as YAML, JSON, and TOML
are accepted. A compact YAML example:

```yaml
light: A7BTB0000000ZZ
timeout: 2s
json: false

watch:
  interval: 200ms
log:
  interval: 500ms

home_assistant:
  url: https://homeassistant.example.test
  credentials: /home/example/.local/state/elgatolight/credentials.json

daemon:
  poll_interval: 250ms
  reconcile_interval: 1s
  min_backoff: 1s
  max_backoff: 30s
```

Use `--config PATH` to select another file.

## Home Assistant

The daemon initiates the connection to Home Assistant, so it only needs outbound
HTTP(S) and WebSocket reachability to the configured URL. The custom integration
creates one device and light entity per stable USB ID, plus scenes `<light name>
I` and `<light name> II` for the physical presets. State, availability, hotplug,
and physical changes are pushed in real time.

### Install the integration

With HACS:

1. Under **HACS → Integrations → Custom repositories**, add
   `https://github.com/ztec/elgatocmd` as an **Integration**.
2. Download **Elgato USB Light Bridge** and restart Home Assistant.
3. Add **Elgato USB Light Bridge** under **Settings → Devices & services**.

For a manual installation, copy `custom_components/elgatolight` to
`/config/custom_components/elgatolight`, restart Home Assistant, and add the
integration.

### Pair and run the daemon

The recommended flow is:

```sh
elgatolight setup --ha-url https://homeassistant.example.test
```

For a foreground daemon, pair during its first interactive start:

```sh
elgatolight daemon --ha-url https://homeassistant.example.test
```

Or authorize separately before a noninteractive start:

```sh
elgatolight pair --ha-url https://homeassistant.example.test
elgatolight daemon
```

The authorization URL is printed and normally opened automatically. Pairing
uses `http://127.0.0.1:18443/oauth/callback`; a remote browser can reach it with
an SSH forward such as `ssh -L 18443:127.0.0.1:18443 HOST`. The configured Home
Assistant URL must be reachable by both the browser and daemon during pairing.

Credentials are stored with mode `0600` under
`$XDG_STATE_HOME/elgatolight/credentials.json`, normally
`~/.local/state/elgatolight/credentials.json`. Inspect or revoke them with:

```sh
elgatolight auth status
elgatolight auth revoke
```

Install a private certificate authority on the daemon host when required.
`--insecure-skip-tls-verify` is intended only for temporary diagnosis.

### Runtime behavior and logs

The daemon polls lights independently, watches for hotplug, and sends sequenced
events. It reconnects with exponential backoff and sends a complete snapshot
after each reconnect; a sequence gap triggers a full resync. Home Assistant
retains disconnected entities and marks them unavailable.

User-service status and logs:

```sh
systemctl --user status elgatolight.service
journalctl --user -u elgatolight.service -f
```

For a system service, omit `--user`. Action logs use `source=light` for physical
controls and `source=home_assistant` for Home Assistant commands. They include
the stable ID, requested or changed values, resulting state, lifecycle changes,
and errors; unchanged polls are not logged.

### Upgrade, move, and remove

- Upgrade the integration through HACS and restart Home Assistant. Manual
  installations must be copied again. Upgrade both sides together when moving
  from bridge protocol version 1 to version 2.
- Upgrade the daemon with `self-update`, restart its service, and rerun `setup`
  only when the service scope, configuration, or executable path changes.
- To move a daemon without changing entity identity, copy its credentials with
  mode `0600`. Alternatively revoke them on the old host and pair again.
- To remove the bridge, revoke authorization, delete the integration entry,
  remove it from HACS or `/config/custom_components`, and restart Home Assistant.

### Troubleshooting

- **Pairing:** install, restart, and add the Home Assistant integration before
  retrying `pair --ha-url URL`.
- **USB access:** rerun `setup --scope none`, reconnect the light, then check
  `info --json`.
- **Discovery:** inspect daemon logs and `auth status`; ensure proxies upgrade
  WebSockets on `/api/websocket`.
- **TLS:** install the private CA, using `--insecure-skip-tls-verify` only to
  isolate certificate problems.

## Development

The `Dockerfile` defines the Go and Python build/test environment used locally
and by CI. The Makefile prefers Podman and falls back to Docker; override it with
`CONTAINER_ENGINE=podman` or `CONTAINER_ENGINE=docker`.

| Target | Purpose |
| --- | --- |
| `make image` | Build the shared development image. |
| `make container-test` | Run Go vet, Go tests, Python compilation, and Home Assistant tests. |
| `make container-build` | Run all tests and produce `bin/elgatolight`. |
| `make box` | Create or update the Distrobox from the same image. |
| `make shell` | Enter the development Distrobox. |
| `make dev` | Run the live USB dashboard from source. |
| `make build` | Default workstation target: test and build in Distrobox. |

The first image build downloads dependencies and can take several minutes;
later builds use the container cache. Override the development command with, for
example, `make dev DEV_ARGS='info --json'`.

## Releases and CI

Every non-empty Git tag is accepted and embedded in binaries exactly as written;
archive names use a filesystem-safe form. Reproduce a release locally with:

```sh
make release VERSION=v0.1
```

For characters that should not pass through Make or shell interpolation, use a
file:

```sh
printf '%s' 'release/0.1+$channel' > .elgatolight-release-version
make release VERSION_FILE=.elgatolight-release-version
```

Release output under `dist/` contains checksummed Linux binaries for amd64,
arm64, and armv7.

`.forgejo/workflows/test.yaml` runs `make container-test` on branch pushes.
Tag pushes run `.forgejo/workflows/release.yaml`, test every target, and publish
the exact tag through the `ubuntu-24.04` runner. The workflow publishes a
pre-release with explicit title and notes, hides generated source archives, and
refreshes tested assets on reruns without replacing the Git tag or its annotated
message.

Without a GitHub Release, HACS tracks the GitHub mirror's default branch by
commit. A GitHub Release makes its tag the named HACS version; a mirrored tag or
Forgejo release alone does not create one.

## Protocol and security

The Key Light Neo exposes one HID interface with 512-byte reports. Report ID
`0x02` carries a frame index, total frame count, little-endian body length, and
up to 505 payload bytes. The payload uses `GET <path>` and `PUT <path> <json>`;
state is read from `GET /elgato/lights`.

Bridge protocol version 2 uses authenticated custom WebSocket commands under the
`elgatolight/` namespace. The daemon exchanges its revocable OAuth refresh token
for short-lived access tokens and retains `elgatolight/subscribe` as the
server-to-daemon command channel. Commands contain either a partial light update
or one preset number. Bridge commands require an administrator token; normal
Home Assistant entity access follows Home Assistant permissions.

The USB implementation was cross-checked against the independent
[Key Light Neo USB protocol analysis](https://zameermanji.com/blog/2026/3/4/elgato-key-light-neo-usb-protocol/).
Preset behavior is documented in Elgato's
[Key Light Neo manual](https://www.elgato.com/us/en/s/user-manual/key-light-neo).
