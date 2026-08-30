# Command line

`elgatolight` controls Elgato Key Light Neo devices over Linux USB and can
maintain an outbound Home Assistant bridge.

| Purpose | Commands |
| --- | --- |
| Inspect | `status`, `info`, `list`, `presets` |
| Control | `on`, `off`, `toggle`, `brightness 30`, `temperature 4500`, `preset 1` |
| Monitor | `watch --interval 200ms`, `log --interval 200ms` |
| Home Assistant | `setup`, `pair`, `daemon`, `auth status`, `auth revoke` |
| Lifecycle | `self-update`, `completion bash`, `--version` |

Run `elgatolight --help` or `elgatolight COMMAND --help` for the generated
reference. Global flags may appear before or after a subcommand.

## Light selection and output

One connected light is selected automatically for writes. Read commands include
all lights. With several lights, select the stable USB serial shown by `info`:

```sh
elgatolight --light A7BTB0000000ZZ brightness 30
elgatolight info --json
```

`--device /dev/hidrawN` is a diagnostic path override. `watch` redraws a
terminal dashboard; `log` emits compact JSON Lines for automation.

## Configuration

Flags have priority over `ELGATOLIGHT_` environment variables, which have
priority over the selected configuration file and built-in defaults. The
default file is `config.*` under `$XDG_CONFIG_HOME/elgatolight`; YAML, TOML,
and JSON are supported.

```yaml
light: A7BTB0000000ZZ
timeout: 2s

watch:
  interval: 200ms

home_assistant:
  url: https://homeassistant.example.test
  credentials: /home/example/.local/state/elgatolight/credentials.json

release:
  api: https://git2.riper.fr/api/v1/repos/ztec/elgatocmd
```

Nested keys use underscores in environment variables, for example
`ELGATOLIGHT_HOME_ASSISTANT_URL` and `ELGATOLIGHT_RELEASE_API`.
`self-update --force` permits a verified reinstall or downgrade but never
bypasses signature, checksum, archive, or embedded-version verification.

See the [technical reference](technical.md) for setup scopes, Home Assistant
pairing, daemon operation, and protocol details.
