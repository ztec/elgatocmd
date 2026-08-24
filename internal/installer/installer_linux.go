//go:build linux

// Package installer installs the release binary, USB rule, and systemd unit.
package installer

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"elgatolight/packaging"
)

type Scope string

const (
	ScopeCLI    Scope = "cli"
	ScopeUser   Scope = "user"
	ScopeSystem Scope = "system"
)

const managedMarker = "# Managed by elgatolight setup."

var (
	changeOwner     = os.Chown
	changeLinkOwner = os.Lchown
)

type TargetUser struct {
	Name string
	Home string
	UID  int
	GID  int
}

type CommandRunner func(name string, args ...string) error

type Config struct {
	Scope            Scope
	Target           TargetUser
	InstallDir       string
	HomeAssistantURL string
	CredentialsPath  string
	SourceExecutable string
	RootDir          string
	Run              CommandRunner
	Warnf            func(string, ...any)
}

type Result struct {
	BinaryPath    string
	UnitPath      string
	RuleChanged   bool
	BinaryChanged bool
	UnitChanged   bool
}

type unitValues struct {
	Binary           string
	HomeAssistantURL string
	Credentials      string
}

func Apply(config Config) (Result, error) {
	if err := validate(config); err != nil {
		return Result{}, err
	}
	if config.Run == nil {
		return Result{}, errors.New("installer command runner is required")
	}
	if config.Warnf == nil {
		config.Warnf = func(string, ...any) {}
	}

	ownerUID, ownerGID := config.Target.UID, config.Target.GID
	if config.Scope == ScopeSystem {
		ownerUID, ownerGID = 0, 0
	} else if err := prepareUserDirectory(config.InstallDir, config.Target, 0o755); err != nil {
		return Result{}, err
	}
	binaryPath := filepath.Join(config.InstallDir, "elgatolight")
	binaryChanged, err := installExecutable(config.SourceExecutable, binaryPath, ownerUID, ownerGID)
	if err != nil {
		return Result{}, fmt.Errorf("install executable: %w", err)
	}

	rulePath := rooted(config.RootDir, "/etc/udev/rules.d/99-elgato-key-light-neo.rules")
	ruleChanged, err := writeManagedFile(rulePath, []byte(packaging.UdevRule), 0o644, 0, 0, true)
	if err != nil {
		return Result{}, fmt.Errorf("install udev rule: %w", err)
	}
	if ruleChanged {
		if err := config.Run("udevadm", "control", "--reload-rules"); err != nil {
			return Result{}, fmt.Errorf("reload udev rules: %w", err)
		}
	}
	if config.Scope == ScopeCLI {
		return Result{BinaryPath: binaryPath, RuleChanged: ruleChanged, BinaryChanged: binaryChanged}, nil
	}

	unit, unitPath, err := renderUnit(config, binaryPath)
	if err != nil {
		return Result{}, err
	}
	unitUID, unitGID := 0, 0
	if config.Scope == ScopeUser {
		unitUID, unitGID = config.Target.UID, config.Target.GID
		if err := prepareUserDirectory(filepath.Dir(unitPath), config.Target, 0o755); err != nil {
			return Result{}, err
		}
	}
	unitChanged, err := writeManagedFile(unitPath, unit, 0o644, unitUID, unitGID, false)
	if err != nil {
		return Result{}, fmt.Errorf("install systemd unit: %w", err)
	}

	if config.Scope == ScopeSystem {
		if err := config.Run("systemctl", "daemon-reload"); err != nil {
			return Result{}, fmt.Errorf("reload system systemd manager: %w", err)
		}
		if err := config.Run("systemctl", "enable", "elgatolight.service"); err != nil {
			return Result{}, fmt.Errorf("enable system service: %w", err)
		}
		if err := config.Run("systemctl", "restart", "elgatolight.service"); err != nil {
			return Result{}, fmt.Errorf("start or restart system service: %w", err)
		}
	} else {
		if err := enableUserUnit(unitPath, config.Target); err != nil {
			return Result{}, err
		}
		userSystemctl := []string{
			"-u", config.Target.Name, "--", "env",
			"XDG_RUNTIME_DIR=/run/user/" + strconv.Itoa(config.Target.UID),
			"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/" + strconv.Itoa(config.Target.UID) + "/bus",
			"systemctl", "--user",
		}
		if err := config.Run("runuser", append(userSystemctl, "daemon-reload")...); err != nil {
			config.Warnf("could not reload the active user systemd manager; the service is enabled for the next login: %v", err)
		} else if err := config.Run("runuser", append(userSystemctl, "restart", "elgatolight.service")...); err != nil {
			config.Warnf("could not start the user service now; it is enabled for the next login: %v", err)
		}
	}

	return Result{BinaryPath: binaryPath, UnitPath: unitPath, RuleChanged: ruleChanged, BinaryChanged: binaryChanged, UnitChanged: unitChanged}, nil
}

