$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
$protocBin = "C:\Users\victo\AppData\Local\Microsoft\WinGet\Packages\Google.Protobuf_Microsoft.Winget.Source_8wekyb3d8bbwe\bin"
$env:Path = "$protocBin;$env:Path"

Push-Location $repoRoot
try {
    protoc --version
    protoc-gen-go --version
    protoc -I . --go_out=. --go_opt=module=github.com/Ruleshift/server internal/protocol/proto/ruleshift.proto
    protoc -I . --csharp_out=unity-client/Assets/Scripts/Network/Generated internal/protocol/proto/ruleshift.proto
}
finally {
    Pop-Location
}

