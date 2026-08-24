//go:build linux

// Package installer configures the USB rule and optional systemd unit.
package installer

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"elgatolight/packaging"
)

type Scope string

const (
	ScopeNone   Scope = "none"
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
	BinaryPath       string
	HomeAssistantURL string
	CredentialsPath  string
	RootDir          string
	SkipUSBRule      bool
	Run              CommandRunner
	Warnf            func(string, ...any)
}

type Result struct {
	UnitPath    string
	RuleChanged bool
	UnitChanged bool
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

	ruleChanged := false
	if !config.SkipUSBRule {
		rulePath := rooted(config.RootDir, "/etc/udev/rules.d/99-elgato-key-light-neo.rules")
		var err error
		ruleChanged, err = writeManagedFile(rulePath, []byte(packaging.UdevRule), 0o644, 0, 0, true)
		if err != nil {
			return Result{}, fmt.Errorf("install udev rule: %w", err)
		}
		if ruleChanged {
			if err := config.Run("udevadm", "control", "--reload-rules"); err != nil {
				return Result{}, fmt.Errorf("reload udev rules: %w", err)
			}
		}
	}
	if config.Scope == ScopeNone {
		return Result{RuleChanged: ruleChanged}, nil
	}

	unit, unitPath, err := renderUnit(config, config.BinaryPath)
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
		if err := config.Run("systemctl", "restart", "--no-block", "elgatolight.service"); err != nil {
			return Result{}, fmt.Errorf("start or restart system service: %w", err)
		}
	} else {
		if err := enableUserUnit(unitPath, config.Target); err != nil {
			return Result{}, err
		}
		if err := config.Run("systemctl", "--user", "daemon-reload"); err != nil {
			config.Warnf("could not reload the active user systemd manager; the service is enabled for the next login: %v", err)
		} else if err := config.Run("systemctl", "--user", "restart", "--no-block", "elgatolight.service"); err != nil {
			config.Warnf("could not start the user service now; it is enabled for the next login: %v", err)
		}
	}

	return Result{UnitPath: unitPath, RuleChanged: ruleChanged, UnitChanged: unitChanged}, nil
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
	if config.Scope != ScopeNone && config.Scope != ScopeUser && config.Scope != ScopeSystem {
		return fmt.Errorf("invalid setup scope %q (expected user, system, or none)", config.Scope)
	}
	if config.Scope != ScopeNone && (config.Target.Name == "" || config.Target.Home == "" || config.Target.UID < 0 || config.Target.GID < 0) {
		return errors.New("target user is incomplete")
	}
	paths := map[string]string{}
	if config.Scope != ScopeNone {
		paths["binary path"] = config.BinaryPath
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
	if config.Scope != ScopeNone && (config.HomeAssistantURL == "" || strings.ContainsAny(config.HomeAssistantURL, "\n\r\x00")) {
		return errors.New("Home Assistant URL is required")
	}
	if config.Scope != ScopeNone {
		info, err := os.Stat(config.BinaryPath)
		if err != nil {
			return fmt.Errorf("inspect installed executable: %w", err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return errors.New("installed executable must be a regular executable file")
		}
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
