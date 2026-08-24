//go:build linux

package hidraw

import "testing"

func TestMatchesDevice(t *testing.T) {
	uevent := "DRIVER=hid-generic\nHID_ID=0003:00000FD9:000000A0\nHID_NAME=Elgato Key Light Neo\nHID_UNIQ=A7BTB4251316ZB\n"
	if !matchesDevice(uevent) {
		t.Fatal("Key Light Neo uevent did not match")
	}
	if matchesDevice("HID_ID=0003:00000FD9:0000009A\n") {
		t.Fatal("another Elgato product matched")
	}
}

func TestParseDeviceUsesSerialAsStableID(t *testing.T) {
	uevent := "HID_ID=0003:00000fd9:000000a0\nHID_NAME=Elgato Key Light Neo\nHID_UNIQ=A7BTB4251316ZB\n"
	device, ok := parseDevice("/dev/hidraw13", uevent)
	if !ok {
		t.Fatal("Key Light Neo uevent did not match")
	}
	if device.ID != "A7BTB4251316ZB" || !device.StableID {
		t.Fatalf("stable identity = %#v", device)
	}
	if device.Path != "/dev/hidraw13" || device.Name != "Elgato Key Light Neo" {
		t.Fatalf("device metadata = %#v", device)
	}
}

func TestParseDeviceFallsBackToNodeWithoutSerial(t *testing.T) {
	uevent := "HID_ID=0003:00000FD9:000000A0\nHID_NAME=Elgato Key Light Neo\n"
	device, ok := parseDevice("/dev/hidraw7", uevent)
	if !ok {
		t.Fatal("Key Light Neo uevent did not match")
	}
	if device.ID != "hidraw7" || device.StableID {
		t.Fatalf("fallback identity = %#v", device)
	}
}
