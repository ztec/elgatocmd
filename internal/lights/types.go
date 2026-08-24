// Package lights manages one or more USB lights independently of the CLI or
// Home Assistant transport.
package lights

import "time"

// State is the authoritative state reported by a physical light.
type State struct {
	On          bool `json:"on"`
	Brightness  int  `json:"brightness"`
	Temperature int  `json:"temperature"` // Kelvin
}

// Capabilities describes the values accepted by a physical light.
type Capabilities struct {
	MinBrightness int `json:"minBrightness"`
	MaxBrightness int `json:"maxBrightness"`
	MinKelvin     int `json:"minKelvin"`
	MaxKelvin     int `json:"maxKelvin"`
}

// Light is one known physical device and its last authoritative state.
type Light struct {
	ID           string       `json:"id"`
	StableID     bool         `json:"stableId"`
	Path         string       `json:"path,omitempty"`
	Name         string       `json:"name,omitempty"`
	Manufacturer string       `json:"manufacturer,omitempty"`
	Model        string       `json:"model,omitempty"`
	Firmware     string       `json:"firmware,omitempty"`
	Available    bool         `json:"available"`
	State        State        `json:"state"`
	Capabilities Capabilities `json:"capabilities"`
	Error        string       `json:"error,omitempty"`
}

// Update is an atomic partial state update. Nil fields remain unchanged.
type Update struct {
	On          *bool `json:"on,omitempty"`
	Brightness  *int  `json:"brightness,omitempty"`
	Temperature *int  `json:"temperature,omitempty"` // Kelvin
}

// EventType identifies a manager lifecycle event.
type EventType string

const (
	EventConnected    EventType = "device_connected"
	EventStateChanged EventType = "state_changed"
	EventDisconnected EventType = "device_disconnected"
)

// EventSource distinguishes state observed through device polling from a
// state produced by an explicit Manager.Update call.
type EventSource string

const (
	EventSourceLight  EventSource = "light"
	EventSourceUpdate EventSource = "update"
)

// Event is emitted in strict sequence order for one Manager instance.
type Event struct {
	Sequence uint64      `json:"sequence"`
	Time     time.Time   `json:"time"`
	Type     EventType   `json:"type"`
	Source   EventSource `json:"source"`
	Light    Light       `json:"light"`
}