func EnsureCredentialOwnership(path string, target TargetUser) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect credential file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("credential file must be a regular file, not a symlink or special file: %s", path)
	}
	if err := changeOwner(path, target.UID, target.GID); err != nil {
		return fmt.Errorf("set credential ownership: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("set credential mode: %w", err)
	}
	directory := filepath.Dir(path)
	if err := changeOwner(directory, target.UID, target.GID); err != nil {
		return fmt.Errorf("set credential directory ownership: %w", err)
	}
	return os.Chmod(directory, 0o700)
}

func PrepareCredentialDirectory(path string, target TargetUser) error {
	directory := filepath.Dir(path)
	return prepareUserDirectory(directory, target, 0o700)
}

func prepareUserDirectory(path string, target TargetUser, leafMode os.FileMode) error {
	path = filepath.Clean(path)
	relative, err := filepath.Rel(target.Home, path)
	insideHome := err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
	if !insideHome {
		if err := os.MkdirAll(path, leafMode); err != nil {
			return fmt.Errorf("create user directory: %w", err)
		}
		if err := changeOwner(path, target.UID, target.GID); err != nil {
			return fmt.Errorf("set user directory ownership: %w", err)
		}
		return os.Chmod(path, leafMode)
	}
	current := filepath.Clean(target.Home)
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		mode := os.FileMode(0o755)
		if current == path {
			mode = leafMode
		}
		if err := os.Mkdir(current, mode); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("create user directory %s: %w", current, err)
		}
		if err := changeOwner(current, target.UID, target.GID); err != nil {
			return fmt.Errorf("set user directory ownership for %s: %w", current, err)
		}
		if current == path {
			if err := os.Chmod(current, mode); err != nil {
				return err
			}
		}
	}
	return nil
}

func validate(config Config) error {
	if config.Scope != ScopeCLI && config.Scope != ScopeUser && config.Scope != ScopeSystem {
		return fmt.Errorf("invalid setup scope %q (expected cli, user, or system)", config.Scope)
	}
	if config.Target.Name == "" || config.Target.Home == "" || config.Target.UID < 0 || config.Target.GID < 0 {
		return errors.New("target user is incomplete")
	}
	paths := map[string]string{
		"install directory": config.InstallDir,
		"source executable": config.SourceExecutable,
	}
	if config.Scope != ScopeCLI {
		paths["credential path"] = config.CredentialsPath
	}
	for name, value := range paths {
		if !filepath.IsAbs(value) {
			return fmt.Errorf("%s must be an absolute path: %s", name, value)
		}
		if strings.ContainsAny(value, "\n\r\x00") {
			return fmt.Errorf("%s contains invalid characters", name)
		}
	}
	if config.Scope != ScopeCLI && (config.HomeAssistantURL == "" || strings.ContainsAny(config.HomeAssistantURL, "\n\r\x00")) {
		return errors.New("Home Assistant URL is required")
	}
	if config.Scope == ScopeSystem {
		relative, err := filepath.Rel(config.Target.Home, config.InstallDir)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("a system service cannot execute a user-home binary; use /usr/local/bin or another root-owned directory")
		}
	}
	info, err := os.Stat(config.SourceExecutable)
	if err != nil {
		return fmt.Errorf("inspect current executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("current executable is not a regular file")
	}
	return nil
}

func renderUnit(config Config, binaryPath string) ([]byte, string, error) {
	text := packaging.SystemServiceTemplate
	unitPath := rooted(config.RootDir, "/etc/systemd/system/elgatolight.service")
	if config.Scope == ScopeUser {
		text = packaging.UserServiceTemplate
		unitPath = filepath.Join(config.Target.Home, ".config/systemd/user/elgatolight.service")
	}
	tmpl, err := template.New("unit").Parse(text)
	if err != nil {
		return nil, "", fmt.Errorf("parse systemd unit template: %w", err)
	}
	values := unitValues{
		Binary:           systemdQuote(binaryPath),
		HomeAssistantURL: systemdQuote(config.HomeAssistantURL),
		Credentials:      systemdQuote(config.CredentialsPath),
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, values); err != nil {
		return nil, "", fmt.Errorf("render systemd unit: %w", err)
	}
	return output.Bytes(), unitPath, nil
}

