package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"git2.riper.fr/ztec/elgatocmd/internal/elgato"
	"git2.riper.fr/ztec/elgatocmd/internal/hidraw"
	"git2.riper.fr/ztec/elgatocmd/internal/lights"
)

type lightSnapshot struct {
	ID               string `json:"id"`
	StableID         bool   `json:"stableId"`
	Device           string `json:"device"`
	Name             string `json:"name,omitempty"`
	On               bool   `json:"on"`
	Brightness       int    `json:"brightness"`
	Temperature      int    `json:"temperature"`
	TemperatureMired int    `json:"temperatureMired"`
	Error            string `json:"error,omitempty"`
}

func snapshotFromStatus(device hidraw.Device, status elgato.Status) (lightSnapshot, error) {
	if len(status.Lights) == 0 {
		return lightSnapshot{}, errors.New("light returned an empty status")
	}
	light := status.Lights[0]
	return lightSnapshot{
		ID:               device.ID,
		StableID:         device.StableID,
		Device:           device.Path,
		Name:             device.Name,
		On:               light.On != 0,
		Brightness:       light.Brightness,
		Temperature:      elgato.MiredToKelvin(light.Temperature),
		TemperatureMired: light.Temperature,
	}, nil
}

func readSnapshots(ctx context.Context, opts options, tolerateErrors, allowEmpty bool) ([]lightSnapshot, error) {
	running, err := startCLIManager(ctx, opts, time.Hour, time.Hour)
	if err != nil {
		return nil, err
	}
	defer running.shutdown()
	connected := running.manager.ConnectedSnapshot()
	if len(connected) == 0 && !allowEmpty {
		return nil, noConnectedLightError(opts)
	}
	return managedSnapshots(connected, tolerateErrors)
}

type runningCLIManager struct {
	manager *lights.Manager
	events  <-chan lights.Event
	done    <-chan error
	cancel  context.CancelFunc
}

func startCLIManager(ctx context.Context, opts options, pollInterval, reconcileInterval time.Duration) (*runningCLIManager, error) {
	manager, err := lights.NewManager(lights.Config{
		PollInterval: pollInterval, ReconcileInterval: reconcileInterval, RequestTimeout: opts.timeout,
		Find: func() ([]hidraw.Device, error) {
			devices, findErr := hidraw.FindDevices()
			if findErr != nil {
				return nil, findErr
			}
			return filterManagerDevices(devices, opts), nil
		},
	})
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	events := make(chan lights.Event, 256)
	done := make(chan error, 1)
	go func() { done <- manager.Run(runCtx, events) }()
	select {
	case <-manager.Ready():
		return &runningCLIManager{manager: manager, events: events, done: done, cancel: cancel}, nil
	case runErr := <-done:
		cancel()
		if runErr == nil {
			runErr = errors.New("light manager stopped before initial discovery")
		}
		return nil, runErr
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	}
}

func (r *runningCLIManager) shutdown() error {
	r.cancel()
	return <-r.done
}

func filterManagerDevices(devices []hidraw.Device, opts options) []hidraw.Device {
	if opts.device == "" && opts.lightID == "" {
		return devices
	}
	filtered := make([]hidraw.Device, 0, 1)
	for _, device := range devices {
		if (opts.device != "" && device.Path == opts.device) || (opts.lightID != "" && device.ID == opts.lightID) {
			filtered = append(filtered, device)
		}
	}
	return filtered
}

func noConnectedLightError(opts options) error {
	if opts.device != "" {
		return fmt.Errorf("no supported light found at %s", opts.device)
	}
	if opts.lightID != "" {
		return fmt.Errorf("light %q is not connected", opts.lightID)
	}
	return errors.New("Elgato Key Light Neo (0fd9:00a0) not found")
}

func managedSnapshots(devices []lights.Light, tolerateErrors bool) ([]lightSnapshot, error) {
	snapshots := make([]lightSnapshot, 0, len(devices))
	for _, device := range devices {
		if !device.Available || device.Error != "" {
			err := device.Error
			if err == "" {
				err = "light is unavailable"
			}
			if !tolerateErrors {
				return nil, fmt.Errorf("read light %s: %s", device.ID, err)
			}
			snapshots = append(snapshots, lightSnapshot{
				ID: device.ID, StableID: device.StableID, Device: device.Path, Name: device.Name, Error: err,
			})
			continue
		}
		mired, err := elgato.KelvinToMired(device.State.Temperature)
		if err != nil {
			if !tolerateErrors {
				return nil, fmt.Errorf("read light %s: %w", device.ID, err)
			}
			snapshots = append(snapshots, lightSnapshot{
				ID: device.ID, StableID: device.StableID, Device: device.Path, Name: device.Name, Error: err.Error(),
			})
			continue
		}
		snapshots = append(snapshots, lightSnapshot{
			ID: device.ID, StableID: device.StableID, Device: device.Path, Name: device.Name,
			On: device.State.On, Brightness: device.State.Brightness, Temperature: device.State.Temperature, TemperatureMired: mired,
		})
	}
	return snapshots, nil
}

func runManagedStatusChange(
	ctx context.Context,
	output io.Writer,
	opts options,
	buildUpdate func(lights.Light) (lights.Update, error),
) error {
	running, err := startCLIManager(ctx, opts, time.Hour, time.Hour)
	if err != nil {
		return err
	}
	defer running.shutdown()
	connected := running.manager.ConnectedSnapshot()
	if len(connected) == 0 {
		return noConnectedLightError(opts)
	}
	if len(connected) != 1 {
		ids := make([]string, len(connected))
		for index, device := range connected {
			ids[index] = device.ID
		}
		return fmt.Errorf("multiple lights found (%s); select one with --light ID", strings.Join(ids, ", "))
	}
	update, err := buildUpdate(connected[0])
	if err != nil {
		return err
	}
	updated, err := running.manager.Update(ctx, connected[0].ID, update)
	if err != nil {
		return err
	}
	snapshots, err := managedSnapshots([]lights.Light{updated}, false)
	if err != nil {
		return err
	}
	return printSnapshots(output, snapshots, opts.json)
}

func printSnapshots(output io.Writer, snapshots []lightSnapshot, jsonOutput bool) error {
	if jsonOutput {
		return printJSON(output, struct {
			Lights []lightSnapshot `json:"lights"`
		}{Lights: snapshots}, true)
	}
	_, err := fmt.Fprintln(output, joinLines(snapshotTreeLines(snapshots)))
	return err
}

func snapshotTreeLines(snapshots []lightSnapshot) []string {
	lines := []string{"Lights"}
	if len(snapshots) == 0 {
		return append(lines, "└── No lights detected")
	}
	for index, light := range snapshots {
		line := fmt.Sprintf("%s Light %d [%s]", treeBranch(index, len(snapshots)), index+1, displayID(light.ID, light.StableID))
		if light.Error != "" {
			line += " - error: " + light.Error
		} else {
			line += fmt.Sprintf(" - %s - brightness %03d%% - temperature %dK", boolPowerName(light.On), light.Brightness, light.Temperature)
		}
		lines = append(lines, line)
	}
	return lines
}

func joinLines(lines []string) string {
	result := ""
	for index, line := range lines {
		if index != 0 {
			result += "\n"
		}
		result += line
	}
	return result
}

func boolPowerName(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func printJSON(output io.Writer, value any, pretty bool) error {
	encoder := json.NewEncoder(output)
	if pretty {
		encoder.SetIndent("", "  ")
	}
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
