$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
$unityOutput = Join-Path $repoRoot "sdk\unity\com.ruleshift.runtime\Runtime\Generated"
$protoc = Get-Command protoc -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Source -First 1
if (-not $protoc) {
    $protoc = Get-ChildItem -Path "$env:LOCALAPPDATA\Microsoft\WinGet\Packages\Google.Protobuf*" `
        -Filter protoc.exe -Recurse -ErrorAction SilentlyContinue |
        Select-Object -ExpandProperty FullName -First 1
}
if (-not $protoc) { throw "protoc was not found; install Google.Protobuf with WinGet" }

Push-Location $repoRoot
try {
	& $protoc --version
    protoc-gen-go --version
    protoc-gen-go-grpc --version
    New-Item -ItemType Directory -Force -Path $unityOutput | Out-Null
	& $protoc -I . --go_out=. --go_opt=module=github.com/Ruleshift/server internal/protocol/proto/ruleshift.proto internal/moduleruntime/proto/module_runtime.proto
	& $protoc -I . --go-grpc_out=. --go-grpc_opt=module=github.com/Ruleshift/server internal/moduleruntime/proto/module_runtime.proto
	& $protoc -I . --csharp_out=$unityOutput internal/protocol/proto/ruleshift.proto internal/moduleruntime/proto/module_runtime.proto
}
finally {
    Pop-Location
}

