# Elgato Key Light Neo USB controller

`elgatolight` controls one or more Elgato Key Light Neo devices (`0fd9:00a0`)
directly through Linux `hidraw`. It does not require Wi-Fi, Elgato Control
Center, MQTT, `libusb`, or a C HID library.

It can be used as a standalone CLI or as an outbound bridge to native Home
Assistant light entities. The Home Assistant integration is installable through
HACS once this project has a published Git repository.

Supported operations:

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

## Home Assistant bridge

The bridge is deliberately outbound-only:

```text
Elgato USB light <-- hidraw --> elgatolight daemon == OAuth WebSocket ==> Home Assistant
```

Home Assistant never needs to resolve or connect to the daemon. The daemon can
move between hosts or networks as long as it can reach the configured Home
Assistant URL. There is no MQTT broker and no permanent inbound daemon port.
The only temporary listener is the loopback OAuth callback used while pairing.

The custom integration creates one native Home Assistant device and light
entity per stable USB serial. It supports power, brightness, color temperature,
availability, hotplug, physical control changes, and dynamic addition of more
lights. State changes are pushed; Home Assistant does not poll the daemon.

### Repository URL placeholder

The future repository URL is currently represented by:

```text
https://REPOSITORY_URL_PLACEHOLDER.invalid/elgatolight
```

