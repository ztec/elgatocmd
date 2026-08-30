package cli

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"git2.riper.fr/ztec/elgatocmd/internal/daemon"
	"git2.riper.fr/ztec/elgatocmd/internal/homeassistant"
	"git2.riper.fr/ztec/elgatocmd/internal/lights"

	"github.com/spf13/cobra"
)

func defaultCredentialPath() string {
	stateDir := os.Getenv("XDG_STATE_HOME")
	if stateDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "credentials.json"
		}
		stateDir = filepath.Join(homeDir, ".local", "state")
	}
	return filepath.Join(stateDir, "elgatolight", "credentials.json")
}

func (app *commandApp) pairCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "pair",
		Short: "Authorize this daemon with Home Assistant",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if app.config.GetString("home_assistant.url") == "" {
				return errors.New("--ha-url is required for pairing")
			}
			return app.pairHomeAssistant(command)
		},
	}
}

func (app *commandApp) authCommand() *cobra.Command {
	auth := &cobra.Command{Use: "auth", Short: "Inspect or revoke Home Assistant authorization", Args: cobra.NoArgs}
	auth.AddCommand(
		&cobra.Command{
			Use: "status", Short: "Show stored authorization metadata", Args: cobra.NoArgs,
			RunE: func(command *cobra.Command, _ []string) error {
				path := app.config.GetString("home_assistant.credentials")
				credentials, err := (homeassistant.CredentialStore{Path: path}).Load()
				if err != nil {
					return fmt.Errorf("load Home Assistant credentials: %w", err)
				}
				status := struct {
					HomeAssistantURL string `json:"homeAssistantUrl"`
					InstanceID       string `json:"instanceId"`
					CreatedAt        string `json:"createdAt"`
					CredentialFile   string `json:"credentialFile"`
				}{credentials.HomeAssistantURL, credentials.InstanceID, credentials.CreatedAt, path}
				if app.config.GetBool("json") {
					return printJSON(command.OutOrStdout(), status, true)
				}
				_, err = fmt.Fprintf(command.OutOrStdout(), "Home Assistant: %s\nInstance: %s\nPaired: %s\nCredentials: %s\n", status.HomeAssistantURL, status.InstanceID, status.CreatedAt, status.CredentialFile)
				return err
			},
		},
		&cobra.Command{
			Use: "revoke", Aliases: []string{"logout"}, Short: "Revoke and remove stored authorization", Args: cobra.NoArgs,
			RunE: func(command *cobra.Command, _ []string) error {
				store := homeassistant.CredentialStore{Path: app.config.GetString("home_assistant.credentials")}
				credentials, err := store.Load()
				if err != nil {
					return fmt.Errorf("load Home Assistant credentials: %w", err)
				}
				client := homeassistant.AuthClient{HTTPClient: app.homeAssistantHTTPClient(), Output: command.OutOrStdout()}
				ctx, cancel := context.WithTimeout(app.ctx, 15*time.Second)
				err = client.Revoke(ctx, credentials)
				cancel()
				if err != nil {
					return err
				}
				if err := store.Delete(); err != nil {
					return fmt.Errorf("remove credential file: %w", err)
				}
				_, err = fmt.Fprintln(command.OutOrStdout(), "Home Assistant authorization revoked.")
				return err
			},
		},
	)
	return auth
}

