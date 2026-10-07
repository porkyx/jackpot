param([switch]$KeepArtifacts)
$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$directory=[IO.Path]::GetFullPath((Join-Path $workspace ('.task/verified-test-round-permissions-'+[guid]::NewGuid().ToString('N'))))
$prefix=Join-Path $workspace '.task/verified-test-round-permissions-'
if(-not $directory.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase)){throw 'Permission mutation directory escaped workspace'}
$null=New-Item -ItemType Directory -Path $directory
$originals=@{};$hashes=@{}
foreach($name in @('commands.go','service.go')){
 $path=Join-Path $workspace ('internal/roundlifecycle/'+$name)
 $originals[$name]=[IO.File]::ReadAllText($path)
 $hashes[$name]=(Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash
}
$mutations=@(
 @{Name='user session equality';File='commands.go';Before='if session != service.options.Session {';After='if false {'},
 @{Name='replay command kind';File='commands.go';Before='operation.Operation.Kind != identity.Kind || ';After=''},
 @{Name='replay public fingerprint';File='commands.go';Before='operation.Operation.PublicFingerprint != identity.PublicFingerprint';After='false'},
 @{Name='retry collection ownership';File='commands.go';Before='round.CollectionID != request.Context.CollectionID || ';After=''},
 @{Name='retry expected revision';File='commands.go';Before='round.Revision != request.Context.Revision || ';After=''},
 @{Name='retry expected round version';File='commands.go';Before=' || round.Version != request.Context.Version';After=''},
 @{Name='retry attempt overflow';File='commands.go';Before=' || round.Attempt == math.MaxUint32';After=''},
 @{Name='rerun number overflow';File='commands.go';Before='if latest.Number == math.MaxUint32 {';After='if false {'},
 @{Name='completed rerun immediate mode';File='commands.go';Before='latest.State == contracts.Completed && request.Mode != ImmediateMode';After='false'},
 @{Name='rerun excludes unselected participants';File='commands.go';Before='participant.Included && !used[participant.ID]';After='!used[participant.ID]'},
 @{Name='rerun excludes consumed participants';File='commands.go';Before='participant.Included && !used[participant.ID]';After='participant.Included'},
 @{Name='create operation presence';File='service.go';Before='request.OperationID == "" || ';After=''},
 @{Name='create context validation';File='service.go';Before='request.Context.Validate() != nil || ';After=''},
 @{Name='create source draft identity';File='service.go';Before='request.Collection.SourceDraftID != request.Context.DraftID || ';After=''},
 @{Name='create frozen revision identity';File='service.go';Before='request.Collection.FinalizedDraftRevision != request.Context.Revision || ';After=''},
 @{Name='create frozen generation identity';File='service.go';Before='request.Collection.ArticleGeneration != request.Context.ArticleGeneration || ';After=''},
 @{Name='create snapshot complete';File='service.go';Before='!request.Collection.Snapshot.Complete || ';After=''},
 @{Name='create snapshot ID presence';File='service.go';Before='request.Collection.Snapshot.SnapshotID == "" || ';After=''},
 @{Name='create participant upper bound';File='service.go';Before='len(request.Collection.Participants) > 100000 || ';After=''},
 @{Name='create minimum two included candidates';File='service.go';Before=' || len(request.Input.CandidateIDs) < 2';After=''},
 @{Name='create participant ID presence';File='service.go';Before='participant.ID == "" || ';After=''},
 @{Name='create participant ID uniqueness';File='service.go';Before='if participant.ID == "" || ids[participant.ID] {';After='if participant.ID == "" {'},
 @{Name='create included count equality';File='service.go';Before='if len(included) != len(request.Input.CandidateIDs) {';After='if false {'},
 @{Name='create included order equality';File='service.go';Before='if request.Input.CandidateIDs[index] != id {';After='if request.Input.CandidateIDs[index] != id && false {'}
)
$pattern='^TestRoundLocalCommands|^TestRoundReplay|^TestRoundRetry|^TestRoundCreateValidation|^TestRoundRerun'
$killed=@()
try{
 Push-Location $workspace
 try{
  $baseline=& go test ./internal/storage/sqlite -run $pattern -count=1 -timeout=90s 2>&1
  if($LASTEXITCODE -ne 0){throw ('Permission mutation baseline failed: '+($baseline -join "`n"))}
  Write-Output 'Round permission mutation baseline PASS'
  $ordinal=0
  foreach($mutation in $mutations){
   $ordinal++
   $original=$originals[$mutation.File]
   if(-not $original.Contains($mutation.Before)){throw ('Mutation target missing: '+$mutation.Name)}
   $mutantPath=Join-Path $directory ('mutant-'+$ordinal+'.go')
   [IO.File]::WriteAllText($mutantPath,$original.Replace($mutation.Before,$mutation.After),(New-Object Text.UTF8Encoding($false)))
   $replace=@{};$replace[(Join-Path $workspace ('internal/roundlifecycle/'+$mutation.File))]=$mutantPath
   $overlay=Join-Path $directory ('overlay-'+$ordinal+'.json')
   [IO.File]::WriteAllText($overlay,(@{Replace=$replace}|ConvertTo-Json -Depth 4),(New-Object Text.UTF8Encoding($false)))
   $output=& go test -overlay $overlay ./internal/storage/sqlite -run $pattern -count=1 -timeout=90s 2>&1
   $exitCode=$LASTEXITCODE
   [IO.File]::WriteAllText((Join-Path $directory ('mutant-'+$ordinal+'.log')),($output -join "`n"))
   if($exitCode -eq 0){throw ('Surviving permission mutation: '+$mutation.Name)}
   if(($output -join "`n") -notmatch 'FAIL: TestRoundLocalCommands|FAIL: TestRoundReplay|FAIL: TestRoundRetry|FAIL: TestRoundCreateValidation|FAIL: TestRoundRerun'){throw ('Compile/infra failure is not a mutation kill: '+$mutation.Name+"`n"+($output -join "`n"))}
   $killed+=$mutation.Name;Write-Output ('KILLED '+$mutation.Name)
  }
 }finally{Pop-Location}
 Write-Output ('Round permission mutation gate PASS: '+$killed.Count+'/'+$mutations.Count)
}finally{
 foreach($name in $hashes.Keys){if((Get-FileHash -LiteralPath (Join-Path $workspace ('internal/roundlifecycle/'+$name)) -Algorithm SHA256).Hash -ne $hashes[$name]){throw ('Original lifecycle source changed: '+$name)}}
 $resolved=[IO.Path]::GetFullPath($directory)
 if(-not $resolved.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase)){throw 'Permission mutation cleanup escaped workspace'}
 if(-not $KeepArtifacts -and (Test-Path -LiteralPath $resolved)){Remove-Item -LiteralPath $resolved -Recurse -Force}
 if(-not $KeepArtifacts -and (Test-Path -LiteralPath $resolved)){throw 'Permission mutation artifact cleanup failed'}
 Write-Output 'Original lifecycle SHA256 and owned artifact cleanup PASS'
}