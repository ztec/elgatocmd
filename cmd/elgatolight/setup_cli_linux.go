//go:build linux

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"elgatolight/internal/homeassistant"
	"elgatolight/internal/installer"

	"github.com/spf13/cobra"
)

type setupFlags struct {
	scope      string
	installDir string
	targetUser string
	assumeYes  bool
}

type resolvedSetup struct {
	scope            installer.Scope
	target           installer.TargetUser
	installDir       string
	homeAssistantURL string
	credentialsPath  string
}

func (app *commandApp) setupCommand() *cobra.Command {
	flags := setupFlags{}
	command := &cobra.Command{
		Use:   "setup",
		Short: "Install the command, USB access, and optionally a daemon service",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if os.Geteuid() != 0 {
				return errors.New("setup needs root access; run sudo elgatolight setup")
			}
			resolved, err := app.resolveSetup(command, flags, stdinIsTerminal())
			if err != nil {
				return err
			}
			if resolved.scope != installer.ScopeCLI {
				if err := app.ensureSetupPairing(command, resolved); err != nil {
					return err
				}
			}
			executable, err := os.Executable()
			if err != nil {
				return fmt.Errorf("locate current executable: %w", err)
			}
			if resolvedPath, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
				executable = resolvedPath
			}
			runner := func(name string, args ...string) error {
				child := exec.Command(name, args...)
				child.Stdout = command.OutOrStdout()
				child.Stderr = command.ErrOrStderr()
				return child.Run()
			}
			result, err := installer.Apply(installer.Config{
				Scope: resolved.scope, Target: resolved.target, InstallDir: resolved.installDir,
				HomeAssistantURL: resolved.homeAssistantURL, CredentialsPath: resolved.credentialsPath,
				SourceExecutable: executable, Run: runner,
				Warnf: func(format string, values ...any) {
					fmt.Fprintf(command.ErrOrStderr(), "warning: "+format+"\n", values...)
				},
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(command.OutOrStdout(), "Installed elgatolight at %s.\n", result.BinaryPath)
			if result.UnitPath == "" {
				fmt.Fprintln(command.OutOrStdout(), "CLI-only setup selected; no daemon service was installed.")
			} else {
				fmt.Fprintf(command.OutOrStdout(), "Installed %s systemd service at %s.\n", resolved.scope, result.UnitPath)
			}
			fmt.Fprintln(command.OutOrStdout(), "USB permissions are installed; unplug and reconnect the light once.")
			return nil
		},
	}
	command.Flags().StringVar(&flags.scope, "scope", "", "setup scope: cli, user, or system")
	command.Flags().StringVar(&flags.installDir, "install-dir", "", "binary installation directory")
	command.Flags().StringVar(&flags.targetUser, "target-user", "", "login user for cli/user installation (default: SUDO_USER)")
	command.Flags().BoolVarP(&flags.assumeYes, "yes", "y", false, "apply setup without the final confirmation prompt")
	return command
}

func (app *commandApp) resolveSetup(command *cobra.Command, flags setupFlags, interactive bool) (resolvedSetup, error) {
	reader := bufio.NewReader(command.InOrStdin())
	scopeValue := strings.TrimSpace(flags.scope)
	var err error
	if scopeValue == "" {
		if !interactive {
			return resolvedSetup{}, errors.New("--scope is required when setup is not interactive (cli, user, or system)")
		}
		scopeValue, err = promptRequired(reader, command.OutOrStdout(), "Setup scope (cli/user/system)")
		if err != nil {
			return resolvedSetup{}, err
		}
	}
	scope := installer.Scope(strings.ToLower(scopeValue))
	if scope != installer.ScopeCLI && scope != installer.ScopeUser && scope != installer.ScopeSystem {
		return resolvedSetup{}, fmt.Errorf("invalid --scope %q (expected cli, user, or system)", scopeValue)
	}

	targetName := strings.TrimSpace(flags.targetUser)
	if scope == installer.ScopeSystem {
		targetName = "root"
	} else if targetName == "" {
		targetName = strings.TrimSpace(os.Getenv("SUDO_USER"))
		if targetName == "" || targetName == "root" {
			if !interactive {
				return resolvedSetup{}, errors.New("--target-user is required for cli or user scope when SUDO_USER is unavailable")
			}
			targetName, err = promptRequired(reader, command.OutOrStdout(), "Login user for the service")
			if err != nil {
				return resolvedSetup{}, err
			}
		}
	}
	target, err := lookupTargetUser(targetName)
	if err != nil {
		return resolvedSetup{}, err
	}

	normalizedURLString := ""
	if scope != installer.ScopeCLI {
		rawURL := strings.TrimSpace(app.config.GetString("home_assistant.url"))
		if rawURL == "" {
			if !interactive {
				return resolvedSetup{}, errors.New("--ha-url is required when setup is not interactive")
			}
			rawURL, err = promptRequired(reader, command.OutOrStdout(), "Home Assistant/proxy URL")
			if err != nil {
				return resolvedSetup{}, err
			}
		}
		normalizedURL, normalizeErr := homeassistant.NormalizeURL(rawURL)
		if normalizeErr != nil {
			return resolvedSetup{}, normalizeErr
		}
		normalizedURLString = normalizedURL.String()
	}

	defaultInstallDir := filepath.Join(target.Home, ".local", "bin")
	if scope == installer.ScopeSystem {
		defaultInstallDir = "/usr/local/bin"
	}
	installDir := strings.TrimSpace(flags.installDir)
	if installDir == "" && interactive {
		installDir, err = promptDefault(reader, command.OutOrStdout(), "Binary installation directory", defaultInstallDir)
		if err != nil {
			return resolvedSetup{}, err
		}
	}
	if installDir == "" {
		installDir = defaultInstallDir
	}
	installDir = expandTargetHome(installDir, target.Home)
	if !filepath.IsAbs(installDir) {
		return resolvedSetup{}, fmt.Errorf("--install-dir must be absolute: %s", installDir)
	}

	credentialsPath := ""
	credentialFlag := app.root.PersistentFlags().Lookup("credentials")
	credentialExplicit := credentialFlag != nil && credentialFlag.Changed
	if value, exists := os.LookupEnv("ELGATOLIGHT_HOME_ASSISTANT_CREDENTIALS"); exists && value != "" {
		credentialExplicit = true
	}
	if app.config.InConfig("home_assistant.credentials") {
		credentialExplicit = true
	}
	if scope == installer.ScopeCLI {
		credentialsPath = ""
	} else if credentialExplicit {
		credentialsPath = expandTargetHome(app.config.GetString("home_assistant.credentials"), target.Home)
	} else if scope == installer.ScopeSystem {
		credentialsPath = "/var/lib/elgatolight/credentials.json"
	} else {
		credentialsPath = filepath.Join(target.Home, ".local", "state", "elgatolight", "credentials.json")
	}
	if scope != installer.ScopeCLI && !filepath.IsAbs(credentialsPath) {
		return resolvedSetup{}, fmt.Errorf("--credentials must be absolute: %s", credentialsPath)
	}

	resolved := resolvedSetup{
		scope: scope, target: target, installDir: filepath.Clean(installDir),
		homeAssistantURL: normalizedURLString, credentialsPath: credentialsPath,
	}
	if !flags.assumeYes {
		if !interactive {
			return resolvedSetup{}, errors.New("--yes is required when setup is not interactive")
		}
		fmt.Fprintf(command.OutOrStdout(), "\nInstall for %s scope\n  user: %s\n  binary: %s\n",
			resolved.scope, resolved.target.Name, filepath.Join(resolved.installDir, "elgatolight"))
		if resolved.scope != installer.ScopeCLI {
			fmt.Fprintf(command.OutOrStdout(), "  Home Assistant: %s\n  credentials: %s\n",
				resolved.homeAssistantURL, resolved.credentialsPath)
		}
		confirmed, confirmErr := promptYesNo(reader, command.OutOrStdout(), "Continue", false)
		if confirmErr != nil {
			return resolvedSetup{}, confirmErr
		}
		if !confirmed {
			return resolvedSetup{}, errors.New("setup canceled")
		}
	}
	return resolved, nil
}

