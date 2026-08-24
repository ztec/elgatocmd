package lights

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"elgatolight/internal/elgato"
	"elgatolight/internal/hidraw"
)

type errorDeviceClient struct{}

func (errorDeviceClient) Status(context.Context) (elgato.Status, error) {
	return elgato.Status{}, errors.New("USB read failed")
}

func (errorDeviceClient) AccessoryInfo(context.Context) (elgato.AccessoryInfo, error) {
	return elgato.AccessoryInfo{}, errors.New("USB info failed")
}

func (errorDeviceClient) Update(context.Context, elgato.Update) (elgato.Status, error) {
	return elgato.Status{}, errors.New("USB write failed")
}

func (errorDeviceClient) ApplyPreset(context.Context, int) (elgato.Status, error) {
	return elgato.Status{}, errors.New("USB preset failed")
}

type fakeDeviceClient struct {
	mu             sync.Mutex
	status         elgato.Status
	info           elgato.AccessoryInfo
	presets        map[int]elgato.Light
	appliedPresets []int
}

func (f *fakeDeviceClient) Status(context.Context) (elgato.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, nil
}

func (f *fakeDeviceClient) AccessoryInfo(context.Context) (elgato.AccessoryInfo, error) {
	return f.info, nil
}

func (f *fakeDeviceClient) Update(_ context.Context, update elgato.Update) (elgato.Status, error) {
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

func (f *fakeDeviceClient) ApplyPreset(_ context.Context, preset int) (elgato.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	light, ok := f.presets[preset]
	if !ok {
		return elgato.Status{}, errors.New("preset is not available")
	}
	f.appliedPresets = append(f.appliedPresets, preset)
	f.status.Lights[0] = light
	return f.status, nil
}

func (f *fakeDeviceClient) setBrightness(value int) {
	f.mu.Lock()
	f.status.Lights[0].Brightness = value
	f.mu.Unlock()
}

func TestManagerLifecycleAndAtomicUpdate(t *testing.T) {
	var discoveryMu sync.Mutex
	devices := []hidraw.Device{{ID: "SERIAL-A", Path: "/dev/hidraw1", Name: "Neo", StableID: true}}
	client := &fakeDeviceClient{
		status: elgato.Status{NumberOfLights: 1, Lights: []elgato.Light{{On: 0, Brightness: 10, Temperature: 200}}},
		info:   elgato.AccessoryInfo{ProductName: "Key Light Neo", FirmwareVersion: "2.0", PowerInfo: elgato.PowerInfo{MaximumBrightness: 40}},
		presets: map[int]elgato.Light{
			2: {On: 1, Brightness: 22, Temperature: 222},
		},
	}
	manager, err := NewManager(Config{
		PollInterval: 10 * time.Millisecond, ReconcileInterval: 10 * time.Millisecond, RequestTimeout: time.Second,
		Find: func() ([]hidraw.Device, error) {
			discoveryMu.Lock()
			defer discoveryMu.Unlock()
			return append([]hidraw.Device(nil), devices...), nil
		},
		NewClient: func(hidraw.Device) DeviceClient { return client },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan Event, 32)
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx, events) }()
	select {
	case <-manager.Ready():
	case <-time.After(2 * time.Second):
		t.Fatal("manager did not become ready")
	}
	if got := manager.ConnectedSnapshot(); len(got) != 1 || got[0].ID != "SERIAL-A" {
		t.Fatalf("connected snapshot = %#v", got)
	}

	connected := waitEvent(t, events, EventConnected)
	if connected.Source != EventSourceLight || connected.Light.ID != "SERIAL-A" || !connected.Light.Available || connected.Light.Capabilities.MaxBrightness != 40 {
		t.Fatalf("connected event = %#v", connected)
	}

	on := true
	brightness := 30
	temperature := 5000
	updated, err := manager.Update(ctx, "SERIAL-A", Update{On: &on, Brightness: &brightness, Temperature: &temperature})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.State.On || updated.State.Brightness != 30 || updated.State.Temperature != 5000 {
		t.Fatalf("updated light = %#v", updated)
	}
	changed := waitEvent(t, events, EventStateChanged)
	if changed.Source != EventSourceUpdate || changed.Sequence <= connected.Sequence || changed.Light.State.Brightness != 30 {
		t.Fatalf("changed event = %#v", changed)
	}

	recalled, err := manager.ApplyPreset(ctx, "SERIAL-A", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !recalled.State.On || recalled.State.Brightness != 22 || recalled.State.Temperature != elgato.MiredToKelvin(222) {
		t.Fatalf("recalled preset = %#v", recalled)
	}
	presetChanged := waitEvent(t, events, EventStateChanged)
	if presetChanged.Source != EventSourceUpdate || presetChanged.Sequence <= changed.Sequence || presetChanged.Light.State.Brightness != 22 {
		t.Fatalf("preset event = %#v", presetChanged)
	}
	client.mu.Lock()
	applied := append([]int(nil), client.appliedPresets...)
	client.mu.Unlock()
	if len(applied) != 1 || applied[0] != 2 {
		t.Fatalf("applied presets = %v", applied)
	}
	if _, err := manager.ApplyPreset(ctx, "SERIAL-A", 3); err == nil || err.Error() != "preset must be 1 or 2" {
		t.Fatalf("invalid preset error = %v", err)
	}

	tooHigh := 41
	if _, err := manager.Update(ctx, "SERIAL-A", Update{Brightness: &tooHigh}); err == nil {
		t.Fatal("device maximum brightness was not enforced")
	}

	client.setBrightness(15)
	physical := waitEventMatching(t, events, func(event Event) bool {
		return event.Type == EventStateChanged && event.Light.State.Brightness == 15
	})
	if physical.Source != EventSourceLight || physical.Sequence <= presetChanged.Sequence {
		t.Fatalf("physical sequence = %d after %d", physical.Sequence, presetChanged.Sequence)
	}

	discoveryMu.Lock()
	devices = nil
	discoveryMu.Unlock()
	disconnected := waitEvent(t, events, EventDisconnected)
	if disconnected.Source != EventSourceLight || disconnected.Light.Available {
		t.Fatalf("disconnected light remained available: %#v", disconnected.Light)
	}
	if got := manager.ConnectedSnapshot(); len(got) != 0 {
		t.Fatalf("connected snapshot after unplug = %#v", got)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerReadyWithZeroLights(t *testing.T) {
	manager, err := NewManager(Config{
		PollInterval: 10 * time.Millisecond, ReconcileInterval: 10 * time.Millisecond,
		Find: func() ([]hidraw.Device, error) { return nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx, make(chan Event, 1)) }()
	select {
	case <-manager.Ready():
	case <-time.After(time.Second):
		t.Fatal("zero-light manager did not become ready")
	}
	if got := manager.Snapshot(); len(got) != 0 {
		t.Fatalf("zero-light snapshot = %#v", got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerMultipleLightsFallbackIdentityAndReadError(t *testing.T) {
	devices := []hidraw.Device{
		{ID: "SERIAL-A", StableID: true, Path: "/dev/hidraw1"},
		{ID: "hidraw2", StableID: false, Path: "/dev/hidraw2"},
	}
	good := &fakeDeviceClient{
		status: elgato.Status{NumberOfLights: 1, Lights: []elgato.Light{{Brightness: 25, Temperature: 250}}},
	}
	manager, err := NewManager(Config{
		PollInterval: time.Second, ReconcileInterval: time.Second, RequestTimeout: time.Second,
		Find: func() ([]hidraw.Device, error) { return devices, nil },
		NewClient: func(device hidraw.Device) DeviceClient {
			if device.ID == "SERIAL-A" {
				return good
			}
			return errorDeviceClient{}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan Event, 4)
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx, events) }()
	select {
	case <-manager.Ready():
	case <-time.After(time.Second):
		t.Fatal("multi-light manager did not become ready")
	}
	snapshot := manager.ConnectedSnapshot()
	if len(snapshot) != 2 || snapshot[0].ID != "SERIAL-A" || snapshot[1].ID != "hidraw2" {
		t.Fatalf("multi-light snapshot = %#v", snapshot)
	}
	if snapshot[1].StableID || snapshot[1].Available || snapshot[1].Error != "USB read failed" {
		t.Fatalf("fallback/error light = %#v", snapshot[1])
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func waitEvent(t *testing.T, events <-chan Event, eventType EventType) Event {
	t.Helper()
	return waitEventMatching(t, events, func(event Event) bool { return event.Type == eventType })
}

func waitEventMatching(t *testing.T, events <-chan Event, match func(Event) bool) Event {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if match(event) {
				return event
			}
		case <-timer.C:
			t.Fatal("timed out waiting for manager event")
		}
	}
}
