# Elgato Key Light Neo USB controller

`elgatolight` controls one or more Elgato Key Light Neo devices directly over
USB. It supports power, brightness, color temperature, stored presets, physical
control updates, and stable device IDs.

The USB controller runs on Linux.

## Command-line usage

Install the latest release. If only pre-releases are available, the installer
automatically selects the newest one. It detects your operating system and CPU,
verifies the download, and asks where to install `elgatolight`.

Install from the primary repository with either `curl` or `wget`:

```sh
curl -fsSL https://git2.riper.fr/ztec/elgatocmd/raw/branch/main/install.sh | sh
wget -qO- https://git2.riper.fr/ztec/elgatocmd/raw/branch/main/install.sh | sh
```

The same installer is mirrored on GitHub:

```sh
curl -fsSL https://github.com/ztec/elgatocmd/raw/refs/heads/main/install.sh | sh
wget -qO- https://github.com/ztec/elgatocmd/raw/refs/heads/main/install.sh | sh
```

The default install directory is `~/.local/bin`, or `/usr/local/bin` when the
installer runs as root. After installation, run the setup command printed by
the installer:

```sh
sudo ~/.local/bin/elgatolight setup
```

Setup installs the USB permissions and offers three service options:

- `user`: start the Home Assistant service when the selected user logs in.
- `system`: start the Home Assistant service when the computer boots.
- `none`: install USB permissions only and use the command-line interface.

Choose `none` for command-line use. Unplug and reconnect the light once after
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
invoke `~/.local/bin/elgatolight` directly, or choose another directory in the
download installer.

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

Then use the installer above on the computer connected to the light and run:

```sh
sudo ~/.local/bin/elgatolight setup
```

Choose `user` to start the daemon whenever that user logs in, or `system` to
start it at boot. Setup asks for the externally reachable Home Assistant/proxy
URL, opens the OAuth authorization flow, installs the USB rule, and enables and
starts the systemd service. It is idempotent and safely refreshes the service
after a binary upgrade.

For unattended setup, provide every choice as flags:

```sh
sudo ~/.local/bin/elgatolight setup \
  --scope user \
  --target-user alice \
  --ha-url https://homeassistant.example.test \
  --yes
```

Setup reuses an existing matching authorization. The first setup displays an
OAuth URL to open in a browser.

## Documentation

See [Technical documentation](docs/technical.md) for release verification,
all setup flags, manual Home Assistant installation, configuration, upgrades,
troubleshooting, containerized development, CI/release automation, security,
and protocol details.
