//go:build linux

package cli

import (
	"bytes"
	"context"
	"io"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoninteractiveSetupInfersScopeAndRequiresRemainingChoices(t *testing.T) {
	app := newTestCommandApp(t)
	command := app.setupCommand()
	if _, err := app.resolveSetup(command, setupFlags{assumeYes: true}, false, 1000); err == nil || !strings.Contains(err.Error(), "--ha-url") {
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
		scope: "user", assumeYes: true,
	}, false, 1000)
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

func TestInteractiveSetupExplainsAndInfersUserScope(t *testing.T) {
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	app := newTestCommandApp(t)
	command := app.setupCommand()
	command.SetIn(strings.NewReader("https://ha.example.test\nyes\n"))
	var output bytes.Buffer
	command.SetOut(&output)
	resolved, err := app.resolveSetup(command, setupFlags{}, true, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.scope != "user" || resolved.target.Name != account.Username {
		t.Fatalf("resolved = %#v", resolved)
	}
	for _, prompt := range []string{
		"user    Run without sudo; start Home Assistant when the current user logs in.",
		"system  Run with sudo; start the Home Assistant service when the computer boots.",
		"none    Install USB permissions only and use the command-line interface.",
		"Detected user setup", "Home Assistant/proxy URL", "Continue",
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
	}, false, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.homeAssistantURL != "" || resolved.credentialsPath != "" || resolved.binaryPath != "" || resolved.target.Name != "" {
		t.Fatalf("none setup unexpectedly resolved service state: %#v", resolved)
	}
}

func TestLegacyCLIScopeIsRejected(t *testing.T) {
	app := newTestCommandApp(t)
	_, err := app.resolveSetup(app.setupCommand(), setupFlags{scope: "cli", assumeYes: true}, false, 1000)
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
		"user    Run without sudo; start Home Assistant when the current user logs in.",
		"system  Run with sudo; start the Home Assistant service when the computer boots.",
		"none    Install USB permissions only and use the command-line interface.",
	} {
		if !strings.Contains(help, text) {
			t.Fatalf("setup help omitted %q:\n%s", text, help)
		}
	}
	if strings.Contains(help, "--install-dir") {
		t.Fatalf("setup help still exposes binary installation:\n%s", help)
	}
	if strings.Contains(help, "--target-user") {
		t.Fatalf("setup help still asks root to select a user:\n%s", help)
	}
}

func TestSetupScopeMustMatchPrivileges(t *testing.T) {
	app := newTestCommandApp(t)
	_, err := app.resolveSetup(app.setupCommand(), setupFlags{scope: "user", assumeYes: true}, false, 0)
	if err == nil || !strings.Contains(err.Error(), "without sudo") {
		t.Fatalf("root user-scope error = %v", err)
	}
	_, err = app.resolveSetup(app.setupCommand(), setupFlags{scope: "system", assumeYes: true}, false, 1000)
	if err == nil || !strings.Contains(err.Error(), "sudo elgatolight setup") {
		t.Fatalf("unprivileged system-scope error = %v", err)
	}
}

func TestRootSetupInfersSystemScope(t *testing.T) {
	app := newTestCommandApp(t)
	app.config.Set("home_assistant.url", "https://ha.example.test")
	resolved, err := app.resolveSetup(app.setupCommand(), setupFlags{assumeYes: true}, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.scope != "system" || resolved.target.Name != "root" || resolved.credentialsPath != "/var/lib/elgatolight/credentials.json" {
		t.Fatalf("resolved = %#v", resolved)
	}
}
