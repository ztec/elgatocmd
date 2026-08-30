//go:build linux

package cli

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

	"git2.riper.fr/ztec/elgatocmd/internal/homeassistant"
	"git2.riper.fr/ztec/elgatocmd/internal/installer"

	"github.com/spf13/cobra"
)

type setupFlags struct {
	scope     string
	assumeYes bool
}

type resolvedSetup struct {
	scope            installer.Scope
	target           installer.TargetUser
	binaryPath       string
	homeAssistantURL string
	credentialsPath  string
}

const setupScopeHelp = `Service options:
  user    Run without sudo; start Home Assistant when the current user logs in.
  system  Run with sudo; start the Home Assistant service when the computer boots.
  none    Install USB permissions only and use the command-line interface.`

func (app *commandApp) setupCommand() *cobra.Command {
	flags := setupFlags{}
	command := &cobra.Command{
		Use:   "setup",
		Short: "Configure USB access and optionally a daemon service",
		Long:  "Configure USB access and optionally a daemon service.\n\n" + setupScopeHelp,
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			effectiveUID := os.Geteuid()
			resolved, err := app.resolveSetup(command, flags, stdinIsTerminal(), effectiveUID)
			if err != nil {
				return err
			}
			skipUSBRule := false
			if effectiveUID != 0 {
				if err := elevateUSBSetup(command); err != nil {
					return err
				}
				skipUSBRule = true
			}
			if resolved.scope != installer.ScopeNone {
				if err := app.ensureSetupPairing(command, resolved); err != nil {
					return err
				}
			}
			result, err := installer.Apply(installer.Config{
				Scope: resolved.scope, Target: resolved.target, BinaryPath: resolved.binaryPath,
				HomeAssistantURL: resolved.homeAssistantURL, CredentialsPath: resolved.credentialsPath,
				SkipUSBRule: skipUSBRule,
				Run:         setupCommandRunner(command),
				Warnf: func(format string, values ...any) {
					fmt.Fprintf(command.ErrOrStderr(), "warning: "+format+"\n", values...)
				},
			})
			if err != nil {
				return err
			}
			if result.UnitPath == "" {
				fmt.Fprintln(command.OutOrStdout(), "CLI-only setup selected; no daemon service was configured.")
			} else {
				fmt.Fprintf(command.OutOrStdout(), "Configured %s systemd service at %s.\n", resolved.scope, result.UnitPath)
			}
			fmt.Fprintln(command.OutOrStdout(), "USB permissions are installed; unplug and reconnect the light once.")
			return nil
		},
	}
	command.Flags().StringVar(&flags.scope, "scope", "", "service option: user, system, or none")
	command.Flags().BoolVarP(&flags.assumeYes, "yes", "y", false, "apply setup without the final confirmation prompt")
	return command
}

func (app *commandApp) setupUSBCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "setup-usb",
		Short:  "Install USB permissions",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if os.Geteuid() != 0 {
				return errors.New("USB permission installation needs root access")
			}
			_, err := installer.Apply(installer.Config{
				Scope: installer.ScopeNone,
				Run:   setupCommandRunner(command),
			})
			return err
		},
	}
}

func setupCommandRunner(command *cobra.Command) installer.CommandRunner {
	return func(name string, args ...string) error {
		child := exec.Command(name, args...)
		child.Stdin = command.InOrStdin()
		child.Stdout = command.OutOrStdout()
		child.Stderr = command.ErrOrStderr()
		return child.Run()
	}
}

func elevateUSBSetup(command *cobra.Command) error {
	binaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate installed executable for USB setup: %w", err)
	}
	if resolvedPath, resolveErr := filepath.EvalSymlinks(binaryPath); resolveErr == nil {
		binaryPath = resolvedPath
	}
	fmt.Fprintln(command.OutOrStdout(), "Installing USB permissions; sudo may ask for your password.")
	if err := setupCommandRunner(command)("sudo", "--", binaryPath, "setup-usb"); err != nil {
		return fmt.Errorf("install USB permissions with sudo: %w", err)
	}
	return nil
}

