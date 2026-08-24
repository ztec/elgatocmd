package homeassistant

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeURL(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		input string
		want  string
		ok    bool
	}{
		{"https://ha.example.test/", "https://ha.example.test", true},
		{" http://ha.local:8123/base/ ", "http://ha.local:8123/base", true},
		{"ha.local:8123", "", false},
		{"ftp://ha.local", "", false},
		{"https://user:pass@ha.local", "", false},
		{"https://ha.local/?query=yes", "", false},
	} {
		t.Run(test.input, func(t *testing.T) {
			parsed, err := NormalizeURL(test.input)
			if test.ok != (err == nil) {
				t.Fatalf("NormalizeURL(%q) error = %v", test.input, err)
			}
			if test.ok && parsed.String() != test.want {
				t.Fatalf("NormalizeURL(%q) = %q, want %q", test.input, parsed, test.want)
			}
		})
	}
}

func TestCredentialStorePermissionsAndRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "credentials.json")
	store := CredentialStore{Path: path}
	want := Credentials{
		HomeAssistantURL: "https://ha.example.test", ClientID: "http://127.0.0.1:18443/",
		RefreshToken: "secret", InstanceID: "instance", CreatedAt: "2026-08-24T17:00:00Z",
	}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential mode = %o, want 600", info.Mode().Perm())
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("credentials = %#v, want %#v", got, want)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil || !strings.Contains(err.Error(), "mode 0600") {
		t.Fatalf("Load with broad permissions error = %v", err)
	}
	if err := store.Delete(); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(); err != nil {
		t.Fatalf("Delete must be idempotent: %v", err)
	}
}

func TestTokenRefreshAndRevoke(t *testing.T) {
	t.Parallel()
	var refreshSeen, revokeSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/base/auth/token":
			if err := request.ParseForm(); err != nil {
				t.Error(err)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			if request.Form.Get("refresh_token") == "invalid" {
				writer.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(writer, `{"error":"invalid_grant","error_description":"revoked"}`)
				return
			}
			refreshSeen = request.Form.Get("grant_type") == "refresh_token" &&
				request.Form.Get("refresh_token") == "refresh" && request.Form.Get("client_id") == "client"
			_ = json.NewEncoder(writer).Encode(TokenResponse{AccessToken: "access", ExpiresIn: 1800, TokenType: "Bearer"})
		case "/base/auth/revoke":
			body, _ := io.ReadAll(request.Body)
			revokeSeen = string(body) == "token=refresh"
			writer.WriteHeader(http.StatusOK)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	credentials := Credentials{HomeAssistantURL: server.URL + "/base", ClientID: "client", RefreshToken: "refresh"}
	client := AuthClient{HTTPClient: server.Client()}
	token, err := client.Refresh(context.Background(), credentials)
	if err != nil {
		t.Fatal(err)
	}
	if token.AccessToken != "access" || !refreshSeen {
		t.Fatalf("refresh returned %#v, request seen = %v", token, refreshSeen)
	}
	if err := client.Revoke(context.Background(), credentials); err != nil {
		t.Fatal(err)
	}
	if !revokeSeen {
		t.Fatal("revoke token request was not observed")
	}
	credentials.RefreshToken = "invalid"
	if _, err := client.Refresh(context.Background(), credentials); !errors.Is(err, ErrAuthorizationRejected) {
		t.Fatalf("invalid refresh error = %v, want ErrAuthorizationRejected", err)
	}
}

func TestPairAuthorizationCodeFlow(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	callback := "http://" + listener.Addr().String() + "/oauth/callback"
	_ = listener.Close()

	var exchange url.Values
	ha := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/auth/token" {
			http.NotFound(writer, request)
			return
		}
		if err := request.ParseForm(); err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		exchange = request.Form
		_ = json.NewEncoder(writer).Encode(TokenResponse{
			AccessToken: "access", RefreshToken: "refresh", ExpiresIn: 1800, TokenType: "Bearer",
		})
	}))
	defer ha.Close()

	client := AuthClient{
		HTTPClient: ha.Client(), Output: io.Discard,
		OpenBrowser: func(target string) error {
			authorize, parseErr := url.Parse(target)
			if parseErr != nil {
				return parseErr
			}
			go func() {
				callbackURL, _ := url.Parse(authorize.Query().Get("redirect_uri"))
				query := callbackURL.Query()
				query.Set("code", "authorization-code")
				query.Set("state", authorize.Query().Get("state"))
				callbackURL.RawQuery = query.Encode()
				response, callbackErr := http.Get(callbackURL.String()) //nolint:gosec // loopback test callback
				if callbackErr == nil {
					_ = response.Body.Close()
				}
			}()
			return nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	token, clientID, err := client.Pair(ctx, ha.URL, callback)
	if err != nil {
		t.Fatal(err)
	}
	if token.RefreshToken != "refresh" || clientID != "http://"+listener.Addr().String()+"/" {
		t.Fatalf("Pair returned token %#v, client ID %q", token, clientID)
	}
	if exchange.Get("grant_type") != "authorization_code" || exchange.Get("code") != "authorization-code" || exchange.Get("client_id") != clientID {
		t.Fatalf("unexpected token exchange: %v", exchange)
	}
}

func TestPairRejectsInvalidCallbacksAndState(t *testing.T) {
	t.Parallel()
	for _, callback := range []string{
		"https://127.0.0.1:18443/oauth/callback",
		"http://localhost:18443/oauth/callback",
		"http://127.0.0.1/oauth/callback",
		"http://127.0.0.1:18443/oauth/callback?unexpected=true",
	} {
		if _, _, err := (AuthClient{}).Pair(context.Background(), "http://ha.local:8123", callback); err == nil {
			t.Errorf("Pair accepted callback %q", callback)
		}
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	callback := "http://" + listener.Addr().String() + "/oauth/callback"
	_ = listener.Close()
	client := AuthClient{Output: io.Discard, OpenBrowser: func(target string) error {
		authorize, _ := url.Parse(target)
		go func() {
			callbackURL, _ := url.Parse(authorize.Query().Get("redirect_uri"))
			query := callbackURL.Query()
			query.Set("code", "code")
			query.Set("state", "attacker-state")
			callbackURL.RawQuery = query.Encode()
			response, callbackErr := http.Get(callbackURL.String()) //nolint:gosec // loopback test callback
			if callbackErr == nil {
				_ = response.Body.Close()
			}
		}()
		return nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := client.Pair(ctx, "http://ha.local:8123", callback); err == nil || !strings.Contains(err.Error(), "invalid OAuth state") {
		t.Fatalf("Pair invalid-state error = %v", err)
	}
}

func TestRefreshValidatesTLSCertificates(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(TokenResponse{AccessToken: "access"})
	}))
	defer server.Close()
	credentials := Credentials{HomeAssistantURL: server.URL, ClientID: "client", RefreshToken: "refresh"}
	client := AuthClient{HTTPClient: &http.Client{Timeout: 2 * time.Second}}
	if _, err := client.Refresh(context.Background(), credentials); err == nil {
		t.Fatal("Refresh accepted an untrusted TLS certificate")
	}
}
