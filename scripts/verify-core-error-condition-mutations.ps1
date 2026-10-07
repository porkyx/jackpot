$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$task=Join-Path $workspace '.task'
$utf8=New-Object Text.UTF8Encoding($false)
if((Get-Item -LiteralPath $task).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Core mutation task root is a reparse point'}
function Assert-Owned([string]$Path){
 $full=[IO.Path]::GetFullPath($Path)
 if([IO.Path]::GetDirectoryName($full) -ne $task -or -not [IO.Path]::GetFileName($full).StartsWith('verified-test-core-errors-',[StringComparison]::Ordinal)){throw 'Core overlay path escaped owned task directory'}
 if(Test-Path -LiteralPath $full){
  $queue=New-Object 'Collections.Generic.Queue[string]';$queue.Enqueue($full)
  while($queue.Count){$item=Get-Item -LiteralPath $queue.Dequeue() -Force;if($item.Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Core overlay contains a reparse point'};if($item.PSIsContainer){foreach($child in @(Get-ChildItem -LiteralPath $item.FullName -Force)){$queue.Enqueue($child.FullName)}}}
 }
}
function Change-Once([string]$Text,[string]$Old,[string]$New){
 $first=$Text.IndexOf($Old,[StringComparison]::Ordinal)
 if($first -lt 0 -or $Text.IndexOf($Old,$first+$Old.Length,[StringComparison]::Ordinal) -ge 0){throw 'Mutation anchor must occur exactly once'}
 return $Text.Substring(0,$first)+$New+$Text.Substring($first+$Old.Length)
}
function Change-Scoped([string]$Text,[string]$Start,[string]$End,[string]$Old,[string]$New){
 if(-not $Start){return Change-Once $Text $Old $New}
 $a=$Text.IndexOf($Start,[StringComparison]::Ordinal);$b=$Text.Length
 if($End){$b=$Text.IndexOf($End,$a+$Start.Length,[StringComparison]::Ordinal)}
 if($a -lt 0 -or $b -le $a){throw 'Mutation function boundary missing'}
 return $Text.Substring(0,$a)+(Change-Once $Text.Substring($a,$b-$a) $Old $New)+$Text.Substring($b)
}
$paths=@('internal/application/draft.go','internal/application/finalize.go','internal/storage/sqlite/round_writes.go','internal/roundlifecycle/state.go','internal/roundlifecycle/ports.go','internal/application/core_condition_edges_test.go','internal/storage/sqlite/round_core_condition_edges_test.go')
$sources=@{};$hashes=@{}
foreach($path in $paths){$absolute=Join-Path $workspace $path;$sources[$path]=[IO.File]::ReadAllText($absolute).Replace("`r`n","`n");$hashes[$path]=(Get-FileHash -LiteralPath $absolute -Algorithm SHA256).Hash}
$commit='func (store *Store) CommitOutcome';$failure='func (store *Store) RecordAttemptFailure'
$cases=@(
 @{id='draft-mode-owner-fence';file='internal/application/finalize.go';old=' || request.Mode != service.draft.Prizes.DrawMode';new='';package='./internal/application';test='^TestFinalizeReadyDraftModeMismatch'},
 @{id='load-terminal-revision-overflow';file='internal/application/draft.go';old="if incrementErr != nil {`n`t`terr = incrementErr`n`t}";new="if incrementErr != nil {`n`t`terr = nil`n`t}";package='./internal/application';test='^TestLoadTerminalCounterOverflow/revision$'},
 @{id='load-terminal-generation-overflow';file='internal/application/draft.go';old='err = generationErr';new='err = nil';package='./internal/application';test='^TestLoadTerminalCounterOverflow/article-generation$'},
 @{id='retry-immutable-message';file='internal/storage/sqlite/round_writes.go';old='if string(beforeRaw) != string(raw) {';new='if false && string(beforeRaw) != string(raw) {';package='./internal/storage/sqlite';test='^TestStorageRetryImmutableMessageMismatch'},
 @{id='commit-public-validation';file='internal/storage/sqlite/round_writes.go';start=$commit;end=$failure;old='if err = rl.ValidateOutcome(round.Input, request.Outcome); err != nil {';new='if err = rl.ValidateOutcome(round.Input, request.Outcome); err != nil && false {';package='./internal/storage/sqlite';test='^TestStorageCommitInvalidPublicOutcome'},
 @{id='commit-early-preflight-error-priority';file='internal/storage/sqlite/round_writes.go';start=$commit;end=$failure;old="if err = txValidateCollection(ctx, tx, request.Claim.CollectionID); err != nil {`n`t`treturn rl.RoundRecord{}, err`n`t}";new="if err = txValidateCollection(ctx, tx, request.Claim.CollectionID); err != nil {`n`t`treturn rl.RoundRecord{}, contracts.NewFault(contracts.InvalidState)`n`t}";package='./internal/storage/sqlite';test='^TestStorageCommitReadDependencyFailure/first$'},
 @{id='commit-nth-read-error-priority';file='internal/storage/sqlite/round_writes.go';start=$commit;end=$failure;old="round, err := txRound(ctx, tx, request.Claim.RoundID)`n`tif err != nil {`n`t`treturn rl.RoundRecord{}, err`n`t}";new="round, err := txRound(ctx, tx, request.Claim.RoundID)`n`tif err != nil {`n`t`treturn rl.RoundRecord{}, contracts.NewFault(contracts.InvalidState)`n`t}";package='./internal/storage/sqlite';test='^TestStorageCommitReadDependencyFailure/second$'},
 @{id='failure-active-operation-fence';file='internal/storage/sqlite/round_writes.go';start=$failure;old='if request.OperationID != activeOperation {';new='if false && request.OperationID != activeOperation {';package='./internal/storage/sqlite';test='^TestStorageAttemptFailureOperation/foreign-operation'},
 @{id='failure-optional-nil-claim';file='internal/storage/sqlite/round_writes.go';start=$failure;old='if request.Claim != nil && (round.Claim == nil || *request.Claim != *round.Claim) {';new='if true && (round.Claim == nil || *request.Claim != *round.Claim) {';package='./internal/storage/sqlite';test='^TestStorageAttemptFailureOperation/foreign-operation-unclaimed$'},
 @{id='failure-missing-stored-claim';file='internal/storage/sqlite/round_writes.go';start=$failure;old='if request.Claim != nil && (round.Claim == nil || *request.Claim != *round.Claim) {';new='if request.Claim != nil && (round.Claim != nil && *request.Claim != *round.Claim) {';package='./internal/storage/sqlite';test='^TestStorageAttemptFailureOperation/provided-claim-without-stored-claim$'},
 @{id='failure-exact-claim-identity';file='internal/storage/sqlite/round_writes.go';start=$failure;old='if request.Claim != nil && (round.Claim == nil || *request.Claim != *round.Claim) {';new='if request.Claim != nil && (round.Claim == nil || false && *request.Claim != *round.Claim) {';package='./internal/storage/sqlite';test='^TestStorageAttemptFailureOperation/foreign-claim$'}
)
$run='^TestFinalizeReadyDraftModeMismatch|^TestLoadTerminalCounterOverflow|^TestStorageRetryImmutableMessageMismatch|^TestStorageCommitInvalidPublicOutcome|^TestStorageCommitReadDependencyFailure|^TestStorageAttemptFailureOperation'
$results=New-Object 'Collections.Generic.List[object]'
$startUTC=[DateTime]::UtcNow.ToString('o');$allPASS=$true
$baselineLog=Join-Path $task 'core-error-conditions-mutation-baseline.jsonl'
try{
 & go test ./internal/application ./internal/storage/sqlite -run $run -count=1 -json -timeout 120s *> $baselineLog
 if($LASTEXITCODE -ne 0){throw 'Actual core error-condition baseline failed'}
 foreach($case in $cases){
  $directory=Join-Path $task ('verified-test-core-errors-'+[guid]::NewGuid().ToString('N'));Assert-Owned $directory;$null=New-Item -ItemType Directory -Path $directory
  $log=Join-Path $task ('core-error-condition-mutant-'+$case.id+'.jsonl')
  try{
   $changed=Change-Scoped $sources[$case.file] $case.start $case.end $case.old $case.new
   $copy=Join-Path $directory 'mutated.go';[IO.File]::WriteAllText($copy,$changed,$utf8)
   $overlay=Join-Path $directory 'overlay.json';$replace=@{};$replace[(Join-Path $workspace $case.file)]=$copy
   [IO.File]::WriteAllText($overlay,(@{Replace=$replace}|ConvertTo-Json -Depth 4),$utf8)
   $oldPreference=$ErrorActionPreference
   try{$ErrorActionPreference='Continue';& go test -overlay $overlay $case.package -run $case.test -count=1 -json -timeout 60s *> $log;$exitCode=$LASTEXITCODE}finally{$ErrorActionPreference=$oldPreference}
   $output=[IO.File]::ReadAllText($log)
   $failedTests=@();foreach($line in [IO.File]::ReadAllLines($log)){try{$item=$line|ConvertFrom-Json;if($item.Action -eq 'fail' -and $item.Test){$failedTests+=$item.Test}}catch{}}
   $invalid=$output -match '(?i)panic:|fatal error:|out of memory|VirtualAlloc|test timed out|\[no tests to run\]|build failed|undefined:|declared and not used|syntax error|no Go files'
   $killed=$exitCode -ne 0 -and $failedTests.Count -gt 0 -and -not $invalid -and $output.Contains('--- FAIL: Test')
   if(-not $killed){$allPASS=$false}
   $results.Add([ordered]@{id=$case.id;file=$case.file;test=$case.test;exitCode=$exitCode;assertionKill=$killed;failedTests=$failedTests;invalidFailure=$invalid;mutatedSHA256=(Get-FileHash -LiteralPath $copy -Algorithm SHA256).Hash;log=$log;tempRemoved=$false})
  }finally{Assert-Owned $directory;if(Test-Path -LiteralPath $directory){Remove-Item -LiteralPath $directory -Force -Recurse};if(Test-Path -LiteralPath $directory){throw 'Owned mutation overlay leaked'};if($results.Count){$results[$results.Count-1].tempRemoved=$true}}
 }
}finally{
 $unchanged=$true;foreach($path in $paths){if((Get-FileHash -LiteralPath (Join-Path $workspace $path) -Algorithm SHA256).Hash -ne $hashes[$path]){$unchanged=$false}}
 $report=[ordered]@{status=if($allPASS -and $results.Count -eq $cases.Count -and $unchanged){'passed'}else{'failed'};startUTC=$startUTC;endUTC=[DateTime]::UtcNow.ToString('o');baselineLog=$baselineLog;expected=$cases.Count;assertionKills=@($results|Where-Object {$_.assertionKill}).Count;sourceSHA256=$hashes;sourceUnchanged=$unchanged;tempRemoved=@($results|Where-Object {-not $_.tempRemoved}).Count -eq 0;compilePanicOOMTimeoutCountedAsKills=$false;formalPerformanceAcceptance=$false;cases=$results}
 [IO.File]::WriteAllText((Join-Path $task 'core-error-condition-mutations.json'),($report|ConvertTo-Json -Depth 8),$utf8)
}
if($report.status -ne 'passed'){throw 'Core error-condition assertion mutation evidence failed; inspect preserved logs'}
$global:LASTEXITCODE=0
Write-Output ('Core error-condition mutations: '+$report.assertionKills+'/'+$report.expected+' compile-valid assertion kills; source SHA unchanged; owned temp cleanup PASS')