GO_MODULE := github.com/Ruleshift/server
PROTO_SRC := internal/protocol/proto/ruleshift.proto
CSHARP_PROTO_OUT := unity-client/Assets/Scripts/Network/Generated
PROTOC ?= C:/Users/victo/AppData/Local/Microsoft/WinGet/Packages/Google.Protobuf_Microsoft.Winget.Source_8wekyb3d8bbwe/bin/protoc.exe

.PHONY: proto proto-check proto-go proto-csharp test

proto: proto-check proto-go proto-csharp

proto-check:
	$(PROTOC) --version
	protoc-gen-go --version

proto-go:
	$(PROTOC) -I . --go_out=. --go_opt=module=$(GO_MODULE) $(PROTO_SRC)

proto-csharp:
	$(PROTOC) -I . --csharp_out=$(CSHARP_PROTO_OUT) $(PROTO_SRC)

test:
	go test ./...
