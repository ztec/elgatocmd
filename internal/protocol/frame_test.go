package protocol

import (
	"bytes"
	"testing"
)

func TestBuildAndParseFrames(t *testing.T) {
	payload := bytes.Repeat([]byte("neo"), 400)
	frames, err := BuildFrames(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 3 {
		t.Fatalf("got %d frames, want 3", len(frames))
	}

	var rebuilt []byte
	for i, report := range frames {
		frame, err := ParseFrame(report)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if frame.Index != i || frame.Total != len(frames) {
			t.Fatalf("frame metadata = %d/%d", frame.Index, frame.Total)
		}
		if frame.Kind != MessageRequest {
			t.Fatalf("frame kind = %#02x, want request", frame.Kind)
		}
		rebuilt = append(rebuilt, frame.Body...)
	}
	if !bytes.Equal(rebuilt, payload) {
		t.Fatal("reassembled payload differs")
	}
}

func TestEmptyPayload(t *testing.T) {
	frames, err := BuildFrames(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	frame, err := ParseFrame(frames[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(frame.Body) != 0 {
		t.Fatalf("empty payload produced %d bytes", len(frame.Body))
	}
}

func TestParseRejectsBadTrailer(t *testing.T) {
	frames, err := BuildFrames([]byte("status"))
	if err != nil {
		t.Fatal(err)
	}
	frames[0][12] = 0
	if _, err := ParseFrame(frames[0]); err == nil {
		t.Fatal("bad trailer was accepted")
	}
}

func TestParseAcceptsResponseHeader(t *testing.T) {
	frames, err := BuildFrames([]byte(`{"numberOfLights":1}`))
	if err != nil {
		t.Fatal(err)
	}
	frames[0][3] = MessageSuccess
	if _, err := ParseFrame(frames[0]); err != nil {
		t.Fatalf("response header was rejected: %v", err)
	}
}

func TestParseAcceptsErrorHeader(t *testing.T) {
	frames, err := BuildFrames([]byte(`{"errors":["not found"]}`))
	if err != nil {
		t.Fatal(err)
	}
	frames[0][3] = MessageError
	frame, err := ParseFrame(frames[0])
	if err != nil {
		t.Fatalf("error header was rejected: %v", err)
	}
	if frame.Kind != MessageError {
		t.Fatalf("frame kind = %#02x, want error", frame.Kind)
	}
}

func TestParseAcceptsRequestErrorHeader(t *testing.T) {
	frames, err := BuildFrames([]byte(`{"errors":["busy"]}`))
	if err != nil {
		t.Fatal(err)
	}
	frames[0][3] = MessageRequestError
	frame, err := ParseFrame(frames[0])
	if err != nil {
		t.Fatalf("request error header was rejected: %v", err)
	}
	if frame.Kind != MessageRequestError {
		t.Fatalf("frame kind = %#02x, want request error", frame.Kind)
	}
}

func TestParseRejectsUnknownHeader(t *testing.T) {
	frames, err := BuildFrames([]byte("status"))
	if err != nil {
		t.Fatal(err)
	}
	frames[0][3] = 0x7f
	if _, err := ParseFrame(frames[0]); err == nil {
		t.Fatal("unknown header was accepted")
	}
}

func TestBuildRejectsOversizedPayload(t *testing.T) {
	if _, err := BuildFrames(make([]byte, MaxBodySize*255+1)); err == nil {
		t.Fatal("oversized payload was accepted")
	}
}