func (app *commandApp) ensureSetupPairing(command *cobra.Command, setup resolvedSetup) error {
	store := homeassistant.CredentialStore{Path: setup.credentialsPath}
	credentials, err := store.Load()
	if err == nil {
		storedURL, normalizeErr := homeassistant.NormalizeURL(credentials.HomeAssistantURL)
		if normalizeErr == nil && storedURL.String() == setup.homeAssistantURL {
			fmt.Fprintf(command.OutOrStdout(), "Existing Home Assistant authorization at %s matches; keeping it.\n", setup.credentialsPath)
			if setup.scope == installer.ScopeUser {
				return installer.EnsureCredentialOwnership(setup.credentialsPath, setup.target)
			}
			return nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("load existing credentials: %w", err)
	}
	if setup.scope == installer.ScopeUser {
		if err := installer.PrepareCredentialDirectory(setup.credentialsPath, setup.target); err != nil {
			return err
		}
	}
	app.config.Set("home_assistant.url", setup.homeAssistantURL)
	app.config.Set("home_assistant.credentials", setup.credentialsPath)
	if err := app.pairHomeAssistant(command); err != nil {
		return fmt.Errorf("pair Home Assistant during setup: %w", err)
	}
	if setup.scope == installer.ScopeUser {
		return installer.EnsureCredentialOwnership(setup.credentialsPath, setup.target)
	}
	return nil
}

func lookupTargetUser(name string) (installer.TargetUser, error) {
	account, err := user.Lookup(name)
	if err != nil {
		return installer.TargetUser{}, fmt.Errorf("look up target user %q: %w", name, err)
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return installer.TargetUser{}, fmt.Errorf("parse UID for %s: %w", name, err)
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return installer.TargetUser{}, fmt.Errorf("parse GID for %s: %w", name, err)
	}
	return installer.TargetUser{Name: account.Username, Home: account.HomeDir, UID: uid, GID: gid}, nil
}

func expandTargetHome(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return path
}

func promptRequired(reader *bufio.Reader, output io.Writer, label string) (string, error) {
	for {
		fmt.Fprintf(output, "%s: ", label)
		value, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		value = strings.TrimSpace(value)
		if value != "" {
			return value, nil
		}
		if errors.Is(err, io.EOF) {
			return "", fmt.Errorf("%s is required", label)
		}
	}
}

func promptDefault(reader *bufio.Reader, output io.Writer, label, defaultValue string) (string, error) {
	fmt.Fprintf(output, "%s [%s]: ", label, defaultValue)
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultValue, nil
	}
	return value, nil
}

func promptYesNo(reader *bufio.Reader, output io.Writer, label string, defaultValue bool) (bool, error) {
	suffix := "y/N"
	if defaultValue {
		suffix = "Y/n"
	}
	for {
		fmt.Fprintf(output, "%s [%s]: ", label, suffix)
		value, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "":
			return defaultValue, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			if errors.Is(err, io.EOF) {
				return false, errors.New("please answer yes or no")
			}
			fmt.Fprintln(output, "Please answer yes or no.")
		}
	}
}
