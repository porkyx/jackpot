param([string]$NodePath='node')
$ErrorActionPreference='Stop'
$projectRoot=[IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent))
Push-Location $projectRoot
try {
    $goFiles=@(& rg --files -g '*.go')
    if($LASTEXITCODE -ne 0 -or $goFiles.Count -eq 0){throw 'Go source inventory failed'}
    $unformatted=@(& gofmt -l @goFiles)
    if($LASTEXITCODE -ne 0 -or $unformatted.Count -gt 0){throw ('Go formatting failed: '+($unformatted -join ', '))}
    & (Join-Path $PSScriptRoot 'verify.ps1') -Suite fast -NodePath $NodePath
} finally {Pop-Location}