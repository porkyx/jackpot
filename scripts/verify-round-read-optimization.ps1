param([switch]$KeepArtifacts,[switch]$SkipMutations,[string]$BaselineDirectory)
$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$task=Join-Path $workspace '.task'
if([string]::IsNullOrWhiteSpace($BaselineDirectory)){$BaselineDirectory=([IO.File]::ReadAllText((Join-Path $task 'read-optimization-baseline.json'))|ConvertFrom-Json).directory}
$baseline=[IO.Path]::GetFullPath($BaselineDirectory).TrimEnd([char[]]'\/')
function Assert-OwnedDirectory([string]$Path,[string]$Prefix){
 $full=[IO.Path]::GetFullPath($Path).TrimEnd([char[]]'\/')
 if([IO.Path]::GetDirectoryName($full) -ne $task -or -not [IO.Path]::GetFileName($full).StartsWith($Prefix,[StringComparison]::Ordinal)){throw 'Read optimization artifact path escaped owned task root'}
 if(Test-Path -LiteralPath $full){
  $pending=New-Object 'Collections.Generic.Queue[string]'
  $pending.Enqueue($full)
  while($pending.Count -gt 0){
   $next=$pending.Dequeue()
   $entry=Get-Item -LiteralPath $next -Force
   if(($entry.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw 'Read optimization artifact contains a reparse point'}
   if($entry.PSIsContainer){foreach($child in @(Get-ChildItem -LiteralPath $next -Force)){$pending.Enqueue($child.FullName)}}
  }
 }
 return $full
}
$baseline=Assert-OwnedDirectory $baseline 'verified-test-read-opt-'
$directory=Assert-OwnedDirectory (Join-Path $task ('verified-test-read-opt-harness-'+[guid]::NewGuid().ToString('N'))) 'verified-test-read-opt-harness-'
$null=New-Item -ItemType Directory -Path $directory
$utf8=New-Object Text.UTF8Encoding($false)
$names=@('round_reads.go','round_writes.go')
$originals=@{};$sourceHashes=@{};$baselineHashes=@{}
foreach($name in $names){
 $file=Join-Path $workspace ('internal/storage/sqlite/'+$name)
 $originals[$name]=[IO.File]::ReadAllText($file)
 $sourceHashes[$name]=(Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash
 $baseFile=Join-Path $baseline $name
 if(-not(Test-Path -LiteralPath $baseFile -PathType Leaf)){throw ('Missing exact before-source: '+$name)}
 $baselineHashes[$name]=(Get-FileHash -LiteralPath $baseFile -Algorithm SHA256).Hash
}
function Write-Overlay([string]$Name,[hashtable]$Overrides){
 $map=@{}
 foreach($key in $Overrides.Keys){$map[(Join-Path $workspace ('internal/storage/sqlite/'+$key))]=$Overrides[$key]}
 $path=Join-Path $directory ($Name+'.json')
 [IO.File]::WriteAllText($path,(@{Replace=$map}|ConvertTo-Json -Depth 6),$utf8)
 return $path
}
function Invoke-Go([string]$Name,[string[]]$Arguments,[switch]$ExpectFailure){
 $started=[DateTime]::UtcNow
 $priorPreference=$ErrorActionPreference
 try{$ErrorActionPreference='Continue';$output=@(& go @Arguments 2>&1);$code=$LASTEXITCODE}finally{$ErrorActionPreference=$priorPreference}
 $log=$output -join "`n"
 [IO.File]::WriteAllText((Join-Path $directory ($Name+'.log')),$log,$utf8)
 if($ExpectFailure){
  if($code -eq 0){throw ('Mutation survived: '+$Name)}
  if($log -notmatch '--- FAIL: Test' -or $log -match 'build failed|undefined:|panic:'){throw ('Mutation lacked an assertion failure: '+$Name+"`n"+$log)}
 }elseif($code -ne 0){throw ('Read optimization run failed: '+$Name+"`n"+$log)}
 return @{name=$Name;exitCode=$code;log=$log;startedUTC=$started.ToString('o');endedUTC=[DateTime]::UtcNow.ToString('o')}
}
function Read-Metrics([string]$Log){
 $metrics=@{}
 $pattern='(?m)^BenchmarkReadOptimization(?<name>\w+)-\d+\s+20\s+(?<ns>\d+)\s+ns/op(?:\s+[\d.]+\s+MB/s)?\s+(?<bytes>\d+)\s+B/op\s+(?<allocs>\d+)\s+allocs/op'
 foreach($match in [regex]::Matches($Log,$pattern)){$metrics[$match.Groups['name'].Value]=@{samples=20;nsPerOp=[long]$match.Groups['ns'].Value;bytesPerOp=[long]$match.Groups['bytes'].Value;allocsPerOp=[long]$match.Groups['allocs'].Value}}
 if($metrics.Count -ne 2){throw 'Read optimization benchmark metrics missing'}
 return $metrics
}
function Fixture-Hashes([string]$Log){
 $input=@([regex]::Matches($Log,'public input bytes=\d+ SHA256=([a-f0-9]{64})')|ForEach-Object{$_.Groups[1].Value}|Select-Object -Unique)
 $round=@([regex]::Matches($Log,'committed round SHA256=([a-f0-9]{64})')|ForEach-Object{$_.Groups[1].Value}|Select-Object -Unique)
 if($input.Count -ne 1 -or $round.Count -ne 1){throw 'Read optimization exact fixture SHA missing'}
 return @{input=$input[0];round=$round[0]}
}
function Replace-One([string]$Text,[string]$Before,[string]$After,[int]$Occurrence=1){
 $position=-1
 for($number=0;$number -lt $Occurrence;$number++){$position=$Text.IndexOf($Before,$position+1,[StringComparison]::Ordinal);if($position -lt 0){throw 'Read optimization mutation anchor missing'}}
 return $Text.Substring(0,$position)+$After+$Text.Substring($position+$Before.Length)
}
$mutations=@(
 @{name='pending input validator';file='round_writes.go';before='inputErr = rl.ValidateRoundInput(record.Input)';after='inputErr = nil';occurrence=1},
 @{name='completed outcome validator';file='round_writes.go';before='outcomeErr = rl.ValidateOutcome(record.Input, *record.Outcome)';after='outcomeErr = nil';occurrence=1},
 @{name='corrupt completed early input validator';file='round_writes.go';before='inputErr = rl.ValidateRoundInput(record.Input)';after='inputErr = nil';occurrence=2},
 @{name='corrupt outcome cannot precede SQL failure';file='round_writes.go';before='inputErr = rl.ValidateRoundInput(record.Input)';after='return rl.RoundRecord{}, contracts.NewFault(contracts.InvalidState)';occurrence=2},
 @{name='late outcome rejection';file='round_writes.go';before="if record.Outcome != nil {`n`t`tif outcomeErr != nil {";after="if record.Outcome != nil {`n`t`tif outcomeErr != nil && false {";occurrence=1},
 @{name='corrupt fallback branch';file='round_writes.go';before="if outcomeErr != nil {`n`t`t`tinputErr";after="if outcomeErr == nil {`n`t`t`tinputErr";occurrence=1},
 @{name='early input error gate';file='round_writes.go';before='if inputErr != nil || record.Number';after='if inputErr != nil && false || record.Number';occurrence=1},
 @{name='outcome branch discriminant';file='round_writes.go';before='if record.Outcome == nil {';after='if record.Outcome == nil || record.Outcome != nil {';occurrence=1},
 @{name='strict unknown JSON field';file='round_reads.go';before='decoder.DisallowUnknownFields()';after='// mutation accepts unknown fields';occurrence=1},
 @{name='strict trailing JSON token';file='round_reads.go';before='if decoder.Decode(new(json.RawMessage)) != io.EOF {';after='if decoder.Decode(new(json.RawMessage)) != io.EOF && false {';occurrence=1},
 @{name='strict persisted null';file='round_reads.go';before='strings.TrimSpace(raw) == "null"';after='strings.TrimSpace(raw) == "null" && false';occurrence=1},
 @{name='stored exact size accepted';file='round_reads.go';before='len(raw) > 100<<20';after='len(raw) >= 100<<20';occurrence=1;exact=$true},
 @{name='stored size plus one rejected';file='round_reads.go';before='len(raw) > 100<<20';after='len(raw) > 101<<20';occurrence=1;exact=$true}
)
$report=$null
try{
 Push-Location $workspace
 try{
  $version=(& go version|Out-String).Trim()
  $overrides=@{};foreach($name in $names){$overrides[$name]=Join-Path $baseline $name}
  $overlay=Write-Overlay 'baseline-overlay' $overrides
  $priorityPattern='^TestRoundReadOptimization|^TestStoredReaderExact|^TestStoredDecoder'
  $beforeChecks=Invoke-Go 'baseline-contracts' @('test','-overlay',$overlay,'-run',$priorityPattern,'-count=1','-timeout=90s','./internal/storage/sqlite')
  $afterChecks=Invoke-Go 'current-contracts' @('test','-run',$priorityPattern,'-count=1','-timeout=90s','./internal/storage/sqlite')
  $beforeRun=Invoke-Go 'baseline-benchmark' @('test','-overlay',$overlay,'-run','^$','-bench','^BenchmarkReadOptimization','-benchtime=20x','-benchmem','-count=1','./internal/storage/sqlite')
  $afterRun=Invoke-Go 'current-benchmark' @('test','-run','^$','-bench','^BenchmarkReadOptimization','-benchtime=20x','-benchmem','-count=1','./internal/storage/sqlite')
  $before=Read-Metrics $beforeRun.log;$after=Read-Metrics $afterRun.log
  $first=Fixture-Hashes $beforeRun.log;$second=Fixture-Hashes $afterRun.log
  if($first.input -ne $second.input -or $first.round -ne $second.round){throw 'Baseline/current exact fixture/output SHA differs'}
  $comparison=@{}
  foreach($name in $before.Keys){
   if($after[$name].bytesPerOp -ge $before[$name].bytesPerOp -or $after[$name].allocsPerOp -ge $before[$name].allocsPerOp){throw ('Expected bounded allocation reduction missing: '+$name)}
   $comparison[$name]=@{before=$before[$name];after=$after[$name];bytesReduced=$before[$name].bytesPerOp-$after[$name].bytesPerOp;allocsReduced=$before[$name].allocsPerOp-$after[$name].allocsPerOp}
  }
  $killed=@()
  if(-not $SkipMutations){
   $index=0
   foreach($mutation in $mutations){
    $index++
    $mutant=Join-Path $directory ('mutation-'+$index+'.go')
    $changed=Replace-One $originals[$mutation.file] $mutation.before $mutation.after $mutation.occurrence
    [IO.File]::WriteAllText($mutant,$changed,$utf8)
    $replace=@{};$replace[$mutation.file]=$mutant
    $mutantOverlay=Write-Overlay ('mutation-overlay-'+$index) $replace
    $pattern='^TestRoundReadOptimization|^TestStoredDecoder'
    if($mutation.exact){$pattern='^TestStoredReaderExact'}
    $run=Invoke-Go ('mutation-'+$index) @('test','-overlay',$mutantOverlay,'-run',$pattern,'-count=1','-timeout=90s','./internal/storage/sqlite') -ExpectFailure
    $killed+=@{name=$mutation.name;exitCode=$run.exitCode;assertionFailure=$true;compileFailure=$false}
    Write-Output ('KILLED '+$mutation.name)
   }
  }
  $report=@{status='passed';goVersion=$version;scope='same-fixture bounded allocator/storage-read diagnostic only';formalNativePerformanceOr500MiBClaim=$false;productionScryptChanged=$false;publicOutcomePortValidationChanged=$false;baselineDirectory=$baseline;baselineSHA256=$baselineHashes;currentSHA256=$sourceHashes;fixturesSHA256=$first;comparison=$comparison;baselineContractsPassed=$true;currentContractsPassed=$true;completedRoundActualSQLite=$true;exactRoundAllFieldsEqual=$true;durableCountsUnchanged=$true;newRNGCalls=0;connectionInUse=0;serviceAndDatabaseClosed=$true;mutationCount=$killed.Count;mutations=$killed;startedUTC=$beforeChecks.startedUTC;endedUTC=[DateTime]::UtcNow.ToString('o')}
  [IO.File]::WriteAllText((Join-Path $task 'read-optimization-report.json'),($report|ConvertTo-Json -Depth 10),$utf8)
  Write-Output ('Read optimization PASS: exact fixture/output SHA; both before/after contracts; '+$killed.Count+' assertion mutants; allocation reduction only')
  foreach($name in $comparison.Keys){Write-Output ($name+': '+$comparison[$name].before.bytesPerOp+' -> '+$comparison[$name].after.bytesPerOp+' B/op; '+$comparison[$name].before.allocsPerOp+' -> '+$comparison[$name].after.allocsPerOp+' allocs/op')}
 }finally{Pop-Location}
}finally{
 foreach($name in $sourceHashes.Keys){if((Get-FileHash -LiteralPath (Join-Path $workspace ('internal/storage/sqlite/'+$name)) -Algorithm SHA256).Hash -ne $sourceHashes[$name]){throw ('Original source changed: '+$name)}}
 foreach($name in $baselineHashes.Keys){if((Get-FileHash -LiteralPath (Join-Path $baseline $name) -Algorithm SHA256).Hash -ne $baselineHashes[$name]){throw ('Exact before-source changed: '+$name)}}
 $resolved=Assert-OwnedDirectory $directory 'verified-test-read-opt-harness-'
 if(-not $KeepArtifacts -and (Test-Path -LiteralPath $resolved)){Remove-Item -LiteralPath $resolved -Recurse -Force}
 if(-not $KeepArtifacts -and (Test-Path -LiteralPath $resolved)){throw 'Read optimization overlay artifact cleanup failed'}
 if($null -ne $report){$report.originalSourceUnchanged=$true;$report.baselineSourceUnchanged=$true;$report.overlayArtifactRemoved=(-not $KeepArtifacts);$report.retainedBeforeSourceEvidence=$true;[IO.File]::WriteAllText((Join-Path $task 'read-optimization-report.json'),($report|ConvertTo-Json -Depth 10),$utf8)}
 Write-Output 'Original/baseline source SHA and bounded owned overlay cleanup PASS'
}