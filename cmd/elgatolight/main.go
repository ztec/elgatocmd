package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"elgatolight/internal/buildinfo"
	"elgatolight/internal/elgato"
	"elgatolight/internal/hidraw"
)

type options struct {
	device  string
	lightID string
	json    bool
	timeout time.Duration
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		if errors.Is(err, fs.ErrPermission) {
			fmt.Fprintln(os.Stderr, "USB access is denied. Run sudo elgatolight setup, then reconnect the light.")
		}
		os.Exit(1)
	}
}

func applicationVersion() string { return buildinfo.Version }

func run(args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	app, err := newCommandApp(ctx, os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	app.root.SetArgs(args)
	return app.root.Execute()
}

func findDevices(opts options, allowEmpty bool) ([]hidraw.Device, error) {
	devices, err := hidraw.FindDevices()
	if err != nil {
		return nil, fmt.Errorf("enumerate HID devices: %w", err)
	}
	return selectDevices(devices, opts, allowEmpty)
}

func selectDevices(devices []hidraw.Device, opts options, allowEmpty bool) ([]hidraw.Device, error) {
	if opts.device != "" {
		for _, device := range devices {
			if device.Path == opts.device {
				return []hidraw.Device{device}, nil
			}
		}
		if allowEmpty {
			return nil, nil
		}
		return nil, fmt.Errorf("no supported light found at %s", opts.device)
	}
	if opts.lightID != "" {
		for _, device := range devices {
			if device.ID == opts.lightID {
				return []hidraw.Device{device}, nil
			}
		}
		if allowEmpty {
			return nil, nil
		}
		return nil, fmt.Errorf("light %q is not connected", opts.lightID)
	}
	if len(devices) == 0 && !allowEmpty {
		return nil, errors.New("Elgato Key Light Neo (0fd9:00a0) not found")
	}
	return devices, nil
}

func oneDevice(opts options) (hidraw.Device, error) {
	devices, err := findDevices(opts, false)
	if err != nil {
		return hidraw.Device{}, err
	}
	return requireOneDevice(devices)
}

func requireOneDevice(devices []hidraw.Device) (hidraw.Device, error) {
	if len(devices) == 1 {
		return devices[0], nil
	}
	ids := make([]string, len(devices))
	for index, device := range devices {
		ids[index] = device.ID
	}
	return hidraw.Device{}, fmt.Errorf("multiple lights found (%s); select one with --light ID", strings.Join(ids, ", "))
}

func clientFor(device hidraw.Device, timeout time.Duration) *elgato.Client {
	return elgato.NewClient(&hidraw.Transport{DevicePath: device.Path, Timeout: timeout})
}

func withRequestTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, timeout)
}

func runStatusChange(ctx context.Context, output io.Writer, opts options, update func(context.Context, *elgato.Client) (elgato.Status, error)) error {
	device, err := oneDevice(opts)
	if err != nil {
		return err
	}
	requestCtx, cancel := withRequestTimeout(ctx, opts.timeout)
	status, err := update(requestCtx, clientFor(device, opts.timeout))
	cancel()
	if err != nil {
		return err
	}
	snapshot, err := snapshotFromStatus(device, status)
	if err != nil {
		return err
	}
	return printSnapshots(output, []lightSnapshot{snapshot}, opts.json)
}

type lightInfo struct {
	ID       string               `json:"id"`
	StableID bool                 `json:"stableId"`
	Device   string               `json:"device"`
	Info     elgato.AccessoryInfo `json:"info"`
}

func runInfo(ctx context.Context, output io.Writer, opts options) error {
	devices, err := findDevices(opts, false)
	if err != nil {
		return err
	}
	items := make([]lightInfo, 0, len(devices))
	for _, device := range devices {
		requestCtx, cancel := withRequestTimeout(ctx, opts.timeout)
		info, err := clientFor(device, opts.timeout).AccessoryInfo(requestCtx)
		cancel()
		if err != nil {
			return fmt.Errorf("read light %s: %w", device.ID, err)
		}
		items = append(items, lightInfo{ID: device.ID, StableID: device.StableID, Device: device.Path, Info: info})
	}
	if opts.json {
		return printJSON(output, struct {
			Lights []lightInfo `json:"lights"`
		}{Lights: items}, true)
	}
	fmt.Fprintln(output, "Lights")
	for index, item := range items {
		fmt.Fprintf(output, "%s Light %d [%s] - %s - firmware %s (build %d) - USB maximum brightness %03d%%\n",
			treeBranch(index, len(items)), index+1, displayID(item.ID, item.StableID), item.Info.ProductName,
			item.Info.FirmwareVersion, item.Info.FirmwareBuildNumber, item.Info.PowerInfo.MaximumBrightness)
	}
	return nil
}

type lightPresets struct {
	ID       string         `json:"id"`
	StableID bool           `json:"stableId"`
	Device   string         `json:"device"`
	Presets  []elgato.Light `json:"presets"`
}

func runPresets(ctx context.Context, output io.Writer, opts options) error {
	devices, err := findDevices(opts, false)
	if err != nil {
		return err
	}
	items := make([]lightPresets, 0, len(devices))
	for _, device := range devices {
		requestCtx, cancel := withRequestTimeout(ctx, opts.timeout)
		settings, err := clientFor(device, opts.timeout).Settings(requestCtx)
		cancel()
		if err != nil {
			return fmt.Errorf("read light %s presets: %w", device.ID, err)
		}
		items = append(items, lightPresets{ID: device.ID, StableID: device.StableID, Device: device.Path, Presets: settings.RemoteControl.Favourites})
	}
	if opts.json {
		return printJSON(output, struct {
			Lights []lightPresets `json:"lights"`
		}{Lights: items}, true)
	}
	fmt.Fprintln(output, "Lights")
	for index, item := range items {
		lastLight := index == len(items)-1
		fmt.Fprintf(output, "%s Light %d [%s]\n", treeBranch(index, len(items)), index+1, displayID(item.ID, item.StableID))
		indent := "│   "
		if lastLight {
			indent = "    "
		}
		for presetIndex, preset := range item.Presets {
			fmt.Fprintf(output, "%s%s Preset %d - %s - brightness %03d%% - temperature %dK\n",
				indent, treeBranch(presetIndex, len(item.Presets)), presetIndex+1, powerName(preset.On),
				preset.Brightness, elgato.MiredToKelvin(preset.Temperature))
		}
	}
	return nil
}

func displayID(id string, stable bool) string {
	if stable {
		return id
	}
	return id + "; path-based"
}

func treeBranch(index, length int) string {
	if index == length-1 {
		return "└──"
	}
	return "├──"
}

func powerName(on int) string {
	if on == 0 {
		return "off"
	}
	return "on"
}
