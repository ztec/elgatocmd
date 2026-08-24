//go:build linux

package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withoutChown(t *testing.T) {
	t.Helper()
	oldOwner, oldLinkOwner := changeOwner, changeLinkOwner
	changeOwner = func(string, int, int) error { return nil }
	changeLinkOwner = func(string, int, int) error { return nil }
	t.Cleanup(func() {
		changeOwner, changeLinkOwner = oldOwner, oldLinkOwner
	})
}

func testSource(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "elgatolight-source")
	if err := os.WriteFile(path, []byte("test executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUserInstallIsIdempotent(t *testing.T) {
	withoutChown(t)
	root := t.TempDir()
	home := filepath.Join(root, "home", "alice")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	var commands []string
	config := Config{
		Scope:            ScopeUser,
		Target:           TargetUser{Name: "alice", Home: home, UID: 1000, GID: 1000},
		InstallDir:       filepath.Join(home, ".local", "bin"),
		HomeAssistantURL: "https://ha.example.test",
		CredentialsPath:  filepath.Join(home, ".local", "state", "elgatolight", "credentials.json"),
		SourceExecutable: testSource(t), RootDir: root,
		Run: func(name string, args ...string) error {
			commands = append(commands, strings.Join(append([]string{name}, args...), " "))
			return nil
		},
	}
	first, err := Apply(config)
	if err != nil {
		t.Fatal(err)
	}
	if !first.BinaryChanged || !first.RuleChanged || !first.UnitChanged {
		t.Fatalf("first result = %#v", first)
	}
	second, err := Apply(config)
	if err != nil {
		t.Fatal(err)
	}
	if second.BinaryChanged || second.RuleChanged || second.UnitChanged {
		t.Fatalf("second result = %#v", second)
	}
	unit, err := os.ReadFile(first.UnitPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), `ExecStart="`+first.BinaryPath+`" daemon`) {
		t.Fatalf("unit does not reference installed binary:\n%s", unit)
	}
	link := filepath.Join(filepath.Dir(first.UnitPath), "default.target.wants", "elgatolight.service")
	if target, err := os.Readlink(link); err != nil || target != "../elgatolight.service" {
		t.Fatalf("service link = %q, %v", target, err)
	}
	if len(commands) != 5 {
		t.Fatalf("commands = %v", commands)
	}
}

func TestCLIInstallSkipsHomeAssistantAndSystemd(t *testing.T) {
	withoutChown(t)
	root := t.TempDir()
	home := filepath.Join(root, "home", "alice")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	var commands []string
	result, err := Apply(Config{
		Scope: ScopeCLI, Target: TargetUser{Name: "alice", Home: home, UID: 1000, GID: 1000},
		InstallDir: filepath.Join(home, ".local", "bin"), SourceExecutable: testSource(t), RootDir: root,
		Run: func(name string, args ...string) error {
			commands = append(commands, strings.Join(append([]string{name}, args...), " "))
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.UnitPath != "" || result.BinaryPath == "" {
		t.Fatalf("result = %#v", result)
	}
	if fmt.Sprint(commands) != fmt.Sprint([]string{"udevadm control --reload-rules"}) {
		t.Fatalf("commands = %v", commands)
	}
}

func TestSystemInstallUsesSystemdAndRootedFiles(t *testing.T) {
	withoutChown(t)
	root := t.TempDir()
	var commands []string
	config := Config{
		Scope:            ScopeSystem,
		Target:           TargetUser{Name: "root", Home: filepath.Join(root, "root"), UID: 0, GID: 0},
		InstallDir:       filepath.Join(root, "usr", "local", "bin"),
		HomeAssistantURL: "https://ha.example.test/base",
		CredentialsPath:  filepath.Join(root, "var", "lib", "elgatolight", "credentials.json"),
		SourceExecutable: testSource(t), RootDir: root,
		Run: func(name string, args ...string) error {
			commands = append(commands, strings.Join(append([]string{name}, args...), " "))
			return nil
		},
	}
	result, err := Apply(config)
	if err != nil {
		t.Fatal(err)
	}
	if result.UnitPath != filepath.Join(root, "etc/systemd/system/elgatolight.service") {
		t.Fatalf("unit path = %s", result.UnitPath)
	}
	want := []string{
		"udevadm control --reload-rules",
		"systemctl daemon-reload",
		"systemctl enable elgatolight.service",
		"systemctl restart elgatolight.service",
	}
	if fmt.Sprint(commands) != fmt.Sprint(want) {
		t.Fatalf("commands = %v, want %v", commands, want)
	}
}

func TestCredentialOwnershipRefusesSymlink(t *testing.T) {
	withoutChown(t)
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "credentials.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := EnsureCredentialOwnership(link, TargetUser{UID: 1000, GID: 1000}); err == nil {
		t.Fatal("expected credential symlink to be rejected")
	}
}

func TestManagedFileRefusesUnrelatedContentAndSymlinks(t *testing.T) {
	withoutChown(t)
	directory := t.TempDir()
	unmanaged := filepath.Join(directory, "unit")
	if err := os.WriteFile(unmanaged, []byte("unrelated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeManagedFile(unmanaged, []byte(managedMarker+"\nnew\n"), 0o644, 0, 0, false); err == nil {
		t.Fatal("expected unmanaged-file error")
	}
	link := filepath.Join(directory, "link")
	if err := os.Symlink(unmanaged, link); err != nil {
		t.Fatal(err)
	}
	if _, err := writeManagedFile(link, []byte(managedMarker+"\nnew\n"), 0o644, 0, 0, false); err == nil {
		t.Fatal("expected symlink error")
	}
}

func TestSystemdQuoteEscapesSpecifiersAndQuotes(t *testing.T) {
	got := systemdQuote(`/tmp/a%b "light"`)
	want := `"/tmp/a%%b \"light\""`
	if got != want {
		t.Fatalf("quote = %q, want %q", got, want)
	}
}
