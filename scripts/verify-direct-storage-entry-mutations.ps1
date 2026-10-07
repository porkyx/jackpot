# Verify direct SQLite entry guards with assertion-only scoped mutations.
# Originals remain unchanged; selected regressions include retry, state and cleanup.
# This runs selected SQLite tests with private Go overlays; originals are never written.
param(
 [string]$ScheduleTestPath='internal/storage/sqlite/direct_storage_schedule_entry_test.go',
 [string]$ClaimTestPath='internal/storage/sqlite/direct_storage_claim_entry_test.go'
)
$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$task=Join-Path $workspace '.task'
$utf8=[Text.UTF8Encoding]::new($false)
if((Get-Item -LiteralPath $task).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Mutation task root is a reparse point'}
function Assert-TestPath([string]$Relative){
 $absolute=[IO.Path]::GetFullPath((Join-Path $workspace $Relative))
 $allowed=[IO.Path]::GetFullPath((Join-Path $workspace 'internal/storage/sqlite'))
 if([IO.Path]::GetDirectoryName($absolute) -ne $allowed -or -not $absolute.EndsWith('_test.go',[StringComparison]::Ordinal)){throw 'Selected test path escaped SQLite'}
 if(-not (Test-Path -LiteralPath $absolute -PathType Leaf)){throw 'Staged Go TXT has not been promoted'}
 return $absolute
}
$null=Assert-TestPath $ScheduleTestPath
$null=Assert-TestPath $ClaimTestPath
function Assert-OwnedTemporary([string]$Path){
 $full=[IO.Path]::GetFullPath($Path)
 if([IO.Path]::GetDirectoryName($full) -ne $task -or [IO.Path]::GetFileName($full) -notmatch '^verified-test-storage-entry-[0-9a-f]{32}$'){throw 'Overlay escaped owned task directory'}
 if(Test-Path -LiteralPath $full){
  $queue=[Collections.Generic.Queue[string]]::new();$queue.Enqueue($full)
  while($queue.Count){
   $item=Get-Item -LiteralPath $queue.Dequeue() -Force
   if($item.Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Overlay contains a reparse point'}
   if($item.PSIsContainer){foreach($child in @(Get-ChildItem -LiteralPath $item.FullName -Force)){$queue.Enqueue($child.FullName)}}
  }
 }
}
function Unique-Index([string]$Text,[string]$Anchor){
 $index=$Text.IndexOf($Anchor,[StringComparison]::Ordinal)
 if($index -lt 0 -or $Text.IndexOf($Anchor,$index+$Anchor.Length,[StringComparison]::Ordinal) -ge 0){throw 'Mutation anchor must occur exactly once'}
 return $index
}
function Change-Scoped([string]$Text,[string]$Start,[string]$End,[string]$Old,[string]$New){
 $startIndex=Unique-Index $Text $Start
 $endIndex=Unique-Index $Text $End
 if($endIndex -le $startIndex){throw 'Function boundaries are out of order'}
 $scope=$Text.Substring($startIndex,$endIndex-$startIndex)
 $index=Unique-Index $scope $Old
 $changed=$scope.Substring(0,$index)+$New+$scope.Substring($index+$Old.Length)
 return $Text.Substring(0,$startIndex)+$changed+$Text.Substring($endIndex)
}
function Read-GoEvents([string]$Path){
 $events=[Collections.Generic.List[object]]::new()
 foreach($line in [IO.File]::ReadAllLines($Path)){
  try{$event=$line|ConvertFrom-Json;if($event.Action){$events.Add($event)}}catch{}
 }
 return ,$events
}
function Invoke-Selected([string[]]$Arguments,[string]$Log){
 $previousPreference=$ErrorActionPreference
 try{$ErrorActionPreference='Continue';& go @Arguments *> $Log;return $LASTEXITCODE}
 finally{$ErrorActionPreference=$previousPreference}
}
$paths=@(
 'internal/storage/sqlite/round_queries.go','internal/storage/sqlite/round_writes.go',
 $ScheduleTestPath,$ClaimTestPath,
 'internal/storage/sqlite/round_core_condition_edges_test.go',
 'internal/storage/sqlite/round_product_test.go','internal/storage/sqlite/round_corruption_test.go',
 'internal/storage/sqlite/round_process_test.go','internal/storage/sqlite/round_schema_test.go',
 'internal/storage/sqlite/migration_test.go','internal/storage/sqlite/pending_test.go','go.mod','go.sum'
)
$sources=@{};$hashes=@{}
foreach($path in $paths){
 $absolute=Join-Path $workspace $path
 $sources[$path]=[IO.File]::ReadAllText($absolute).Replace(([string][char]13+[char]10),[string][char]10)
 $hashes[$path]=(Get-FileHash -LiteralPath $absolute -Algorithm SHA256).Hash
}
$set='func (store *Store) SetSchedule('
$cancel='func (store *Store) CancelSchedule('
$count='func (store *Store) CollectionCount('
$write='func writeContext('
$changed='func changed('
$claim='func (store *Store) ClaimAttempt('
$commit='func (store *Store) CommitOutcome('
$setTest='TestStorageSetScheduleEachEntryGuardPreservesDurableStateAndOperationID'
$cancelTest='TestStorageCancelScheduleEachEntryGuardPreservesDurableStateAndOperationID'
$counterTest='TestStorageClaimAttemptEachCounterMismatchIsNoOpAndDoesNotConsumeEntropy'
$sessionTest='TestStorageClaimAttemptInvalidSessionAndUnknownOperationPreserveThePendingOwner'
$cases=@(
 @{id='set-operation-id';file='internal/storage/sqlite/round_queries.go';start=$set;end=$cancel;old='request.Operation.ID == ""';new='false && request.Operation.ID == ""';test=('^'+$setTest+'/empty_operation$')},
 @{id='set-operation-kind';file='internal/storage/sqlite/round_queries.go';start=$set;end=$cancel;old='request.Operation.Kind != "SetSchedule"';new='false && request.Operation.Kind != "SetSchedule"';test=('^'+$setTest+'/wrong_kind$')},
 @{id='set-collection-id';file='internal/storage/sqlite/round_queries.go';start=$set;end=$cancel;old='request.CollectionID == ""';new='false && request.CollectionID == ""';test=('^'+$setTest+'/empty_collection$')},
 @{id='set-round-id';file='internal/storage/sqlite/round_queries.go';start=$set;end=$cancel;old='request.RoundID == ""';new='false && request.RoundID == ""';test=('^'+$setTest+'/empty_round$')},
 @{id='set-timezone';file='internal/storage/sqlite/round_queries.go';start=$set;end=$cancel;old='request.Timezone != "Asia/Seoul"';new='false && request.Timezone != "Asia/Seoul"';test=('^'+$setTest+'/wrong_timezone$')},
 @{id='set-accepted-time';file='internal/storage/sqlite/round_queries.go';start=$set;end=$cancel;old='request.AcceptedAt.IsZero()';new='false && request.AcceptedAt.IsZero()';test=('^'+$setTest+'/zero_acceptance$')},
 @{id='set-scheduled-zero';file='internal/storage/sqlite/round_queries.go';start=$set;end=$cancel;old='request.ScheduledAt.IsZero()';new='false && request.ScheduledAt.IsZero()';test=('^'+$setTest+'/zero_schedule_with_independent_minimum_operand$')},
 @{id='set-minimum-delta';file='internal/storage/sqlite/round_queries.go';start=$set;end=$cancel;old='request.ScheduledAt.Before(request.AcceptedAt.Add(10*time.Second))';new='false && request.ScheduledAt.Before(request.AcceptedAt.Add(10*time.Second))';test=('^'+$setTest+'/one_nanosecond_before_minimum$')},
 @{id='cancel-operation-id';file='internal/storage/sqlite/round_queries.go';start=$cancel;end=$count;old='request.Operation.ID == ""';new='false && request.Operation.ID == ""';test=('^'+$cancelTest+'/empty_operation$')},
 @{id='cancel-operation-kind';file='internal/storage/sqlite/round_queries.go';start=$cancel;end=$count;old='request.Operation.Kind != "CancelSchedule"';new='false && request.Operation.Kind != "CancelSchedule"';test=('^'+$cancelTest+'/wrong_kind$')},
 @{id='cancel-collection-id';file='internal/storage/sqlite/round_queries.go';start=$cancel;end=$count;old='request.CollectionID == ""';new='false && request.CollectionID == ""';test=('^'+$cancelTest+'/empty_collection$')},
 @{id='cancel-round-id';file='internal/storage/sqlite/round_queries.go';start=$cancel;end=$count;old='request.RoundID == ""';new='false && request.RoundID == ""';test=('^'+$cancelTest+'/empty_round$')},
 @{id='cancel-accepted-time';file='internal/storage/sqlite/round_queries.go';start=$cancel;end=$count;old='request.AcceptedAt.IsZero()';new='false && request.AcceptedAt.IsZero()';test=('^'+$cancelTest+'/zero_acceptance$')},
 @{id='write-context-nil-safe-code';file='internal/storage/sqlite/round_writes.go';start=$write;end=$changed;old='return contracts.NewFault(contracts.InvalidInput)';new='return contracts.NewFault(contracts.InvalidState)';test=('^('+$setTest+'|'+$cancelTest+')/nil_context$')},
 @{id='write-context-cancel-priority';file='internal/storage/sqlite/round_writes.go';start=$write;end=$changed;old='return ctx.Err()';new='return nil';test=('^('+$setTest+'|'+$cancelTest+')/cancelled_context_wins_over_invalid_request$')},
 @{id='claim-attempt-fence';file='internal/storage/sqlite/round_writes.go';start=$claim;end=$commit;old='round.Attempt != request.Attempt';new='false && round.Attempt != request.Attempt';test=('^'+$counterTest+'/(zero_attempt|maximum_attempt)$')},
 @{id='claim-version-fence';file='internal/storage/sqlite/round_writes.go';start=$claim;end=$commit;old='round.Version != request.ExpectedVersion';new='false && round.Version != request.ExpectedVersion';test=('^'+$counterTest+'/(zero_version|maximum_safe_version)$')},
 @{id='claim-revision-fence';file='internal/storage/sqlite/round_writes.go';start=$claim;end=$commit;old='round.Revision != request.ExpectedRevision';new='false && round.Revision != request.ExpectedRevision';test=('^'+$counterTest+'/(zero_revision|maximum_safe_revision)$')},
 @{id='claim-session-presence';file='internal/storage/sqlite/round_writes.go';start=$claim;end=$commit;old='request.Session == ""';new='false && request.Session == ""';test=('^'+$sessionTest+'/empty_session$')}
)
$baselinePattern='^('+$setTest+'|'+$cancelTest+'|'+$counterTest+'|'+$sessionTest+')$'
$artifact=Join-Path $task ('storage-direct-entry-artifacts-'+[guid]::NewGuid().ToString('N'))
$null=New-Item -ItemType Directory -Path $artifact
$rows=[Collections.Generic.List[object]]::new()
$baselinePassed=$false;$allPassed=$true;$sourceUnchanged=$true;$failure=$null
$started=[DateTime]::UtcNow.ToString('o')
$baselineLog=Join-Path $artifact 'baseline.jsonl'
try{
 Push-Location $workspace
 try{
  $baselineCode=Invoke-Selected @('test','./internal/storage/sqlite','-run',$baselinePattern,'-count=1','-json','-timeout','120s') $baselineLog
  $baselineEvents=Read-GoEvents $baselineLog
  $parentPasses=@($baselineEvents|Where-Object {$_.Action -eq 'pass' -and $_.Test -match $baselinePattern})
  $leafPasses=@($baselineEvents|Where-Object {$_.Action -eq 'pass' -and $_.Test -match '/'} )
  $skips=@($baselineEvents|Where-Object {$_.Action -eq 'skip' -and $_.Test})
  if($baselineCode -ne 0 -or $parentPasses.Count -ne 4 -or $leafPasses.Count -ne 29 -or $skips.Count -ne 0){throw 'Selected baseline must pass all four parents and 29 reviewed subcases without skips'}
  $baselinePassed=$true
  foreach($case in $cases){
   foreach($path in $paths){if((Get-FileHash -LiteralPath (Join-Path $workspace $path) -Algorithm SHA256).Hash -ne $hashes[$path]){throw 'Original source changed during mutation run'}}
   $directory=Join-Path $task ('verified-test-storage-entry-'+[guid]::NewGuid().ToString('N'))
   Assert-OwnedTemporary $directory
   $null=New-Item -ItemType Directory -Path $directory
   $row=[ordered]@{id=$case.id;file=$case.file;test=$case.test;assertionKill=$false;exitCode=$null;invalidFailure=$false;failedTests=@();mutatedSHA256=$null;log=(Join-Path $artifact ($case.id+'.jsonl'));tempRemoved=$false;cleanupFailure=$null}
   $rows.Add($row)
   try{
    $mutated=Change-Scoped $sources[$case.file] $case.start $case.end $case.old $case.new
    $copy=Join-Path $directory 'mutated.go'
    [IO.File]::WriteAllText($copy,$mutated,$utf8)
    $row.mutatedSHA256=(Get-FileHash -LiteralPath $copy -Algorithm SHA256).Hash
    $overlay=Join-Path $directory 'overlay.json'
    $replacement=@{};$replacement[[IO.Path]::GetFullPath((Join-Path $workspace $case.file))]=$copy
    [IO.File]::WriteAllText($overlay,(@{Replace=$replacement}|ConvertTo-Json -Depth 3),$utf8)
    $row.exitCode=Invoke-Selected @('test','-overlay',$overlay,'./internal/storage/sqlite','-run',$case.test,'-count=1','-json','-timeout','60s') $row.log
    $output=[IO.File]::ReadAllText($row.log)
    $events=Read-GoEvents $row.log
    $matchingRuns=@($events|Where-Object {$_.Action -eq 'run' -and $_.Test -match $case.test})
    $row.failedTests=@($events|Where-Object {$_.Action -eq 'fail' -and $_.Test -match $case.test}|ForEach-Object {$_.Test})
    $matchingSkips=@($events|Where-Object {$_.Action -eq 'skip' -and $_.Test})
    $row.invalidFailure=$output -match '(?i)panic:|fatal error:|out of memory|VirtualAlloc|test timed out|\[no tests to run\]|\[no test files\]|build failed|undefined:|declared and not used|syntax error|no Go files' -or $matchingRuns.Count -eq 0 -or $matchingSkips.Count -ne 0
    $row.assertionKill=$row.exitCode -ne 0 -and $row.failedTests.Count -gt 0 -and -not $row.invalidFailure -and $output.Contains('--- FAIL: Test')
    if(-not $row.assertionKill){$allPassed=$false}
   }finally{
    try{Assert-OwnedTemporary $directory;if(Test-Path -LiteralPath $directory){Remove-Item -LiteralPath $directory -Recurse -Force};$row.tempRemoved= -not (Test-Path -LiteralPath $directory)}
    catch{$row.cleanupFailure=$_.Exception.Message;$allPassed=$false}
   }
   if(-not $row.tempRemoved){throw 'Owned overlay cleanup failed; stop before creating another mutation'}
  }
 }finally{Pop-Location}
}catch{$allPassed=$false;$failure=$_.Exception.Message}
finally{
 foreach($path in $paths){if((Get-FileHash -LiteralPath (Join-Path $workspace $path) -Algorithm SHA256).Hash -ne $hashes[$path]){$sourceUnchanged=$false}}
 $report=[ordered]@{
  status=if($baselinePassed -and $allPassed -and $sourceUnchanged -and $rows.Count -eq 19){'passed'}else{'failed'}
  startUTC=$started;endUTC=[DateTime]::UtcNow.ToString('o');baselineLog=$baselineLog;baselinePassed=$baselinePassed
  expected=19;assertionKills=@($rows|Where-Object {$_.assertionKill}).Count
  sourceSHA256=$hashes;sourceUnchanged=$sourceUnchanged;tempRemoved=@($rows|Where-Object {-not $_.tempRemoved}).Count -eq 0
  compilePanicOOMTimeoutNoTestsCountedAsKills=$false;formalPerformanceAcceptance=$false;failure=$failure;cases=$rows
 }
 [IO.File]::WriteAllText((Join-Path $artifact 'report.json'),($report|ConvertTo-Json -Depth 9),$utf8)
}
if($report.status -ne 'passed'){throw ('Direct storage assertion mutation gate failed; retained report at '+$artifact)}
$global:LASTEXITCODE=0
Write-Output ('Direct storage mutation assertion kills '+$report.assertionKills+'/19; original SHA unchanged; private overlays removed')