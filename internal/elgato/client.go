// Package elgato implements the Key Light Neo application protocol.
package elgato

import (
	"context"
	"encoding/json"
	"fmt"
)

const (
	MinBrightness = 0
	MaxBrightness = 100
	MinKelvin     = 2900
	MaxKelvin     = 7000
	MinMired      = 143
	MaxMired      = 344
)

// RoundTripper exchanges one application payload with the light.
type RoundTripper interface {
	Request(context.Context, []byte) ([]byte, error)
}

type Client struct {
	transport RoundTripper
}

func NewClient(transport RoundTripper) *Client {
	return &Client{transport: transport}
}

type Light struct {
	On          int `json:"on"`
	Brightness  int `json:"brightness"`
	Temperature int `json:"temperature"`
}

type Status struct {
	NumberOfLights int     `json:"numberOfLights"`
	Lights         []Light `json:"lights"`
}

type PowerInfo struct {
	OperationMode     int `json:"operationMode"`
	MaximumBrightness int `json:"maximumBrightness"`
}

type BluetoothInfo struct {
	BroadcastMode int  `json:"broadcastMode"`
	Pairing       bool `json:"pairing"`
	Paired        bool `json:"paired"`
}

type AccessoryInfo struct {
	ProductName         string        `json:"productName"`
	HardwareBoardType   int           `json:"hardwareBoardType"`
	HardwareRevision    string        `json:"hardwareRevision"`
	MACAddress          string        `json:"macAddress"`
	FirmwareBuildNumber int           `json:"firmwareBuildNumber"`
	FirmwareVersion     string        `json:"firmwareVersion"`
	SerialNumber        string        `json:"serialNumber"`
	DisplayName         string        `json:"displayName"`
	Features            []string      `json:"features"`
	PowerInfo           PowerInfo     `json:"power-info"`
	BluetoothInfo       BluetoothInfo `json:"bt-info"`
}

type AutoMode struct {
	TargetLuxValue int `json:"targetLuxValue"`
}

type RemoteControl struct {
	Favourites []Light  `json:"favourites"`
	AutoMode   AutoMode `json:"autoMode"`
}

type Settings struct {
	PowerOnBehavior       int           `json:"powerOnBehavior"`
	PowerOnBrightness     int           `json:"powerOnBrightness"`
	PowerOnTemperature    int           `json:"powerOnTemperature"`
	SwitchOnDurationMS    int           `json:"switchOnDurationMs"`
	SwitchOffDurationMS   int           `json:"switchOffDurationMs"`
	ColorChangeDurationMS int           `json:"colorChangeDurationMs"`
	RemoteControl         RemoteControl `json:"remoteControl"`
}

type lightUpdate struct {
	On          *int `json:"on,omitempty"`
	Brightness  *int `json:"brightness,omitempty"`
	Temperature *int `json:"temperature,omitempty"`
}

// Update describes an atomic partial light update. Nil fields are left
// unchanged by the device.
type Update struct {
	On          *bool
	Brightness  *int
	Temperature *int // Kelvin
}

type updateRequest struct {
	Lights []lightUpdate `json:"lights"`
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var status Status
	err := c.getJSON(ctx, "/elgato/lights", &status)
	return status, err
}

func (c *Client) AccessoryInfo(ctx context.Context) (AccessoryInfo, error) {
	var info AccessoryInfo
	err := c.getJSON(ctx, "/elgato/accessory-info", &info)
	return info, err
}

func (c *Client) Settings(ctx context.Context) (Settings, error) {
	var settings Settings
	err := c.getJSON(ctx, "/elgato/lights/settings", &settings)
	return settings, err
}

