package protocol

import (
	"errors"
	"testing"

	ruleshiftv1 "github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv1"
)

func TestFrameCodecRoundTrip(t *testing.T) {
	codec, err := NewFrameCodec(16)
	if err != nil {
		t.Fatalf("NewFrameCodec returned error: %v", err)
	}

	frame, err := codec.Encode([]byte{1, 2, 3})
	if err != nil {
		t.Fatalf("Encode returned error: %v", err)
	}

	payload, err := codec.Decode(frame)
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}

	if string(payload) != string([]byte{1, 2, 3}) {
		t.Fatalf("payload = %v, want [1 2 3]", payload)
	}
}

func TestFrameCodecRejectsOversizedPayload(t *testing.T) {
	codec, err := NewFrameCodec(2)
	if err != nil {
		t.Fatalf("NewFrameCodec returned error: %v", err)
	}

	if _, err := codec.Encode([]byte{1, 2, 3}); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("Encode error = %v, want ErrFrameTooLarge", err)
	}
}

func TestFrameCodecRejectsMalformedLength(t *testing.T) {
	codec, err := NewFrameCodec(16)
	if err != nil {
		t.Fatalf("NewFrameCodec returned error: %v", err)
	}

	_, err = codec.Decode([]byte{0, 0, 0, 3, 1, 2})
	if !errors.Is(err, ErrMalformedFrame) {
		t.Fatalf("Decode error = %v, want ErrMalformedFrame", err)
	}
}

func TestFrameCodecRejectsOversizedDeclaredLength(t *testing.T) {
	codec, err := NewFrameCodec(2)
	if err != nil {
		t.Fatalf("NewFrameCodec returned error: %v", err)
	}

	_, err = codec.Decode([]byte{0, 0, 0, 3, 1, 2, 3})
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("Decode error = %v, want ErrFrameTooLarge", err)
	}
}

func TestFrameCodecEncodeDecodeMessage(t *testing.T) {
	codec, err := NewFrameCodec(64)
	if err != nil {
		t.Fatalf("NewFrameCodec returned error: %v", err)
	}

	want := &ruleshiftv1.Ping{ClientTimeUnixMs: 123}
	frame, err := codec.EncodeMessage(want)
	if err != nil {
		t.Fatalf("EncodeMessage returned error: %v", err)
	}

	var decoded ruleshiftv1.Ping
	if err := codec.DecodeMessage(frame, &decoded); err != nil {
		t.Fatalf("DecodeMessage returned error: %v", err)
	}
	if decoded.GetClientTimeUnixMs() != want.GetClientTimeUnixMs() {
		t.Fatalf("decoded client time = %d, want %d", decoded.GetClientTimeUnixMs(), want.GetClientTimeUnixMs())
	}
}
