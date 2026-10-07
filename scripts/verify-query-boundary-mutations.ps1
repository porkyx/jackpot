$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$task=Join-Path $workspace '.task'
$directory=Join-Path $task ('verified-test-query-boundary-'+[guid]::NewGuid().ToString('N'))
$utf8=New-Object Text.UTF8Encoding($false)
function Assert-Owned([string]$Path){
 $full=[IO.Path]::GetFullPath($Path).TrimEnd([char[]]'\/')
 if([IO.Path]::GetDirectoryName($full) -ne $task -or -not [IO.Path]::GetFileName($full).StartsWith('verified-test-query-boundary-',[StringComparison]::Ordinal)){throw 'Overlay path escaped task root'}
 if(Test-Path -LiteralPath $full){foreach($entry in @((Get-Item -LiteralPath $full -Force))+@(Get-ChildItem -LiteralPath $full -Recurse -Force)){if(($entry.Attributes -band [IO.FileAttributes]::ReparsePoint)-ne 0){throw 'Overlay reparse point rejected'}}}
 return $full
}
function Replace-One([string]$Source,[string]$Old,[string]$New){$at=$Source.IndexOf($Old,[StringComparison]::Ordinal);if($at -lt 0 -or $Source.IndexOf($Old,$at+1,[StringComparison]::Ordinal) -ge 0){throw 'Missing or ambiguous mutation anchor'};return $Source.Substring(0,$at)+$New+$Source.Substring($at+$Old.Length)}
$contract='internal/contracts/query_size.go';$checked='internal/desktop/query_size.go';$draft='internal/desktop/draft.go';$round='internal/desktop/round.go'
$paths=@($contract,$checked,$draft,$round,'internal/contracts/query_size_test.go','internal/desktop/query_size_test.go','internal/desktop/collection_page_test.go')
$sources=@{};$hashes=@{}
foreach($path in $paths){$file=Join-Path $workspace $path;$sources[$path]=[IO.File]::ReadAllText($file).Replace("`r`n","`n");$hashes[$path]=(Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash}
$sizeTest='^TestQueryJSONSize';$sizePackage='./internal/contracts';$desktopPackage='./internal/desktop';$pageTest='^TestCollectionPageActualSQLiteFiftyBoundaryAnchorAndLatestManagement$'
$cases=@(
 @{name='exact 8MiB accepted';file=$contract;old='len(encoded) > MaxQueryResponseBytes';new='len(encoded) >= MaxQueryResponseBytes';test=$sizeTest;package=$sizePackage},
 @{name='oversize independent of marshal error';file=$contract;old='len(encoded) > MaxQueryResponseBytes';new='len(encoded) > MaxQueryResponseBytes && false';test=$sizeTest;package=$sizePackage},
 @{name='marshal error never empty success';file=$contract;old='err != nil ||';new='err != nil && false ||';test=$sizeTest;package=$sizePackage},
 @{name='absolute 8MiB byte bound';file=$contract;old='8 * 1024 * 1024';new='8 * 1024 * 1024 + 1';test=$sizeTest;package=$sizePackage},
 @{name='envelope shape validated first';file=$checked;old='if err := response.Validate(); err != nil {';new='if err := response.Validate(); false {';test='^TestCheckedQueryInvalidShape';package=$desktopPackage},
 @{name='query reference operation retained';file=$checked;old='failure.OperationID = response.OperationID';new='failure.OperationID = nil';test='^TestCheckedQueryExactEnvelope';package=$desktopPackage},
 @{name='query revision retained';file=$checked;old='failure.Revision = response.Revision';new='failure.Revision = nil';test='^TestCheckedQueryExactEnvelope';package=$desktopPackage},
 @{name='query response header retained';file=$checked;old='failureEnvelope[T](response.ResponseHeader, err)';new='failureEnvelope[T](contracts.ResponseHeader{}, err)';test='^TestCheckedQueryExactEnvelope';package=$desktopPackage},
 @{name='durable draft outcome not rewritten';file=$draft;old='if operation == nil {';new='if true {';test='^TestDraftMutationOutcome';package=$desktopPackage},
 @{name='read draft remains size bounded';file=$draft;old='if operation == nil {';new='if operation != nil {';test='^TestDraftMutationOutcome';package=$desktopPackage},
 @{name='default latest page remains 50';file=$round;old="if limit == 0 {`n`t`tlimit = 50";new="if limit == 0 {`n`t`tlimit = 1";test=$pageTest;package=$desktopPackage},
 @{name='explicit query offset forwarded';file=$round;old='Offset: request.RoundOffset';new='Offset: 0';test=$pageTest;package=$desktopPackage},
 @{name='explicit round anchor forwarded';file=$round;old='RoundID: request.RoundID})';new='RoundID: ""})';test=$pageTest;package=$desktopPackage},
 @{name='latest management reference independent';file=$round;old='roundProjection(page.LatestRound, participants)';new='roundProjection(rl.RoundRecord{}, participants)';test=$pageTest;package=$desktopPackage}
)
$results=New-Object 'Collections.Generic.List[object]'
$report=[ordered]@{status='failed';startedUTC=[DateTime]::UtcNow.ToString('o');sourceSHA256=$hashes;results=@();sourceUnchanged=$false;temporaryRemoved=$false;compileTimeoutPanicKills=0}
$null=Assert-Owned $directory;$null=New-Item -ItemType Directory -Path $directory
Push-Location $workspace
try {
 $saved=$ErrorActionPreference
 try{$ErrorActionPreference='Continue';$output=@(& go test ./internal/contracts ./internal/desktop -run '^TestQueryJSON|^TestCheckedQuery|^TestDraftMutationOutcome|^TestFrozenCommentsLarge|^TestCollectionPageActual' -count=1 -timeout 120s 2>&1);$code=$LASTEXITCODE}finally{$ErrorActionPreference=$saved}
 [IO.File]::WriteAllText((Join-Path $task 'query-boundary-mutation-baseline.log'),($output -join "`n"),$utf8)
 if($code -ne 0){throw 'Query boundary baseline failed'}
 $index=0
 foreach($case in $cases){
  $index++;$changed=Replace-One $sources[$case.file] $case.old $case.new
  $mutant=Join-Path $directory ('mutant-'+$index+'.go');[IO.File]::WriteAllText($mutant,$changed,$utf8)
  $mapping=@{};$mapping[(Join-Path $workspace $case.file)]=$mutant
  $overlay=Join-Path $directory 'overlay.json';[IO.File]::WriteAllText($overlay,(@{Replace=$mapping}|ConvertTo-Json -Depth 4),$utf8)
  try{$ErrorActionPreference='Continue';$output=@(& go test -overlay $overlay $case.package -run $case.test -count=1 -timeout 120s 2>&1);$code=$LASTEXITCODE}finally{$ErrorActionPreference=$saved}
  $log=$output -join "`n";[IO.File]::WriteAllText((Join-Path $task ('query-boundary-mutant-'+$index+'.log')),$log,$utf8)
  if($code -eq 0 -or $log -notmatch '--- FAIL: Test' -or $log -match 'build failed|undefined:|timed out|panic:|fatal error:|no tests to run'){throw ('No compile-valid assertion failure: '+$case.name+"`n"+$log)}
  $results.Add(@{name=$case.name;status='assertion-killed'});Write-Output ('ASSERTION KILL '+$case.name)
 }
 $report.status='passed';$report.assertionKills=$results.Count
} catch {$report.error=$_.Exception.Message;throw} finally {
 try{
  $report.results=$results.ToArray()
  foreach($path in $paths){if((Get-FileHash -LiteralPath (Join-Path $workspace $path) -Algorithm SHA256).Hash -ne $hashes[$path]){throw ('Original source changed: '+$path)}}
  $report.sourceUnchanged=$true;$resolved=Assert-Owned $directory
  if(Test-Path -LiteralPath $resolved){Remove-Item -LiteralPath $resolved -Recurse -Force}
  $report.temporaryRemoved=-not(Test-Path -LiteralPath $resolved);$report.endedUTC=[DateTime]::UtcNow.ToString('o')
  [IO.File]::WriteAllText((Join-Path $task 'query-boundary-mutations.json'),($report|ConvertTo-Json -Depth 7),$utf8)
 } finally {Pop-Location}
}
if ($report.status -eq 'passed') { $global:LASTEXITCODE = 0 }
