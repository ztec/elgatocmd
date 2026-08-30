package lights

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"git2.riper.fr/ztec/elgatocmd/internal/elgato"
	"git2.riper.fr/ztec/elgatocmd/internal/hidraw"
)

// DeviceClient is the subset of the Elgato client needed by the manager.
type DeviceClient interface {
	Status(context.Context) (elgato.Status, error)
	AccessoryInfo(context.Context) (elgato.AccessoryInfo, error)
	Update(context.Context, elgato.Update) (elgato.Status, error)
	ApplyPreset(context.Context, int) (elgato.Status, error)
}

// Finder discovers the currently connected supported USB devices.
type Finder func() ([]hidraw.Device, error)

// ClientFactory opens a protocol client for a discovered device.
type ClientFactory func(hidraw.Device) DeviceClient

// Config controls polling, discovery, and USB request timeouts.
type Config struct {
	PollInterval      time.Duration
	ReconcileInterval time.Duration
	RequestTimeout    time.Duration
	Find              Finder
	NewClient         ClientFactory
}

// Manager discovers devices, serializes access per device, and emits state
// changes for consumers such as the CLI and Home Assistant bridge.
type Manager struct {
	config Config

	mu      sync.RWMutex
	workers map[string]*worker
	lights  map[string]Light
	events  chan<- Event
	seq     atomic.Uint64
	ready   chan struct{}
	started bool
}

// NewManager constructs a manager with production defaults for omitted fields.
func NewManager(config Config) (*Manager, error) {
	if config.PollInterval == 0 {
		config.PollInterval = 250 * time.Millisecond
	}
	if config.ReconcileInterval == 0 {
		config.ReconcileInterval = time.Second
	}
	if config.RequestTimeout == 0 {
		config.RequestTimeout = 2 * time.Second
	}
	if config.PollInterval <= 0 || config.ReconcileInterval <= 0 || config.RequestTimeout <= 0 {
		return nil, errors.New("light manager intervals and timeout must be positive")
	}
	if config.Find == nil {
		config.Find = hidraw.FindDevices
	}
	if config.NewClient == nil {
		config.NewClient = func(device hidraw.Device) DeviceClient {
			return elgato.NewClient(&hidraw.Transport{DevicePath: device.Path, Timeout: config.RequestTimeout})
		}
	}
	return &Manager{
		config: config, workers: make(map[string]*worker), lights: make(map[string]Light), ready: make(chan struct{}),
	}, nil
}

// Run reconciles devices until ctx is canceled. Only one Run call may be
// active for a Manager.
func (m *Manager) Run(ctx context.Context, events chan<- Event) error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return errors.New("light manager can only be run once")
	}
	m.started = true
	m.events = events
	m.mu.Unlock()
	defer func() {
		m.stopAll()
		m.mu.Lock()
		m.events = nil
		m.mu.Unlock()
	}()

	if err := m.reconcile(ctx); err != nil {
		return err
	}
	m.waitInitial(ctx)
	close(m.ready)
	ticker := time.NewTicker(m.config.ReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := m.reconcile(ctx); err != nil {
				return err
			}
		}
	}
}

// Ready closes after every device found by the initial discovery has completed
// its first status read. If initial discovery fails, Run returns instead;
// callers waiting for readiness should also observe the Run result.
func (m *Manager) Ready() <-chan struct{} { return m.ready }

