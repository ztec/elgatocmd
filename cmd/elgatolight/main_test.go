package main

import (
	"bytes"
	"strings"
	"testing"

	"elgatolight/internal/hidraw"
)

func TestSnapshotTreeLines(t *testing.T) {
	lines := snapshotTreeLines([]lightSnapshot{
		{ID: "SERIAL-A", StableID: true, On: true, Brightness: 34, Temperature: 3000},
		{ID: "hidraw9", StableID: false, On: false, Brightness: 5, Temperature: 6500},
	})
	want := []string{
		"Lights",
		"├── Light 1 [SERIAL-A] - on - brightness 034% - temperature 3000K",
		"└── Light 2 [hidraw9; path-based] - off - brightness 005% - temperature 6500K",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("tree:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

func TestSelectDevicesKeepsSingleLightConvenient(t *testing.T) {
	devices := []hidraw.Device{{ID: "A", Path: "/dev/hidraw1", StableID: true}}
	selected, err := selectDevices(devices, options{}, false)
	if err != nil || len(selected) != 1 || selected[0].ID != "A" {
		t.Fatalf("single-light selection = %#v, %v", selected, err)
	}
	device, err := requireOneDevice(selected)
	if err != nil || device.ID != "A" {
		t.Fatalf("single-light write selection = %#v, %v", device, err)
	}
}

func TestMultipleWritesRequireStableID(t *testing.T) {
	devices := []hidraw.Device{
		{ID: "A", Path: "/dev/hidraw1", StableID: true},
		{ID: "B", Path: "/dev/hidraw2", StableID: true},
	}
	selected, err := selectDevices(devices, options{}, false)
	if err != nil || len(selected) != 2 {
		t.Fatalf("multi-light read selection = %#v, %v", selected, err)
	}
	if _, err := requireOneDevice(selected); err == nil || !strings.Contains(err.Error(), "--light ID") {
		t.Fatalf("multi-light write error = %v", err)
	}
	selected, err = selectDevices(devices, options{lightID: "B"}, false)
	if err != nil || len(selected) != 1 || selected[0].ID != "B" {
		t.Fatalf("explicit stable-ID selection = %#v, %v", selected, err)
	}
}

func TestLineRendererRewritesExistingRows(t *testing.T) {
	var output bytes.Buffer
	renderer := lineRenderer{output: &output}
	if err := renderer.Render([]string{"Lights", "old"}); err != nil {
		t.Fatal(err)
	}
	if err := renderer.Render([]string{"Lights", "new"}); err != nil {
		t.Fatal(err)
	}
	want := "Lights\nold\n\x1b[2A\x1b[2KLights\n\x1b[2Knew\n"
	if output.String() != want {
		t.Fatalf("rendered %q, want %q", output.String(), want)
	}
}
