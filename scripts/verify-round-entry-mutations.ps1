param([switch]$KeepArtifacts)
$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$directory=[IO.Path]::GetFullPath((Join-Path $workspace ('.task/verified-test-round-entry-'+[guid]::NewGuid().ToString('N'))))
$prefix=Join-Path $workspace '.task/verified-test-round-entry-'
if(-not $directory.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase)){throw 'Entry mutation directory escaped workspace'}
$null=New-Item -ItemType Directory -Path $directory
$paths=@{lifecycle=(Join-Path $workspace 'internal/roundlifecycle/service.go');queries=(Join-Path $workspace 'internal/storage/sqlite/round_queries.go')}
$originals=@{};$hashes=@{}
foreach($key in $paths.Keys){$originals[$key]=[IO.File]::ReadAllText($paths[$key]).Replace("`r`n","`n");$hashes[$key]=(Get-FileHash -LiteralPath $paths[$key] -Algorithm SHA256).Hash}
$beginWork="func (service *Service) beginWork() (func(), error) {`n`tservice.mu.Lock()`n`tdefer service.mu.Unlock()`n`tif service.closed.Load() {"
$postGate="}()`n`tif service.closed.Load() {`n`t`treturn nil, contracts.NewFault(contracts.InvalidState)`n`t}`n`toperation, err := service.options.Storage.ReadOperation"
$mutations=@(
 @{Name='new work rejected after close';File='lifecycle';Before=$beginWork;After=$beginWork.Replace('if service.closed.Load() {','if false {')},
 @{Name='graceful close waits registered claims';File='lifecycle';Before='service.executions.Wait(); ';After=''},
 @{Name='prepared snapshot rechecks close before storage';File='lifecycle';Before=$postGate;After=$postGate.Replace('if service.closed.Load() {','if false {')},
 @{Name='committed notice is delivered';File='lifecycle';Before='if service.options.Publish == nil {';After='if true {'},
 @{Name='history equal timestamp tie desc';File='queries';Before='ORDER BY c.created_at DESC,c.id DESC';After='ORDER BY c.created_at DESC,c.id ASC'},
 @{Name='history offset excludes exact boundary';File='queries';Before='if total <= offset || len(page) >= int(limit) {';After='if total < offset || len(page) >= int(limit) {'}
)
$pattern='^TestRoundCreateConcurrentPublic|^TestRoundRegisteredWorkBeforeClose|^TestRoundIndependentPreparedSnapshots|^TestRoundMultiCollectionShutdown|^TestRoundHistoryEmptyFifty|^TestRoundUserAndScheduledPublication'
$killed=@()
try{
 Push-Location $workspace
 try{
  $baseline=& go test ./internal/storage/sqlite -run $pattern -count=1 -timeout=60s 2>&1
  if($LASTEXITCODE -ne 0){throw ('Entry mutation baseline failed: '+($baseline -join "`n"))}
  Write-Output 'Round entry mutation baseline PASS: local admission, shutdown, history and publication'
  $ordinal=0
  foreach($mutation in $mutations){
   $ordinal++
   $original=$originals[$mutation.File]
   if(-not $original.Contains($mutation.Before)){throw ('Mutation target missing: '+$mutation.Name)}
   $mutant=Join-Path $directory ('mutant-'+$ordinal+'.go')
   [IO.File]::WriteAllText($mutant,$original.Replace($mutation.Before,$mutation.After),(New-Object Text.UTF8Encoding($false)))
   $replace=@{};$replace[$paths[$mutation.File]]=$mutant
   $overlay=Join-Path $directory ('overlay-'+$ordinal+'.json')
   [IO.File]::WriteAllText($overlay,(@{Replace=$replace}|ConvertTo-Json -Depth 4),(New-Object Text.UTF8Encoding($false)))
   $output=& go test -overlay $overlay ./internal/storage/sqlite -run $pattern -count=1 -timeout=60s 2>&1
   $exitCode=$LASTEXITCODE
   [IO.File]::WriteAllText((Join-Path $directory ('mutant-'+$ordinal+'.log')),($output -join "`n"))
   if($exitCode -eq 0){throw ('Surviving entry mutation: '+$mutation.Name)}
   if(($output -join "`n") -notmatch 'FAIL: TestRoundCreateConcurrentPublic|FAIL: TestRoundRegisteredWorkBeforeClose|FAIL: TestRoundIndependentPreparedSnapshots|FAIL: TestRoundMultiCollectionShutdown|FAIL: TestRoundHistoryEmptyFifty|FAIL: TestRoundUserAndScheduledPublication'){throw ('Compile/timeout/infra failure is not a mutation kill: '+$mutation.Name+"`n"+($output -join "`n"))}
   $killed+=$mutation.Name;Write-Output ('KILLED '+$mutation.Name)
  }
 }finally{Pop-Location}
 Write-Output ('Round entry mutation gate PASS: '+$killed.Count+'/'+$mutations.Count)
}finally{
 foreach($key in $hashes.Keys){if((Get-FileHash -LiteralPath $paths[$key] -Algorithm SHA256).Hash -ne $hashes[$key]){throw ('Original entry source changed: '+$key)}}
 $resolved=[IO.Path]::GetFullPath($directory)
 if(-not $resolved.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase)){throw 'Entry mutation cleanup escaped workspace'}
 if(-not $KeepArtifacts -and (Test-Path -LiteralPath $resolved)){Remove-Item -LiteralPath $resolved -Recurse -Force}
 if(-not $KeepArtifacts -and (Test-Path -LiteralPath $resolved)){throw 'Entry mutation cleanup failed'}
 Write-Output 'Original entry source SHA256 and owned artifact cleanup PASS'
}