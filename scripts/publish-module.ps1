[CmdletBinding()]
param(
    [string]$ModuleId = "hiddennumber",
    [string]$DisplayName = "Hidden Number",
    [string]$RegistryRepository = "ghcr.io/ruleshift/hiddennumber",
    [string]$RuleshiftUrl = "https://api.ruleshift.ru",
    [string]$RegistryCredential = "main",
    [string]$Platform = "linux/amd64",
    [string]$RegistryUsername = $env:GHCR_USERNAME,
    [string]$RegistryTokenEnvironment = "GHCR_TOKEN",
    [switch]$PublicImage,
    [int]$ValidationTimeoutSeconds = 300,
    [int]$ValidationPollSeconds = 2
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Write-Step {
    param([string]$Message)
    Write-Host "[ruleshift-publish] $Message" -ForegroundColor Cyan
}

function Assert-Command {
    param([string]$Name)
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "Required command is not available: $Name"
    }
}

function Get-StatusCode {
    param($ErrorRecord)
    if ($null -eq $ErrorRecord.Exception.Response) {
        return $null
    }
    return [int]$ErrorRecord.Exception.Response.StatusCode
}

function Get-ExistingVersion {
    param(
        [string]$Uri,
        [hashtable]$RequestHeaders
    )
    try {
        return Invoke-RestMethod -Method Get -Uri $Uri -Headers $RequestHeaders
    }
    catch {
        if ((Get-StatusCode $_) -eq 404) {
            return $null
        }
        throw
    }
}

function Ensure-ModuleKey {
    param(
        [string]$BaseUrl,
        [string]$Id,
        [string]$Name,
        [hashtable]$RequestHeaders
    )
    $body = @{ key = $Id; display_name = $Name } | ConvertTo-Json -Compress
    try {
        $null = Invoke-RestMethod -Method Post `
            -Uri "$BaseUrl/v2/developer/modules" `
            -Headers $RequestHeaders `
            -ContentType "application/json" `
            -Body $body
        Write-Step "Created module key $Id"
    }
    catch {
        if ((Get-StatusCode $_) -eq 409) {
            Write-Step "Module key $Id already exists"
            return
        }
        throw
    }
}

function Wait-Validation {
    param(
        [string]$VersionUri,
        [string]$ValidationUri,
        [hashtable]$RequestHeaders,
        [int]$TimeoutSeconds,
        [int]$PollSeconds
    )
    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    while ($true) {
        $versionResult = Invoke-RestMethod -Method Get -Uri $VersionUri -Headers $RequestHeaders
        $validationResult = Invoke-RestMethod -Method Get -Uri $ValidationUri -Headers $RequestHeaders
        if ($versionResult.status -ne "validating" -and $validationResult.result -ne "validating") {
            return [PSCustomObject]@{
                Version = $versionResult
                Validation = $validationResult
            }
        }
        if ([DateTime]::UtcNow -ge $deadline) {
            throw "Validation did not finish within $TimeoutSeconds seconds"
        }
        Start-Sleep -Seconds $PollSeconds
    }
}

Assert-Command docker
Assert-Command curl.exe

if ($ValidationTimeoutSeconds -le 0 -or $ValidationPollSeconds -le 0) {
    throw "Validation timeout and poll interval must be positive"
}
if ([string]::IsNullOrWhiteSpace($env:RULESHIFT_DEVELOPER_API_KEY) -or $env:RULESHIFT_DEVELOPER_API_KEY.Length -lt 16) {
    throw "RULESHIFT_DEVELOPER_API_KEY must contain the Developer API key"
}
if ($PublicImage -and -not [string]::IsNullOrWhiteSpace($RegistryCredential)) {
    Write-Step "Public image selected; registry credential will not be sent to Ruleshift"
}

$repoRoot = Split-Path -Parent $PSScriptRoot
$moduleDirectory = Join-Path $repoRoot "examples/modules/$ModuleId"
$manifestPath = Join-Path $moduleDirectory "manifest.json"
$dockerfilePath = Join-Path $moduleDirectory "Dockerfile"
$vectorsPath = Join-Path $moduleDirectory "conformance.json"
foreach ($path in @($manifestPath, $dockerfilePath, $vectorsPath)) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "Required module file does not exist: $path"
    }
}

$manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
$version = [string]$manifest.version
if ([string]$manifest.module_id -ne $ModuleId) {
    throw "manifest module_id '$($manifest.module_id)' does not match '$ModuleId'"
}
if ([string]::IsNullOrWhiteSpace($version)) {
    throw "manifest version is required"
}
if ($version -notmatch '^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$') {
    throw "manifest version '$version' cannot be used as a Docker tag"
}
$dockerfile = Get-Content -LiteralPath $dockerfilePath -Raw
$dockerVersion = [regex]::Match($dockerfile, 'RULESHIFT_MODULE_VERSION=(?<version>[^\s]+)')
if (-not $dockerVersion.Success -or $dockerVersion.Groups['version'].Value -ne $version) {
    throw "Dockerfile RULESHIFT_MODULE_VERSION must equal manifest version $version"
}

$ruleshiftBaseUrl = $RuleshiftUrl.TrimEnd('/')
$headers = @{ Authorization = "Bearer $($env:RULESHIFT_DEVELOPER_API_KEY)" }
$versionUri = "$ruleshiftBaseUrl/v2/developer/modules/$ModuleId/versions/$version"
$validationUri = "$versionUri/validation"

Write-Step "Checking Ruleshift at $ruleshiftBaseUrl"
$health = Invoke-RestMethod -Method Get -Uri "$ruleshiftBaseUrl/healthz"
if ($health.status -ne "ok") {
    throw "Ruleshift health check returned status '$($health.status)'"
}
Ensure-ModuleKey -BaseUrl $ruleshiftBaseUrl -Id $ModuleId -Name $DisplayName -RequestHeaders $headers

