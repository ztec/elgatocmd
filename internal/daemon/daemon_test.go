package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"git2.riper.fr/ztec/elgatocmd/internal/elgato"
	"git2.riper.fr/ztec/elgatocmd/internal/hidraw"
	"git2.riper.fr/ztec/elgatocmd/internal/homeassistant"
	"git2.riper.fr/ztec/elgatocmd/internal/lights"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type bridgeFakeLight struct {
	mu     sync.Mutex
	status elgato.Status
}

func TestJitteredBackoffStaysBounded(t *testing.T) {
	for range 100 {
		delay := jitteredBackoff(time.Second)
		if delay < 800*time.Millisecond || delay > time.Second {
			t.Fatalf("jittered delay %s is outside [800ms, 1s]", delay)
		}
	}
}

func (f *bridgeFakeLight) Status(context.Context) (elgato.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, nil
}

func (f *bridgeFakeLight) AccessoryInfo(context.Context) (elgato.AccessoryInfo, error) {
	return elgato.AccessoryInfo{
		ProductName: "Key Light Neo", FirmwareVersion: "test", PowerInfo: elgato.PowerInfo{MaximumBrightness: 100},
	}, nil
}

func (f *bridgeFakeLight) Update(_ context.Context, update elgato.Update) (elgato.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	light := f.status.Lights[0]
	if update.On != nil {
		light.On = 0
		if *update.On {
			light.On = 1
		}
	}
	if update.Brightness != nil {
		light.Brightness = *update.Brightness
	}
	if update.Temperature != nil {
		mired, err := elgato.KelvinToMired(*update.Temperature)
		if err != nil {
			return elgato.Status{}, err
		}
		light.Temperature = mired
	}
	f.status.Lights[0] = light
	return f.status, nil
}

func (f *bridgeFakeLight) ApplyPreset(_ context.Context, preset int) (elgato.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if preset != 2 {
		return elgato.Status{}, fmt.Errorf("unexpected preset %d", preset)
	}
	temperature, err := elgato.KelvinToMired(3000)
	if err != nil {
		return elgato.Status{}, err
	}
	f.status.Lights[0] = elgato.Light{On: 1, Brightness: 65, Temperature: temperature}
	return f.status, nil
}

func TestDaemonPublishesSnapshotAndHandlesHACommand(t *testing.T) {
	physical := &bridgeFakeLight{status: elgato.Status{
		NumberOfLights: 1,
		Lights:         []elgato.Light{{On: 0, Brightness: 20, Temperature: 250}},
	}}
	manager, err := lights.NewManager(lights.Config{
		PollInterval: time.Second, ReconcileInterval: time.Second, RequestTimeout: time.Second,
		Find: func() ([]hidraw.Device, error) {
			return []hidraw.Device{{ID: "SERIAL-A", StableID: true, Path: "/dev/hidraw-test", Name: "Test light"}}, nil
		},
		NewClient: func(hidraw.Device) lights.DeviceClient { return physical },
	})
	if err != nil {
		t.Fatal(err)
	}

	serverErrors := make(chan error, 1)
	completed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/auth/token":
			_ = json.NewEncoder(writer).Encode(homeassistant.TokenResponse{AccessToken: "access", ExpiresIn: 1800})
		case "/api/websocket":
			connection, acceptErr := websocket.Accept(writer, request, nil)
			if acceptErr != nil {
				serverErrors <- acceptErr
				return
			}
			defer connection.CloseNow()
			if serveErr := serveDaemonTestBridge(request.Context(), connection); serveErr != nil {
				serverErrors <- serveErr
				return
			}
			completed <- struct{}{}
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan error, 1)
	var logMu sync.Mutex
	var logs []string
	go func() {
		done <- Run(ctx, Config{
			Credentials: homeassistant.Credentials{
				HomeAssistantURL: server.URL, ClientID: "client", RefreshToken: "refresh", InstanceID: "daemon-1",
			},
			Auth: homeassistant.AuthClient{HTTPClient: server.Client()}, HTTPClient: server.Client(), Manager: manager,
			CallTimeout: time.Second, MinBackoff: time.Millisecond, MaxBackoff: 10 * time.Millisecond,
			Logf: func(format string, values ...any) {
				logMu.Lock()
				logs = append(logs, fmt.Sprintf(format, values...))
				logMu.Unlock()
			},
		})
	}()

	select {
	case err := <-serverErrors:
		cancel()
		t.Fatal(err)
	case <-completed:
		cancel()
	case <-ctx.Done():
		cancel()
		t.Fatal(ctx.Err())
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	physical.mu.Lock()
	state := physical.status.Lights[0]
	physical.mu.Unlock()
	if state.On != 1 || state.Brightness != 65 || elgato.MiredToKelvin(state.Temperature) != 3003 {
		t.Fatalf("physical state after HA command = %#v", state)
	}
	logMu.Lock()
	output := strings.Join(logs, "\n")
	logMu.Unlock()
	for _, field := range []string{
		`action source=home_assistant light="SERIAL-A" request="request-1"`,
		"requested_on=true", "requested_brightness=50", "requested_temperature=4500K",
		"result=success", "on=true", "brightness=50", "temperature=4505K",
		`action source=home_assistant light="SERIAL-A" request="request-2" requested_preset=2`,
		"on=true brightness=65 temperature=3003K",
	} {
		if !strings.Contains(output, field) {
			t.Errorf("daemon log omitted %q:\n%s", field, output)
		}
	}
	if strings.Contains(output, "action source=light") {
		t.Fatalf("HA command was also logged as a physical action:\n%s", output)
	}
}

