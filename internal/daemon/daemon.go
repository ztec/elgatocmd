// Package daemon connects the USB light manager to the HACS-installed Home
// Assistant integration over a daemon-initiated WebSocket.
package daemon

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"git2.riper.fr/ztec/elgatocmd/internal/homeassistant"
	"git2.riper.fr/ztec/elgatocmd/internal/lights"
)

// Config contains the durable credentials and injected runtime dependencies.
type Config struct {
	Credentials homeassistant.Credentials
	Auth        homeassistant.AuthClient
	HTTPClient  *http.Client
	Manager     *lights.Manager
	Version     string
	CallTimeout time.Duration
	MinBackoff  time.Duration
	MaxBackoff  time.Duration
	Logf        func(string, ...any)
}

// Run starts USB discovery once and reconnects the outbound HA bridge until
// ctx is canceled.
func Run(ctx context.Context, config Config) error {
	if config.Manager == nil {
		return errors.New("daemon requires a light manager")
	}
	if config.CallTimeout == 0 {
		config.CallTimeout = 10 * time.Second
	}
	if config.MinBackoff == 0 {
		config.MinBackoff = time.Second
	}
	if config.MaxBackoff == 0 {
		config.MaxBackoff = 30 * time.Second
	}
	if config.CallTimeout <= 0 || config.MinBackoff <= 0 || config.MaxBackoff < config.MinBackoff {
		return errors.New("invalid daemon timeout or reconnect backoff")
	}
	if config.Version == "" {
		config.Version = "dev"
	}
	if config.Logf == nil {
		config.Logf = func(string, ...any) {}
	}
	epoch, err := homeassistant.NewInstanceID()
	if err != nil {
		return fmt.Errorf("create daemon session epoch: %w", err)
	}

	managerCtx, stopManager := context.WithCancel(ctx)
	defer stopManager()
	events := make(chan lights.Event, 128)
	managerDone := make(chan error, 1)
	go func() { managerDone <- config.Manager.Run(managerCtx, events) }()

	backoff := config.MinBackoff
	for {
		if ctx.Err() != nil {
			return nil
		}
		tokenCtx, cancel := context.WithTimeout(ctx, config.CallTimeout)
		token, err := config.Auth.Refresh(tokenCtx, config.Credentials)
		cancel()
		if errors.Is(err, homeassistant.ErrAuthorizationRejected) {
			return fmt.Errorf("Home Assistant authorization is no longer valid; run elgatolight pair again: %w", err)
		}
		if err == nil {
			var client *homeassistant.WebSocketClient
			client, err = homeassistant.DialWebSocket(ctx, config.Credentials.HomeAssistantURL, token.AccessToken, config.HTTPClient)
			if err == nil {
				config.Logf("connected to Home Assistant at %s", config.Credentials.HomeAssistantURL)
				err = runConnection(ctx, config, client, epoch, events, managerDone)
				_ = client.Close()
				backoff = config.MinBackoff
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		select {
		case managerErr := <-managerDone:
			if managerErr == nil && ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("USB light manager stopped: %w", managerErr)
		default:
		}
		delay := jitteredBackoff(backoff)
		config.Logf("Home Assistant bridge unavailable: %v; retrying in %s", err, delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case managerErr := <-managerDone:
			timer.Stop()
			if managerErr == nil && ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("USB light manager stopped: %w", managerErr)
		case <-timer.C:
		}
		backoff *= 2
		if backoff > config.MaxBackoff {
			backoff = config.MaxBackoff
		}
	}
}

func jitteredBackoff(maximum time.Duration) time.Duration {
	minimum := maximum * 4 / 5
	span := maximum - minimum
	if span <= 0 {
		return maximum
	}
	random, err := rand.Int(rand.Reader, big.NewInt(int64(span)+1))
	if err != nil {
		return maximum
	}
	return minimum + time.Duration(random.Int64())
}

func runConnection(
	ctx context.Context,
	config Config,
	client *homeassistant.WebSocketClient,
	epoch string,
	events <-chan lights.Event,
	managerDone <-chan error,
) error {
	callCtx, cancel := context.WithTimeout(ctx, config.CallTimeout)
	infoResult, err := client.Call(callCtx, map[string]any{"type": homeassistant.TypeInfo})
	cancel()
	if err != nil {
		return fmt.Errorf("verify Home Assistant integration: %w (install and enable the HACS Elgato USB Light Bridge integration)", err)
	}
	var info struct {
		ProtocolVersion int `json:"protocolVersion"`
	}
	if err := json.Unmarshal(infoResult, &info); err != nil || info.ProtocolVersion != homeassistant.ProtocolVersion {
		return fmt.Errorf("Home Assistant bridge protocol mismatch: got %d, need %d", info.ProtocolVersion, homeassistant.ProtocolVersion)
	}

	baseline := config.Manager.Sequence()
	command := homeassistant.SubscribeCommand{
		Type: homeassistant.TypeSubscribe, ProtocolVersion: homeassistant.ProtocolVersion,
		InstanceID: config.Credentials.InstanceID, Epoch: epoch, Sequence: baseline,
		DaemonVersion: config.Version, Devices: config.Manager.Snapshot(),
	}
	callCtx, cancel = context.WithTimeout(ctx, config.CallTimeout)
	subscriptionID, subscribeResult, err := client.CallWithID(callCtx, command)
	cancel()
	if err != nil {
		return fmt.Errorf("register daemon with Home Assistant: %w", err)
	}
	if err := json.Unmarshal(subscribeResult, &info); err != nil || info.ProtocolVersion != homeassistant.ProtocolVersion {
		return fmt.Errorf("Home Assistant subscription protocol mismatch: got %d, need %d", info.ProtocolVersion, homeassistant.ProtocolVersion)
	}
	config.Logf("registered daemon %s with %d light(s)", config.Credentials.InstanceID, len(command.Devices))

	for {
		select {
		case <-ctx.Done():
			return nil
		case managerErr := <-managerDone:
			return managerErr
		case err := <-client.Done():
			if err == nil {
				return errors.New("Home Assistant WebSocket closed")
			}
			return err
		case event := <-events:
			logManagerEvent(config, event)
			if event.Sequence <= baseline {
				continue
			}
			if event.Sequence != baseline+1 {
				return fmt.Errorf("local event sequence gap: expected %d, got %d", baseline+1, event.Sequence)
			}
			if err := publishEvent(ctx, config, client, epoch, event); err != nil {
				return err
			}
			baseline = event.Sequence
		case event, ok := <-client.Events():
			if !ok {
				return errors.New("Home Assistant WebSocket event stream closed")
			}
			if event.SubscriptionID != subscriptionID {
				continue
			}
			var payload homeassistant.SubscriptionEvent
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return fmt.Errorf("decode Home Assistant bridge event: %w", err)
			}
			switch payload.Event {
			case "command":
				if err := handleCommand(ctx, config, client, epoch, payload); err != nil {
					return err
				}
			case "resync":
				return fmt.Errorf("Home Assistant requested a full resync: %s", payload.Reason)
			default:
				return fmt.Errorf("unknown Home Assistant bridge event %q", payload.Event)
			}
		}
	}
}

func publishEvent(ctx context.Context, config Config, client *homeassistant.WebSocketClient, epoch string, event lights.Event) error {
	var command any
	switch event.Type {
	case lights.EventConnected:
		command = homeassistant.StateCommand{
			Type: homeassistant.TypeDeviceConnected, InstanceID: config.Credentials.InstanceID,
			Epoch: epoch, Sequence: event.Sequence, Light: event.Light,
		}
	case lights.EventStateChanged:
		command = homeassistant.StateCommand{
			Type: homeassistant.TypeState, InstanceID: config.Credentials.InstanceID,
			Epoch: epoch, Sequence: event.Sequence, Light: event.Light,
		}
	case lights.EventDisconnected:
		command = homeassistant.DisconnectCommand{
			Type: homeassistant.TypeDeviceDisconnected, InstanceID: config.Credentials.InstanceID,
			Epoch: epoch, Sequence: event.Sequence, DeviceID: event.Light.ID, Error: event.Light.Error,
		}
	default:
		return fmt.Errorf("unknown light manager event %q", event.Type)
	}
	callCtx, cancel := context.WithTimeout(ctx, config.CallTimeout)
	_, err := client.Call(callCtx, command)
	cancel()
	if err != nil {
		return fmt.Errorf("publish %s event for %s: %w", event.Type, event.Light.ID, err)
	}
	return nil
}

func handleCommand(ctx context.Context, config Config, client *homeassistant.WebSocketClient, epoch string, command homeassistant.SubscriptionEvent) error {
	if command.RequestID == "" || command.DeviceID == "" {
		return errors.New("Home Assistant sent an incomplete light command")
	}
	hasUpdate := command.Update.On != nil || command.Update.Brightness != nil || command.Update.Temperature != nil
	hasPreset := command.Preset != nil
	if hasUpdate == hasPreset {
		return errors.New("Home Assistant light command must contain exactly one update or preset")
	}
	commandCtx, cancel := context.WithTimeout(ctx, config.CallTimeout)
	var light lights.Light
	var updateErr error
	if hasPreset {
		light, updateErr = config.Manager.ApplyPreset(commandCtx, command.DeviceID, *command.Preset)
	} else {
		light, updateErr = config.Manager.Update(commandCtx, command.DeviceID, command.Update)
	}
	cancel()
	result := homeassistant.CommandResult{
		Type: homeassistant.TypeCommandResult, InstanceID: config.Credentials.InstanceID,
		Epoch: epoch, RequestID: command.RequestID, Success: updateErr == nil,
	}
	if updateErr != nil {
		result.Error = updateErr.Error()
	} else {
		result.Light = &light
	}
	logHomeAssistantAction(config, command, light, updateErr)
	callCtx, cancel := context.WithTimeout(ctx, config.CallTimeout)
	_, err := client.Call(callCtx, result)
	cancel()
	if err != nil {
		return fmt.Errorf("send command result for %s: %w", command.DeviceID, err)
	}
	return nil
}

func logManagerEvent(config Config, event lights.Event) {
	switch event.Type {
	case lights.EventConnected:
		logConnectedLight(config, event.Light)
	case lights.EventDisconnected:
		config.Logf("device event=disconnected source=light light=%q error=%q", event.Light.ID, event.Light.Error)
	case lights.EventStateChanged:
		if event.Source != lights.EventSourceLight {
			return
		}
		if !event.Light.Available {
			config.Logf("device event=unavailable source=light light=%q error=%q", event.Light.ID, event.Light.Error)
			return
		}
		config.Logf("action source=light light=%q %s", event.Light.ID, formatState(event.Light.State))
	}
}

func logConnectedLight(config Config, light lights.Light) {
	if !light.Available {
		config.Logf("device event=connected source=light light=%q available=false error=%q", light.ID, light.Error)
		return
	}
	config.Logf("device event=connected source=light light=%q available=true %s", light.ID, formatState(light.State))
}

func logHomeAssistantAction(config Config, command homeassistant.SubscriptionEvent, light lights.Light, actionErr error) {
	fields := formatRequestedAction(command)
	if actionErr != nil {
		config.Logf("action source=home_assistant light=%q request=%q %s result=error error=%q",
			command.DeviceID, command.RequestID, fields, actionErr)
		return
	}
	config.Logf("action source=home_assistant light=%q request=%q %s result=success %s",
		command.DeviceID, command.RequestID, fields, formatState(light.State))
}

func formatRequestedAction(command homeassistant.SubscriptionEvent) string {
	if command.Preset != nil {
		return fmt.Sprintf("requested_preset=%d", *command.Preset)
	}
	return formatUpdate(command.Update)
}

func formatUpdate(update lights.Update) string {
	fields := make([]string, 0, 3)
	if update.On != nil {
		fields = append(fields, fmt.Sprintf("requested_on=%t", *update.On))
	}
	if update.Brightness != nil {
		fields = append(fields, fmt.Sprintf("requested_brightness=%d", *update.Brightness))
	}
	if update.Temperature != nil {
		fields = append(fields, fmt.Sprintf("requested_temperature=%dK", *update.Temperature))
	}
	if len(fields) == 0 {
		return "requested=none"
	}
	return strings.Join(fields, " ")
}

func formatState(state lights.State) string {
	return fmt.Sprintf("on=%t brightness=%d temperature=%dK", state.On, state.Brightness, state.Temperature)
}
