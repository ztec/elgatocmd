package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func newTestCommandApp(t *testing.T) *commandApp {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, key := range []string{
		"CONFIG", "DEVICE", "LIGHT", "JSON", "TIMEOUT", "WATCH_INTERVAL", "LOG_INTERVAL",
		"HOME_ASSISTANT_URL", "HOME_ASSISTANT_CREDENTIALS", "HOME_ASSISTANT_OAUTH_CALLBACK",
		"HOME_ASSISTANT_INSECURE_SKIP_TLS_VERIFY", "DAEMON_POLL_INTERVAL", "DAEMON_RECONCILE_INTERVAL",
		"DAEMON_CALL_TIMEOUT", "DAEMON_MIN_BACKOFF", "DAEMON_MAX_BACKOFF", "RELEASE_API", "SELF_UPDATE_FORCE",
	} {
		t.Setenv("ELGATOLIGHT_"+key, "")
	}
	app, err := newCommandApp(context.Background(), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func TestHomeAssistantCommandTree(t *testing.T) {
	app := newTestCommandApp(t)
	for _, path := range []string{"pair", "daemon", "auth status", "auth revoke", "setup", "self-update"} {
		command, _, err := app.root.Find(strings.Fields(path))
		if err != nil {
			t.Fatalf("find %q: %v", path, err)
		}
		if command == app.root {
			t.Fatalf("%q resolved to the root command", path)
		}
	}
	for _, flag := range []string{"ha-url", "credentials", "oauth-callback", "insecure-skip-tls-verify"} {
		if app.root.PersistentFlags().Lookup(flag) == nil {
			t.Errorf("missing persistent --%s flag", flag)
		}
	}
}

func TestVersionOutputIsExact(t *testing.T) {
	app := newTestCommandApp(t)
	var output strings.Builder
	app.root.SetOut(&output)
	app.root.SetArgs([]string{"--version"})
	if err := app.root.Execute(); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), applicationVersion()+"\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}

func TestSelfUpdateConfiguration(t *testing.T) {
	app := newTestCommandApp(t)
	t.Setenv("ELGATOLIGHT_RELEASE_API", "https://updates.example.test/api")

	command, _, err := app.root.Find([]string{"self-update"})
	if err != nil {
		t.Fatal(err)
	}
	if command.Flags().Lookup("release-api") == nil || command.Flags().Lookup("force") == nil {
		t.Fatal("self-update flags are incomplete")
	}
	if err := app.loadConfig(); err != nil {
		t.Fatal(err)
	}
	if got := app.config.GetString("release.api"); got != "https://updates.example.test/api" {
		t.Fatalf("release API = %q", got)
	}
}

func captureOptions(t *testing.T, app *commandApp, commandName string, args []string) options {
	t.Helper()
	command, _, err := app.root.Find([]string{commandName})
	if err != nil {
		t.Fatal(err)
	}
	var captured options
	command.RunE = func(_ *cobra.Command, _ []string) error {
		var err error
		captured, err = app.options()
		return err
	}
	app.root.SetArgs(args)
	if err := app.root.Execute(); err != nil {
		t.Fatal(err)
	}
	return captured
}

func TestPersistentFlagAfterSubcommand(t *testing.T) {
	app := newTestCommandApp(t)
	opts := captureOptions(t, app, "info", []string{"info", "--json", "--timeout", "750ms"})
	if !opts.json || opts.timeout != 750*time.Millisecond {
		t.Fatalf("trailing persistent flags produced %#v", opts)
	}
}

func TestListIsInfoAlias(t *testing.T) {
	app := newTestCommandApp(t)
	command, _, err := app.root.Find([]string{"list"})
	if err != nil {
		t.Fatal(err)
	}
	if command.Name() != "info" || !command.HasAlias("list") {
		t.Fatalf("list resolved to %q with aliases %v", command.Name(), command.Aliases)
	}
}

func TestEnvironmentOverridesConfigFile(t *testing.T) {
	app := newTestCommandApp(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("light: from-config\ntimeout: 4s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ELGATOLIGHT_LIGHT", "from-environment")
	t.Setenv("ELGATOLIGHT_TIMEOUT", "3s")
	opts := captureOptions(t, app, "info", []string{"info", "--config", configPath})
	if opts.lightID != "from-environment" || opts.timeout != 3*time.Second {
		t.Fatalf("environment precedence produced %#v", opts)
	}
}

func TestFlagsOverrideEnvironmentAndConfig(t *testing.T) {
	app := newTestCommandApp(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("light: from-config\njson: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ELGATOLIGHT_LIGHT", "from-environment")
	opts := captureOptions(t, app, "info", []string{"info", "--config", configPath, "--light", "from-flag", "--json"})
	if opts.lightID != "from-flag" || !opts.json {
		t.Fatalf("flag precedence produced %#v", opts)
	}
}

func TestMonitorIntervalFromConfigAndFlag(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want time.Duration
	}{
		{name: "config", want: 350 * time.Millisecond},
		{name: "flag overrides config", args: []string{"--interval", "125ms"}, want: 125 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := newTestCommandApp(t)
			configPath := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(configPath, []byte("watch:\n  interval: 350ms\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			command, _, err := app.root.Find([]string{"watch"})
			if err != nil {
				t.Fatal(err)
			}
			var interval time.Duration
			command.RunE = func(_ *cobra.Command, _ []string) error {
				interval = app.config.GetDuration("watch.interval")
				return nil
			}
			args := []string{"watch", "--config", configPath}
			args = append(args, test.args...)
			app.root.SetArgs(args)
			if err := app.root.Execute(); err != nil {
				t.Fatal(err)
			}
			if interval != test.want {
				t.Fatalf("interval = %s, want %s", interval, test.want)
			}
		})
	}
}