// ApplyPreset recalls one of the device's one-based stored favourites without
// modifying either stored slot.
func (c *Client) ApplyPreset(ctx context.Context, preset int) (Status, error) {
	settings, err := c.Settings(ctx)
	if err != nil {
		return Status{}, err
	}
	if preset < 1 || preset > len(settings.RemoteControl.Favourites) {
		return Status{}, fmt.Errorf("preset must be between 1 and %d", len(settings.RemoteControl.Favourites))
	}
	favourite := settings.RemoteControl.Favourites[preset-1]
	return c.update(ctx, lightUpdate{
		On:          &favourite.On,
		Brightness:  &favourite.Brightness,
		Temperature: &favourite.Temperature,
	})
}

func (c *Client) SetPower(ctx context.Context, on bool) (Status, error) {
	value := 0
	if on {
		value = 1
	}
	return c.update(ctx, lightUpdate{On: &value})
}

func (c *Client) SetBrightness(ctx context.Context, brightness int) (Status, error) {
	if brightness < MinBrightness || brightness > MaxBrightness {
		return Status{}, fmt.Errorf("brightness must be between %d and %d", MinBrightness, MaxBrightness)
	}
	return c.update(ctx, lightUpdate{Brightness: &brightness})
}

func (c *Client) SetTemperature(ctx context.Context, kelvin int) (Status, error) {
	mired, err := KelvinToMired(kelvin)
	if err != nil {
		return Status{}, err
	}
	return c.update(ctx, lightUpdate{Temperature: &mired})
}

// Update applies power, brightness, and/or color temperature in one USB
// request. This is useful for Home Assistant actions that carry multiple
// fields and must not expose intermediate states.
func (c *Client) Update(ctx context.Context, update Update) (Status, error) {
	if update.On == nil && update.Brightness == nil && update.Temperature == nil {
		return Status{}, fmt.Errorf("update must contain at least one field")
	}
	wire := lightUpdate{Brightness: update.Brightness}
	if update.On != nil {
		value := 0
		if *update.On {
			value = 1
		}
		wire.On = &value
	}
	if update.Brightness != nil {
		if *update.Brightness < MinBrightness || *update.Brightness > MaxBrightness {
			return Status{}, fmt.Errorf("brightness must be between %d and %d", MinBrightness, MaxBrightness)
		}
	}
	if update.Temperature != nil {
		mired, err := KelvinToMired(*update.Temperature)
		if err != nil {
			return Status{}, err
		}
		wire.Temperature = &mired
	}
	return c.update(ctx, wire)
}

func (c *Client) Toggle(ctx context.Context) (Status, error) {
	status, err := c.Status(ctx)
	if err != nil {
		return Status{}, err
	}
	if len(status.Lights) == 0 {
		return Status{}, fmt.Errorf("light returned an empty status")
	}
	return c.SetPower(ctx, status.Lights[0].On == 0)
}

func (c *Client) getJSON(ctx context.Context, path string, destination any) error {
	response, err := c.transport.Request(ctx, []byte("GET "+path))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(response, destination); err != nil {
		return fmt.Errorf("decode response to GET %s: %w", path, err)
	}
	return nil
}

func (c *Client) update(ctx context.Context, update lightUpdate) (Status, error) {
	body, err := json.Marshal(updateRequest{Lights: []lightUpdate{update}})
	if err != nil {
		return Status{}, err
	}
	response, err := c.transport.Request(ctx, append([]byte("PUT /elgato/lights "), body...))
	if err != nil {
		return Status{}, err
	}
	var status Status
	if err := json.Unmarshal(response, &status); err != nil {
		return Status{}, fmt.Errorf("decode response to PUT /elgato/lights: %w", err)
	}
	return status, nil
}

func KelvinToMired(kelvin int) (int, error) {
	if kelvin < MinKelvin || kelvin > MaxKelvin {
		return 0, fmt.Errorf("temperature must be between %dK and %dK", MinKelvin, MaxKelvin)
	}
	mired := (1_000_000 + kelvin/2) / kelvin
	if mired < MinMired {
		mired = MinMired
	}
	if mired > MaxMired {
		mired = MaxMired
	}
	return mired, nil
}

func MiredToKelvin(mired int) int {
	if mired <= 0 {
		return 0
	}
	return (1_000_000 + mired/2) / mired
}
