# Elgato Key Light Neo USB controller

`elgatolight` controls one or more Elgato Key Light Neo devices directly over
USB on Linux. It supports power, brightness, color temperature, the two stored
presets, physical-control updates, and stable device IDs. Wi-Fi and Elgato
Control Center are not required.

There are two ways to use it:

- Run the command-line application directly.
- Connect the lights to Home Assistant through the included integration and
  daemon.

## Command-line usage

You need Linux, Podman, and Distrobox. Clone the repository, then build and set
up access to the USB light:

```sh
git clone https://github.com/ztec/elgatocmd.git
cd elgatocmd
make setup
# Unplug and reconnect the light once.
make build
```

Run `make setup` as your normal user; it asks for `sudo` only when installing
the USB permission rule.

Common commands:

```sh
bin/elgatolight info
bin/elgatolight on
bin/elgatolight off
bin/elgatolight brightness 30
bin/elgatolight temperature 4500
bin/elgatolight presets
bin/elgatolight preset 1
bin/elgatolight watch
```

With one light, it is selected automatically. With multiple lights, `info`
shows their stable IDs; select one with `--light ID`. Run
`bin/elgatolight --help` for every command and option.

## Home Assistant usage

The Home Assistant integration is installed through HACS. The daemon runs on
the Linux computer connected to the USB light and initiates the connection to
Home Assistant. It needs only the Home Assistant URL—no MQTT broker or inbound
connection to the daemon is required.

### 1. Install the integration

1. Open **HACS → Integrations**.
2. Open **Custom repositories**, add
   `https://github.com/ztec/elgatocmd`, and select **Integration**.
3. Download **Elgato USB Light Bridge** and restart Home Assistant.
4. Open **Settings → Devices & services → Add integration** and add
   **Elgato USB Light Bridge**.

### 2. Start the daemon

On the Linux computer connected to the light, complete the command-line setup
above, then run:

```sh
bin/elgatolight daemon --ha-url https://homeassistant.example.test
```

The first start opens Home Assistant in your browser for authorization and
saves the credential locally. Later starts only need:

```sh
bin/elgatolight daemon
```

Home Assistant will create one light entity per connected USB light and receive
power, brightness, temperature, physical-button, and availability updates.

## Documentation

See [Technical documentation](docs/technical.md) for manual Home Assistant
installation, configuration, daemon operation, upgrades, troubleshooting,
development, security, and protocol details.
