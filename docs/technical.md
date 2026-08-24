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
apply them without overwriting either slot. Saving a new value remains a
physical operation: adjust the light, then hold preset button I or II for three
seconds. Recalling a preset changes ordinary light state, so the CLI monitor and
Home Assistant both see it.

## USB permission

Fedora normally exposes the light as root-only. Install the included,
device-specific udev rule once:

```sh
make setup
```

Run Make as your normal user. The target uses `sudo` only to install the rule
and reload udev; running the whole Make process with `sudo` can leave root-owned
build files behind. If already in a root shell, `make setup` automatically
omits `sudo`.

Unplug and reconnect the light after setup. The rule uses systemd-logind's
`uaccess` tag, so only the active local desktop user receives access.

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
Hardware without a serial falls back to a visibly marked path-based ID. The
`--device /dev/hidrawN` option remains available for diagnostics.

`watch` requires a terminal. It rediscovers lights on every polling cycle and
rewrites its existing tree rows using ANSI cursor controls, so physical changes
do not add scrolling output. Use `log` for automation or redirected output. It
emits one compact JSON object per line, beginning with an `initial` event and
then a `change` event whenever state or the connected-light set changes:

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

The bridge is outbound-only. Home Assistant never needs to resolve or connect
to the daemon. The daemon can move between hosts or networks as long as it can
reach the configured Home Assistant URL. There is no MQTT broker and no
permanent inbound daemon port. The only temporary listener is the loopback OAuth
callback used while pairing.

The custom integration creates one native Home Assistant device and light
entity per stable USB serial. It supports power, brightness, color temperature,
availability, hotplug, physical control changes, and dynamic addition of more
lights. State changes are pushed; Home Assistant does not poll the daemon.

### HACS installation

1. Open **HACS → Integrations**.
2. Open **Custom repositories**, add
   `https://github.com/ztec/elgatocmd`, and select **Integration**.
3. Download **Elgato USB Light Bridge** and restart Home Assistant.
4. Open **Settings → Devices & services → Add integration**, search for
   **Elgato USB Light Bridge**, and submit its one-click form.

There is no daemon address, webhook, or MQTT configuration in Home Assistant.
Until the first release is published, HACS installs from the repository's
default branch.

### Manual installation

1. Clone `https://github.com/ztec/elgatocmd.git`, or update an existing clone.
2. Copy `custom_components/elgatolight` into the Home Assistant persistent
   configuration volume as `/config/custom_components/elgatolight`.
3. Restart Home Assistant. For Kubernetes, restart the Home Assistant pod using
   the deployment or StatefulSet that owns `/config`; do not copy into an
   ephemeral container filesystem.
4. Add **Elgato USB Light Bridge** under **Settings → Devices & services**.

### Pairing and daemon operation

Set up USB access and build the binary on the light host:

```sh
make setup
# Unplug and reconnect the light once after setup.
make build
```

The simplest first start supplies only the Home Assistant URL:

```sh
bin/elgatolight daemon --ha-url https://homeassistant.example.test
```

If credentials do not exist and the command has an interactive terminal, it
opens Home Assistant in a browser, asks the user to authorize, verifies that the
custom integration is enabled, saves the refresh token, and starts the bridge.
On subsequent starts the URL is read from saved credentials:

```sh
bin/elgatolight daemon
```

To authorize separately before starting a service or non-interactive process:

```sh
bin/elgatolight pair --ha-url https://homeassistant.example.test
bin/elgatolight daemon
```

The authorization URL is always printed, even if opening the browser fails.
Pairing listens on `http://127.0.0.1:18443/oauth/callback` by default. The
browser must run on the daemon host or reach that loopback listener through an
SSH local forward such as `ssh -L 18443:127.0.0.1:18443 HOST`.

The configured Home Assistant URL must be reachable by both the daemon and the
browser during authorization. For Home Assistant in Kubernetes, use its normal
LAN or HTTPS ingress URL rather than a cluster-only service name. Only outbound
HTTP(S)/WebSocket access to that URL is required.

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
local file. Re-pairing preserves the daemon instance ID so retained entities do
not change identity.

For a private Home Assistant installation with an untrusted TLS certificate,
`--insecure-skip-tls-verify` is available as an explicit last resort. Installing
the correct CA certificate is safer.

### Resilience

The daemon polls each physical light independently (250 ms by default), watches
for USB hotplug, and maintains a sequenced event stream. It reconnects to Home
Assistant with exponential backoff and sends a complete authoritative snapshot
after every reconnect. A sequence gap causes a full resync rather than silently
accepting stale state. If the daemon or a USB light disconnects, the retained
Home Assistant entity becomes unavailable instead of disappearing.

### Upgrade, move, and removal

To upgrade the Home Assistant side, download the update in HACS and restart
Home Assistant. For manual installations, update the clone and copy the custom
component into the persistent configuration volume again before restarting.
Rebuild/redeploy the Go binary separately and restart the daemon.

The credential file is sufficient to move the daemon without a Home Assistant
address change. Treat it as a secret and preserve mode `0600` when copying it.
Alternatively, run `auth revoke` on the old host and pair again on the new host.

To remove the bridge completely:

1. Stop the daemon and run `bin/elgatolight auth revoke` while Home Assistant is
   reachable.
2. Delete **Elgato USB Light Bridge** under **Settings → Devices & services**.
3. Remove the integration in HACS, or delete the manually copied integration
   directory, and restart Home Assistant.

The final systemd unit/installer is tracked in
[RIP-297](https://linear.app/riper/issue/RIP-297/prepare-the-systemd-daemon-setup-script).
Until then, run the daemon under an existing process supervisor or in a
terminal. Non-interactive supervisors must use credentials created by `pair`
beforehand.

### Troubleshooting

- **Integration is not ready during pairing:** install/restart Home Assistant,
  add the integration in Devices & services, then run `pair` again.
- **Authorization was revoked:** run `pair --ha-url URL` interactively.
- **No USB light or permission denied:** run `make setup`, unplug/reconnect the
  light, and verify `bin/elgatolight info --json`.
- **No entity appears:** inspect daemon logs and `auth status`; the HA URL must
  support WebSocket upgrades at `/api/websocket` through its ingress.
- **Untrusted certificate:** install the private CA on the daemon host. Use
  `--insecure-skip-tls-verify` only for temporary diagnosis.

## Development workflow

All build and test dependencies are installed inside the Distrobox. The host
only needs Distrobox/Podman and the host-level udev rule.

```sh
make box    # create elgatolight-dev from distrobox.ini
make shell  # create it if needed, then enter it
make dev    # run the live USB dashboard from source in the box
make build  # run Go + Home Assistant tests and build bin/elgatolight
```

`make build` is the default target. It creates an ignored `.venv` from inside
the box for the Home Assistant test runtime. The box contains Go, Make, Git,
Python, and the native compiler/header packages needed by those tests.
Distrobox shares the host's `/dev` and supplementary groups, so the udev
permission is also effective inside the box.

To run another development command, override `DEV_ARGS`:

```sh
make dev DEV_ARGS='daemon --ha-url https://homeassistant.example.test'
make dev DEV_ARGS='info --json'
```

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
