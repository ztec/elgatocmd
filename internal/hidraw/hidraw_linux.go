//go:build linux

// Package hidraw talks to Linux HID devices without a C library.
package hidraw

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"git2.riper.fr/ztec/elgatocmd/internal/protocol"
)

const (
	VendorID  = "0FD9"
	ProductID = "00A0"
)

// Transport sends application payloads over the Key Light Neo HID reports.
type Transport struct {
	DevicePath string
	Timeout    time.Duration
}

// Device identifies one supported hidraw device. ID is the USB/HID serial when
// the firmware exposes one, so it remains stable when the hidraw node changes.
type Device struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Name     string `json:"name"`
	StableID bool   `json:"stableId"`
}

// FindDevices returns all hidraw devices matching the Elgato Key Light Neo
// VID/PID, ordered by stable ID and then device path.
func FindDevices() ([]Device, error) {
	entries, err := filepath.Glob("/sys/class/hidraw/hidraw*")
	if err != nil {
		return nil, err
	}
	var devices []Device
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(entry, "device", "uevent"))
		if err != nil {
			continue
		}
		device, ok := parseDevice(filepath.Join("/dev", filepath.Base(entry)), string(data))
		if !ok {
			continue
		}
		devices = append(devices, device)
	}
	sort.Slice(devices, func(i, j int) bool {
		if devices[i].ID == devices[j].ID {
			return devices[i].Path < devices[j].Path
		}
		return devices[i].ID < devices[j].ID
	})
	return devices, nil
}

func matchesDevice(uevent string) bool {
	_, ok := parseDevice("", uevent)
	return ok
}

func parseDevice(path, uevent string) (Device, bool) {
	properties := make(map[string]string)
	for _, line := range strings.Split(uevent, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			properties[key] = value
		}
	}
	want := "0003:0000" + VendorID + ":0000" + ProductID
	if !strings.EqualFold(properties["HID_ID"], want) {
		return Device{}, false
	}

	id := strings.TrimSpace(properties["HID_UNIQ"])
	stable := id != ""
	if !stable {
		id = filepath.Base(path)
	}
	return Device{
		ID:       id,
		Path:     path,
		Name:     strings.TrimSpace(properties["HID_NAME"]),
		StableID: stable,
	}, true
}

// Request writes one request and reads its complete response.
func (t *Transport) Request(ctx context.Context, payload []byte) ([]byte, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && t.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.Timeout)
		defer cancel()
	}

	path := t.DevicePath
	if path == "" {
		devices, err := FindDevices()
		if err != nil {
			return nil, fmt.Errorf("enumerate HID devices: %w", err)
		}
		switch len(devices) {
		case 0:
			return nil, errors.New("Elgato Key Light Neo (0fd9:00a0) not found")
		case 1:
			path = devices[0].Path
		default:
			ids := make([]string, len(devices))
			for index, device := range devices {
				ids[index] = device.ID
			}
			return nil, fmt.Errorf("multiple Key Light Neo devices found (%s); select one with --light or --device", strings.Join(ids, ", "))
		}
	}
	unlock, err := acquireDeviceLock(ctx, path)
	if err != nil {
		return nil, err
	}
	defer unlock()

	device, err := syscall.Open(path, syscall.O_RDWR|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	defer syscall.Close(device)

	if err := discardPending(device); err != nil {
		return nil, fmt.Errorf("discard pending input: %w", err)
	}
	frames, err := protocol.BuildFrames(payload)
	if err != nil {
		return nil, err
	}
	for _, frame := range frames {
		written, err := syscall.Write(device, frame)
		if err != nil {
			return nil, fmt.Errorf("write HID report: %w", err)
		}
		if written != len(frame) {
			return nil, io.ErrShortWrite
		}
	}

	return readResponse(ctx, device)
}

// acquireDeviceLock prevents two CLI processes from interleaving requests and
// consuming each other's HID responses. The lock is held only for one complete
// request/response exchange.
func acquireDeviceLock(ctx context.Context, devicePath string) (func(), error) {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		runtimeDir = os.TempDir()
	}
	lockPath := filepath.Join(runtimeDir, fmt.Sprintf("elgatolight-%d-%s.lock", os.Getuid(), filepath.Base(devicePath)))
	lockFD, err := syscall.Open(lockPath, syscall.O_RDWR|syscall.O_CREAT|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open USB transaction lock: %w", err)
	}
	for {
		err := syscall.Flock(lockFD, syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case err == nil:
			return func() {
				_ = syscall.Flock(lockFD, syscall.LOCK_UN)
				_ = syscall.Close(lockFD)
			}, nil
		case errors.Is(err, syscall.EINTR):
			continue
		case errors.Is(err, syscall.EAGAIN), errors.Is(err, syscall.EWOULDBLOCK):
			if err := waitForInput(ctx); err != nil {
				_ = syscall.Close(lockFD)
				return nil, fmt.Errorf("wait for USB transaction lock: %w", err)
			}
		default:
			_ = syscall.Close(lockFD)
			return nil, fmt.Errorf("lock USB transaction: %w", err)
		}
	}
}

func discardPending(device int) error {
	buf := make([]byte, protocol.ReportSize)
	for {
		_, err := syscall.Read(device, buf)
		switch {
		case err == nil:
			continue
		case errors.Is(err, syscall.EAGAIN), errors.Is(err, syscall.EWOULDBLOCK):
			return nil
		case errors.Is(err, syscall.EINTR):
			continue
		default:
			return err
		}
	}
}

func readResponse(ctx context.Context, device int) ([]byte, error) {
	chunks := make(map[int][]byte)
	total := 0
	kind := byte(0xff)
	buf := make([]byte, protocol.ReportSize)

	for {
		n, err := syscall.Read(device, buf)
		switch {
		case err == nil:
			if n != protocol.ReportSize {
				return nil, fmt.Errorf("short HID report: got %d bytes", n)
			}
			frame, err := protocol.ParseFrame(buf[:n])
			if err != nil {
				return nil, fmt.Errorf("decode HID report: %w", err)
			}
			if total != 0 && total != frame.Total {
				return nil, fmt.Errorf("response frame count changed from %d to %d", total, frame.Total)
			}
			if kind != 0xff && kind != frame.Kind {
				return nil, fmt.Errorf("response message kind changed from %#02x to %#02x", kind, frame.Kind)
			}
			total = frame.Total
			kind = frame.Kind
			chunks[frame.Index] = frame.Body
			if len(chunks) == total {
				var response []byte
				for i := 0; i < total; i++ {
					body, ok := chunks[i]
					if !ok {
						return nil, fmt.Errorf("response is missing frame %d", i)
					}
					response = append(response, body...)
				}
				if kind == protocol.MessageError || kind == protocol.MessageRequestError {
					return nil, fmt.Errorf("Key Light Neo rejected request: %s", response)
				}
				return response, nil
			}
		case errors.Is(err, syscall.EAGAIN), errors.Is(err, syscall.EWOULDBLOCK):
			if err := waitForInput(ctx); err != nil {
				return nil, fmt.Errorf("wait for Key Light Neo response: %w", err)
			}
		case errors.Is(err, syscall.EINTR):
			continue
		default:
			return nil, fmt.Errorf("read HID report: %w", err)
		}
	}
}

func waitForInput(ctx context.Context) error {
	timer := time.NewTimer(5 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
