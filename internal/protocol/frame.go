// Package protocol implements the Key Light Neo's USB HID framing.
package protocol

import (
	"encoding/binary"
	"fmt"
)

const (
	ReportID       = byte(0x02)
	ReportSize     = 512
	MaxBodySize    = 505
	MessageSuccess = byte(0x00)
	MessageRequest = byte(0x03)
	MessageError   = byte(0x04)
	// MessageRequestError is another rejected-request response header observed
	// on update validation errors. It combines the request/error flags.
	MessageRequestError = byte(0x07)
	marker              = byte(0x03)
)

// Frame is one decoded HID report.
type Frame struct {
	Index int
	Total int
	Kind  byte
	Body  []byte
}

// BuildFrames splits an application payload into fixed-size HID reports.
func BuildFrames(payload []byte) ([][]byte, error) {
	total := (len(payload) + MaxBodySize - 1) / MaxBodySize
	if total == 0 {
		total = 1
	}
	if total > 255 {
		return nil, fmt.Errorf("payload needs %d frames; maximum is 255", total)
	}

	frames := make([][]byte, 0, total)
	for index := 0; index < total; index++ {
		start := index * MaxBodySize
		end := min(start+MaxBodySize, len(payload))
		body := payload[start:end]

		report := make([]byte, ReportSize)
		report[0] = ReportID
		report[1] = byte(index)
		report[2] = byte(total)
		report[3] = MessageRequest
		binary.LittleEndian.PutUint16(report[4:6], uint16(len(body)))
		copy(report[6:], body)
		report[6+len(body)] = marker
		frames = append(frames, report)
	}
	return frames, nil
}

// ParseFrame validates and decodes one HID report.
func ParseFrame(report []byte) (Frame, error) {
	if len(report) != ReportSize {
		return Frame{}, fmt.Errorf("unexpected report size %d, want %d", len(report), ReportSize)
	}
	if report[0] != ReportID {
		return Frame{}, fmt.Errorf("unexpected report ID %#02x", report[0])
	}
	if report[3] != MessageRequest && report[3] != MessageSuccess && report[3] != MessageError && report[3] != MessageRequestError {
		return Frame{}, fmt.Errorf("unexpected message kind %#02x (report prefix: % x)", report[3], report[:16])
	}
	total := int(report[2])
	index := int(report[1])
	if total == 0 || index >= total {
		return Frame{}, fmt.Errorf("invalid frame index %d/%d", index, total)
	}
	size := int(binary.LittleEndian.Uint16(report[4:6]))
	if size > MaxBodySize {
		return Frame{}, fmt.Errorf("invalid frame body size %d", size)
	}
	if report[6+size] != marker {
		return Frame{}, fmt.Errorf("missing frame trailer marker: got %#02x", report[6+size])
	}
	body := append([]byte(nil), report[6:6+size]...)
	return Frame{Index: index, Total: total, Kind: report[3], Body: body}, nil
}
