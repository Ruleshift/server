package protocol

import (
	"strings"
	"testing"

	ruleshiftv2 "github.com/Ruleshift/server/internal/protocol/generated/go/ruleshiftv2"
	"google.golang.org/protobuf/proto"
)

func TestDecodeRejectsProtocolV1(t *testing.T) {
	payload, err := proto.Marshal(&ruleshiftv2.ClientEnvelope{ProtocolVersion: 1, ClientSequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = DecodeClientEnvelope(payload)
	if err == nil || !strings.Contains(err.Error(), "got=1 want=2") {
		t.Fatalf("expected v1 rejection, got %v", err)
	}
}
