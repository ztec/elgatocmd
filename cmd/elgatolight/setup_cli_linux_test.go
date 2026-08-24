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
	resolved, err := app.resolveSetup(command, setupFlags{
		scope: "user", targetUser: account.Username, assumeYes: true,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.homeAssistantURL != "https://ha.example.test/base" || !filepath.IsAbs(resolved.binaryPath) {
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
	command.SetIn(strings.NewReader("user\n" + account.Username + "\nhttps://ha.example.test\nyes\n"))
	var output bytes.Buffer
	command.SetOut(&output)
	resolved, err := app.resolveSetup(command, setupFlags{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.scope != "user" || resolved.target.Name != account.Username {
		t.Fatalf("resolved = %#v", resolved)
	}
	for _, prompt := range []string{
		"user    Start the Home Assistant service when the selected user logs in.",
		"system  Start the Home Assistant service when the computer boots.",
		"none    Install USB permissions only and use the command-line interface.",
		"Service option", "Login user", "Home Assistant/proxy URL", "Continue",
	} {
		if !strings.Contains(output.String(), prompt) {
			t.Errorf("output omitted %q:\n%s", prompt, output.String())
		}
	}
}

func TestNoneSetupDoesNotRequireHomeAssistantOrUser(t *testing.T) {
	app := newTestCommandApp(t)
	resolved, err := app.resolveSetup(app.setupCommand(), setupFlags{
		scope: "none", assumeYes: true,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.homeAssistantURL != "" || resolved.credentialsPath != "" || resolved.binaryPath != "" || resolved.target.Name != "" {
		t.Fatalf("none setup unexpectedly resolved service state: %#v", resolved)
	}
}

func TestLegacyCLIScopeIsRejected(t *testing.T) {
	app := newTestCommandApp(t)
	_, err := app.resolveSetup(app.setupCommand(), setupFlags{scope: "cli", assumeYes: true}, false)
	if err == nil || !strings.Contains(err.Error(), "expected user, system, or none") {
		t.Fatalf("legacy CLI scope error = %v", err)
	}
}

func TestSetupHelpExplainsAllServiceOptions(t *testing.T) {
	app := newTestCommandApp(t)
	command, _, err := app.root.Find([]string{"setup"})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command.SetOut(&output)
	if err := command.Help(); err != nil {
		t.Fatal(err)
	}
	help := output.String()
	for _, text := range []string{
		"user    Start the Home Assistant service when the selected user logs in.",
		"system  Start the Home Assistant service when the computer boots.",
		"none    Install USB permissions only and use the command-line interface.",
	} {
		if !strings.Contains(help, text) {
			t.Fatalf("setup help omitted %q:\n%s", text, help)
		}
	}
	if strings.Contains(help, "--install-dir") {
		t.Fatalf("setup help still exposes binary installation:\n%s", help)
	}
}