func TestActionLoggingDistinguishesPhysicalAndUpdateEvents(t *testing.T) {
	var logs []string
	config := Config{Logf: func(format string, values ...any) {
		logs = append(logs, fmt.Sprintf(format, values...))
	}}
	light := lights.Light{
		ID: "SERIAL-A", Available: true,
		State: lights.State{On: true, Brightness: 34, Temperature: 3000},
	}
	logManagerEvent(config, lights.Event{Type: lights.EventStateChanged, Source: lights.EventSourceLight, Light: light})
	if got, want := logs[len(logs)-1], `action source=light light="SERIAL-A" on=true brightness=34 temperature=3000K`; got != want {
		t.Fatalf("physical action log = %q, want %q", got, want)
	}

	before := len(logs)
	logManagerEvent(config, lights.Event{Type: lights.EventStateChanged, Source: lights.EventSourceUpdate, Light: light})
	if len(logs) != before {
		t.Fatalf("explicit update was also logged as a physical action: %v", logs)
	}

	brightness := 99
	command := homeassistant.SubscriptionEvent{
		RequestID: "request-2", DeviceID: light.ID,
		Update: lights.Update{Brightness: &brightness},
	}
	logHomeAssistantAction(config, command, lights.Light{}, errors.New("USB write failed"))
	got := logs[len(logs)-1]
	for _, field := range []string{
		`action source=home_assistant light="SERIAL-A" request="request-2"`,
		"requested_brightness=99", "result=error", `error="USB write failed"`,
	} {
		if !strings.Contains(got, field) {
			t.Errorf("failed HA action log omitted %q: %s", field, got)
		}
	}
}

func TestHandleCommandRejectsMissingOrAmbiguousOperation(t *testing.T) {
	brightness := 50
	preset := 1
	for _, command := range []homeassistant.SubscriptionEvent{
		{RequestID: "request-empty", DeviceID: "SERIAL-A"},
		{
			RequestID: "request-ambiguous", DeviceID: "SERIAL-A", Preset: &preset,
			Update: lights.Update{Brightness: &brightness},
		},
	} {
		err := handleCommand(context.Background(), Config{}, nil, "epoch", command)
		if err == nil || !strings.Contains(err.Error(), "exactly one update or preset") {
			t.Fatalf("operation validation error = %v", err)
		}
	}
}

