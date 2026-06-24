GO_MODULE := github.com/Ruleshift/server
PROTO_SRC := internal/protocol/proto/ruleshift.proto internal/moduleruntime/proto/module_runtime.proto
CSHARP_PROTO_OUT := sdk/unity/com.ruleshift.runtime/Runtime/Generated
PROTOC ?= C:/Users/victo/AppData/Local/Microsoft/WinGet/Packages/Google.Protobuf_Microsoft.Winget.Source_8wekyb3d8bbwe/bin/protoc.exe

.PHONY: proto proto-check proto-go proto-csharp test

proto: proto-check proto-go proto-csharp

proto-check:
	$(PROTOC) --version
	protoc-gen-go --version
	protoc-gen-go-grpc --version

proto-go:
	$(PROTOC) -I . --go_out=. --go_opt=module=$(GO_MODULE) $(PROTO_SRC)
	$(PROTOC) -I . --go-grpc_out=. --go-grpc_opt=module=$(GO_MODULE) internal/moduleruntime/proto/module_runtime.proto

proto-csharp:
	$(PROTOC) -I . --csharp_out=$(CSHARP_PROTO_OUT) $(PROTO_SRC)

test:
	go test ./...