func (app *commandApp) daemonCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "daemon",
		Short: "Push USB lights to Home Assistant",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if app.config.GetString("light") != "" || app.config.GetString("device") != "" {
				return errors.New("daemon always manages every light; --light and --device are not supported")
			}
			store := homeassistant.CredentialStore{Path: app.config.GetString("home_assistant.credentials")}
			credentials, err := store.Load()
			if errors.Is(err, fs.ErrNotExist) && app.config.GetString("home_assistant.url") != "" {
				if !stdinIsTerminal() {
					return fmt.Errorf("Home Assistant is not paired; run elgatolight pair --ha-url %s interactively first", app.config.GetString("home_assistant.url"))
				}
				if err := app.pairHomeAssistant(command); err != nil {
					return err
				}
				credentials, err = store.Load()
			}
			if err != nil {
				return fmt.Errorf("load Home Assistant credentials (run elgatolight pair --ha-url URL first): %w", err)
			}
			if configuredURL := app.config.GetString("home_assistant.url"); configuredURL != "" {
				normalized, normalizeErr := homeassistant.NormalizeURL(configuredURL)
				if normalizeErr != nil {
					return normalizeErr
				}
				stored, _ := homeassistant.NormalizeURL(credentials.HomeAssistantURL)
				if stored == nil || normalized.String() != stored.String() {
					return errors.New("--ha-url differs from the paired instance; run elgatolight pair for the new URL")
				}
			}
			manager, err := lights.NewManager(lights.Config{
				PollInterval: app.config.GetDuration("daemon.poll_interval"), ReconcileInterval: app.config.GetDuration("daemon.reconcile_interval"),
				RequestTimeout: app.config.GetDuration("timeout"),
			})
			if err != nil {
				return err
			}
			logger := log.New(command.ErrOrStderr(), "elgatolight: ", log.LstdFlags|log.Lmsgprefix)
			return daemon.Run(app.ctx, daemon.Config{
				Credentials: credentials,
				Auth:        homeassistant.AuthClient{HTTPClient: app.homeAssistantHTTPClient(), Output: command.OutOrStdout()},
				HTTPClient:  app.homeAssistantHTTPClient(), Manager: manager, Version: applicationVersion(),
				CallTimeout: app.config.GetDuration("daemon.call_timeout"), MinBackoff: app.config.GetDuration("daemon.min_backoff"),
				MaxBackoff: app.config.GetDuration("daemon.max_backoff"), Logf: logger.Printf,
			})
		},
	}
	command.Flags().Duration("poll-interval", 250*time.Millisecond, "USB state polling interval")
	command.Flags().Duration("reconcile-interval", time.Second, "USB hotplug discovery interval")
	command.Flags().Duration("call-timeout", 10*time.Second, "Home Assistant command timeout")
	command.Flags().Duration("min-backoff", time.Second, "initial Home Assistant reconnect delay")
	command.Flags().Duration("max-backoff", 30*time.Second, "maximum Home Assistant reconnect delay")
	for flag, key := range map[string]string{
		"poll-interval": "daemon.poll_interval", "reconcile-interval": "daemon.reconcile_interval",
		"call-timeout": "daemon.call_timeout", "min-backoff": "daemon.min_backoff", "max-backoff": "daemon.max_backoff",
	} {
		if err := app.config.BindPFlag(key, command.Flags().Lookup(flag)); err != nil {
			panic(fmt.Sprintf("bind daemon --%s: %v", flag, err))
		}
	}
	return command
}

func (app *commandApp) pairHomeAssistant(command *cobra.Command) error {
	rawURL := app.config.GetString("home_assistant.url")
	auth := homeassistant.AuthClient{HTTPClient: app.homeAssistantHTTPClient(), Output: command.OutOrStdout()}
	token, clientID, err := auth.Pair(app.ctx, rawURL, app.config.GetString("home_assistant.oauth_callback"))
	if err != nil {
		return err
	}
	normalized, err := homeassistant.NormalizeURL(rawURL)
	if err != nil {
		return err
	}
	verifyCtx, cancel := context.WithTimeout(app.ctx, 15*time.Second)
	ws, err := homeassistant.DialWebSocket(verifyCtx, normalized.String(), token.AccessToken, app.homeAssistantHTTPClient())
	if err == nil {
		_, err = ws.Call(verifyCtx, map[string]any{"type": homeassistant.TypeInfo})
		_ = ws.Close()
	}
	cancel()
	if err != nil {
		return fmt.Errorf("Home Assistant authorization succeeded, but the Elgato USB Light Bridge integration is not ready: %w", err)
	}
	if token.RefreshToken == "" {
		return errors.New("Home Assistant did not return a refresh token")
	}
	path := app.config.GetString("home_assistant.credentials")
	store := homeassistant.CredentialStore{Path: path}
	instanceID := ""
	if existing, loadErr := store.Load(); loadErr == nil {
		instanceID = existing.InstanceID
	}
	if instanceID == "" {
		instanceID, err = homeassistant.NewInstanceID()
		if err != nil {
			return err
		}
	}
	credentials := homeassistant.Credentials{
		HomeAssistantURL: normalized.String(), ClientID: clientID, RefreshToken: token.RefreshToken,
		InstanceID: instanceID, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := store.Save(credentials); err != nil {
		return fmt.Errorf("save Home Assistant credentials: %w", err)
	}
	_, err = fmt.Fprintf(command.OutOrStdout(), "Paired with %s. Credentials saved to %s.\n", normalized.Redacted(), path)
	return err
}

func (app *commandApp) homeAssistantHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if app.config.GetBool("home_assistant.insecure_skip_tls_verify") {
		// This is intentionally available only as an explicit, visibly unsafe
		// development option for private installations with incomplete TLS.
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}
	return &http.Client{Transport: transport}
}

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
