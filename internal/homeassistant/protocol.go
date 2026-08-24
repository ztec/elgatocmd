// Package homeassistant implements authorization and the outbound bridge to
// the HACS-installed Home Assistant custom integration.
package homeassistant

import "elgatolight/internal/lights"

const ProtocolVersion = 1

const (
	TypeInfo               = "elgatolight/info"
	TypeSubscribe          = "elgatolight/subscribe"
	TypeState              = "elgatolight/state"
	TypeDeviceConnected    = "elgatolight/device_connected"
	TypeDeviceDisconnected = "elgatolight/device_disconnected"
	TypeCommandResult      = "elgatolight/command_result"
)

// SubscribeCommand registers a daemon session and its complete current state.
type SubscribeCommand struct {
	Type            string         `json:"type"`
	ProtocolVersion int            `json:"protocolVersion"`
	InstanceID      string         `json:"instanceId"`
	Epoch           string         `json:"epoch"`
	Sequence        uint64         `json:"sequence"`
	DaemonVersion   string         `json:"daemonVersion"`
	Devices         []lights.Light `json:"devices"`
}

// StateCommand carries one sequenced authoritative device state.
type StateCommand struct {
	Type       string       `json:"type"`
	InstanceID string       `json:"instanceId"`
	Epoch      string       `json:"epoch"`
	Sequence   uint64       `json:"sequence"`
	Light      lights.Light `json:"light"`
}

// DisconnectCommand marks one USB device unavailable.
type DisconnectCommand struct {
	Type       string `json:"type"`
	InstanceID string `json:"instanceId"`
	Epoch      string `json:"epoch"`
	Sequence   uint64 `json:"sequence"`
	DeviceID   string `json:"deviceId"`
	Error      string `json:"error,omitempty"`
}

// CommandResult reports a correlated USB command outcome to Home Assistant.
type CommandResult struct {
	Type       string        `json:"type"`
	InstanceID string        `json:"instanceId"`
	Epoch      string        `json:"epoch"`
	RequestID  string        `json:"requestId"`
	Success    bool          `json:"success"`
	Error      string        `json:"error,omitempty"`
	Light      *lights.Light `json:"light,omitempty"`
}

// SubscriptionEvent is sent by Home Assistant over the daemon's subscription.
type SubscriptionEvent struct {
	Event     string        `json:"event"`
	RequestID string        `json:"requestId,omitempty"`
	DeviceID  string        `json:"deviceId,omitempty"`
	Update    lights.Update `json:"update,omitempty"`
	Reason    string        `json:"reason,omitempty"`
}
