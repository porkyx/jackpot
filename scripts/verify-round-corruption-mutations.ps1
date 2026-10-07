param([switch]$KeepArtifacts,[switch]$SnapshotOnly)
$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$directory=[IO.Path]::GetFullPath((Join-Path $workspace ('.task/verified-test-round-corruption-'+[guid]::NewGuid().ToString('N'))))
$prefix=Join-Path $workspace '.task/verified-test-round-corruption-'
if(-not $directory.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase)){throw 'Mutation directory escaped workspace'}
$null=New-Item -ItemType Directory -Path $directory
$originals=@{}
$hashes=@{}
foreach($name in @('round_reads.go','round_writes.go','round_queries.go','round_snapshot.go')){
 $source=Join-Path $workspace ('internal/storage/sqlite/'+$name)
 $originals[$name]=[IO.File]::ReadAllText($source)
 $hashes[$name]=(Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash
}
$mutations=@(
 @{Name='active operation collection identity';File='round_writes.go';Before='operation.CollectionID != record.CollectionID || ';After=''},
 @{Name='active operation round identity';File='round_writes.go';Before='operation.RoundID != record.ID || ';After=''},
 @{Name='operation future revision';File='round_writes.go';Before=' || operation.Revision > record.Revision';After=''},
 @{Name='active operation terminal status';File='round_writes.go';Before='operation.Status != expectedStatus || ';After='expectedStatus != expectedStatus || '},
 @{Name='failed operation code agreement';File='round_writes.go';Before='(record.State == contracts.Failed && operation.FailureCode != record.FailureCode)';After='false'},
 @{Name='result collection ownership';File='round_writes.go';Before='if outcome.Valid && (!resultOwner.Valid || resultOwner.String != string(record.CollectionID)) {';After='if false {'},
 @{Name='active operation execution kind';File='round_writes.go';Before='if operation.Operation.Kind != "CreateCollection" && operation.Operation.Kind != "Rerun" && operation.Operation.Kind != "RetryRound" && operation.Operation.Kind != "ExecuteDue" {';After='if false {'},
 @{Name='outcome domain validation';File='round_writes.go';Before="if record.Outcome != nil {`n`t`tif outcomeErr != nil {";After="if record.Outcome != nil {`n`t`tif false {"},
 @{Name='SQL winner exact agreement';File='round_writes.go';Before=' || winner != record.Outcome.Winners[index]';After=''},
 @{Name='SQL winner count';File='round_writes.go';Before='if index != len(record.Outcome.Winners) {';After='if false {'},
 @{Name='completed requires result';File='round_writes.go';Before=' || (record.State == contracts.Completed) != (record.Outcome != nil)';After=''},
 @{Name='claim session identity';File='round_writes.go';Before=' || record.Claim.Session == ""';After=''},
 @{Name='startup includes completed validation';File='round_queries.go';Before="SELECT id FROM rounds ORDER BY COALESCE(scheduled_at,''),id";After="SELECT id FROM rounds WHERE state IN ('executing','scheduled') ORDER BY COALESCE(scheduled_at,''),id"},
 @{Name='existing collection past round write gate';File='round_writes.go';Before='if _, err = txRound(ctx, tx, roundID); err != nil {';After='if false {'},
 @{Name='snapshot validates discarded history';File='round_snapshot.go';Before='if err = txValidateCollection(ctx, tx, id); err != nil {';After='if err = nil; false {'},
 @{Name='snapshot returns same transaction revision';File='round_snapshot.go';Before='return collection, revision, nil';After='return collection, revision+1, nil'}
)
if($SnapshotOnly){$mutations=@($mutations | Where-Object{$_.File -eq 'round_snapshot.go'})}
$results=@()
try{
 $baseline=Join-Path $directory 'baseline'
 $null=New-Item -ItemType Directory -Path $baseline
 Get-ChildItem -LiteralPath (Join-Path $workspace 'internal/storage/sqlite') -File -Filter '*.go' | ForEach-Object{Copy-Item -LiteralPath $_.FullName -Destination $baseline}
 Push-Location $workspace
 try{
  $basePath='./.task/'+[IO.Path]::GetFileName($directory)+'/baseline'
  $output=& go test $basePath -run '^TestRoundCorruption|^TestCollectionSnapshot' -count=1 -timeout=90s 2>&1
  if($LASTEXITCODE -ne 0){throw ('Corruption baseline failed: '+($output -join "`n"))}
  Write-Output 'Round corruption mutation baseline PASS'
  $index=0
  foreach($mutation in $mutations){
   $index++
   $original=$originals[$mutation.File]
   if(-not $original.Contains($mutation.Before)){throw ('Missing mutation target: '+$mutation.Name)}
   $mutant=Join-Path $directory ('mutant-'+$index)
   $null=New-Item -ItemType Directory -Path $mutant
   Get-ChildItem -LiteralPath $baseline -File -Filter '*.go' | ForEach-Object{Copy-Item -LiteralPath $_.FullName -Destination $mutant}
   [IO.File]::WriteAllText((Join-Path $mutant $mutation.File),$original.Replace($mutation.Before,$mutation.After),(New-Object Text.UTF8Encoding($false)))
   $testPath='./.task/'+[IO.Path]::GetFileName($directory)+'/mutant-'+$index
   $output=& go test $testPath -run '^TestRoundCorruption|^TestCollectionSnapshot' -count=1 -timeout=90s 2>&1
   $exitCode=$LASTEXITCODE
   [IO.File]::WriteAllText((Join-Path $mutant 'test.log'),($output -join "`n"))
   if($exitCode -eq 0){throw ('Surviving mutation: '+$mutation.Name)}
   if(($output -join "`n") -notmatch 'FAIL: TestRoundCorruption|FAIL: TestCollectionSnapshot'){throw ('Mutation failed without observable corruption regression: '+$mutation.Name+"`n"+($output -join "`n"))}
   $results+=$mutation.Name
   Write-Output ('KILLED '+$mutation.Name)
  }
 }finally{Pop-Location}
 Write-Output ('Round corruption mutation gate PASS: '+$results.Count+'/'+$mutations.Count)
}finally{
 foreach($name in $hashes.Keys){
  $source=Join-Path $workspace ('internal/storage/sqlite/'+$name)
  if((Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash -ne $hashes[$name]){throw ('Original source changed during isolated mutation: '+$name)}
 }
 $resolved=[IO.Path]::GetFullPath($directory)
 if(-not $resolved.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase)){throw 'Mutation cleanup escaped workspace'}
 if(-not $KeepArtifacts -and (Test-Path -LiteralPath $resolved)){Remove-Item -LiteralPath $resolved -Recurse -Force}
 if(-not $KeepArtifacts -and (Test-Path -LiteralPath $resolved)){throw 'Mutation artifact cleanup failed'}
}