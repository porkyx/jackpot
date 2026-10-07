$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$task=Join-Path $workspace '.task'
$utf8=[Text.UTF8Encoding]::new($false)
function Assert-Owned([string]$Path){
 $full=[IO.Path]::GetFullPath($Path)
 if([IO.Path]::GetDirectoryName($full) -ne $task -or -not [IO.Path]::GetFileName($full).StartsWith('verified-test-draft-boundaries-',[StringComparison]::Ordinal)){throw 'Draft overlay escaped its owned task root'}
 if(Test-Path -LiteralPath $full){
  $queue=[Collections.Generic.Queue[string]]::new();$queue.Enqueue($full)
  while($queue.Count){$item=Get-Item -LiteralPath $queue.Dequeue() -Force;if($item.Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Draft overlay reparse point forbidden'};if($item.PSIsContainer){foreach($child in @(Get-ChildItem -LiteralPath $item.FullName -Force)){$queue.Enqueue($child.FullName)}}}
 }
}
function Change-Scoped([string]$Text,$Case){
 $a=$Text.IndexOf($Case.start,[StringComparison]::Ordinal);$b=$Text.Length
 if($a -lt 0){throw 'Missing function start'}
 if($Case.end){$b=$Text.IndexOf($Case.end,$a+$Case.start.Length,[StringComparison]::Ordinal)}
 if($b -le $a){throw 'Missing function end'}
 $body=$Text.Substring($a,$b-$a);$i=$body.IndexOf($Case.old,[StringComparison]::Ordinal)
 if($i -lt 0 -or $body.IndexOf($Case.old,$i+$Case.old.Length,[StringComparison]::Ordinal) -ge 0){throw 'Mutation anchor must occur exactly once inside its function'}
 return $Text.Substring(0,$a)+$body.Substring(0,$i)+$Case.new+$body.Substring($i+$Case.old.Length)+$Text.Substring($b)
}
$paths=@('internal/application/draft.go','internal/application/finalize.go','internal/application/collector.go','internal/application/draft_priority_edges_test.go','internal/application/draft_additional_boundaries_test.go')
$hashes=@{};foreach($path in $paths){$hashes[$path]=(Get-FileHash -LiteralPath (Join-Path $workspace $path)).Hash}
$source=[IO.File]::ReadAllText((Join-Path $workspace 'internal/application/draft.go')).Replace("`r`n","`n")
$cases=@(@'
[
  {
    "new": "for len(service.order) > 129 {",
    "start": "func (service *DraftService) record(",
    "id": "receipt-cap-128",
    "test": "^TestDraftReceiptCapacity127128129EvictsOnlyTheOldestTerminalReceipt$",
    "end": "func (service *DraftService) publish(",
    "old": "for len(service.order) > 128 {"
  },
  {
    "new": "old := service.order[len(service.order)-1]",
    "start": "func (service *DraftService) record(",
    "id": "oldest-receipt",
    "test": "^TestDraftReceiptCapacity127128129EvictsOnlyTheOldestTerminalReceipt$",
    "end": "func (service *DraftService) publish(",
    "old": "old := service.order[0]"
  },
  {
    "new": "service.draft.Load.State == \"cancelling\"",
    "start": "func (service *DraftService) record(",
    "id": "retain-loading",
    "test": "^TestDraftReceiptCapacityRetainsLiveLoadDuringLoadingAndCancellingThenEvictsTerminal/loading$",
    "end": "func (service *DraftService) publish(",
    "old": "service.draft.Load.State == \"loading\" || service.draft.Load.State == \"cancelling\""
  },
  {
    "new": "service.draft.Load.State == \"loading\"",
    "start": "func (service *DraftService) record(",
    "id": "retain-cancelling",
    "test": "^TestDraftReceiptCapacityRetainsLiveLoadDuringLoadingAndCancellingThenEvictsTerminal/cancelling$",
    "end": "func (service *DraftService) publish(",
    "old": "service.draft.Load.State == \"loading\" || service.draft.Load.State == \"cancelling\""
  },
  {
    "new": "service.draft.Load.State == \"loading\" || service.draft.Load.State == \"cancelling\" || service.draft.Load.State == \"completed\"",
    "start": "func (service *DraftService) record(",
    "id": "terminal-release",
    "test": "^TestDraftReceiptCapacityRetainsLiveLoadDuringLoadingAndCancellingThenEvictsTerminal/loading$",
    "end": "func (service *DraftService) publish(",
    "old": "service.draft.Load.State == \"loading\" || service.draft.Load.State == \"cancelling\""
  },
  {
    "new": " && service.draft.Load.State == \"loading\"",
    "start": "func (service *DraftService) collect(",
    "id": "callback-operation-fence",
    "test": "^TestDraftOldProgressCannotModifyAnotherActivelyLoadingOperation$",
    "end": "func validatePrizes(",
    "old": " && service.draft.Load.OperationID == request.OperationID && service.draft.Load.State == \"loading\""
  },
  {
    "new": " {",
    "start": "func (service *DraftService) collect(",
    "id": "callback-terminal-fence",
    "test": "^TestDraftUnknownPostedAtPromotionRollsBackConfiguredOwnerAndReleasesWorkerLease$",
    "end": "func validatePrizes(",
    "old": " && service.draft.Load.State == \"loading\" {"
  },
  {
    "new": "// retired Load incorrectly retained",
    "start": "func (service *DraftService) Edit(",
    "id": "reset-null-load",
    "test": "^TestDraftOldProgressAfterPublicResetCannotRecreateNullLoad$",
    "end": "func (service *DraftService) Participants(",
    "old": "service.draft.Load = nil"
  },
  {
    "new": "_ = oldDraft",
    "start": "func (service *DraftService) collect(",
    "id": "rollback-draft",
    "test": "^TestDraftUnknownPostedAtPromotionRollsBackConfiguredOwnerAndReleasesWorkerLease$",
    "end": "func validatePrizes(",
    "old": "service.draft = oldDraft"
  },
  {
    "new": "_ = oldParticipants",
    "start": "func (service *DraftService) collect(",
    "id": "rollback-participants",
    "test": "^TestDraftUnknownPostedAtPromotionRollsBackConfiguredOwnerAndReleasesWorkerLease$",
    "end": "func validatePrizes(",
    "old": "service.participants = oldParticipants"
  },
  {
    "new": "_ = oldAuthor",
    "start": "func (service *DraftService) collect(",
    "id": "rollback-author",
    "test": "^TestDraftUnknownPostedAtPromotionRollsBackConfiguredOwnerAndReleasesWorkerLease$",
    "end": "func validatePrizes(",
    "old": "service.author = oldAuthor"
  },
  {
    "new": "_ = value",
    "start": "func cloneDraft(",
    "id": "filter-pointee-owner",
    "test": "^TestDraftPublicProjectionsDetachEachOptionalPointee/filter-timecut$",
    "end": "func (service *DraftService) Summary(",
    "old": "data.Filters.TimeCut = &value"
  },
  {
    "new": "_ = value",
    "start": "func cloneDraft(",
    "id": "article-time-pointee-owner",
    "test": "^TestDraftPublicProjectionsDetachEachOptionalPointee/article-posted-at$",
    "end": "func (service *DraftService) Summary(",
    "old": "article.PostedAt = &value"
  },
  {
    "new": "_ = value",
    "start": "func cloneDraft(",
    "id": "article-author-pointee-owner",
    "test": "^TestDraftPublicProjectionsDetachEachOptionalPointee/article-author-identifier$",
    "end": "func (service *DraftService) Summary(",
    "old": "article.AuthorIdentifier = &value"
  },
  {
    "new": "_ = code",
    "start": "func cloneDraft(",
    "id": "failure-code-pointee-owner",
    "test": "^TestDraftUnknownPostedAtPromotionRollsBackConfiguredOwnerAndReleasesWorkerLease$",
    "end": "func (service *DraftService) Summary(",
    "old": "value.FailureCode = &code"
  },
  {
    "new": "len(query.Query) > 1001",
    "start": "func (service *DraftService) Participants(",
    "id": "query-1001-byte-bound",
    "test": "^TestDraftParticipantQueryByteLimitsGroupsAndSearchOperands/ascii-1001-bytes$",
    "end": "func participantData(",
    "old": "len(query.Query) > 1000"
  },
  {
    "new": "len([]rune(query.Query)) > 1000",
    "start": "func (service *DraftService) Participants(",
    "id": "query-byte-not-rune-bound",
    "test": "^TestDraftParticipantQueryByteLimitsGroupsAndSearchOperands/unicode-1001-bytes$",
    "end": "func participantData(",
    "old": "len(query.Query) > 1000"
  },
  {
    "new": "!strings.Contains(strings.ToLower(row.Participant.Key.Identifier), search)",
    "start": "func (service *DraftService) Participants(",
    "id": "nickname-search-operand",
    "test": "^TestDraftParticipantQueryByteLimitsGroupsAndSearchOperands/nickname-only$",
    "end": "func participantData(",
    "old": "!strings.Contains(strings.ToLower(row.Participant.Key.Nickname), search) && !strings.Contains(strings.ToLower(row.Participant.Key.Identifier), search)"
  },
  {
    "new": "!strings.Contains(strings.ToLower(row.Participant.Key.Nickname), search)",
    "start": "func (service *DraftService) Participants(",
    "id": "identifier-search-operand",
    "test": "^TestDraftParticipantQueryByteLimitsGroupsAndSearchOperands/identifier-only-ignore-case$",
    "end": "func participantData(",
    "old": "!strings.Contains(strings.ToLower(row.Participant.Key.Nickname), search) && !strings.Contains(strings.ToLower(row.Participant.Key.Identifier), search)"
  },
  {
    "new": "if false && query.Group != \"\" && query.Group != \"unclassified\" && query.Group != \"included\" && query.Group != \"excluded\" {",
    "start": "func (service *DraftService) Participants(",
    "id": "group-validation",
    "test": "^TestDraftParticipantQueryByteLimitsGroupsAndSearchOperands/unknown-group-only$",
    "end": "func participantData(",
    "old": "if query.Group != \"\" && query.Group != \"unclassified\" && query.Group != \"included\" && query.Group != \"excluded\" {"
  },
  {
    "new": "hash, err := hashPublic(request)\n\tif err != nil && false {",
    "start": "func (service *DraftService) Edit(",
    "id": "hash-error-pre-admission",
    "test": "^TestDraftEditUnrepresentableTimeRefusesBeforeAdmissionAndSameIDCanBeCorrected$",
    "end": "func (service *DraftService) Participants(",
    "old": "hash, err := hashPublic(request)\n\tif err != nil {"
  },
  {
    "new": "return contracts.NewFault(contracts.InvalidState)",
    "start": "func requestContext(",
    "id": "nil-context-code",
    "test": "^TestDraftOperationReadIndependentGuardFailuresHaveNoEffects/nil-context$",
    "end": "func hashPublic(",
    "old": "return contracts.NewFault(contracts.InvalidInput)"
  },
  {
    "new": "return nil",
    "start": "func requestContext(",
    "id": "cancelled-context",
    "test": "^TestDraftOperationReadIndependentGuardFailuresHaveNoEffects/cancelled-context$",
    "end": "func hashPublic(",
    "old": "return ctx.Err()"
  },
  {
    "new": "if false && id == \"\" {",
    "start": "func (service *DraftService) Operation(",
    "id": "operation-empty-id",
    "test": "^TestDraftOperationReadIndependentGuardFailuresHaveNoEffects/empty-id$",
    "end": "",
    "old": "if id == \"\" {"
  },
  {
    "new": "if err := service.guard(); err != nil && false {",
    "start": "func (service *DraftService) Operation(",
    "id": "operation-closed-owner",
    "test": "^TestDraftOperationReadIndependentGuardFailuresHaveNoEffects/closed-owner$",
    "end": "",
    "old": "if err := service.guard(); err != nil {"
  },
  {
    "new": "State: contracts.OperationFailed",
    "start": "func (service *DraftService) Operation(",
    "id": "unknown-not-failed",
    "test": "^TestDraftOperationReadIndependentGuardFailuresHaveNoEffects/nil-context$",
    "end": "",
    "old": "State: contracts.OperationUnknown"
  }
]
'@ | ConvertFrom-Json)
$pattern='^(TestDraftReceiptCapacity127128129EvictsOnlyTheOldestTerminalReceipt|TestDraftReceiptCapacityRetainsLiveLoadDuringLoadingAndCancellingThenEvictsTerminal|TestDraftOldProgressAfterPublicResetCannotRecreateNullLoad|TestDraftOldProgressCannotModifyAnotherActivelyLoadingOperation|TestDraftUnknownPostedAtPromotionRollsBackConfiguredOwnerAndReleasesWorkerLease|TestDraftPublicProjectionsDetachEachOptionalPointee|TestDraftParticipantQueryByteLimitsGroupsAndSearchOperands|TestDraftEditUnrepresentableTimeRefusesBeforeAdmissionAndSameIDCanBeCorrected|TestDraftOperationReadIndependentGuardFailuresHaveNoEffects)$'
$results=[Collections.Generic.List[object]]::new();$allPASS=$true;$begin=[DateTime]::UtcNow.ToString('o')
try{
 foreach($case in $cases){[void](Change-Scoped $source $case)}
 $baseline=Join-Path $task 'draft-boundary-mutation-baseline.jsonl'
 & go -C $workspace test ./internal/application -run $pattern -count=1 -timeout=60s -json *> $baseline
 if($LASTEXITCODE -ne 0){throw 'Draft baseline failed'}
 $baseEvents=@(Get-Content -LiteralPath $baseline|ForEach-Object {try{$_|ConvertFrom-Json}catch{}})
 $parents=@($baseEvents|Where-Object {$_.Action -eq 'pass' -and $_.Test -and -not $_.Test.Contains('/')})
 if($parents.Count -ne 9 -or @($baseEvents|Where-Object {$_.Action -eq 'skip' -or $_.Action -eq 'fail'}).Count){throw 'Baseline must execute nine exact parents without skipped/failed tests'}
 foreach($case in $cases){
  $directory=Join-Path $task ('verified-test-draft-boundaries-'+[guid]::NewGuid().ToString('N'));Assert-Owned $directory;[void](New-Item -ItemType Directory -Path $directory)
  $log=Join-Path $task ('draft-boundary-mutant-'+$case.id+'.jsonl')
  try{
   $copy=Join-Path $directory 'draft.go';[IO.File]::WriteAllText($copy,(Change-Scoped $source $case),$utf8)
   $overlay=Join-Path $directory 'overlay.json';$mapping=@{};$mapping[(Join-Path $workspace 'internal/application/draft.go')]=$copy
   [IO.File]::WriteAllText($overlay,(@{Replace=$mapping}|ConvertTo-Json -Depth 4),$utf8)
   $oldPreference=$ErrorActionPreference
   try{$ErrorActionPreference='Continue';& go -C $workspace test -overlay $overlay ./internal/application -run $case.test -count=1 -timeout=45s -json *> $log;$code=$LASTEXITCODE}finally{$ErrorActionPreference=$oldPreference}
   $out=[IO.File]::ReadAllText($log);$events=@(Get-Content -LiteralPath $log|ForEach-Object {try{$_|ConvertFrom-Json}catch{}})
   $failed=@($events|Where-Object {$_.Action -eq 'fail' -and $_.Test -match $case.test}|ForEach-Object {$_.Test})
   $ran=@($events|Where-Object {$_.Action -eq 'run' -and $_.Test -match $case.test}).Count -gt 0
   $invalid=$out -match '(?i)panic:|fatal error:|out of memory|VirtualAlloc|test timed out|\[no tests to run\]|build failed|undefined:|declared and not used|syntax error|no Go files|unknown flag'
   $killed=$code -ne 0 -and $ran -and $failed.Count -gt 0 -and -not $invalid -and $out.Contains('--- FAIL: Test')
   if(-not $killed){$allPASS=$false}
   $results.Add([ordered]@{id=$case.id;test=$case.test;exitCode=$code;assertionKill=$killed;ranSelected=$ran;failedTests=$failed;invalidFailure=$invalid;mutatedSHA256=(Get-FileHash -LiteralPath $copy).Hash;log=$log;tempRemoved=$false})
   Write-Output ($case.id+': assertionKill='+$killed)
  }finally{Assert-Owned $directory;if(Test-Path -LiteralPath $directory){Remove-Item -LiteralPath $directory -Recurse -Force};if(Test-Path -LiteralPath $directory){throw 'Owned draft overlay leaked'};if($results.Count){$results[$results.Count-1].tempRemoved=$true}}
 }
}finally{
 $unchanged=$true;foreach($path in $paths){if((Get-FileHash -LiteralPath (Join-Path $workspace $path)).Hash -ne $hashes[$path]){$unchanged=$false}}
 $report=[ordered]@{status=if($allPASS -and $results.Count -eq $cases.Count -and $unchanged){'passed'}else{'failed'};startUTC=$begin;endUTC=[DateTime]::UtcNow.ToString('o');expected=$cases.Count;assertionKills=@($results|Where-Object {$_.assertionKill}).Count;sourceSHA256=$hashes;sourceUnchanged=$unchanged;tempRemoved=@($results|Where-Object {-not $_.tempRemoved}).Count -eq 0;compilePanicOOMTimeoutNoTestsCountedAsKills=$false;formalPerformanceAcceptance=$false;cases=$results}
 [IO.File]::WriteAllText((Join-Path $task 'draft-boundary-mutations.json'),($report|ConvertTo-Json -Depth 9),$utf8)
}
if($report.status -ne 'passed'){throw 'Draft boundary mutation evidence failed; inspect individual preserved logs'}
$global:LASTEXITCODE=0
Write-Output ('Draft boundaries '+$report.assertionKills+'/'+$report.expected+' assertion kills; original SHA and owned cleanup verified')