func (app *commandApp) resolveSetup(command *cobra.Command, flags setupFlags, interactive bool, effectiveUID int) (resolvedSetup, error) {
	reader := bufio.NewReader(command.InOrStdin())
	scopeValue := strings.TrimSpace(flags.scope)
	if scopeValue == "" {
		if effectiveUID == 0 {
			scopeValue = string(installer.ScopeSystem)
		} else {
			scopeValue = string(installer.ScopeUser)
		}
	}
	if interactive && strings.TrimSpace(flags.scope) == "" {
		fmt.Fprintln(command.OutOrStdout(), setupScopeHelp)
		fmt.Fprintf(command.OutOrStdout(), "\nDetected %s setup from the current privileges.\n", scopeValue)
	}
	scope := installer.Scope(strings.ToLower(scopeValue))
	if scope != installer.ScopeNone && scope != installer.ScopeUser && scope != installer.ScopeSystem {
		return resolvedSetup{}, fmt.Errorf("invalid --scope %q (expected user, system, or none)", scopeValue)
	}
	if scope == installer.ScopeUser && effectiveUID == 0 {
		return resolvedSetup{}, errors.New("user service setup must run without sudo; rerun elgatolight setup as the login user")
	}
	if scope == installer.ScopeSystem && effectiveUID != 0 {
		return resolvedSetup{}, errors.New("system service setup needs root access; rerun sudo elgatolight setup")
	}

	var err error
	targetName := ""
	if scope == installer.ScopeSystem {
		targetName = "root"
	} else if scope == installer.ScopeUser {
		account, currentErr := user.Current()
		if currentErr != nil {
			return resolvedSetup{}, fmt.Errorf("look up current user: %w", currentErr)
		}
		targetName = account.Username
	}
	target := installer.TargetUser{}
	if scope != installer.ScopeNone {
		target, err = lookupTargetUser(targetName)
		if err != nil {
			return resolvedSetup{}, err
		}
	}

	normalizedURLString := ""
	if scope != installer.ScopeNone {
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

	binaryPath := ""
	if scope != installer.ScopeNone {
		binaryPath, err = os.Executable()
		if err != nil {
			return resolvedSetup{}, fmt.Errorf("locate installed executable: %w", err)
		}
		if resolvedPath, resolveErr := filepath.EvalSymlinks(binaryPath); resolveErr == nil {
			binaryPath = resolvedPath
		}
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
	if scope == installer.ScopeNone {
		credentialsPath = ""
	} else if credentialExplicit {
		credentialsPath = expandTargetHome(app.config.GetString("home_assistant.credentials"), target.Home)
	} else if scope == installer.ScopeSystem {
		credentialsPath = "/var/lib/elgatolight/credentials.json"
	} else {
		credentialsPath = filepath.Join(target.Home, ".local", "state", "elgatolight", "credentials.json")
	}
	if scope != installer.ScopeNone && !filepath.IsAbs(credentialsPath) {
		return resolvedSetup{}, fmt.Errorf("--credentials must be absolute: %s", credentialsPath)
	}

	resolved := resolvedSetup{
		scope: scope, target: target, binaryPath: binaryPath,
		homeAssistantURL: normalizedURLString, credentialsPath: credentialsPath,
	}
	if !flags.assumeYes {
		if !interactive {
			return resolvedSetup{}, errors.New("--yes is required when setup is not interactive")
		}
		fmt.Fprintf(command.OutOrStdout(), "\nConfigure %s service option\n", resolved.scope)
		if resolved.scope != installer.ScopeNone {
			fmt.Fprintf(command.OutOrStdout(), "  user: %s\n  binary: %s\n  Home Assistant: %s\n  credentials: %s\n",
				resolved.target.Name, resolved.binaryPath, resolved.homeAssistantURL, resolved.credentialsPath)
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
