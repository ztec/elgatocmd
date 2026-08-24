# Elgato USB Light technical documentation

This document contains the detailed operational, development, and protocol
information for `elgatolight`. For basic setup, start with the
[README](../README.md).

## Supported operations

- Read power, brightness, and color temperature.
- Turn a light on and off, toggle it, or apply an atomic multi-field update.
- Set brightness and color temperature.
- List and apply the two presets stored in the light.
- Discover and independently manage multiple USB lights.
- Watch all lights in an in-place terminal dashboard.
- Log initial state, physical changes, and hotplug changes as JSON Lines.
- Push state, physical control changes, and availability to Home Assistant.
- Receive Home Assistant light commands over a daemon-initiated connection.

The two hardware preset buttons store brightness/temperature combinations. The
USB settings endpoint exposes both slots as favourites, so the CLI can list and
apply each one independently. Save a new value by adjusting the light, then
holding preset button I or II for three seconds. Recalling a preset changes
ordinary light state, so the CLI monitor and Home Assistant both see it.

## Self-installer and USB permission

Download the Linux archive for your CPU from the
[latest release](https://git2.riper.fr/ztec/elgatocmd/releases/latest). The
release binary embeds its device-specific udev rule and systemd templates.
The download installer installs the binary. Setup then configures USB access
and an optional service:

```sh
./elgatolight setup --scope none
```

The root `install.sh` script automates the download. It queries the Forgejo API
for the latest stable release and falls back to the latest non-draft
pre-release. It selects the published archive matching the detected operating
system and architecture, verifies it against the release checksum file, and
installs the binary in the interactively selected directory. The default is
`$HOME/.local/bin` for a regular user and `/usr/local/bin` for root.

Set `ELGATOLIGHT_INSTALL_DIR` to choose the directory without a prompt. Set
`ELGATOLIGHT_RELEASE_API` to use another compatible Forgejo release API.

Setup infers the service mode from its privileges:

- Run `elgatolight setup` as a regular user to pair with Home Assistant and
  enable a service under `~/.config/systemd/user`; it starts at login. Setup
  invokes sudo only for the embedded udev rule.
- Run `sudo elgatolight setup` to pair as root and enable a system service that
  starts when the computer boots.
- Run `elgatolight setup --scope none` to install USB access for command-line
  use without a daemon service.

User and system services execute the binary that invoked setup. Install the
binary in a durable path; `/usr/local/bin` is the recommended location for a
system service. Setup keeps an existing matching OAuth authorization, reloads
udev when its managed rule changes, and queues a non-blocking refresh of the
selected service. The user service starts independently of a user-level
`network-online.target`; the daemon's reconnect loop handles network readiness.
Destination validation protects existing administrator-owned units, rules, and
links.

Every setup value has a flag equivalent:

```sh
./elgatolight setup \
  --ha-url https://homeassistant.example.test \
  --credentials /home/example/.local/state/elgatolight/credentials.json \
  --yes
```

Noninteractive setup infers user or system scope, requires `--yes`, and requires
`--ha-url` for a service. Use an explicit `--scope none` for CLI-only setup. The
existing persistent flags
`--oauth-callback` and `--insecure-skip-tls-verify` also apply to OAuth during
setup. Run `elgatolight setup --help` for the generated reference.

Unplug and reconnect the light after the first setup. The rule uses
systemd-logind's `uaccess` tag to grant the active local desktop user access. A
system service runs as root and accesses the device directly.

## Command-line reference

```sh
bin/elgatolight status
bin/elgatolight info
bin/elgatolight info --json
bin/elgatolight list  # alias for info
bin/elgatolight on
bin/elgatolight off
bin/elgatolight toggle
bin/elgatolight brightness 30
bin/elgatolight temperature 4500
bin/elgatolight presets
bin/elgatolight preset 1
bin/elgatolight watch --interval 200ms
bin/elgatolight log --interval 200ms
bin/elgatolight --json status
bin/elgatolight status --json
```

The command tree uses Cobra, so persistent options such as `--json`, `--light`,
`--device`, `--timeout`, `--config`, `--ha-url`, and `--credentials` may appear
before or after a subcommand. Run `bin/elgatolight --help` or
`bin/elgatolight COMMAND --help` for generated help. Cobra also provides shell
completion generation, for example `bin/elgatolight completion bash`.

The program auto-detects every light by USB vendor/product ID. Its status output
is a tree and includes the stable USB/HID serial:

```text
Lights
├── Light 1 [A7BTB4251316ZB] - off - brightness 034% - temperature 3000K
└── Light 2 [ANOTHER-SERIAL] - on - brightness 080% - temperature 4505K
```

With one connected light, commands select it automatically. Read commands,
`watch`, and `log` include all connected lights. If multiple lights are
connected, a state-changing command requires the stable ID:

```sh
bin/elgatolight --light A7BTB4251316ZB brightness 30
```

The serial survives unplugging, reconnecting, and changes to `/dev/hidrawN`.
Hardware that exposes a USB serial receives a stable serial-based ID. Other
hardware receives a visibly marked path-based ID. The `--device /dev/hidrawN`
option is available for diagnostics.

`watch` requires a terminal. It rediscovers lights on every polling cycle and
rewrites its existing tree rows using ANSI cursor controls, keeping the display
in place as physical state changes. Use `log` for automation or redirected
output. It emits one compact JSON object per line, beginning with an `initial`
event and then a `change` event whenever state or the connected-light set
changes:

```json
{"time":"2026-08-24T15:55:49Z","event":"initial","lights":[{"id":"A7BTB4251316ZB","stableId":true,"device":"/dev/hidraw13","on":false,"brightness":40,"temperature":3300,"temperatureMired":303}]}
```

## Configuration

Viper merges configuration in this order: command-line flags, environment
variables, config file, then defaults. Environment variables use the
`ELGATOLIGHT_` prefix:

```sh
ELGATOLIGHT_JSON=true bin/elgatolight info
ELGATOLIGHT_LIGHT=A7BTB4251316ZB bin/elgatolight brightness 30
ELGATOLIGHT_WATCH_INTERVAL=500ms bin/elgatolight watch
ELGATOLIGHT_HOME_ASSISTANT_URL=https://homeassistant.example.test bin/elgatolight daemon
```

By default, the program looks for `config.*` under
`$XDG_CONFIG_HOME/elgatolight` (normally `~/.config/elgatolight`). YAML, JSON,
TOML, and the other Viper-supported formats are accepted. Example:

```yaml
light: A7BTB4251316ZB
timeout: 2s
json: false

watch:
  interval: 200ms
log:
  interval: 500ms

home_assistant:
  url: https://homeassistant.example.test
  credentials: /home/example/.local/state/elgatolight/credentials.json
  oauth_callback: http://127.0.0.1:18443/oauth/callback
  insecure_skip_tls_verify: false

daemon:
  poll_interval: 250ms
  reconcile_interval: 1s
  call_timeout: 10s
  min_backoff: 1s
  max_backoff: 30s
```

Use `--config PATH` to select a different file. The option can also follow the
command: `bin/elgatolight info --config ./lights.yaml --json`.

## Home Assistant architecture

```text
Elgato USB light <-- hidraw --> elgatolight daemon == OAuth WebSocket ==> Home Assistant
```

The daemon establishes an authenticated WebSocket connection to the configured
Home Assistant URL. It can move between hosts or networks while retaining that
outbound reachability. Pairing uses a temporary loopback OAuth callback.

The custom integration creates one native Home Assistant device and light
entity per stable USB serial. It supports power, brightness, color temperature,
availability, hotplug, physical control changes, and dynamic addition of more
lights. State changes are pushed to Home Assistant in real time.

### HACS installation

1. Open **HACS → Integrations**.
2. Open **Custom repositories**, add
   `https://github.com/ztec/elgatocmd`, and select **Integration**.
3. Download **Elgato USB Light Bridge** and restart Home Assistant.
4. Open **Settings → Devices & services → Add integration**, search for
   **Elgato USB Light Bridge**, and submit its one-click form.

The Home Assistant integration uses a one-click configuration entry. The daemon
receives its Home Assistant URL during `elgatolight setup`. HACS installs the
integration from the repository's published version or default branch.

### Manual installation

1. Clone `https://github.com/ztec/elgatocmd.git`, or update an existing clone.
2. Copy `custom_components/elgatolight` into the Home Assistant persistent
   configuration volume as `/config/custom_components/elgatolight`.
3. Restart Home Assistant. For Kubernetes, copy into the persistent `/config`
   volume and restart the pod through the deployment or StatefulSet that owns
   that volume.
4. Add **Elgato USB Light Bridge** under **Settings → Devices & services**.

### Pairing and daemon operation

The release self-installer is the simplest daemon setup. After installing the
Home Assistant integration, run:

```sh
./elgatolight setup
```

Enter the externally reachable Home Assistant URL. Setup completes OAuth
pairing, installs the user service, enables it for login, and starts it
immediately. Run `sudo ./elgatolight setup` instead for a boot-time system
service. To upgrade, run the download installer again, then rerun setup to
preserve configuration and restart the service with the new binary.

For a foreground or manually supervised daemon, the first start supplies the
Home Assistant URL:

```sh
bin/elgatolight daemon --ha-url https://homeassistant.example.test
```

On first start with an interactive terminal, the command opens Home Assistant in
a browser, asks the user to authorize, verifies that the custom integration is
enabled, saves the refresh token, and starts the bridge. On subsequent starts
the URL is read from saved credentials:

```sh
bin/elgatolight daemon
```

To authorize separately before starting a service or non-interactive process:

```sh
bin/elgatolight pair --ha-url https://homeassistant.example.test
bin/elgatolight daemon
```

The authorization URL is always printed so it can also be opened manually.
Pairing listens on `http://127.0.0.1:18443/oauth/callback` by default. The
browser must run on the daemon host or reach that loopback listener through an
SSH local forward such as `ssh -L 18443:127.0.0.1:18443 HOST`.

The configured Home Assistant URL must be reachable by both the daemon and the
browser during authorization. For Home Assistant in Kubernetes, use a LAN or
HTTPS ingress URL reachable from both. The daemon uses outbound HTTP(S) and
WebSocket access to that URL.

Authorization metadata can be inspected or revoked:

```sh
bin/elgatolight auth status
bin/elgatolight auth status --json
bin/elgatolight auth revoke
```

The refresh token is stored by default at
`$XDG_STATE_HOME/elgatolight/credentials.json`, normally
`~/.local/state/elgatolight/credentials.json`, with owner-only mode `0600`.
`auth revoke` invalidates the refresh token in Home Assistant and deletes the
local file. Re-pairing preserves the daemon instance ID and retains the same
entity identity.

For a Home Assistant installation using a private certificate authority,
install that CA on the daemon host. The `--insecure-skip-tls-verify` flag is
available for temporary certificate diagnosis.

### Resilience

The daemon polls each physical light independently (250 ms by default), watches
for USB hotplug, and maintains a sequenced event stream. It reconnects to Home
Assistant with exponential backoff and sends a complete authoritative snapshot
after every reconnect. A sequence gap causes a full authoritative resync. When
the daemon or a USB light disconnects, Home Assistant retains the entity and
marks it unavailable.

### Upgrade, move, and removal

To upgrade the Home Assistant side, download the update in HACS and restart
Home Assistant. For manual installations, update the clone and copy the custom
component into the persistent configuration volume again before restarting.
To upgrade the daemon, run the download installer again and rerun
`elgatolight setup` with the same user or system mode; matching credentials are
preserved and the service is restarted.

The credential file supports moving the daemon while keeping the same Home
Assistant address. Treat it as a secret and preserve mode `0600` when copying
it. Alternatively, run `auth revoke` on the old host and pair again on the new
host.

To remove the bridge completely:

1. Stop the daemon and run `bin/elgatolight auth revoke` while Home Assistant is
   reachable.
2. Delete **Elgato USB Light Bridge** under **Settings → Devices & services**.
3. Remove the integration in HACS, or delete the manually copied integration
   directory, and restart Home Assistant.

Non-interactive supervisors must use credentials created by `pair` or `setup`
beforehand.

### Troubleshooting

- **Pairing readiness:** install and restart the Home Assistant integration,
  add it under Devices & services, then run `pair` again.
- **Authorization renewal:** run `pair --ha-url URL` interactively.
- **USB access:** rerun `elgatolight setup --scope none`, unplug and reconnect the
  light, then verify it with `elgatolight info --json`.
- **Entity discovery:** inspect daemon logs and `auth status`, and configure the
  HA ingress for WebSocket upgrades at `/api/websocket`.
- **Private certificates:** install the private CA on the daemon host. Use
  `--insecure-skip-tls-verify` for temporary diagnosis.

## Development and container workflow

The `Dockerfile` is the single definition of the Go and Python build/test
environment. The Makefile prefers Podman and uses Docker as its fallback; set
`CONTAINER_ENGINE=docker` or `CONTAINER_ENGINE=podman` to choose explicitly.
Automation requires either engine.

Direct container targets, including the ones used by Forgejo, are:

```sh
make image           # build localhost/elgatolight-build:dev
make container-test  # build the image, then run every Go and HA test
make container-build # run every test, then produce bin/elgatolight
```

On a development workstation, Distrobox uses that same locally built image:

```sh
make box    # build the image and create elgatolight-dev from it
make shell  # create it if needed, then enter it
make dev    # run the live USB dashboard from source in the box
make build  # run all tests and build bin/elgatolight in the box
```

`make build` is the default developer target. Python dependencies and Go module
downloads are cached in the image. Direct container targets copy the checked-out
source into the image and copy completed binaries or release assets back to the
working directory, which also supports containerized CI runners. Each build
prints its image, container-entry, static-analysis, Go-test, Home Assistant-test,
and binary stages. The first image build can take several minutes; subsequent
builds use the engine cache. Distrobox shares the host's `/dev` and supplementary
groups, so the udev permission is also effective inside the box.

The Makefile gives Distrobox an isolated session-bus address while it enters the
container, then restores the desktop address for the command. This keeps
host-spawn integration responsive on desktop sessions. Podman uses `cgroupfs`
and `runc` when available for a self-contained runtime. Docker receives the
current UID/GID for mounted output files.

To run another development command, override `DEV_ARGS`:

```sh
make dev DEV_ARGS='daemon --ha-url https://homeassistant.example.test'
make dev DEV_ARGS='info --json'
```

## Releases and Forgejo Actions

Every non-empty Git tag is accepted as the release version. The exact tag is
injected into every binary at link time, so tags such as `v0.1`, `0.1-rc1`, and
`release/0.1+build` are preserved by `elgatolight --version`. Archive filenames
use a filesystem-safe form of the tag. Locally, the complete release can be
reproduced with:

```sh
make release VERSION=v0.1
```

Automation can pass an opaque tag through a file, which preserves every
character independently of Make and shell interpolation:

```sh
printf '%s' 'release/0.1+$channel' > .elgatolight-release-version
make release VERSION_FILE=.elgatolight-release-version
```

The command first runs every Go and Home Assistant test inside the Dockerfile
image, then writes archives and SHA-256 checksums under `dist/` for:

- Linux: amd64, arm64, and armv7.
- macOS (`darwin`): amd64 and arm64.

Linux artifacts include the USB transport and self-installer. macOS artifacts
provide the portable configuration, help, authentication, and version surface.
USB light access remains Linux-only.

`.forgejo/workflows/test.yaml` runs `make container-test` for branch pushes.
Tag pushes run `.forgejo/workflows/release.yaml`, which accepts every existing
Git tag, runs the same tests, builds all targets, and uploads `dist/` to the
Forgejo release whose tag and title preserve the exact value. Both jobs use the
Forgejo runner label `ubuntu-24.04`, matching the deployment runner used by the
referenced infrastructure project. Release publishing uses override mode, so a
rerun refreshes the release and its assets for the same tag. Releases are marked
as pre-releases and their automatically generated source archives are hidden in
favor of the tested cross-platform artifacts. Override mode leaves the existing
Git tag unchanged, preserving any annotated tag message created from the release
title and content. The workflow resolves and passes the selected tag's commit SHA
explicitly, including for manual dispatches, so the publisher never replaces that
tag because of the workflow branch's SHA.

## Protocol notes

The Key Light Neo has one HID interface with 512-byte input and output reports.
Report ID `0x02` contains an index, total frame count, little-endian body length,
and up to 505 bytes of payload. The application payload uses `GET <path>` and
`PUT <path> <json>` messages. Status changes made on the light are observable
through `GET /elgato/lights`; the daemon and monitor commands poll this endpoint.
Linux exposes the hardware serial as `HID_UNIQ`, which is used as the stable
device and Home Assistant entity ID.

The Home Assistant side exposes authenticated custom WebSocket commands under
the `elgatolight/` namespace. The daemon authenticates with a short-lived access
token obtained from a revocable OAuth refresh token, then retains the
`elgatolight/subscribe` command as the server-to-daemon command channel. These
bridge commands require an administrator token; ordinary entity access remains
subject to Home Assistant's normal permissions.

The USB implementation was cross-checked against the independent
[Key Light Neo USB protocol analysis](https://zameermanji.com/blog/2026/3/4/elgato-key-light-neo-usb-protocol/).
The stored preset buttons are also documented in Elgato's
[Key Light Neo manual](https://www.elgato.com/us/en/s/user-manual/key-light-neo).
