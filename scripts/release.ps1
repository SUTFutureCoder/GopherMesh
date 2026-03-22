[CmdletBinding()]
param(
    [string]$Version,
    [string]$OutputDir = "dist",
    [switch]$Publish,
    [switch]$SkipTests
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Resolve-Version {
    param([string]$RequestedVersion)

    if ($RequestedVersion) {
        return $RequestedVersion.Trim()
    }

    $tag = (& git describe --tags --exact-match HEAD 2>$null)
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($tag)) {
        throw "HEAD is not exactly on a git tag. Pass -Version explicitly, for example: -Version v1.2.0"
    }

    return $tag.Trim()
}

function Assert-TagExists {
    param([string]$Tag)

    $existing = (& git tag --list $Tag)
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($existing)) {
        throw "Git tag '$Tag' does not exist locally."
    }
}

function New-CleanDirectory {
    param([string]$Path)

    if (Test-Path $Path) {
        Remove-Item -Recurse -Force $Path
    }
    New-Item -ItemType Directory -Path $Path | Out-Null
}

function Invoke-GoBuild {
    param(
        [string]$Goos,
        [string]$Goarch,
        [string]$OutputPath
    )

    $previous = @{
        CGO_ENABLED = $env:CGO_ENABLED
        GOOS        = $env:GOOS
        GOARCH      = $env:GOARCH
    }

    try {
        $env:CGO_ENABLED = "0"
        $env:GOOS = $Goos
        $env:GOARCH = $Goarch

        & go build -trimpath -ldflags "-s -w" -o $OutputPath .
        if ($LASTEXITCODE -ne 0) {
            throw "go build failed for $Goos/$Goarch"
        }
    }
    finally {
        $env:CGO_ENABLED = $previous.CGO_ENABLED
        $env:GOOS = $previous.GOOS
        $env:GOARCH = $previous.GOARCH
    }
}

function New-ReleaseArchive {
    param(
        [string]$ProjectName,
        [string]$VersionTag,
        [hashtable]$Target,
        [string]$StageRoot,
        [string]$ReleaseRoot,
        [string]$RepoRoot
    )

    $archiveBase = "{0}_{1}_{2}_{3}" -f $ProjectName, $VersionTag, $Target.GOOS, $Target.GOARCH
    $stageDir = Join-Path $StageRoot $archiveBase
    New-CleanDirectory -Path $stageDir

    $binaryName = if ($Target.GOOS -eq "windows") { "$ProjectName.exe" } else { $ProjectName }
    $binaryPath = Join-Path $stageDir $binaryName

    Write-Host "Building $($Target.GOOS)/$($Target.GOARCH) -> $archiveBase.zip"
    Invoke-GoBuild -Goos $Target.GOOS -Goarch $Target.GOARCH -OutputPath $binaryPath

    Copy-Item (Join-Path $RepoRoot "README.md") (Join-Path $stageDir "README.md")
    Copy-Item (Join-Path $RepoRoot "LICENSE") (Join-Path $stageDir "LICENSE")
    Copy-Item (Join-Path $RepoRoot "sample\sample_config.json") (Join-Path $stageDir "sample_config.json")

    $archivePath = Join-Path $ReleaseRoot ($archiveBase + ".zip")
    if (Test-Path $archivePath) {
        Remove-Item -Force $archivePath
    }
    Compress-Archive -Path (Join-Path $stageDir "*") -DestinationPath $archivePath -CompressionLevel Optimal

    return $archivePath
}

function Write-Sha256Sums {
    param(
        [string[]]$AssetPaths,
        [string]$OutputPath
    )

    $lines = foreach ($asset in $AssetPaths | Sort-Object) {
        $hash = (Get-FileHash -Algorithm SHA256 -Path $asset).Hash.ToLowerInvariant()
        "{0}  {1}" -f $hash, (Split-Path $asset -Leaf)
    }

    [System.IO.File]::WriteAllLines($OutputPath, $lines)
}

function Publish-GitHubRelease {
    param(
        [string]$VersionTag,
        [string[]]$Assets
    )

    if (-not (Get-Command gh -ErrorAction SilentlyContinue)) {
        throw "GitHub CLI 'gh' was not found in PATH."
    }

    & gh release view $VersionTag *> $null
    $releaseExists = ($LASTEXITCODE -eq 0)

    if ($releaseExists) {
        & gh release upload $VersionTag @Assets --clobber
        if ($LASTEXITCODE -ne 0) {
            throw "gh release upload failed for $VersionTag"
        }
        return
    }

    & gh release create $VersionTag @Assets --title $VersionTag --generate-notes
    if ($LASTEXITCODE -ne 0) {
        throw "gh release create failed for $VersionTag"
    }
}

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
Push-Location $repoRoot

try {
    $resolvedVersion = Resolve-Version -RequestedVersion $Version
    Assert-TagExists -Tag $resolvedVersion

    if (-not $SkipTests) {
        Write-Host "Running go test ./..."
        & go test ./...
        if ($LASTEXITCODE -ne 0) {
            throw "go test ./... failed"
        }
    }

    $releaseRoot = Join-Path $repoRoot (Join-Path $OutputDir $resolvedVersion)
    $stageRoot = Join-Path $releaseRoot ".stage"
    New-CleanDirectory -Path $releaseRoot
    New-CleanDirectory -Path $stageRoot

    $targets = @(
        @{ GOOS = "windows"; GOARCH = "amd64" },
        @{ GOOS = "windows"; GOARCH = "arm64" },
        @{ GOOS = "linux"; GOARCH = "amd64" },
        @{ GOOS = "linux"; GOARCH = "arm64" },
        @{ GOOS = "darwin"; GOARCH = "amd64" },
        @{ GOOS = "darwin"; GOARCH = "arm64" }
    )

    $assets = foreach ($target in $targets) {
        New-ReleaseArchive -ProjectName "gophermesh" -VersionTag $resolvedVersion -Target $target -StageRoot $stageRoot -ReleaseRoot $releaseRoot -RepoRoot $repoRoot
    }

    $checksumsPath = Join-Path $releaseRoot "SHA256SUMS.txt"
    Write-Sha256Sums -AssetPaths $assets -OutputPath $checksumsPath
    $assets += $checksumsPath

    Remove-Item -Recurse -Force $stageRoot

    Write-Host ""
    Write-Host "Release artifacts created in: $releaseRoot"
    Get-ChildItem $releaseRoot | Select-Object Name, Length

    if ($Publish) {
        Write-Host ""
        Write-Host "Publishing GitHub release $resolvedVersion"
        Publish-GitHubRelease -VersionTag $resolvedVersion -Assets $assets
    }
}
finally {
    Pop-Location
}
