package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"elgatolight/internal/elgato"
	"elgatolight/internal/homeassistant"
	"elgatolight/internal/lights"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type commandApp struct {
	ctx          context.Context
	config       *viper.Viper
	root         *cobra.Command
	configLoaded bool
}

func newCommandApp(ctx context.Context, stdout, stderr io.Writer) (*commandApp, error) {
	app := &commandApp{ctx: ctx, config: viper.New()}
	app.config.SetEnvPrefix("ELGATOLIGHT")
	app.config.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	app.config.AutomaticEnv()
	app.config.SetDefault("timeout", 2*time.Second)
	app.config.SetDefault("home_assistant.credentials", defaultCredentialPath())
	app.config.SetDefault("home_assistant.oauth_callback", homeassistant.DefaultOAuthCallback)
	app.config.SetDefault("daemon.poll_interval", 250*time.Millisecond)
	app.config.SetDefault("daemon.reconcile_interval", time.Second)
	app.config.SetDefault("daemon.call_timeout", 10*time.Second)
	app.config.SetDefault("daemon.min_backoff", time.Second)
	app.config.SetDefault("daemon.max_backoff", 30*time.Second)

	root := &cobra.Command{
		Use:           "elgatolight",
		Short:         "Control Elgato Key Light Neo devices over USB",
		Version:       applicationVersion(),
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			return app.loadConfig()
		},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetVersionTemplate("{{.Version}}\n")
	root.PersistentFlags().String("config", "", "configuration file (default: $XDG_CONFIG_HOME/elgatolight/config.*)")
	root.PersistentFlags().String("light", "", "stable USB serial to select")
	root.PersistentFlags().String("device", "", "hidraw device path instead of a stable ID")
	root.PersistentFlags().Bool("json", false, "print JSON instead of human-readable output")
	root.PersistentFlags().Duration("timeout", 2*time.Second, "timeout for each USB request")
	root.PersistentFlags().String("ha-url", "", "Home Assistant base URL")
	root.PersistentFlags().String("credentials", defaultCredentialPath(), "Home Assistant credential file")
	root.PersistentFlags().String("oauth-callback", homeassistant.DefaultOAuthCallback, "loopback OAuth callback URL")
	root.PersistentFlags().Bool("insecure-skip-tls-verify", false, "accept an untrusted Home Assistant TLS certificate (unsafe)")
	bindings := map[string]string{
		"config": "config", "light": "light", "device": "device", "json": "json", "timeout": "timeout",
		"ha-url": "home_assistant.url", "credentials": "home_assistant.credentials",
		"oauth-callback": "home_assistant.oauth_callback", "insecure-skip-tls-verify": "home_assistant.insecure_skip_tls_verify",
	}
	for flag, key := range bindings {
		if err := app.config.BindPFlag(key, root.PersistentFlags().Lookup(flag)); err != nil {
			return nil, fmt.Errorf("bind --%s: %w", flag, err)
		}
	}

	app.root = root
	root.AddCommand(
		app.statusCommand(),
		app.infoCommand(),
		app.powerCommand("on", true),
		app.powerCommand("off", false),
		app.toggleCommand(),
		app.brightnessCommand(),
		app.temperatureCommand(),
		app.presetsCommand(),
		app.presetCommand(),
		app.monitorCommand("watch"),
		app.monitorCommand("log"),
		app.pairCommand(),
		app.authCommand(),
		app.daemonCommand(),
		app.setupCommand(),
	)
	root.InitDefaultCompletionCmd()
	return app, nil
}

func (app *commandApp) loadConfig() error {
	if app.configLoaded {
		return nil
	}
	app.configLoaded = true
	explicit := app.config.GetString("config")
	if explicit != "" {
		app.config.SetConfigFile(explicit)
		if err := app.config.ReadInConfig(); err != nil {
			return fmt.Errorf("read config %s: %w", explicit, err)
		}
		return nil
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil
	}
	app.config.SetConfigName("config")
	app.config.AddConfigPath(filepath.Join(configDir, "elgatolight"))
	if err := app.config.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if errors.As(err, &notFound) {
			return nil
		}
		return fmt.Errorf("read default config: %w", err)
	}
	return nil
}

func (app *commandApp) options() (options, error) {
	opts := options{
		device:  app.config.GetString("device"),
		lightID: app.config.GetString("light"),
		json:    app.config.GetBool("json"),
		timeout: app.config.GetDuration("timeout"),
	}
	if opts.timeout <= 0 {
		return options{}, errors.New("timeout must be positive")
	}
	if opts.device != "" && opts.lightID != "" {
		return options{}, errors.New("--light and --device cannot be used together")
	}
	return opts, nil
}

