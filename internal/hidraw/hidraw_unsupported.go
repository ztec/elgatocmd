//go:build !linux

// Package hidraw provides a clear runtime error outside Linux so unsupported
// source builds fail when they attempt USB access.
package hidraw

import (
	"context"
	"errors"
	"time"
)

const (
	VendorID  = "0FD9"
	ProductID = "00A0"
)

var errUnsupported = errors.New("USB light access is supported only on Linux")

// Transport is retained on unsupported platforms so the protocol-facing code
// can compile; Request always returns a platform error.
type Transport struct {
	DevicePath string
	Timeout    time.Duration
}

// Device has the same portable discovery representation as the Linux build.
type Device struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Name     string `json:"name"`
	StableID bool   `json:"stableId"`
}

func FindDevices() ([]Device, error) {
	return nil, errUnsupported
}

func (t *Transport) Request(context.Context, []byte) ([]byte, error) {
	return nil, errUnsupported
}