func systemdQuote(value string) string {
	value = strings.ReplaceAll(value, "%", "%%")
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return "\"" + value + "\""
}

func rooted(root, path string) string {
	if root == "" {
		return path
	}
	return filepath.Join(root, strings.TrimPrefix(filepath.Clean(path), string(filepath.Separator)))
}

func installExecutable(source, destination string, uid, gid int) (bool, error) {
	same, err := filesEqual(source, destination)
	if err != nil {
		return false, err
	}
	if same {
		if err := os.Chmod(destination, 0o755); err != nil {
			return false, err
		}
		return false, changeOwner(destination, uid, gid)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return false, err
	}
	if err := changeOwner(filepath.Dir(destination), uid, gid); err != nil {
		return false, err
	}
	input, err := os.Open(source)
	if err != nil {
		return false, err
	}
	defer input.Close()
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".elgatolight-*")
	if err != nil {
		return false, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := io.Copy(temporary, input); err != nil {
		temporary.Close()
		return false, err
	}
	if err := temporary.Chmod(0o755); err != nil {
		temporary.Close()
		return false, err
	}
	if err := changeOwner(temporaryPath, uid, gid); err != nil {
		temporary.Close()
		return false, err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return false, err
	}
	if err := temporary.Close(); err != nil {
		return false, err
	}
	return true, os.Rename(temporaryPath, destination)
}

func filesEqual(left, right string) (bool, error) {
	rightInfo, err := os.Lstat(right)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !rightInfo.Mode().IsRegular() {
		return false, fmt.Errorf("destination exists and is not a regular file: %s", right)
	}
	leftData, err := os.ReadFile(left)
	if err != nil {
		return false, err
	}
	rightData, err := os.ReadFile(right)
	if err != nil {
		return false, err
	}
	return sha256.Sum256(leftData) == sha256.Sum256(rightData), nil
}

func writeManagedFile(path string, content []byte, mode os.FileMode, uid, gid int, allowLegacy bool) (bool, error) {
	info, statErr := os.Lstat(path)
	if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("refusing to replace symlink %s", path)
	}
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return false, statErr
	}
	existing, err := os.ReadFile(path)
	if err == nil {
		if bytes.Equal(existing, content) {
			if err := os.Chmod(path, mode); err != nil {
				return false, err
			}
			return false, changeOwner(path, uid, gid)
		}
		managed := bytes.HasPrefix(existing, []byte(managedMarker))
		legacy := allowLegacy && bytes.Equal(existing, bytes.TrimPrefix(content, []byte(managedMarker+"\n")))
		if !managed && !legacy {
			return false, fmt.Errorf("refusing to overwrite unmanaged file %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".elgatolight-managed-*")
	if err != nil {
		return false, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return false, err
	}
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return false, err
	}
	if err := changeOwner(temporaryPath, uid, gid); err != nil {
		temporary.Close()
		return false, err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return false, err
	}
	if err := temporary.Close(); err != nil {
		return false, err
	}
	return true, os.Rename(temporaryPath, path)
}

func enableUserUnit(unitPath string, target TargetUser) error {
	wantsDir := filepath.Join(filepath.Dir(unitPath), "default.target.wants")
	if err := os.MkdirAll(wantsDir, 0o755); err != nil {
		return fmt.Errorf("create user systemd wants directory: %w", err)
	}
	if err := changeOwner(filepath.Dir(unitPath), target.UID, target.GID); err != nil {
		return err
	}
	if err := changeOwner(wantsDir, target.UID, target.GID); err != nil {
		return err
	}
	link := filepath.Join(wantsDir, "elgatolight.service")
	want := "../elgatolight.service"
	existing, err := os.Readlink(link)
	if err == nil {
		if existing == want {
			return nil
		}
		return fmt.Errorf("refusing to replace unexpected user service link %s -> %s", link, existing)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect user service link: %w", err)
	}
	if err := os.Symlink(want, link); err != nil {
		return fmt.Errorf("enable user service: %w", err)
	}
	return changeLinkOwner(link, target.UID, target.GID)
}
