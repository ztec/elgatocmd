//go:build linux

package main

import (
	"bytes"
	"context"
	"io"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoninteractiveSetupRequiresExplicitChoices(t *testing.T) {
	app := newTestCommandApp(t)
	command := app.setupCommand()
	if _, err := app.resolveSetup(command, setupFlags{assumeYes: true}, false); err == nil || !strings.Contains(err.Error(), "--scope") {
		t.Fatalf("missing-scope error = %v", err)
	}

	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.resolveSetup(command, setupFlags{scope: "user", targetUser: account.Username, assumeYes: true}, false); err == nil || !strings.Contains(err.Error(), "--ha-url") {
		t.Fatalf("missing-URL error = %v", err)
	}
}

func TestNoninteractiveSetupFlagsResolveWithoutPrompts(t *testing.T) {
	t.Setenv("SUDO_USER", "")
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	app, err := newCommandApp(context.Background(), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	app.config.Set("home_assistant.url", "https://ha.example.test/base/")
	command := app.setupCommand()
	installDir := filepath.Join(account.HomeDir, "custom-bin")
	resolved, err := app.resolveSetup(command, setupFlags{
		scope: "user", targetUser: account.Username, installDir: installDir, assumeYes: true,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.homeAssistantURL != "https://ha.example.test/base" || resolved.installDir != installDir {
		t.Fatalf("resolved = %#v", resolved)
	}
	wantCredentials := filepath.Join(account.HomeDir, ".local", "state", "elgatolight", "credentials.json")
	if resolved.credentialsPath != wantCredentials {
		t.Fatalf("credentials = %s, want %s", resolved.credentialsPath, wantCredentials)
	}
}

func TestInteractiveSetupPromptsForEveryMissingChoice(t *testing.T) {
	t.Setenv("SUDO_USER", "")
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	app := newTestCommandApp(t)
	command := app.setupCommand()
	command.SetIn(strings.NewReader("user\n" + account.Username + "\nhttps://ha.example.test\n\nyes\n"))
	var output bytes.Buffer
	command.SetOut(&output)
	resolved, err := app.resolveSetup(command, setupFlags{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.scope != "user" || resolved.target.Name != account.Username {
		t.Fatalf("resolved = %#v", resolved)
	}
	for _, prompt := range []string{"Setup scope", "Login user", "Home Assistant/proxy URL", "Binary installation directory", "Continue"} {
		if !strings.Contains(output.String(), prompt) {
			t.Errorf("output omitted %q:\n%s", prompt, output.String())
		}
	}
}

func TestCLISetupDoesNotRequireHomeAssistant(t *testing.T) {
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	app := newTestCommandApp(t)
	resolved, err := app.resolveSetup(app.setupCommand(), setupFlags{
		scope: "cli", targetUser: account.Username, assumeYes: true,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.homeAssistantURL != "" || resolved.credentialsPath != "" {
		t.Fatalf("CLI setup unexpectedly resolved Home Assistant state: %#v", resolved)
	}
}
