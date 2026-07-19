[CmdletBinding()]
param(
    [string]$HostUrl = 'https://tb.oidcs.com',
    [string]$Username = 'tenant@thingsboard.org',
    [string]$OutputDir = '.scratch/go-data-ai-platform/research/thingsboard-contract-evidence',
    [int]$WindowHours = 24,
    [int]$RepresentativesPerProfile = 2,
    [int]$MaxEntities = 500
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..')).Path
$captureScript = Join-Path $PSScriptRoot 'capture-thingsboard-contracts.mjs'

if (-not (Test-Path $captureScript)) {
    throw "Capture script not found: $captureScript"
}

$node = Get-Command node.exe -ErrorAction SilentlyContinue
if (-not $node) {
    $knownNode = 'D:\nvm4w\nodejs\node.exe'
    if (Test-Path $knownNode) {
        $nodePath = $knownNode
    } else {
        throw 'node.exe was not found. Install Node.js 20+ or add it to PATH.'
    }
} else {
    $nodePath = $node.Source
}

$securePassword = Read-Host 'ThingsBoard password' -AsSecureString
$password = [System.Net.NetworkCredential]::new('', $securePassword).Password

try {
    Set-Location $repoRoot
    $env:TB_HOST = $HostUrl
    $env:TB_USERNAME = $Username
    $env:TB_PASSWORD = $password
    $env:TB_OUTPUT_DIR = $OutputDir
    $env:TB_WINDOW_HOURS = [string]$WindowHours
    $env:TB_REPRESENTATIVES_PER_PROFILE = [string]$RepresentativesPerProfile
    $env:TB_MAX_ENTITIES = [string]$MaxEntities

    & $nodePath $captureScript
    if ($LASTEXITCODE -ne 0) {
        throw "ThingsBoard contract capture failed with exit code $LASTEXITCODE"
    }

    $report = Join-Path $repoRoot (Join-Path $OutputDir 'capture-report.md')
    Write-Host "Capture completed: $report"
} finally {
    Remove-Item Env:TB_PASSWORD -ErrorAction SilentlyContinue
    Remove-Item Env:TB_USERNAME -ErrorAction SilentlyContinue
    Remove-Item Env:TB_HOST -ErrorAction SilentlyContinue
    Remove-Item Env:TB_OUTPUT_DIR -ErrorAction SilentlyContinue
    Remove-Item Env:TB_WINDOW_HOURS -ErrorAction SilentlyContinue
    Remove-Item Env:TB_REPRESENTATIVES_PER_PROFILE -ErrorAction SilentlyContinue
    Remove-Item Env:TB_MAX_ENTITIES -ErrorAction SilentlyContinue
    $password = $null
    $securePassword = $null
}