func serveDaemonTestBridge(ctx context.Context, connection *websocket.Conn) error {
	if err := wsjson.Write(ctx, connection, map[string]any{"type": "auth_required"}); err != nil {
		return err
	}
	var auth map[string]any
	if err := wsjson.Read(ctx, connection, &auth); err != nil {
		return err
	}
	if auth["access_token"] != "access" {
		return fmt.Errorf("unexpected auth: %#v", auth)
	}
	if err := wsjson.Write(ctx, connection, map[string]any{"type": "auth_ok"}); err != nil {
		return err
	}

	info, err := readDaemonCommand(ctx, connection, homeassistant.TypeInfo)
	if err != nil {
		return err
	}
	if err := resultFor(ctx, connection, info, map[string]any{"protocolVersion": homeassistant.ProtocolVersion}); err != nil {
		return err
	}
	subscribe, err := readDaemonCommand(ctx, connection, homeassistant.TypeSubscribe)
	if err != nil {
		return err
	}
	if subscribe["instanceId"] != "daemon-1" {
		return fmt.Errorf("unexpected subscription: %#v", subscribe)
	}
	if err := resultFor(ctx, connection, subscribe, map[string]any{"protocolVersion": homeassistant.ProtocolVersion}); err != nil {
		return err
	}

	devices, _ := subscribe["devices"].([]any)
	if len(devices) == 0 {
		connected, readErr := readDaemonCommand(ctx, connection, homeassistant.TypeDeviceConnected)
		if readErr != nil {
			return readErr
		}
		if err := resultFor(ctx, connection, connected, nil); err != nil {
			return err
		}
	}
	subscriptionID := subscribe["id"]
	if err := wsjson.Write(ctx, connection, map[string]any{
		"id": subscriptionID, "type": "event",
		"event": map[string]any{
			"event": "command", "requestId": "request-1", "deviceId": "SERIAL-A",
			"update": map[string]any{"on": true, "brightness": 50, "temperature": 4500},
		},
	}); err != nil {
		return err
	}
	commandResult, err := readDaemonCommand(ctx, connection, homeassistant.TypeCommandResult)
	if err != nil {
		return err
	}
	if commandResult["success"] != true || commandResult["requestId"] != "request-1" {
		return fmt.Errorf("unexpected command result: %#v", commandResult)
	}
	if err := resultFor(ctx, connection, commandResult, nil); err != nil {
		return err
	}
	if err := wsjson.Write(ctx, connection, map[string]any{
		"id": subscriptionID, "type": "event",
		"event": map[string]any{
			"event": "command", "requestId": "request-2", "deviceId": "SERIAL-A", "preset": 2,
		},
	}); err != nil {
		return err
	}
	for {
		command, err := readDaemonCommand(ctx, connection, "")
		if err != nil {
			return err
		}
		switch command["type"] {
		case homeassistant.TypeState:
			if err := resultFor(ctx, connection, command, nil); err != nil {
				return err
			}
		case homeassistant.TypeCommandResult:
			if command["success"] != true || command["requestId"] != "request-2" {
				return fmt.Errorf("unexpected preset command result: %#v", command)
			}
			return resultFor(ctx, connection, command, nil)
		default:
			return fmt.Errorf("unexpected command while waiting for preset result: %#v", command)
		}
	}
}

func readDaemonCommand(ctx context.Context, connection *websocket.Conn, wantType string) (map[string]any, error) {
	var command map[string]any
	if err := wsjson.Read(ctx, connection, &command); err != nil {
		return nil, err
	}
	if wantType != "" && command["type"] != wantType {
		return nil, fmt.Errorf("command type = %v, want %s: %#v", command["type"], wantType, command)
	}
	return command, nil
}

func resultFor(ctx context.Context, connection *websocket.Conn, command map[string]any, result any) error {
	return wsjson.Write(ctx, connection, map[string]any{
		"id": command["id"], "type": "result", "success": true, "result": result,
	})
}

func TestDaemonStopsOnRejectedAuthorization(t *testing.T) {
	manager, err := lights.NewManager(lights.Config{Find: func() ([]hidraw.Device, error) { return nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"error":"invalid_grant","error_description":"revoked"}`))
	}))
	defer server.Close()
	err = Run(context.Background(), Config{
		Credentials: homeassistant.Credentials{HomeAssistantURL: server.URL, ClientID: "client", RefreshToken: "revoked"},
		Auth:        homeassistant.AuthClient{HTTPClient: server.Client()}, Manager: manager, CallTimeout: time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "pair again") {
		t.Fatalf("Run error = %v, want re-pair instruction", err)
	}
}
