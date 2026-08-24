package homeassistant

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const DefaultOAuthCallback = "http://127.0.0.1:18443/oauth/callback"

// ErrAuthorizationRejected identifies a token request that cannot recover by
// retrying. The daemon should ask the operator to pair again.
var ErrAuthorizationRejected = errors.New("Home Assistant authorization rejected")

// Credentials are the durable, secret daemon authorization state.
type Credentials struct {
	HomeAssistantURL string `json:"homeAssistantUrl"`
	ClientID         string `json:"clientId"`
	RefreshToken     string `json:"refreshToken"`
	InstanceID       string `json:"instanceId"`
	CreatedAt        string `json:"createdAt"`
}

// TokenResponse is returned by Home Assistant's OAuth-compatible token API.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
}

// AuthClient implements Home Assistant's authorization-code and refresh flow.
type AuthClient struct {
	HTTPClient  *http.Client
	OpenBrowser func(string) error
	Output      io.Writer
}

// Pair opens a local callback, asks the user to authorize in a browser, and
// returns the resulting token. The callback URL is also the IndieAuth client
// origin, which Home Assistant accepts without a public client registration.
func (a AuthClient) Pair(ctx context.Context, rawHAURL, callback string) (TokenResponse, string, error) {
	haURL, err := NormalizeURL(rawHAURL)
	if err != nil {
		return TokenResponse{}, "", err
	}
	callbackURL, err := url.Parse(callback)
	if err != nil {
		return TokenResponse{}, "", fmt.Errorf("parse OAuth callback: %w", err)
	}
	if callbackURL.Scheme != "http" || callbackURL.Hostname() != "127.0.0.1" || callbackURL.Port() == "" ||
		callbackURL.User != nil || callbackURL.RawQuery != "" || callbackURL.Fragment != "" {
		return TokenResponse{}, "", errors.New("OAuth callback must be an http://127.0.0.1:PORT URL")
	}
	if callbackURL.Path == "" {
		callbackURL.Path = "/"
	}
	clientID := callbackURL.Scheme + "://" + callbackURL.Host + "/"
	listener, err := net.Listen("tcp", callbackURL.Host)
	if err != nil {
		return TokenResponse{}, "", fmt.Errorf("listen for OAuth callback on %s: %w", callbackURL.Host, err)
	}
	defer listener.Close()

	state, err := randomHex(32)
	if err != nil {
		return TokenResponse{}, "", err
	}
	type callbackResult struct {
		code string
		err  error
	}
	result := make(chan callbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(callbackURL.Path, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("state") != state {
			http.Error(writer, "Invalid OAuth state. Return to the terminal and try again.", http.StatusBadRequest)
			select {
			case result <- callbackResult{err: errors.New("Home Assistant returned an invalid OAuth state")}:
			default:
			}
			return
		}
		if oauthError := request.URL.Query().Get("error"); oauthError != "" {
			description := request.URL.Query().Get("error_description")
			http.Error(writer, "Authorization failed. Return to the terminal.", http.StatusBadRequest)
			select {
			case result <- callbackResult{err: fmt.Errorf("Home Assistant authorization failed: %s: %s", oauthError, description)}:
			default:
			}
			return
		}
		code := request.URL.Query().Get("code")
		if code == "" {
			http.Error(writer, "Missing authorization code. Return to the terminal.", http.StatusBadRequest)
			select {
			case result <- callbackResult{err: errors.New("Home Assistant callback did not contain an authorization code")}:
			default:
			}
			return
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(writer, "<!doctype html><title>Elgato light connected</title><p>Authorization complete. You can close this tab.</p>")
		select {
		case result <- callbackResult{code: code}:
		default:
		}
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	serveDone := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveDone <- err
	}()

	authorizeURL := haURL.ResolveReference(&url.URL{Path: joinURLPath(haURL.Path, "/auth/authorize")})
	query := authorizeURL.Query()
	query.Set("response_type", "code")
	query.Set("client_id", clientID)
	query.Set("redirect_uri", callbackURL.String())
	query.Set("state", state)
	authorizeURL.RawQuery = query.Encode()
	output := a.Output
	if output == nil {
		output = io.Discard
	}
	fmt.Fprintf(output, "Open this URL to authorize elgatolight:\n%s\n", authorizeURL.Redacted())
	open := a.OpenBrowser
	if open == nil {
		open = openBrowser
	}
	_ = open(authorizeURL.String())

	var callbackResultValue callbackResult
	select {
	case callbackResultValue = <-result:
	case err := <-serveDone:
		if err == nil {
			err = errors.New("OAuth callback server stopped before authorization")
		}
		return TokenResponse{}, "", err
	case <-ctx.Done():
		return TokenResponse{}, "", ctx.Err()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	_ = server.Shutdown(shutdownCtx)
	cancel()
	if callbackResultValue.err != nil {
		return TokenResponse{}, "", callbackResultValue.err
	}
	token, err := a.Exchange(ctx, haURL, clientID, callbackResultValue.code)
	return token, clientID, err
}

// Exchange swaps a short authorization code for access and refresh tokens.
func (a AuthClient) Exchange(ctx context.Context, haURL *url.URL, clientID, code string) (TokenResponse, error) {
	return a.tokenRequest(ctx, haURL, url.Values{
		"grant_type": {"authorization_code"},
		"code":       {code},
		"client_id":  {clientID},
	})
}

// Refresh creates a short-lived access token from durable credentials.
func (a AuthClient) Refresh(ctx context.Context, credentials Credentials) (TokenResponse, error) {
	haURL, err := NormalizeURL(credentials.HomeAssistantURL)
	if err != nil {
		return TokenResponse{}, err
	}
	return a.tokenRequest(ctx, haURL, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {credentials.RefreshToken},
		"client_id":     {credentials.ClientID},
	})
}

// Revoke invalidates the stored refresh token and every access token derived
// from it.
func (a AuthClient) Revoke(ctx context.Context, credentials Credentials) error {
	haURL, err := NormalizeURL(credentials.HomeAssistantURL)
	if err != nil {
		return err
	}
	endpoint := haURL.ResolveReference(&url.URL{Path: joinURLPath(haURL.Path, "/auth/revoke")})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(url.Values{"token": {credentials.RefreshToken}}.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := a.httpClient().Do(request)
	if err != nil {
		return fmt.Errorf("revoke Home Assistant token: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("revoke Home Assistant token: HTTP %s", response.Status)
	}
	return nil
}

func (a AuthClient) tokenRequest(ctx context.Context, haURL *url.URL, values url.Values) (TokenResponse, error) {
	endpoint := haURL.ResolveReference(&url.URL{Path: joinURLPath(haURL.Path, "/auth/token")})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(values.Encode()))
	if err != nil {
		return TokenResponse{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := a.httpClient().Do(request)
	if err != nil {
		return TokenResponse{}, fmt.Errorf("request Home Assistant token: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return TokenResponse{}, err
	}
	if response.StatusCode != http.StatusOK {
		var oauthError struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &oauthError)
		tokenErr := fmt.Errorf("Home Assistant token request failed (HTTP %s): %s: %s", response.Status, oauthError.Error, oauthError.Description)
		if response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusUnauthorized {
			return TokenResponse{}, fmt.Errorf("%w: %v", ErrAuthorizationRejected, tokenErr)
		}
		return TokenResponse{}, tokenErr
	}
	var token TokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return TokenResponse{}, fmt.Errorf("decode Home Assistant token: %w", err)
	}
	if token.AccessToken == "" {
		return TokenResponse{}, errors.New("Home Assistant token response omitted access_token")
	}
	return token, nil
}

func (a AuthClient) httpClient() *http.Client {
	if a.HTTPClient != nil {
		return a.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// CredentialStore persists one revocable refresh token with strict file mode.
type CredentialStore struct {
	Path string
}

func (s CredentialStore) Load() (Credentials, error) {
	info, err := os.Lstat(s.Path)
	if err != nil {
		return Credentials{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return Credentials{}, fmt.Errorf("credential file %s must be a regular file accessible only by its owner (mode 0600)", s.Path)
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return Credentials{}, err
	}
	var credentials Credentials
	if err := json.Unmarshal(data, &credentials); err != nil {
		return Credentials{}, fmt.Errorf("decode credentials: %w", err)
	}
	if credentials.HomeAssistantURL == "" || credentials.ClientID == "" || credentials.RefreshToken == "" || credentials.InstanceID == "" {
		return Credentials{}, errors.New("credential file is incomplete")
	}
	return credentials, nil
}

func (s CredentialStore) Save(credentials Credentials) error {
	directory := filepath.Dir(s.Path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".credentials-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(credentials); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, s.Path)
}

func (s CredentialStore) Delete() error {
	err := os.Remove(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// NormalizeURL validates a Home Assistant HTTP(S) base URL.
func NormalizeURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("parse Home Assistant URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("Home Assistant URL must use http or https")
	}
	if parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("Home Assistant URL must contain only scheme, host, optional port, and base path")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed, nil
}

func NewInstanceID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	data[6] = (data[6] & 0x0f) | 0x40
	data[8] = (data[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", data[0:4], data[4:6], data[6:8], data[8:10], data[10:16]), nil
}

func randomHex(bytes int) (string, error) {
	data := make([]byte, bytes)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func joinURLPath(base, suffix string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(suffix, "/")
}

func openBrowser(target string) error {
	return exec.Command("xdg-open", target).Start()
}