func (app *commandApp) statusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "List current light states",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			opts, err := app.options()
			if err != nil {
				return err
			}
			snapshots, err := readSnapshots(app.ctx, opts, false, false)
			if err != nil {
				return err
			}
			return printSnapshots(command.OutOrStdout(), snapshots, opts.json)
		},
	}
}

func (app *commandApp) infoCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "info",
		Aliases: []string{"list"},
		Short:   "List device and firmware information",
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			opts, err := app.options()
			if err != nil {
				return err
			}
			return runInfo(app.ctx, command.OutOrStdout(), opts)
		},
	}
}

func (app *commandApp) powerCommand(name string, power bool) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: "Turn the selected light " + name,
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			opts, err := app.options()
			if err != nil {
				return err
			}
			return runManagedStatusChange(app.ctx, command.OutOrStdout(), opts, func(lights.Light) (lights.Update, error) {
				return lights.Update{On: &power}, nil
			})
		},
	}
}

func (app *commandApp) toggleCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "toggle",
		Short: "Toggle the selected light",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			opts, err := app.options()
			if err != nil {
				return err
			}
			return runManagedStatusChange(app.ctx, command.OutOrStdout(), opts, func(current lights.Light) (lights.Update, error) {
				power := !current.State.On
				return lights.Update{On: &power}, nil
			})
		},
	}
}

func (app *commandApp) brightnessCommand() *cobra.Command {
	return app.managedIntegerUpdateCommand(
		"brightness PERCENT",
		"Set brightness (0 to the device's current maximum)",
		"brightness",
		func(value int) lights.Update {
			return lights.Update{Brightness: &value}
		},
	)
}

func (app *commandApp) temperatureCommand() *cobra.Command {
	return app.managedIntegerUpdateCommand(
		"temperature KELVIN",
		"Set color temperature (2900-7000K)",
		"temperature",
		func(value int) lights.Update {
			return lights.Update{Temperature: &value}
		},
	)
}

func (app *commandApp) managedIntegerUpdateCommand(use, short, valueName string, update func(int) lights.Update) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			value, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid %s %q: %w", valueName, args[0], err)
			}
			opts, err := app.options()
			if err != nil {
				return err
			}
			return runManagedStatusChange(app.ctx, command.OutOrStdout(), opts, func(lights.Light) (lights.Update, error) {
				return update(value), nil
			})
		},
	}
}

func (app *commandApp) presetCommand() *cobra.Command {
	return app.integerUpdateCommand(
		"preset NUMBER",
		"Apply stored preset 1 or 2",
		"preset",
		func(ctx context.Context, client *elgato.Client, value int) (elgato.Status, error) {
			return client.ApplyPreset(ctx, value)
		},
	)
}

func (app *commandApp) integerUpdateCommand(use, short, valueName string, update func(context.Context, *elgato.Client, int) (elgato.Status, error)) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			value, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid %s %q: %w", valueName, args[0], err)
			}
			opts, err := app.options()
			if err != nil {
				return err
			}
			return runStatusChange(app.ctx, command.OutOrStdout(), opts, func(requestCtx context.Context, client *elgato.Client) (elgato.Status, error) {
				return update(requestCtx, client, value)
			})
		},
	}
}

func (app *commandApp) presetsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "presets",
		Short: "List device-stored presets",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			opts, err := app.options()
			if err != nil {
				return err
			}
			return runPresets(app.ctx, command.OutOrStdout(), opts)
		},
	}
}

func (app *commandApp) monitorCommand(name string) *cobra.Command {
	short := "Log JSON Lines events, including initial state"
	if name == "watch" {
		short = "Watch all lights in a live terminal dashboard"
	}
	command := &cobra.Command{
		Use:   name,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			opts, err := app.options()
			if err != nil {
				return err
			}
			interval := app.config.GetDuration(name + ".interval")
			if interval <= 0 {
				return fmt.Errorf("%s interval must be positive", name)
			}
			return monitor(app.ctx, name, interval, opts, command.OutOrStdout())
		},
	}
	key := name + ".interval"
	app.config.SetDefault(key, 200*time.Millisecond)
	command.Flags().Duration("interval", 200*time.Millisecond, "polling interval")
	if err := app.config.BindPFlag(key, command.Flags().Lookup("interval")); err != nil {
		panic(fmt.Sprintf("bind %s --interval: %v", name, err))
	}
	return command
}
