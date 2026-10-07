param([switch]$KeepArtifacts)
$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$directory=[IO.Path]::GetFullPath((Join-Path $workspace ('.task/equivalence-'+[guid]::NewGuid().ToString('N'))))
$prefix=Join-Path $workspace '.task/equivalence-'
if(-not $directory.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase) -or [IO.Path]::GetDirectoryName($directory) -ne (Join-Path $workspace '.task')){throw 'Build equivalence directory escaped workspace'}
$null=New-Item -ItemType Directory -Path $directory
$sourcePaths=@('internal/storage/sqlite/round_build_equivalence_test.go','internal/storage/sqlite/round_product_test.go','internal/storage/sqlite/round_writes.go','internal/storage/sqlite/round_queries.go','internal/roundlifecycle/service.go','internal/roundlifecycle/commands.go')
$sourceHashes=@{}
foreach($name in $sourcePaths){$sourceHashes[$name]=(Get-FileHash -LiteralPath (Join-Path $workspace $name) -Algorithm SHA256).Hash}
function Invoke-EquivalenceFixture([string]$Executable,[string]$Output,[string]$TempRoot,[string]$Label){
 $null=New-Item -ItemType Directory -Path $TempRoot
 $start=New-Object Diagnostics.ProcessStartInfo
 $start.FileName=$Executable
 $start.Arguments='-test.run=^TestRoundBuildEquivalenceActualSQLiteThreeTwoOneTrace$ -test.timeout=45s -test.v'
 $start.WorkingDirectory=$workspace
 $start.UseShellExecute=$false
 $start.CreateNoWindow=$true
 $start.RedirectStandardOutput=$true
 $start.RedirectStandardError=$true
 $start.EnvironmentVariables['JACKPOT_BUILD_EQUIVALENCE_OUTPUT']=$Output
 $start.EnvironmentVariables['TEMP']=$TempRoot
 $start.EnvironmentVariables['TMP']=$TempRoot
 $process=New-Object Diagnostics.Process
 $process.StartInfo=$start
 $ownedId=$null
 try{
  if(-not $process.Start()){throw ($Label+' fixture did not start')}
  $ownedId=$process.Id
  $stdout=$process.StandardOutput.ReadToEndAsync()
  $stderr=$process.StandardError.ReadToEndAsync()
  if(-not $process.WaitForExit(60000)){$process.Kill();$null=$process.WaitForExit(5000);throw ($Label+' fixture exceeded bounded timeout')}
  $log=$stdout.Result+$stderr.Result
  [IO.File]::WriteAllText((Join-Path $directory ($Label+'.log')),$log,(New-Object Text.UTF8Encoding($false)))
  if($process.ExitCode -ne 0){throw ($Label+' fixture failed: '+$log)}
  if(-not(Test-Path -LiteralPath $Output -PathType Leaf)){throw ($Label+' canonical trace missing')}
  if(@(Get-ChildItem -LiteralPath $TempRoot -Force).Count -ne 0){throw ($Label+' fixture left temporary DB/sidecars/directory')}
  $data=[IO.File]::ReadAllText($Output)|ConvertFrom-Json
  if($data.connectionInUse -ne 0 -or $data.closedConnections -ne 0 -or -not $data.integrityOK -or -not $data.foreignKeysOK -or -not $data.remainingZeroDenied -or $data.entropyCalls -ne 3 -or $data.steps.Count -ne 3 -or $data.finalRounds.Count -ne 3 -or $data.revision -ne 9){throw ($Label+' canonical fixture/resource invariant failed')}
  return @{label=$Label;ownedPid=$ownedId;exitCode=$process.ExitCode;pidExited=$process.HasExited;tempEmpty=$true;traceBytes=(Get-Item -LiteralPath $Output).Length;traceSHA256=(Get-FileHash -LiteralPath $Output -Algorithm SHA256).Hash;executableSHA256=(Get-FileHash -LiteralPath $Executable -Algorithm SHA256).Hash}
 }finally{
  if($null -ne $ownedId -and -not $process.HasExited){$process.Kill();$null=$process.WaitForExit(5000)}
  $process.Dispose()
 }
}
$passed=$false
try{
 Push-Location $workspace
 try{
  $goVersion=(& go version|Out-String).Trim()
  if($LASTEXITCODE -ne 0){throw 'Go version lookup failed'}
  $optimized=Join-Path $directory 'optimized.test.exe'
  $unoptimized=Join-Path $directory 'unoptimized.test.exe'
  & go test -c '-gcflags=all=' -trimpath -o $optimized ./internal/storage/sqlite
  if($LASTEXITCODE -ne 0){throw 'Optimized test binary build failed'}
  & go test -c '-gcflags=all=-N -l' -trimpath -o $unoptimized ./internal/storage/sqlite
  if($LASTEXITCODE -ne 0){throw 'Unoptimized test binary build failed'}
  $optimizedOutput=Join-Path $directory 'optimized.json'
  $unoptimizedOutput=Join-Path $directory 'unoptimized.json'
  $first=Invoke-EquivalenceFixture $optimized $optimizedOutput (Join-Path $directory 'optimized-temp') 'optimized'
  $second=Invoke-EquivalenceFixture $unoptimized $unoptimizedOutput (Join-Path $directory 'unoptimized-temp') 'unoptimized'
  $left=[IO.File]::ReadAllBytes($optimizedOutput)
  $right=[IO.File]::ReadAllBytes($unoptimizedOutput)
  if($left.Length -ne $right.Length -or $first.traceSHA256 -ne $second.traceSHA256){throw 'Optimized/unoptimized canonical output SHA/length differs'}
  for($index=0;$index -lt $left.Length;$index++){if($left[$index] -ne $right[$index]){throw ('Optimized/unoptimized canonical byte differs at '+$index)}}
  $manifest=@{status='passed';goVersion=$goVersion;optimizedFlags=@('go test -c','-gcflags=all=','-trimpath');unoptimizedFlags=@('go test -c','-gcflags=all=-N -l','-trimpath');fixture='actual SQLite 3 -> 2 -> 1; controlled entropy; password-free local creation; replay and readonly final state';exactAllFieldsAndBytesEqual=$true;traceSHA256=$first.traceSHA256;traceBytes=$left.Length;runs=@($first,$second);sourceSHA256=$sourceHashes;processKillOrPowerLossClaim=$false;passwordRequired=$false}
  $manifestText=$manifest|ConvertTo-Json -Depth 8
  [IO.File]::WriteAllText((Join-Path $directory 'manifest.json'),$manifestText,(New-Object Text.UTF8Encoding($false)))
  [IO.File]::WriteAllText((Join-Path $workspace '.task/build-equivalence-report.json'),$manifestText,(New-Object Text.UTF8Encoding($false)))
  $passed=$true
  Write-Output ('Build equivalence PASS: exact '+$left.Length+' bytes / SHA256 '+$first.traceSHA256+'; 3 rounds / 3 replay / RNG3 / handles0 / 2 owned PID exited / 2 TEMP roots empty')
 }finally{Pop-Location}
}finally{
 foreach($name in $sourceHashes.Keys){if((Get-FileHash -LiteralPath (Join-Path $workspace $name) -Algorithm SHA256).Hash -ne $sourceHashes[$name]){throw ('Build equivalence original source changed: '+$name)}}
 $resolved=[IO.Path]::GetFullPath($directory)
 if(-not $resolved.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase) -or [IO.Path]::GetDirectoryName($resolved) -ne (Join-Path $workspace '.task')){throw 'Build equivalence cleanup escaped workspace'}
 if(-not $KeepArtifacts -and (Test-Path -LiteralPath $resolved)){Remove-Item -LiteralPath $resolved -Recurse -Force}
 if(-not $KeepArtifacts -and (Test-Path -LiteralPath $resolved)){throw 'Build equivalence artifact cleanup failed'}
 if($KeepArtifacts){Write-Output ('Owned equivalence artifact retained: '+$resolved)}else{Write-Output 'Original source SHA256 and owned equivalence artifact cleanup PASS'}
}