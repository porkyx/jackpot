param([string]$NodePath = 'node', [switch]$Keyboard)
$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent))
$taskRoot = Join-Path $projectRoot '.task'
$nativeDirectory = $null
Push-Location $projectRoot
try {
    Push-Location (Join-Path $projectRoot 'frontend')
    try {
        & $NodePath node_modules/vite/bin/vite.js build --config scripts/native-smoke.vite.config.ts
        if ($LASTEXITCODE -ne 0) { throw 'Native smoke assets failed to build' }
    } finally { Pop-Location }
    $assets = Join-Path $taskRoot 'native-smoke-assets'
    Copy-Item -LiteralPath (Join-Path $assets 'native-smoke.html') -Destination (Join-Path $assets 'index.html')
    $executable = Join-Path $taskRoot 'wailssmoke.exe'
    & go build -o $executable ./cmd/wailssmoke
    if ($LASTEXITCODE -ne 0) { throw 'Native smoke executable failed to build' }
    $report = Join-Path $taskRoot 'native-smoke-report.json'
    if ($Keyboard) {
        & $NodePath frontend/tests/native/keyboard.driver.mjs --exe $executable --assets $assets --report $report
    } else {
        $nativeDirectory = Join-Path $taskRoot ('native-run-' + [Guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Path $nativeDirectory | Out-Null
        & $executable -assets $assets -out $report -work-dir $nativeDirectory
    }
    if ($LASTEXITCODE -ne 0) { throw 'Actual native Wails verification failed' }
    Get-Content -LiteralPath $report
} finally {
    if ($null -ne $nativeDirectory) {
        $target = [IO.Path]::GetFullPath($nativeDirectory)
        $prefix = [IO.Path]::GetFullPath($taskRoot).TrimEnd([char[]]'\/') + [IO.Path]::DirectorySeparatorChar
        if (-not $target.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase) -or -not [IO.Path]::GetFileName($target).StartsWith('native-run-',[StringComparison]::Ordinal)) { throw 'Native cleanup target escaped the task directory' }
        Remove-Item -LiteralPath $target -Recurse -Force
    }
    Pop-Location
}
