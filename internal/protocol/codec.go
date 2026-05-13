package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const CurrentVersion uint32 = 1

var (
	ErrFrameTooLarge  = errors.New("frame payload exceeds max size")
	ErrMalformedFrame = errors.New("malformed frame")
	ErrNilWireCodec   = errors.New("nil protobuf wire codec")
)

type FrameCodec struct {
	MaxMessageBytes uint32
}

type ProtobufCodec interface {
	Marshal(message any) ([]byte, error)
	Unmarshal(payload []byte, message any) error
}

func NewFrameCodec(maxMessageBytes int) (FrameCodec, error) {
	if maxMessageBytes <= 0 {
		return FrameCodec{}, fmt.Errorf("max message bytes must be positive")
	}
	return FrameCodec{MaxMessageBytes: uint32(maxMessageBytes)}, nil
}

func (c FrameCodec) Encode(payload []byte) ([]byte, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("%w: empty payload", ErrMalformedFrame)
	}
	if uint32(len(payload)) > c.MaxMessageBytes {
		return nil, fmt.Errorf("%w: got=%d max=%d", ErrFrameTooLarge, len(payload), c.MaxMessageBytes)
	}

	frame := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	copy(frame[4:], payload)
	return frame, nil
}

func (c FrameCodec) Decode(frame []byte) ([]byte, error) {
	if len(frame) < 4 {
		return nil, fmt.Errorf("%w: missing length prefix", ErrMalformedFrame)
	}

	size := binary.BigEndian.Uint32(frame[:4])
	if size == 0 {
		return nil, fmt.Errorf("%w: empty payload", ErrMalformedFrame)
	}
	if size > c.MaxMessageBytes {
		return nil, fmt.Errorf("%w: got=%d max=%d", ErrFrameTooLarge, size, c.MaxMessageBytes)
	}
	if len(frame[4:]) != int(size) {
		return nil, fmt.Errorf("%w: declared=%d actual=%d", ErrMalformedFrame, size, len(frame[4:]))
	}

	payload := make([]byte, size)
	copy(payload, frame[4:])
	return payload, nil
}

func (c FrameCodec) EncodeMessage(wire ProtobufCodec, message any) ([]byte, error) {
	if wire == nil {
		return nil, ErrNilWireCodec
	}

	payload, err := wire.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("marshal protobuf message: %w", err)
	}

	frame, err := c.Encode(payload)
	if err != nil {
		return nil, fmt.Errorf("encode protobuf frame: %w", err)
	}
	return frame, nil
}

func (c FrameCodec) DecodeMessage(wire ProtobufCodec, frame []byte, message any) error {
	if wire == nil {
		return ErrNilWireCodec
	}

	payload, err := c.Decode(frame)
	if err != nil {
		return fmt.Errorf("decode protobuf frame: %w", err)
	}
	if err := wire.Unmarshal(payload, message); err != nil {
		return fmt.Errorf("unmarshal protobuf message: %w", err)
	}
	return nil
}
