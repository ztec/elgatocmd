# Elgato Key Light Neo USB controller

`elgatolight` controls one or more Elgato Key Light Neo devices directly over
USB. It supports power, brightness, color temperature, stored presets, physical
control updates, and stable device IDs.

The USB controller runs on Linux.

## Command-line usage

Download the Linux archive for your CPU from the
[latest release](https://git2.riper.fr/ztec/elgatocmd/releases/latest), extract
it, then run its self-installer:

```sh
sudo ./elgatolight setup
```

Choose the `cli` scope for command-line use. Setup installs the binary in
the login user's `~/.local/bin` by default, installs USB permissions, and can be
run again safely after an upgrade. Unplug and reconnect the light once after
the first setup.

Common commands:

```sh
elgatolight info
elgatolight on
elgatolight off
elgatolight brightness 30
elgatolight temperature 4500
elgatolight presets
elgatolight preset 1
elgatolight watch
elgatolight log
```

With one light, it is selected automatically. With multiple lights, `info`
shows every stable ID; select one with `--light ID`. `watch` updates a live tree
in place, while `log` emits an initial state followed by JSON Lines changes.

Run `elgatolight --help` for all commands. Add `~/.local/bin` to your `PATH`,
invoke `~/.local/bin/elgatolight` directly, or choose another directory during
setup.

## Home Assistant usage

The included Home Assistant integration receives live light state from the
daemon and sends light commands back through the same connection. Setup needs
the Home Assistant URL.

First install the integration:

1. Open **HACS → Integrations → Custom repositories**.
2. Add `https://github.com/ztec/elgatocmd` as an **Integration** repository.
3. Download **Elgato USB Light Bridge** and restart Home Assistant.
4. Open **Settings → Devices & services → Add integration** and add
   **Elgato USB Light Bridge**.

Then download the Linux archive from the
[latest release](https://git2.riper.fr/ztec/elgatocmd/releases/latest), extract
it on the computer connected to the light, and run:

```sh
sudo ./elgatolight setup
```

Choose `user` to start the daemon whenever that user logs in, or `system` to
start it at boot. Setup asks for the externally reachable Home Assistant/proxy
URL, opens the OAuth authorization flow, installs the binary and USB rule, and
enables and starts the systemd service. It is idempotent, so the same command
also upgrades an existing installation.

For unattended setup, provide every choice as flags:

```sh
sudo ./elgatolight setup \
  --scope user \
  --target-user alice \
  --ha-url https://homeassistant.example.test \
  --install-dir /home/alice/.local/bin \
  --yes
```

Setup reuses an existing matching authorization. The first setup displays an
OAuth URL to open in a browser.

## Documentation

See [Technical documentation](docs/technical.md) for release verification,
all setup flags, manual Home Assistant installation, configuration, upgrades,
troubleshooting, containerized development, CI/release automation, security,
and protocol details.
