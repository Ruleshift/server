package protocol

import (
	"fmt"

	ruleshiftv1 "github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv1"
	"google.golang.org/protobuf/proto"
)

const CurrentVersion uint32 = 1

func EncodeClientEnvelope(env *ruleshiftv1.ClientEnvelope) ([]byte, error) {
	if env == nil {
		return nil, fmt.Errorf("client envelope must not be nil")
	}
	return proto.Marshal(env)
}

func DecodeClientEnvelope(payload []byte) (*ruleshiftv1.ClientEnvelope, error) {
	var env ruleshiftv1.ClientEnvelope
	if err := proto.Unmarshal(payload, &env); err != nil {
		return nil, fmt.Errorf("decode client envelope: %w", err)
	}
	if env.GetProtocolVersion() != CurrentVersion {
		return nil, fmt.Errorf("unsupported protocol version: got=%d want=%d", env.GetProtocolVersion(), CurrentVersion)
	}
	return &env, nil
}

func EncodeServerEnvelope(env *ruleshiftv1.ServerEnvelope) ([]byte, error) {
	if env == nil {
		return nil, fmt.Errorf("server envelope must not be nil")
	}
	return proto.Marshal(env)
}

func DecodeServerEnvelope(payload []byte) (*ruleshiftv1.ServerEnvelope, error) {
	var env ruleshiftv1.ServerEnvelope
	if err := proto.Unmarshal(payload, &env); err != nil {
		return nil, fmt.Errorf("decode server envelope: %w", err)
	}
	return &env, nil
}