$existing = Get-ExistingVersion -Uri $versionUri -RequestHeaders $headers
if ($null -ne $existing) {
    if ($existing.status -eq "active") {
        Write-Step "Version $ModuleId/$version is already active; verifying validation"
        $result = Wait-Validation `
            -VersionUri $versionUri `
            -ValidationUri $validationUri `
            -RequestHeaders $headers `
            -TimeoutSeconds $ValidationTimeoutSeconds `
            -PollSeconds $ValidationPollSeconds
        if ($result.Validation.result -ne "active") {
            throw "Existing version is active but validation result is '$($result.Validation.result)'"
        }
        $result
        exit 0
    }
    throw "Version $ModuleId/$version already exists with status '$($existing.status)'. Bump manifest and Dockerfile versions before publishing again."
}

$registryServer = ($RegistryRepository -split '/', 2)[0]
$registryToken = [Environment]::GetEnvironmentVariable($RegistryTokenEnvironment)
if (-not [string]::IsNullOrWhiteSpace($RegistryUsername)) {
    if ([string]::IsNullOrWhiteSpace($registryToken)) {
        throw "$RegistryTokenEnvironment must contain the registry token when RegistryUsername is set"
    }
    Write-Step "Authenticating Docker to $registryServer"
    $registryToken | & docker login $registryServer --username $RegistryUsername --password-stdin
    if ($LASTEXITCODE -ne 0) {
        throw "docker login failed with exit code $LASTEXITCODE"
    }

    if (-not $PublicImage) {
        Write-Step "Updating Ruleshift registry credential '$RegistryCredential'"
        $credentialBody = @{
            server = $registryServer
            username = $RegistryUsername
            token = $registryToken
        } | ConvertTo-Json -Compress
        $null = Invoke-RestMethod -Method Put `
            -Uri "$ruleshiftBaseUrl/v2/developer/registry-credentials/$RegistryCredential" `
            -Headers $headers `
            -ContentType "application/json" `
            -Body $credentialBody
    }
}

$imageTag = "${RegistryRepository}:$version"
$temporaryDescriptor = Join-Path ([IO.Path]::GetTempPath()) "ruleshift-$ModuleId-$([guid]::NewGuid().ToString('N'))-descriptor.pb"
$temporaryContainer = $null

try {
    Write-Step "Building $imageTag for $Platform"
    & docker build --pull --platform $Platform -f $dockerfilePath -t $imageTag $repoRoot
    if ($LASTEXITCODE -ne 0) {
        throw "docker build failed with exit code $LASTEXITCODE"
    }

    Write-Step "Extracting the protobuf descriptor from the built image"
    $temporaryContainer = (& docker create $imageTag).Trim()
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($temporaryContainer)) {
        throw "docker create failed"
    }
    & docker cp "${temporaryContainer}:/app/descriptor.pb" $temporaryDescriptor
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $temporaryDescriptor -PathType Leaf)) {
        throw "could not extract /app/descriptor.pb from $imageTag"
    }
    & docker rm $temporaryContainer | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "docker rm failed for temporary container $temporaryContainer"
    }
    $temporaryContainer = $null

    Write-Step "Pushing $imageTag"
    & docker push $imageTag
    if ($LASTEXITCODE -ne 0) {
        throw "docker push failed with exit code $LASTEXITCODE"
    }

    $repoDigestsJson = (& docker image inspect --format '{{json .RepoDigests}}' $imageTag).Trim()
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($repoDigestsJson)) {
        throw "could not inspect pushed image digests"
    }
    $imageRef = @($repoDigestsJson | ConvertFrom-Json) |
        Where-Object { $_ -like "${RegistryRepository}@sha256:*" } |
        Select-Object -First 1
    if ($null -eq $imageRef -or $imageRef -notmatch '^.+@sha256:[0-9a-f]{64}$') {
        throw "pushed image does not have an immutable digest for $RegistryRepository"
    }
    Write-Step "Immutable image: $imageRef"

    $publishUri = "$ruleshiftBaseUrl/v2/developer/modules/$ModuleId/versions"
    $curlArguments = @(
        '--silent', '--show-error', '--connect-timeout', '10',
        '-X', 'POST',
        '-H', "Authorization: Bearer $($env:RULESHIFT_DEVELOPER_API_KEY)",
        '-F', "manifest=@$manifestPath;type=application/json",
        '-F', "descriptor_set=@$temporaryDescriptor;type=application/octet-stream",
        '-F', "conformance_vectors=@$vectorsPath;type=application/json",
        '-F', "oci_reference=$imageRef"
    )
    if (-not $PublicImage) {
        if ([string]::IsNullOrWhiteSpace($RegistryCredential)) {
            throw "RegistryCredential is required for a private image"
        }
        $curlArguments += @('-F', "registry_credential=$RegistryCredential")
    }
    $curlArguments += @('--write-out', "`n%{http_code}", $publishUri)

    Write-Step "Publishing $ModuleId/$version to Ruleshift"
    $responseLines = @(& curl.exe @curlArguments)
    if ($LASTEXITCODE -ne 0) {
        throw "curl failed with exit code $LASTEXITCODE"
    }
    if ($responseLines.Count -eq 0 -or $responseLines[-1] -notmatch '^\d{3}$') {
        throw "Developer API returned an invalid HTTP response"
    }
    $statusCode = [int]$responseLines[-1]
    $responseBody = if ($responseLines.Count -gt 1) {
        $responseLines[0..($responseLines.Count - 2)] -join "`n"
    } else {
        ""
    }
    if ($statusCode -lt 200 -or $statusCode -ge 300) {
        throw "Developer API returned HTTP ${statusCode}: $responseBody"
    }
    $published = $responseBody | ConvertFrom-Json
    Write-Step "Publish request returned status '$($published.status)'"

    $result = Wait-Validation `
        -VersionUri $versionUri `
        -ValidationUri $validationUri `
        -RequestHeaders $headers `
        -TimeoutSeconds $ValidationTimeoutSeconds `
        -PollSeconds $ValidationPollSeconds

    if ($result.Version.status -ne "active") {
        throw "Module version finished with status '$($result.Version.status)': $($result.Validation.logs)"
    }
    if ($result.Validation.result -ne "active") {
        throw "Validation finished with result '$($result.Validation.result)': $($result.Validation.logs)"
    }
    if ([string]$result.Version.image_ref -ne [string]$imageRef) {
        throw "Ruleshift pinned '$($result.Version.image_ref)', expected '$imageRef'"
    }
    $expectedDigest = ([string]$imageRef -split '@', 2)[1]
    if ([string]$result.Version.ref.module_id -ne $ModuleId -or [string]$result.Version.ref.version -ne $version) {
        throw "Ruleshift returned an unexpected module reference"
    }
    if ([string]$result.Version.ref.image_digest -ne $expectedDigest) {
        throw "Ruleshift pinned digest '$($result.Version.ref.image_digest)', expected '$expectedDigest'"
    }
    if ([string]::IsNullOrWhiteSpace([string]$result.Version.descriptor_digest)) {
        throw "Ruleshift returned an empty descriptor digest"
    }

    Write-Step "Publication and validation succeeded"
    [PSCustomObject]@{
        ModuleId = $ModuleId
        Version = $version
        Status = $result.Version.status
        ImageRef = $result.Version.image_ref
        DescriptorDigest = $result.Version.descriptor_digest
        ValidationResult = $result.Validation.result
        ValidationLogs = $result.Validation.logs
    }
}
finally {
    if (-not [string]::IsNullOrWhiteSpace($temporaryContainer)) {
        & docker rm -f $temporaryContainer 2>$null | Out-Null
    }
    Remove-Item -LiteralPath $temporaryDescriptor -Force -ErrorAction SilentlyContinue
}
