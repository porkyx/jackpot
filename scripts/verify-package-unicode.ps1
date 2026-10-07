param([string]$Candidate='dist/Jackpot-0.1.0-windows-x64/Jackpot.exe')
$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$task=Join-Path $workspace '.task'
$baseline=Join-Path $workspace 'scripts/verify-package.ps1'
$directory=Join-Path $task ('verified-test-package-unicode-'+[guid]::NewGuid().ToString('N'))
$reportPath=Join-Path $directory 'report.json'
$driver=Join-Path $directory 'driver.ps1'
$candidatePath=[IO.Path]::GetFullPath((Join-Path $workspace $Candidate))
$releasePrefix=[IO.Path]::GetFullPath((Join-Path $task 'releases')).TrimEnd([char[]]'\/')+[IO.Path]::DirectorySeparatorChar
if(-not $candidatePath.StartsWith($releasePrefix,[StringComparison]::OrdinalIgnoreCase) -or [IO.Path]::GetFileName($candidatePath) -cne 'Jackpot.exe' -or (Get-Item -LiteralPath $candidatePath).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Exact workspace portable Jackpot.exe required'}
if([IO.Path]::GetDirectoryName([IO.Path]::GetFullPath($directory)) -ine $task){throw 'Unicode evidence escaped workspace'}
$null=New-Item -ItemType Directory -Path $directory
$sourceSHA=(Get-FileHash -LiteralPath $baseline -Algorithm SHA256).Hash
$candidateSHA=(Get-FileHash -LiteralPath $candidatePath -Algorithm SHA256).Hash
$callerState=@{};foreach($name in @('APPDATA','LOCALAPPDATA','PATH')){$callerState[$name]=[Environment]::GetEnvironmentVariable($name)}
function Quote-Literal([string]$Value){"'"+$Value.Replace("'","''")+"'"}
$source=[IO.File]::ReadAllText($baseline)
$changes=@(
 @('$projectRoot=[IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent))',('$projectRoot='+ (Quote-Literal $workspace))),
 @('$runRoot=Join-Path $taskRoot (''package-run-''+[Guid]::NewGuid().ToString(''N''))','$runRoot=Join-Path $taskRoot (''package-run-한글 데이터 공백-''+[Guid]::NewGuid().ToString(''N''))'),
 @('$reportPath=Join-Path $taskRoot ''package-e2e-report.json''',('$reportPath='+ (Quote-Literal $reportPath))),
 @('$active=$null','$report[''runRoot'']=$runRoot;$report[''unicodeDataDirectory'']=$true;$report[''clipboardTouched'']=$false;$active=$null')
)
foreach($change in $changes){if(-not $source.Contains($change[0])){throw 'Portable source anchor changed'};$source=$source.Replace($change[0],$change[1])}
[IO.File]::WriteAllText($driver,$source,(New-Object Text.UTF8Encoding($true)))
$tokens=$null;$errors=$null;$null=[System.Management.Automation.Language.Parser]::ParseFile($driver,[ref]$tokens,[ref]$errors)
if($errors.Count){throw 'Unicode private driver syntax failed'}
$manifest=[ordered]@{status='prepared';candidate=$candidatePath;candidateSHA256=$candidateSHA;baseline=$baseline;baselineSHA256=$sourceSHA;privateDriver=$driver;privateDriverSHA256=(Get-FileHash -LiteralPath $driver -Algorithm SHA256).Hash;profile='child APPDATA/LOCALAPPDATA only; package-run-한글 데이터 공백-*';OSUserAccountChanged=$false;OSSettingsChanged=$false;clipboardTouched=$false;newGoBuild=$false}
$manifestPath=Join-Path $directory 'manifest.json'
$utf8=New-Object Text.UTF8Encoding($false)
[IO.File]::WriteAllText($manifestPath,($manifest|ConvertTo-Json -Depth 5),$utf8)
Push-Location $workspace
try {
 & powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $driver -Candidate $Candidate *> (Join-Path $directory 'run.log')
 if($LASTEXITCODE -ne 0){throw ('Unicode package native failed; preserved '+$reportPath)}
 $report=[IO.File]::ReadAllText($reportPath)|ConvertFrom-Json
 if($report.status -ne 'passed' -or $report.cleanupVerified -ne $true -or $report.unicodeDataDirectory -ne $true -or $report.checks.Count -ne 5 -or $report.candidateSHA256 -cne $candidateSHA -or $report.clipboardTouched -ne $false){throw 'Unicode native lifecycle evidence incomplete'}
 if($report.runRoot -notmatch 'package-run-한글 데이터 공백-[a-f0-9]{32}$' -or (Test-Path -LiteralPath $report.runRoot)){throw 'Unicode profile path/cleanup incomplete'}
 if((Get-FileHash -LiteralPath $baseline -Algorithm SHA256).Hash -cne $sourceSHA -or (Get-FileHash -LiteralPath $candidatePath -Algorithm SHA256).Hash -cne $candidateSHA){throw 'Original source or candidate changed'}
 foreach($name in $callerState.Keys){if([Environment]::GetEnvironmentVariable($name) -cne $callerState[$name]){throw 'Caller environment changed'}}
 $manifest.status='passed';$manifest.callerEnvironmentUnchanged=$true;$manifest.report=$reportPath
 Write-Output ('Unicode package path native PASS; production boot/no-history/duplicate-no-write/close/restart, real SQLite/header and profile cleanup; '+$reportPath)
}catch{$manifest.status='failed';$manifest.failure=$_.Exception.Message;throw}
finally{[IO.File]::WriteAllText($manifestPath,($manifest|ConvertTo-Json -Depth 5),$utf8);Pop-Location}
