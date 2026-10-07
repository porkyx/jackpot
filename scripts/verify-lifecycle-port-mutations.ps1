$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$taskRoot=Join-Path $workspace '.task'
$directory=[IO.Path]::GetFullPath((Join-Path $taskRoot ('verified-test-lifecycle-ports-'+[Guid]::NewGuid().ToString('N'))))
$prefix=$taskRoot.TrimEnd([char[]]'\/')+[IO.Path]::DirectorySeparatorChar+'verified-test-lifecycle-ports-'
if(-not $directory.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase)){throw 'Mutation directory escaped workspace'}
foreach($path in @($workspace,$taskRoot)){if(Test-Path -LiteralPath $path){if(((Get-Item -LiteralPath $path -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)-ne 0){throw 'Mutation root reparse point forbidden'}}}
[void](New-Item -ItemType Directory -Path $directory)
$paths=@{service=Join-Path $workspace 'internal/roundlifecycle/service.go';commands=Join-Path $workspace 'internal/roundlifecycle/commands.go';tests=Join-Path $workspace 'internal/roundlifecycle/port_boundaries_test.go'}
$original=@{};$hashes=@{}
foreach($key in $paths.Keys){$original[$key]=[IO.File]::ReadAllText($paths[$key]).Replace("`r`n","`n");$hashes[$key]=(Get-FileHash -LiteralPath $paths[$key] -Algorithm SHA256).Hash}
$pattern='^TestNilPort|^TestNewService|^TestRecoverRequires|^TestPendingOperation|^TestPreparedCreate|^TestNilPrepared|^TestAdmissionAcknowledgement|^TestAdmissionOwnership'
$variants=@(
 @{name='typed nil differs from initialized value';file='service';before='return reflect.ValueOf(value).IsNil()';after='return !reflect.ValueOf(value).IsNil()'},
 @{name='plain zero values remain nonnil';file='service';before="default:`n`t`treturn false";after="default:`n`t`treturn true"},
 @{name='storage and session independently required';file='service';before='nilPort(options.Storage) || options.Session == ""';after='nilPort(options.Storage) && options.Session == ""'},
 @{name='typed nil entropy receives default';file='service';before='if nilPort(options.Entropy) {';after='if false {'},
 @{name='prepared owner fence';file='service';before='prepared.owner != service || prepared.closed';after='prepared.closed'},
 @{name='prepared closed fence';file='service';before='prepared.owner != service || prepared.closed';after='prepared.owner != service'},
 @{name='reader missing capability typed refusal';file='commands';before="if !ok {`n`t`treturn nil, contracts.NewFault(contracts.InvalidState)";after="if !ok {`n`t`treturn nil, contracts.NewFault(contracts.StorageUnavailable)"},
 @{name='active operation status independent';file='commands';before='operation.Status != contracts.OperationPending || operation.CollectionID != round.CollectionID || operation.RoundID != round.ID';after='operation.CollectionID != round.CollectionID || operation.RoundID != round.ID'},
 @{name='active operation collection independent';file='commands';before='operation.Status != contracts.OperationPending || operation.CollectionID != round.CollectionID || operation.RoundID != round.ID';after='operation.Status != contracts.OperationPending || operation.RoundID != round.ID'},
 @{name='active operation round independent';file='commands';before='operation.Status != contracts.OperationPending || operation.CollectionID != round.CollectionID || operation.RoundID != round.ID';after='operation.Status != contracts.OperationPending || operation.CollectionID != round.CollectionID'},
 @{name='acknowledgement read error prevents reconciliation';file='service';before='readErr == nil && observed.Status';after='(readErr == nil || readErr != nil) && observed.Status'},
 @{name='acknowledgement status required';file='service';before='observed.Status != contracts.OperationUnknown && ';after=''},
 @{name='acknowledgement kind required';file='service';before='observed.Operation.Kind == request.Operation.Kind && ';after=''},
 @{name='acknowledgement fingerprint required';file='service';before='observed.Operation.PublicFingerprint == request.Operation.PublicFingerprint && ';after=''},
 @{name='acknowledgement collection required';file='service';before='observed.CollectionID == request.CollectionID && ';after=''},
 @{name='acknowledgement round required';file='service';before=' && observed.RoundID == request.RoundID';after=''},
 @{name='acknowledgement committed round read required';file='service';before='if roundErr == nil {';after='if roundErr == nil || roundErr != nil {'},
 @{name='live admission success required';file='service';before='err == nil && !admission.Replay && admission.Round.State == contracts.Executing';after='!admission.Replay && admission.Round.State == contracts.Executing'},
 @{name='live admission nonreplay required';file='service';before='err == nil && !admission.Replay && admission.Round.State == contracts.Executing';after='err == nil && admission.Round.State == contracts.Executing'},
 @{name='live admission executing required';file='service';before='err == nil && !admission.Replay && admission.Round.State == contracts.Executing';after='err == nil && !admission.Replay'}
)
$report=[ordered]@{status='running';sourceSHA256=$hashes;killed=@();compileTimeoutInfrastructureKills=0;cleanupVerified=$false}
try{
 Push-Location $workspace
 try{
  $baseline=& go test ./internal/roundlifecycle -run $pattern -count=1 -timeout=30s 2>&1
  if($LASTEXITCODE -ne 0){throw ('Baseline failed: '+($baseline -join "`n"))}
  Write-Output 'Lifecycle port branch baseline PASS'
  $ordinal=0
  foreach($variant in $variants){
   $ordinal++
   $text=$original[$variant.file]
   if(-not $text.Contains($variant.before)){throw ('Missing target: '+$variant.name)}
   $changed=$text.Replace($variant.before,$variant.after)
   $file=Join-Path $directory ('mutant-'+$ordinal+'.go')
   [IO.File]::WriteAllText($file,$changed,[Text.UTF8Encoding]::new($false))
   $mapping=@{};$mapping[$paths[$variant.file]]=$file
   $overlay=Join-Path $directory ('overlay-'+$ordinal+'.json')
   [IO.File]::WriteAllText($overlay,(@{Replace=$mapping}|ConvertTo-Json -Depth 4),[Text.UTF8Encoding]::new($false))
   $output=& go test -overlay $overlay ./internal/roundlifecycle -run $pattern -count=1 -timeout=30s 2>&1
   $code=$LASTEXITCODE;$joined=$output -join "`n"
   [IO.File]::WriteAllText((Join-Path $directory ('mutant-'+$ordinal+'.log')),$joined,[Text.UTF8Encoding]::new($false))
   if($code -eq 0){throw ('Surviving mutation: '+$variant.name)}
   if($joined -match 'panic:|build failed|timed out|no test files' -or $joined -notmatch '--- FAIL: Test'){throw ('Only observable assertion failures count: '+$variant.name+"`n"+$joined)}
   $report.killed+=@([ordered]@{name=$variant.name;exitCode=$code});Write-Output ('KILLED '+$variant.name)
  }
  $report.status='passed'
 }finally{Pop-Location}
}catch{
 $report.status='failed';$report.failure=$_.Exception.Message;throw
}finally{
 foreach($key in $hashes.Keys){if((Get-FileHash -LiteralPath $paths[$key] -Algorithm SHA256).Hash -ne $hashes[$key]){throw ('Original source changed: '+$key)}}
 $absolute=[IO.Path]::GetFullPath($directory)
 if(-not $absolute.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase)){throw 'Cleanup escaped owned directory'}
 if(Test-Path -LiteralPath $absolute){foreach($entry in @((Get-Item -LiteralPath $absolute -Force))+@(Get-ChildItem -LiteralPath $absolute -Recurse -Force)){if(($entry.Attributes -band [IO.FileAttributes]::ReparsePoint)-ne 0){throw 'Cleanup reparse point forbidden'}};Remove-Item -LiteralPath $absolute -Recurse -Force}
 if(Test-Path -LiteralPath $absolute){throw 'Cleanup left artifacts'}
 $report.cleanupVerified=$true
 [IO.File]::WriteAllText((Join-Path $taskRoot 'lifecycle-port-mutations-report.json'),($report|ConvertTo-Json -Depth 5),[Text.UTF8Encoding]::new($false))
 Write-Output 'Original source SHA and unique overlay cleanup PASS'
}