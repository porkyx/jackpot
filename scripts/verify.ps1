param(
    [ValidateSet('fast', 'full', 'release')][string]$Suite = 'fast',
    [string]$NodePath = 'node',
    [string]$WailsPath = 'wails3'
)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
$frontendRoot = Join-Path $projectRoot 'frontend'
$taskRoot = Join-Path $projectRoot '.task'
New-Item -ItemType Directory -Force $taskRoot | Out-Null

function Invoke-Check([string]$Name, [string]$Executable, [string[]]$Arguments) {
    Write-Host "Checking $Name"
    & $Executable @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Name failed (exit $LASTEXITCODE)" }
}
function Assert-SameFiles([string]$Expected, [string]$Actual) {
    $expectedRoot = [System.IO.Path]::GetFullPath($Expected).TrimEnd([char[]]'\/')
    $actualRoot = [System.IO.Path]::GetFullPath($Actual).TrimEnd([char[]]'\/')
    # GetRelativePath is absent from Windows PowerShell 5.1's .NET runtime.
    $expectedNames = @(Get-ChildItem -LiteralPath $expectedRoot -File -Recurse | ForEach-Object { $_.FullName.Substring($expectedRoot.Length + 1) } | Sort-Object)
    $actualNames = @(Get-ChildItem -LiteralPath $actualRoot -File -Recurse | ForEach-Object { $_.FullName.Substring($actualRoot.Length + 1) } | Sort-Object)
    if ($expectedNames.Count -eq 0 -or (Compare-Object $expectedNames $actualNames)) { throw "Contract file set differs: $Expected" }
    foreach ($name in $expectedNames) {
        if ((Get-FileHash -LiteralPath (Join-Path $expectedRoot $name)).Hash -ne (Get-FileHash -LiteralPath (Join-Path $actualRoot $name)).Hash) { throw "Contract content differs: $name" }
    }
}

function Remove-VerificationDirectory([string]$Path) {
    if ([string]::IsNullOrEmpty($Path) -or -not (Test-Path -LiteralPath $Path)) { return }
    $absolute = [IO.Path]::GetFullPath($Path).TrimEnd([char[]]'\/')
    $prefix = [IO.Path]::GetFullPath($taskRoot).TrimEnd([char[]]'\/') + [IO.Path]::DirectorySeparatorChar
    $name = [IO.Path]::GetFileName($absolute)
    if (-not $absolute.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase) -or ($name -notlike 'contract-check-*' -and $name -notlike 'bindings-check-*')) { throw 'Verification cleanup escaped the owned temporary directory' }
    $entries = @((Get-Item -LiteralPath $absolute -Force)) + @(Get-ChildItem -LiteralPath $absolute -Force -Recurse)
    foreach ($entry in $entries) { if (($entry.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'Verification cleanup found an unexpected reparse point' } }
    Remove-Item -LiteralPath $absolute -Recurse -Force
    if (Test-Path -LiteralPath $absolute) { throw 'Verification temporary directory was not removed' }
}
$fixtureRoot = $null
$bindingRoot = $null
Push-Location $projectRoot
try {
    Invoke-Check 'Go architecture policy' 'go' @('run', './cmd/gopolicy')
    if ($Suite -eq 'fast') { Invoke-Check 'Go unit and SQLite regression' 'go' @('test', './...') }
    else {
        Invoke-Check 'Go race regression' 'go' @('test', './...', '-race', '-count=1', '-coverpkg=./internal/...', '-coverprofile=.task/full.cover')
        Invoke-Check 'Go unoptimized regression' 'go' @('test', './...', '-count=1', '-gcflags=all=-N -l')
        Invoke-Check 'Go vet' 'go' @('vet', './...')
    }
    $fixtureRoot = Join-Path $taskRoot ('contract-check-' + [System.Guid]::NewGuid().ToString('N'))
    Invoke-Check 'actual Go serializer and SQLite service fixtures' 'go' @('run', './cmd/ipcfixtures', '-out', $fixtureRoot)
    Assert-SameFiles (Join-Path $projectRoot 'testdata/ipc') $fixtureRoot
    Invoke-Check 'read-only source fixture integrity' $NodePath @('--test', 'testdata/dcinside/fixtures.test.mjs', 'testdata/parity/observer.test.mjs')
    Push-Location $frontendRoot
    try {
        Invoke-Check 'strict TypeScript' $NodePath @('node_modules/typescript/bin/tsc', '--noEmit')
        Invoke-Check 'frontend import policy' $NodePath @('scripts/policy.mjs')
        Invoke-Check 'policy independent harness' $NodePath @('--test', 'scripts/policy.test.mjs')
        Invoke-Check 'production and development content security policy' $NodePath @('--test', 'scripts/content-security.test.mjs')
        Invoke-Check 'unit DOM and Go contract tests' $NodePath @('node_modules/vitest/vitest.mjs', 'run')
        if ($Suite -ne 'fast') {
            Invoke-Check 'Schema branch coverage' $NodePath @('node_modules/vitest/vitest.mjs', 'run', '--coverage')
            Invoke-Check 'frontend optimized build' $NodePath @('node_modules/vite/bin/vite.js', 'build')
        }
    } finally { Pop-Location }
    if ($Suite -ne 'fast') {
        $bindingRoot = Join-Path $taskRoot ('bindings-check-' + [System.Guid]::NewGuid().ToString('N'))
        Invoke-Check 'generated Wails TypeScript interfaces and events' $WailsPath @('generate', 'bindings', '-clean', '-ts', '-i', '-d', $bindingRoot)
        Assert-SameFiles (Join-Path $frontendRoot 'bindings') $bindingRoot
        Invoke-Check 'Windows desktop build' 'go' @('build', '-o', '.task/jackpot-verified.exe', '.')
    }
    Write-Host 'This suite does not execute native/product/OS/package gates. Use verify-product.ps1, verify-product-stress.ps1, verify-native-export.ps1 and verify-package.ps1; pre-release.ps1 also requires candidate RC evidence.'
    if ($Suite -eq 'release') { throw 'Release gate is incomplete: native/product E2E and the concrete RC checklist require recorded evidence.' }
    Write-Host "$Suite checks passed. This result is not a release approval."
} finally { try { Remove-VerificationDirectory $fixtureRoot; Remove-VerificationDirectory $bindingRoot } finally { Pop-Location } }
