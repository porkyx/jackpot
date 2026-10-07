param([string]$NodePath='node',[string]$WailsPath='wails3')
$ErrorActionPreference='Stop'
$projectRoot=[IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent))
Push-Location $projectRoot
try {
    & (Join-Path $PSScriptRoot 'pre-release-gates.test.ps1')
    & (Join-Path $PSScriptRoot 'verify.ps1') -Suite full -NodePath $NodePath -WailsPath $WailsPath
    & (Join-Path $PSScriptRoot 'build-release.ps1') -NodePath $NodePath
    & (Join-Path $PSScriptRoot 'verify-product.ps1') -NodePath $NodePath
    & (Join-Path $PSScriptRoot 'verify-product-stress.ps1')
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'verify-package.ps1')
    if($LASTEXITCODE -ne 0){throw 'Actual production package regression failed'}
    & go build -tags production -o .task/native-close-seed.exe ./cmd/nativecloseseed
    if($LASTEXITCODE -ne 0){throw 'Native close reservation seed build failed'}
    $candidate=Join-Path $projectRoot 'dist/Jackpot-0.1.0-windows-x64/Jackpot.exe'
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'verify-native-close.ps1') -Candidate 'dist/Jackpot-0.1.0-windows-x64/Jackpot.exe'
    if($LASTEXITCODE -ne 0){throw 'Actual production close confirmation failed'}
    $closeReport=Get-Content -LiteralPath .task/native-close-report.json -Raw -Encoding UTF8|ConvertFrom-Json
    if($closeReport.status -ne 'passed' -or $closeReport.checks.Count -ne 4 -or -not $closeReport.cleanupVerified -or $closeReport.candidateSHA256 -ne (Get-FileHash -LiteralPath $candidate -Algorithm SHA256).Hash){throw 'Native close evidence does not identify this cleaned-up release candidate'}
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'verify-native-export.ps1')
    if($LASTEXITCODE -ne 0){throw 'Actual OS export regression failed'}
    $acceptancePath=Join-Path $projectRoot 'docs/releases/0.1.0/rc-acceptance.json'
    if(-not(Test-Path -LiteralPath $acceptancePath -PathType Leaf)){throw 'RC review is incomplete: record candidate digest, fuzz/mutation/resource/performance/rollback and actual Windows input/sleep evidence before release approval.'}
    $acceptance=Get-Content -LiteralPath $acceptancePath -Raw -Encoding UTF8 | ConvertFrom-Json
    foreach($gate in @('fuzz','mutation','resources','performance','rollback','humanReview')){
        if($acceptance.gates.$gate.status -ne 'passed' -or [string]::IsNullOrWhiteSpace($acceptance.gates.$gate.evidence)){throw ('RC evidence missing or failed: '+$gate)}
    }
    foreach($gate in @('nativeIME','nativeSleep')){
        $physical=$acceptance.gates.$gate
        $excluded=$physical.status -eq 'excluded_by_user' -and $physical.approvedBy -eq 'user'
        if(($physical.status -ne 'passed' -and -not $excluded) -or [string]::IsNullOrWhiteSpace($physical.evidence)){throw ('RC physical evidence or explicit user exclusion missing: '+$gate)}
    }
    $candidate=Join-Path $projectRoot 'dist/Jackpot-0.1.0-windows-x64/Jackpot.exe'
    if(-not(Test-Path -LiteralPath $candidate) -or $acceptance.candidateSHA256 -ne (Get-FileHash -LiteralPath $candidate -Algorithm SHA256).Hash){throw 'RC acceptance does not identify the current candidate executable'}
    Write-Host 'Recorded regression and RC acceptance gates passed. No package was published.'
} finally {Pop-Location}
