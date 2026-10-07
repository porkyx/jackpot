$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$task=Join-Path $workspace '.task'
$directory=Join-Path $task ('verified-test-collection-page-'+[guid]::NewGuid().ToString('N'))
$utf8=New-Object Text.UTF8Encoding($false)
function Assert-OwnedDirectory([string]$Path){
 $full=[IO.Path]::GetFullPath($Path).TrimEnd([char[]]'\/')
 if([IO.Path]::GetDirectoryName($full) -ne $task -or -not [IO.Path]::GetFileName($full).StartsWith('verified-test-collection-page-',[StringComparison]::Ordinal)){throw 'Page overlay path escaped task root'}
 if(Test-Path -LiteralPath $full){
  $queue=New-Object 'Collections.Generic.Queue[string]';$queue.Enqueue($full)
  while($queue.Count -gt 0){$item=Get-Item -LiteralPath $queue.Dequeue() -Force;if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw 'Page overlay path has reparse point'};if($item.PSIsContainer){foreach($child in @(Get-ChildItem -LiteralPath $item.FullName -Force)){$queue.Enqueue($child.FullName)}}}
 }
 return $full
}
function Replace-ExactlyOnce([string]$Source,[string]$Before,[string]$After){
 $at=$Source.IndexOf($Before,[StringComparison]::Ordinal)
 if($at -lt 0 -or $Source.IndexOf($Before,$at+1,[StringComparison]::Ordinal) -ge 0){throw 'Page mutation marker missing or ambiguous'}
 return $Source.Substring(0,$at)+$After+$Source.Substring($at+$Before.Length)
}
$paths=@('internal/roundlifecycle/collection_page.go','internal/storage/sqlite/round_page.go','internal/roundlifecycle/collection_page_test.go','internal/storage/sqlite/round_page_test.go','internal/storage/sqlite/round_page_corruption_test.go')
$sources=@{};$hashes=@{}
foreach($path in $paths){$file=Join-Path $workspace $path;$sources[$path]=[IO.File]::ReadAllText($file).Replace("`r`n","`n");$hashes[$path]=(Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash}
$rl='internal/roundlifecycle/collection_page.go';$sql='internal/storage/sqlite/round_page.go'
$queryTest='^TestCollectionPageQueryIndependent';$queryPackage='./internal/roundlifecycle';$sqlPackage='./internal/storage/sqlite'
$cases=@(
 @{name='limit minimum independent';file=$rl;old='query.Limit == 0';new='query.Limit == 0 && false';test=$queryTest;package=$queryPackage},
 @{name='limit maximum independent';file=$rl;old='query.Limit > 50';new='query.Limit > 50 && false';test=$queryTest;package=$queryPackage},
 @{name='anchor forbids nonzero offset';file=$rl;old='query.RoundID != "" && query.Offset != 0';new='query.RoundID != "" && query.Offset != 0 && false';test=$queryTest;package=$queryPackage},
 @{name='unanchored offset remains allowed';file=$rl;old='query.RoundID != "" && query.Offset != 0';new='query.RoundID != "" || query.Offset != 0';test=$queryTest;package=$queryPackage},
 @{name='query safe invalid-input code';file=$rl;old='contracts.InvalidInput';new='contracts.InvalidState';test=$queryTest;package=$queryPackage},
 @{name='store validates page query';file=$sql;old='if err := query.Validate(); err != nil {';new='if err := query.Validate(); false {';test='^TestCollectionPageInvalidQueries/zero-limit$';package=$sqlPackage},
 @{name='orphan latest guard';file=$sql;old='if len(ids) == 0 {';new='if len(ids) == 0 && false {';test='^TestCollectionPageEmptyDatabase';package=$sqlPackage},
 @{name='anchor resolves older offset';file=$sql;old='offset = (total - 1 - uint32(index)) / query.Limit * query.Limit';new='offset = uint32(index) * 0';test='^TestCollectionPageAnchor';package=$sqlPackage},
 @{name='anchor identity exact';file=$sql;old='if roundID == query.RoundID {';new='if roundID != query.RoundID {';test='^TestCollectionPageAnchor';package=$sqlPackage},
 @{name='offset counts backwards';file=$sql;old='end = total - offset';new='end = total';test='^TestCollectionPageConsumedCount';package=$sqlPackage},
 @{name='page upper bound 50';file=$sql;old='if end > query.Limit {';new='if end > query.Limit && false {';test='^TestCollectionPageAnchor';package=$sqlPackage},
 @{name='page lower bound';file=$sql;old='uint32(index) >= start && uint32(index) < end';new='uint32(index) >= start || uint32(index) < end';test='^TestCollectionPageConsumedCount';package=$sqlPackage},
 @{name='page upper bound';file=$sql;old='uint32(index) >= start && uint32(index) < end';new='uint32(index) >= start';test='^TestCollectionPageConsumedCount';package=$sqlPackage},
 @{name='all-past validation outside selected page';file=$sql;old="for index, roundID := range ids {`n`t`tround, err := txRound(ctx, tx, roundID)";new="for index, roundID := range ids {`n`t`tif uint32(index) < start || uint32(index) >= end { continue }`n`t`tround, err := txRound(ctx, tx, roundID)";test='^TestCollectionPageOutsidePageCorruption';package=$sqlPackage},
 @{name='latest independent of selected page';file=$sql;old='if index == len(ids)-1 {';new='if index == len(ids)-1 && false {';test='^TestCollectionPageConsumedCount';package=$sqlPackage},
 @{name='ordinal continuity';file=$sql;old='round.Number != uint32(index)+1';new='round.Number != uint32(index)+1 && false';test='^TestCollectionPageNumberGaps';package=$sqlPackage},
 @{name='collection owner independent';file=$sql;old='round.CollectionID != id';new='round.CollectionID != id && false';test='^TestCollectionPageForeignMetadataOwner';package=$sqlPackage},
 @{name='winner presence independent';file=$sql;old='!exists[winner.ParticipantID]';new='!exists[winner.ParticipantID] && false';test='^TestCollectionPageAggregateWinnerPresence/missing-participant$';package=$sqlPackage},
 @{name='winner uniqueness independent';file=$sql;old='consumed[winner.ParticipantID] {';new='consumed[winner.ParticipantID] && false {';test='^TestCollectionPageAggregateWinnerPresence/duplicate-consumed$';package=$sqlPackage},
 @{name='selected bound independent';file=$sql;old='len(consumed) > int(selected)';new='len(consumed) > int(selected) && false';test='^TestCollectionPageAggregateWinnerPresence/consumed-exceeds-selected$';package=$sqlPackage},
 @{name='consumed spans all validated outcomes';file=$sql;old='if round.Outcome != nil {';new='if round.Outcome != nil && uint32(index) >= start && uint32(index) < end {';test='^TestCollectionPageConsumedCount';package=$sqlPackage},
 @{name='consumed projection';file=$sql;old='page.ConsumedCount = uint32(len(consumed))';new='page.ConsumedCount = 0';test='^TestCollectionPageConsumedCount';package=$sqlPackage},
 @{name='missing anchor never publishes page';file=$sql;old='if !anchorFound {';new='if !anchorFound && false {';test='^TestCollectionPageInvalidQueries/(missing-anchor|foreign-anchor)$';package=$sqlPackage},
 @{name='missing anchor still checks corrupt past';file=$sql;old='var start, end uint32';new="if !anchorFound { return rl.CollectionPage{}, rl.ErrNotFound }`n`tvar start, end uint32";test='^TestCollectionPageOutsidePageCorruption';package=$sqlPackage},
 @{name='late SQL priority after earlier aggregate damage';file=$sql;old='invalidConsumption = true';new='return rl.CollectionPage{}, contracts.NewFault(contracts.InvalidState)';test='^TestCollectionPageLateSQLFailure';package=$sqlPackage}
)
$results=New-Object 'Collections.Generic.List[object]'
$report=@{status='failed';acceptancePerformanceClaim=$false;sourceSHA256=$hashes;results=@();sourceUnchanged=$false;temporaryRemoved=$false;startedUTC=[DateTime]::UtcNow.ToString('o')}
$null=Assert-OwnedDirectory $directory
$null=New-Item -ItemType Directory -Path $directory
Push-Location $workspace
try{
 $oldPreference=$ErrorActionPreference
 try{$ErrorActionPreference='Continue';$output=@(& go test ./internal/roundlifecycle ./internal/storage/sqlite -run '^TestCollectionPage' -count=1 -timeout 180s 2>&1);$code=$LASTEXITCODE}finally{$ErrorActionPreference=$oldPreference}
 [IO.File]::WriteAllText((Join-Path $task 'collection-page-mutation-baseline.log'),($output -join "`n"),$utf8)
 if($code -ne 0){throw 'Page actual baseline failed'}
 $index=0
 foreach($case in $cases){
  $index++
  $changed=Replace-ExactlyOnce $sources[$case.file] $case.old $case.new
  $mutant=Join-Path $directory ('mutant-'+$index+'.go');[IO.File]::WriteAllText($mutant,$changed,$utf8)
  $replace=@{};$replace[(Join-Path $workspace $case.file)]=$mutant
  $overlay=Join-Path $directory 'overlay.json';[IO.File]::WriteAllText($overlay,(@{Replace=$replace}|ConvertTo-Json -Depth 4),$utf8)
  try{$ErrorActionPreference='Continue';$output=@(& go test -overlay $overlay $case.package -run $case.test -count=1 -timeout 60s 2>&1);$code=$LASTEXITCODE}finally{$ErrorActionPreference=$oldPreference}
  $log=$output -join "`n"
  [IO.File]::WriteAllText((Join-Path $task ('collection-page-mutant-'+$index+'.log')),$log,$utf8)
  if($code -eq 0 -or $log -notmatch '--- FAIL: Test' -or $log -match 'build failed|undefined:|timed out|panic:|fatal error:|no tests to run'){throw ('Page mutant lacked independent compile-valid assertion failure: '+$case.name+"`n"+$log)}
  $results.Add(@{name=$case.name;status='assertion-killed';compileFailure=$false})
  Write-Output ('ASSERTION KILL '+$case.name)
 }
 $report.status='passed';$report.results=$results.ToArray();$report.assertionKills=$results.Count
}finally{
 try{
  foreach($path in $paths){if((Get-FileHash -LiteralPath (Join-Path $workspace $path) -Algorithm SHA256).Hash -ne $hashes[$path]){throw ('Page source or tests changed: '+$path)}}
  $report.sourceUnchanged=$true
  $resolved=Assert-OwnedDirectory $directory
  if(Test-Path -LiteralPath $resolved){Remove-Item -LiteralPath $resolved -Recurse -Force}
  $report.temporaryRemoved=-not(Test-Path -LiteralPath $resolved);$report.endedUTC=[DateTime]::UtcNow.ToString('o')
  [IO.File]::WriteAllText((Join-Path $task 'collection-page-mutations.json'),($report|ConvertTo-Json -Depth 6),$utf8)
 }finally{Pop-Location}
}
# Every native mutant intentionally exits nonzero. Successful script completion
# must not leak the last expected native failure into a parent verifier.
$global:LASTEXITCODE=0
Write-Output ('PASS '+$results.Count+' compile-valid assertion kills')