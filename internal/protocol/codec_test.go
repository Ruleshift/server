package protocol

import (
	"errors"
	"fmt"
	"testing"
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
	codec, err := NewFrameCodec(16)
	if err != nil {
		t.Fatalf("NewFrameCodec returned error: %v", err)
	}

	wire := fakeProtobufCodec{}
	frame, err := codec.EncodeMessage(wire, "hello")
	if err != nil {
		t.Fatalf("EncodeMessage returned error: %v", err)
	}

	var decoded string
	if err := codec.DecodeMessage(wire, frame, &decoded); err != nil {
		t.Fatalf("DecodeMessage returned error: %v", err)
	}
	if decoded != "hello" {
		t.Fatalf("decoded = %q, want hello", decoded)
	}
}

type fakeProtobufCodec struct{}

func (fakeProtobufCodec) Marshal(message any) ([]byte, error) {
	value, ok := message.(string)
	if !ok {
		return nil, fmt.Errorf("unsupported fake message %T", message)
	}
	return []byte(value), nil
}

func (fakeProtobufCodec) Unmarshal(payload []byte, message any) error {
	value, ok := message.(*string)
	if !ok {
		return fmt.Errorf("unsupported fake target %T", message)
	}
	*value = string(payload)
	return nil
}
