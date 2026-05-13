GO_MODULE := github.com/Ruleshift/server
PROTO_SRC := internal/protocol/proto/ruleshift.proto
CSHARP_PROTO_OUT := unity-client/Assets/Scripts/Network/Generated

.PHONY: proto proto-check proto-go proto-csharp test

proto: proto-check proto-go proto-csharp

proto-check:
	protoc --version
	protoc-gen-go --version

proto-go:
	protoc -I . --go_out=. --go_opt=module=$(GO_MODULE) $(PROTO_SRC)

proto-csharp:
	protoc -I . --csharp_out=$(CSHARP_PROTO_OUT) $(PROTO_SRC)

test:
	go test ./...
