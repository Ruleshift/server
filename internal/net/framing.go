package netx

import "github.com/Ruleshift/server/internal/protocol"

type FrameCodec = protocol.FrameCodec

func NewFrameCodec(maxMessageBytes int) (FrameCodec, error) {
	return protocol.NewFrameCodec(maxMessageBytes)
}
