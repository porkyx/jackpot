$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$task=Join-Path $workspace '.task'
$directory=Join-Path $task ('verified-test-recovery-timer-'+[guid]::NewGuid().ToString('N'))
$utf8=New-Object Text.UTF8Encoding($false)
function Assert-OwnedDirectory([string]$Path){
 $full=[IO.Path]::GetFullPath($Path).TrimEnd([char[]]'\/')
 if([IO.Path]::GetDirectoryName($full) -ne $task -or -not [IO.Path]::GetFileName($full).StartsWith('verified-test-recovery-timer-',[StringComparison]::Ordinal)){throw 'Timer overlay path escaped owned task root'}
 if(Test-Path -LiteralPath $full){
  $queue=New-Object 'Collections.Generic.Queue[string]';$queue.Enqueue($full)
  while($queue.Count -gt 0){$item=Get-Item -LiteralPath $queue.Dequeue() -Force;if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw 'Timer overlay path has reparse point'};if($item.PSIsContainer){foreach($child in @(Get-ChildItem -LiteralPath $item.FullName -Force)){$queue.Enqueue($child.FullName)}}}
 }
 return $full
}
function Replace-Occurrence([string]$Source,[string]$Before,[string]$After,[int]$Occurrence){
 $at=-1
 for($i=0;$i -lt $Occurrence;$i++){$at=$Source.IndexOf($Before,$at+1,[StringComparison]::Ordinal);if($at -lt 0){throw 'Timer mutation source marker missing'}}
 return $Source.Substring(0,$at)+$After+$Source.Substring($at+$Before.Length)
}
$paths=@('internal/roundlifecycle/recovery_timer.go','internal/storage/sqlite/recovery_hint.go','internal/roundlifecycle/recovery_timer_test.go','internal/storage/sqlite/recovery_hint_test.go')
$sources=@{};$hashes=@{}
foreach($path in $paths){$file=Join-Path $workspace $path;$sources[$path]=[IO.File]::ReadAllText($file).Replace("`r`n","`n");$hashes[$path]=(Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash}
$rl='internal/roundlifecycle/recovery_timer.go';$sql='internal/storage/sqlite/recovery_hint.go'
$cases=@(
 @{name='idle must skip full scan';file=$rl;old='if !present {';new='if !present && false {';test='^TestRecoverTimerFalse';package='./internal/roundlifecycle'},
 @{name='active must recover';file=$rl;old='if !present {';new='if !present || present {';test='^TestRecoveryHintFalseThenAdmission';package='./internal/storage/sqlite'},
 @{name='unsupported retains full path';file=$rl;old='return service.Recover(ctx, request)';new='return RecoveryReport{Completed: make([]contracts.RoundID,0), Failed: make([]contracts.RoundID,0), Due: make([]contracts.RoundID,0)}, nil';test='^TestRecoverTimerFalse';package='./internal/roundlifecycle'},
 @{name='supported hint needs lifecycle reader';file=$rl;old='if _, err = service.reader(); err != nil {';new='if _, err = service.reader(); false {';test='^TestRecoverTimerFalse';package='./internal/roundlifecycle'},
 @{name='hint error fail closed';file=$rl;old="present, err := reader.HasRecoveryWork(ctx)`n`tif err != nil {";new="present, err := reader.HasRecoveryWork(ctx)`n`tif err != nil && false {";test='^TestRecoverTimerContext';package='./internal/roundlifecycle'},
 @{name='safe hint error mapping';file=$rl;old='return RecoveryReport{}, storageError(err)';new='return RecoveryReport{}, err';test='^TestRecoverTimerContext';package='./internal/roundlifecycle'},
 @{name='context before hint';file=$rl;old='if err = contextError(ctx); err != nil {';new='if err = contextError(ctx); false {';test='^TestRecoverTimerContext';package='./internal/roundlifecycle'},
 @{name='context after hint';file=$rl;old='if err = contextError(ctx); err != nil {';new='if err = contextError(ctx); false {';occurrence=2;test='^TestRecoverTimerContext';package='./internal/roundlifecycle'},
 @{name='close after beginWork';file=$rl;old='if service.closed.Load() {';new='if service.closed.Load() && false {';test='^TestRecoverTimerCloseAfterBeginWork';package='./internal/roundlifecycle'},
 @{name='close during hint';file=$rl;old='if service.closed.Load() {';new='if service.closed.Load() && false {';occurrence=2;test='^TestRecoverTimerCloseDuring';package='./internal/roundlifecycle'},
 @{name='scheduled independently matches SQL';file=$sql;old="state IN ('scheduled','executing')";new="state IN ('executing')";test='^TestRecoveryHintActualSQLiteSix';package='./internal/storage/sqlite'},
 @{name='executing independently matches SQL';file=$sql;old="state IN ('scheduled','executing')";new="state IN ('scheduled')";test='^TestRecoveryHintActualSQLiteSix';package='./internal/storage/sqlite'},
 @{name='terminal states cannot activate timer';file=$sql;old="state IN ('scheduled','executing')";new="state IN ('completed','failed')";test='^TestRecoveryHintActualSQLiteSix';package='./internal/storage/sqlite'},
 @{name='store nil context contract';file=$sql;old='return false, err';new='return false, context.Canceled';test='^TestRecoveryHintActualSQLiteContextClosedAndMissingSchemaNeverConfirmNoWork/nil$';package='./internal/storage/sqlite'}
)
$results=New-Object 'Collections.Generic.List[object]'
$started=[DateTime]::UtcNow
$report=@{status='failed';acceptancePerformanceClaim=$false;sourceSHA256=$hashes;results=@();sourceUnchanged=$false;temporaryRemoved=$false;startedUTC=$started.ToString('o')}
$null=Assert-OwnedDirectory $directory
$null=New-Item -ItemType Directory -Path $directory
Push-Location $workspace
try{
 $oldPreference=$ErrorActionPreference
 try{$ErrorActionPreference='Continue';$output=@(& go test ./internal/roundlifecycle ./internal/storage/sqlite -run '^Test(RecoverTimer|RecoveryHint)' -count=1 -timeout 60s 2>&1);$code=$LASTEXITCODE}finally{$ErrorActionPreference=$oldPreference}
 [IO.File]::WriteAllText((Join-Path $task 'recovery-timer-mutation-baseline.log'),($output -join "`n"),$utf8)
 if($code -ne 0){throw 'Timer mutation actual baseline failed'}
 $index=0
 foreach($case in $cases){
  $index++;$occurrence=1;if($case.ContainsKey('occurrence')){$occurrence=$case.occurrence}
  $changed=Replace-Occurrence $sources[$case.file] $case.old $case.new $occurrence
  $mutant=Join-Path $directory ('mutant-'+$index+'.go');[IO.File]::WriteAllText($mutant,$changed,$utf8)
  $replace=@{};$replace[(Join-Path $workspace $case.file)]=$mutant
  $overlay=Join-Path $directory 'overlay.json';[IO.File]::WriteAllText($overlay,(@{Replace=$replace}|ConvertTo-Json -Depth 4),$utf8)
  try{$ErrorActionPreference='Continue';$output=@(& go test -overlay $overlay $case.package -run $case.test -count=1 -timeout 20s 2>&1);$code=$LASTEXITCODE}finally{$ErrorActionPreference=$oldPreference}
  $log=$output -join "`n"
  [IO.File]::WriteAllText((Join-Path $task ('recovery-timer-mutant-'+$index+'.log')),$log,$utf8)
  if($code -eq 0 -or $log -notmatch '--- FAIL: Test' -or $log -match 'build failed|undefined:|timed out|panic:|fatal error:|no tests to run'){throw ('Timer mutant lacked an independent compile-valid assertion failure: '+$case.name+"`n"+$log)}
  $results.Add(@{name=$case.name;status='assertion-killed';compileFailure=$false})
  Write-Output ('ASSERTION KILL '+$case.name)
 }
 $report.status='passed';$report.results=$results.ToArray();$report.assertionKills=$results.Count
}finally{
 try{
  foreach($path in $paths){if((Get-FileHash -LiteralPath (Join-Path $workspace $path) -Algorithm SHA256).Hash -ne $hashes[$path]){throw ('Timer source or tests changed: '+$path)}}
  $report.sourceUnchanged=$true
  $resolved=Assert-OwnedDirectory $directory
  if(Test-Path -LiteralPath $resolved){Remove-Item -LiteralPath $resolved -Recurse -Force}
  $report.temporaryRemoved=-not(Test-Path -LiteralPath $resolved);$report.endedUTC=[DateTime]::UtcNow.ToString('o')
  [IO.File]::WriteAllText((Join-Path $task 'recovery-timer-mutations.json'),($report|ConvertTo-Json -Depth 6),$utf8)
 }finally{Pop-Location}
}