// Snapshot returns all known lights ordered by stable ID.
func (m *Manager) Snapshot() []Light {
	m.mu.RLock()
	result := make([]Light, 0, len(m.lights))
	for _, light := range m.lights {
		result = append(result, light)
	}
	m.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// ConnectedSnapshot returns devices currently present in discovery. A device
// whose status read failed is retained with Available=false, while an unplugged
// device is omitted. This is useful for interactive CLI views; Snapshot keeps
// the full retained inventory needed by Home Assistant.
func (m *Manager) ConnectedSnapshot() []Light {
	m.mu.RLock()
	result := make([]Light, 0, len(m.workers))
	for _, current := range m.workers {
		result = append(result, current.snapshot())
	}
	m.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// Sequence returns the most recently emitted event sequence. A subsequent
// event will use Sequence()+1.
func (m *Manager) Sequence() uint64 {
	return m.seq.Load()
}

func (m *Manager) waitInitial(ctx context.Context) {
	m.mu.RLock()
	workers := make([]*worker, 0, len(m.workers))
	for _, current := range m.workers {
		workers = append(workers, current)
	}
	m.mu.RUnlock()
	for _, current := range workers {
		select {
		case <-current.ready:
		case <-ctx.Done():
			return
		}
	}
}

// Update atomically applies a partial update to one connected light.
func (m *Manager) Update(ctx context.Context, id string, update Update) (Light, error) {
	m.mu.RLock()
	worker := m.workers[id]
	m.mu.RUnlock()
	if worker == nil {
		return Light{}, fmt.Errorf("light %q is not connected", id)
	}
	return worker.update(ctx, update)
}

// ApplyPreset recalls one of the two presets currently stored on a connected
// light. The operation shares the worker queue with polling and state updates.
func (m *Manager) ApplyPreset(ctx context.Context, id string, preset int) (Light, error) {
	m.mu.RLock()
	worker := m.workers[id]
	m.mu.RUnlock()
	if worker == nil {
		return Light{}, fmt.Errorf("light %q is not connected", id)
	}
	return worker.applyPreset(ctx, preset)
}

func (m *Manager) reconcile(ctx context.Context) error {
	devices, err := m.config.Find()
	if err != nil {
		return fmt.Errorf("enumerate lights: %w", err)
	}
	found := make(map[string]hidraw.Device, len(devices))
	for _, device := range devices {
		found[device.ID] = device
	}

	var removed []*worker
	m.mu.Lock()
	for id, current := range m.workers {
		device, ok := found[id]
		if ok && device.Path == current.device.Path {
			delete(found, id)
			continue
		}
		delete(m.workers, id)
		removed = append(removed, current)
	}
	m.mu.Unlock()
	for _, current := range removed {
		current.stop()
		light := current.snapshot()
		light.Available = false
		light.Error = "device disconnected"
		m.record(ctx, EventDisconnected, EventSourceLight, light)
	}

	for _, device := range found {
		current := newWorker(m, device, m.config.NewClient(device))
		m.mu.Lock()
		m.workers[device.ID] = current
		m.mu.Unlock()
		current.start(ctx)
	}
	return nil
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	workers := make([]*worker, 0, len(m.workers))
	for _, current := range m.workers {
		workers = append(workers, current)
	}
	m.workers = make(map[string]*worker)
	m.mu.Unlock()
	for _, current := range workers {
		current.stop()
	}
}

func (m *Manager) record(ctx context.Context, eventType EventType, source EventSource, light Light) {
	m.mu.Lock()
	previous, exists := m.lights[light.ID]
	if eventType == EventStateChanged && exists && reflect.DeepEqual(previous, light) {
		m.mu.Unlock()
		return
	}
	m.lights[light.ID] = light
	events := m.events
	m.mu.Unlock()
	if events == nil {
		return
	}
	event := Event{Sequence: m.seq.Add(1), Time: time.Now().UTC(), Type: eventType, Source: source, Light: light}
	select {
	case events <- event:
	case <-ctx.Done():
	}
}

type operationRequest struct {
	ctx      context.Context
	update   *Update
	preset   int
	response chan operationResponse
}

type operationResponse struct {
	light Light
	err   error
}

type worker struct {
	manager *Manager
	device  hidraw.Device
	client  DeviceClient

	mu       sync.RWMutex
	light    Light
	requests chan operationRequest
	cancel   context.CancelFunc
	done     chan struct{}
	ready    chan struct{}
}

func newWorker(manager *Manager, device hidraw.Device, client DeviceClient) *worker {
	return &worker{
		manager: manager,
		device:  device,
		client:  client,
		light: Light{
			ID: device.ID, StableID: device.StableID, Path: device.Path, Name: device.Name,
			Manufacturer: "Elgato", Model: "Key Light Neo",
			Capabilities: Capabilities{MinBrightness: 0, MaxBrightness: 100, MinKelvin: elgato.MinKelvin, MaxKelvin: elgato.MaxKelvin},
		},
		requests: make(chan operationRequest),
		done:     make(chan struct{}),
		ready:    make(chan struct{}),
	}
}

func (w *worker) start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	w.cancel = cancel
	go w.run(ctx)
}

func (w *worker) stop() {
	if w.cancel != nil {
		w.cancel()
		<-w.done
	}
}

func (w *worker) snapshot() Light {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.light
}

func (w *worker) update(ctx context.Context, update Update) (Light, error) {
	response := make(chan operationResponse, 1)
	request := operationRequest{ctx: ctx, update: &update, response: response}
	return w.request(ctx, request, response)
}

func (w *worker) applyPreset(ctx context.Context, preset int) (Light, error) {
	response := make(chan operationResponse, 1)
	request := operationRequest{ctx: ctx, preset: preset, response: response}
	return w.request(ctx, request, response)
}

func (w *worker) request(ctx context.Context, request operationRequest, response <-chan operationResponse) (Light, error) {
	select {
	case w.requests <- request:
	case <-ctx.Done():
		return Light{}, ctx.Err()
	case <-w.done:
		return Light{}, fmt.Errorf("light %q disconnected", w.device.ID)
	}
	select {
	case result := <-response:
		return result.light, result.err
	case <-ctx.Done():
		return Light{}, ctx.Err()
	case <-w.done:
		return Light{}, fmt.Errorf("light %q disconnected", w.device.ID)
	}
}

func (w *worker) run(ctx context.Context) {
	defer close(w.done)
	w.initialize(ctx)
	close(w.ready)
	ticker := time.NewTicker(w.manager.config.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-w.requests:
			var light Light
			var err error
			if request.update != nil {
				light, err = w.apply(request.ctx, *request.update)
			} else {
				light, err = w.recallPreset(request.ctx, request.preset)
			}
			request.response <- operationResponse{light: light, err: err}
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

func (w *worker) initialize(ctx context.Context) {
	requestCtx, cancel := context.WithTimeout(ctx, w.manager.config.RequestTimeout)
	info, infoErr := w.client.AccessoryInfo(requestCtx)
	cancel()
	if infoErr == nil {
		w.mu.Lock()
		if info.ProductName != "" {
			w.light.Model = info.ProductName
		}
		if info.DisplayName != "" {
			w.light.Name = info.DisplayName
		} else if info.ProductName != "" {
			w.light.Name = info.ProductName
		}
		w.light.Firmware = info.FirmwareVersion
		if info.PowerInfo.MaximumBrightness > 0 {
			w.light.Capabilities.MaxBrightness = info.PowerInfo.MaximumBrightness
		}
		w.mu.Unlock()
	}
	w.pollWithType(ctx, EventConnected)
}

func (w *worker) poll(ctx context.Context) {
	w.pollWithType(ctx, EventStateChanged)
}

func (w *worker) pollWithType(ctx context.Context, eventType EventType) {
	requestCtx, cancel := context.WithTimeout(ctx, w.manager.config.RequestTimeout)
	status, err := w.client.Status(requestCtx)
	cancel()
	light := w.snapshot()
	if err != nil {
		light.Available = false
		light.Error = err.Error()
		w.set(light)
		w.manager.record(ctx, eventType, EventSourceLight, light)
		return
	}
	state, err := stateFromStatus(status)
	if err != nil {
		light.Available = false
		light.Error = err.Error()
	} else {
		light.Available = true
		light.Error = ""
		light.State = state
	}
	w.set(light)
	w.manager.record(ctx, eventType, EventSourceLight, light)
}

func (w *worker) apply(ctx context.Context, update Update) (Light, error) {
	if update.On == nil && update.Brightness == nil && update.Temperature == nil {
		return w.snapshot(), errors.New("update must contain at least one field")
	}
	current := w.snapshot()
	if update.Brightness != nil && (*update.Brightness < current.Capabilities.MinBrightness || *update.Brightness > current.Capabilities.MaxBrightness) {
		return current, fmt.Errorf("brightness must be between %d and %d", current.Capabilities.MinBrightness, current.Capabilities.MaxBrightness)
	}
	requestCtx, cancel := context.WithTimeout(ctx, w.manager.config.RequestTimeout)
	status, err := w.client.Update(requestCtx, elgato.Update{On: update.On, Brightness: update.Brightness, Temperature: update.Temperature})
	cancel()
	if err != nil {
		return current, err
	}
	state, err := stateFromStatus(status)
	if err != nil {
		return current, err
	}
	current.Available = true
	current.Error = ""
	current.State = state
	w.set(current)
	w.manager.record(ctx, EventStateChanged, EventSourceUpdate, current)
	return current, nil
}

func (w *worker) recallPreset(ctx context.Context, preset int) (Light, error) {
	current := w.snapshot()
	if preset < 1 || preset > 2 {
		return current, errors.New("preset must be 1 or 2")
	}
	requestCtx, cancel := context.WithTimeout(ctx, w.manager.config.RequestTimeout)
	status, err := w.client.ApplyPreset(requestCtx, preset)
	cancel()
	if err != nil {
		return current, err
	}
	state, err := stateFromStatus(status)
	if err != nil {
		return current, err
	}
	current.Available = true
	current.Error = ""
	current.State = state
	w.set(current)
	w.manager.record(ctx, EventStateChanged, EventSourceUpdate, current)
	return current, nil
}

func (w *worker) set(light Light) {
	w.mu.Lock()
	w.light = light
	w.mu.Unlock()
}

func stateFromStatus(status elgato.Status) (State, error) {
	if len(status.Lights) == 0 {
		return State{}, errors.New("light returned an empty status")
	}
	light := status.Lights[0]
	return State{On: light.On != 0, Brightness: light.Brightness, Temperature: elgato.MiredToKelvin(light.Temperature)}, nil
}