It appears in the instructions below and in the custom integration manifest.
[RIP-311](https://linear.app/riper/issue/RIP-311/replace-repository-url-placeholders-after-publishing-the-git)
tracks replacing the URL and code-owner placeholders after the repository is
published. HACS cannot install from the placeholder; use the manual installation
path until RIP-311 is completed.

### 1. Install the Home Assistant integration

Temporary manual installation:

1. Copy the repository directory `custom_components/elgatolight` into the Home
   Assistant persistent configuration volume as
   `/config/custom_components/elgatolight`.
2. Restart Home Assistant. For a Kubernetes deployment, restart the Home
   Assistant pod using the deployment/StatefulSet that owns the `/config`
   volume; do not copy into an ephemeral container filesystem.

HACS installation after the repository is published:

1. Open HACS in Home Assistant and select **Integrations**.
2. Open the menu, select **Custom repositories**, and add
   `https://REPOSITORY_URL_PLACEHOLDER.invalid/elgatolight` with category
   **Integration**.
3. Select **Elgato USB Light Bridge**, download it, and restart Home Assistant.

After either installation method, open **Settings → Devices & services → Add
integration**, search for **Elgato USB Light Bridge**, and submit its one-click
form. There is no daemon address, webhook, or MQTT configuration in Home
Assistant.

### 2. Build and authorize the daemon

Set up USB access and build the binary on the computer connected to the light:

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
opens Home Assistant in a browser, asks you to authorize with an administrator
account, verifies that the custom integration is enabled, saves the refresh
token, and starts the bridge. On subsequent starts the URL is read from the
saved credentials, so this is sufficient:

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
browser must therefore run on the daemon host, or reach that loopback listener
through an SSH local forward such as `ssh -L 18443:127.0.0.1:18443 HOST`.

The configured Home Assistant URL must be reachable by both the daemon and the
browser during authorization. For a manually deployed Home Assistant in
Kubernetes, use its normal LAN or HTTPS ingress URL rather than a cluster-only
service name. Only outbound HTTP(S)/WebSocket access to that URL is required.

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

### Bridge resilience

The daemon polls each physical light independently (250 ms by default), watches
for USB hotplug, and maintains a sequenced event stream. It reconnects to Home
Assistant with exponential backoff and sends a complete authoritative snapshot
after every reconnect. A sequence gap causes a full resync rather than silently
accepting stale state. If the daemon or a USB light disconnects, the retained
Home Assistant entity becomes unavailable instead of disappearing.

### Upgrade, move, and removal

To upgrade the Home Assistant side, download the update in HACS and restart
Home Assistant. Rebuild/redeploy the Go binary separately, then restart the
daemon; protocol incompatibilities fail with an actionable error instead of
silently applying state.

The credential file is sufficient to move the daemon without any Home
Assistant address change. Treat it as a secret and preserve mode `0600` when
copying it. Alternatively, run `auth revoke` on the old host and pair again on
the new host.

To remove the bridge completely:

1. Stop the daemon and run `bin/elgatolight auth revoke` while Home Assistant is
   still reachable.
2. Delete **Elgato USB Light Bridge** under **Settings → Devices & services**.
3. Remove the integration in HACS (or delete the manually copied directory) and
   restart Home Assistant.

The final systemd unit/installer is intentionally tracked separately in
[RIP-297](https://linear.app/riper/issue/RIP-297/prepare-the-systemd-daemon-setup-script).
Until then, run `elgatolight daemon` under your existing process supervisor or
in a terminal. Non-interactive supervisors must use credentials created by
`pair` beforehand.

### Troubleshooting

- **Integration is not ready during pairing:** install/restart Home Assistant,
  add the integration in Devices & services, then run `pair` again.
- **Authorization was revoked:** the daemon exits with a re-pair instruction;
  run `pair --ha-url URL` interactively.
- **No USB light or permission denied:** run `make setup`, unplug/reconnect the
  light, and verify `bin/elgatolight info --json` before starting the daemon.
- **No entity appears:** inspect daemon logs and `auth status`; the HA URL must
  support WebSocket upgrades at `/api/websocket` through its ingress.
- **Untrusted certificate:** install the private CA on the daemon host. Use
  `--insecure-skip-tls-verify` only for temporary diagnosis.

## USB permission

Fedora normally exposes this device as root-only. Install the included,
device-specific udev rule once:

```sh
make setup
```

Run Make as your normal user. The `setup` target uses `sudo` only for installing
the rule and reloading udev; running the whole Make process with `sudo` can
leave root-owned build files behind. If already in a root shell, `make setup`
automatically omits `sudo`.

Then unplug and reconnect the light. The rule uses systemd-logind's `uaccess`
tag, so only the active local desktop user receives access.

## Distrobox workflow

All build and test dependencies are installed inside the Distrobox. The host
only needs Distrobox/Podman and the one host-level udev rule above.

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

## Standalone CLI usage

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

The program auto-detects every light by VID/PID. Its status output is a tree and
includes the stable USB/HID serial:

```text
Lights
├── Light 1 [A7BTB4251316ZB] - off - brightness 034% - temperature 3000K
└── Light 2 [ANOTHER-SERIAL] - on - brightness 080% - temperature 4505K
```

With one connected light, commands remain short: `elgatolight on` and all other
commands select it automatically. Read commands, `watch`, and `log` include all
connected lights. If multiple lights are connected, a state-changing command
requires the stable ID:

```sh
bin/elgatolight --light A7BTB4251316ZB brightness 30
```

The serial survives unplugging, reconnecting, and changes to `/dev/hidrawN`.
Hardware without a serial falls back to a visibly marked path-based ID. The
`--device /dev/hidrawN` option remains available for diagnostics.

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
TOML, and the other Viper-supported formats are accepted. An example
`config.yaml` is:

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

`watch` requires a terminal. It rediscovers lights on every polling cycle and
rewrites its existing tree rows using ANSI cursor controls, so physical changes
do not add scrolling output. Use `log` for automation or redirected output. It
always emits one compact JSON object per line, beginning with an `initial` event
and then a `change` event whenever state or the connected-light set changes:

```json
{"time":"2026-08-24T15:55:49Z","event":"initial","lights":[{"id":"A7BTB4251316ZB","stableId":true,"device":"/dev/hidraw13","on":false,"brightness":40,"temperature":3300,"temperatureMired":303}]}
```

## Protocol notes

The device has one HID interface with 512-byte input and output reports. Report
ID `0x02` contains an index, total frame count, little-endian body length, and
up to 505 bytes of payload. The application payload uses `GET <path>` and
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
The behavior of the two stored preset buttons is described in Elgato's
[Key Light Neo manual](https://www.elgato.com/us/en/s/user-manual/key-light-neo).
