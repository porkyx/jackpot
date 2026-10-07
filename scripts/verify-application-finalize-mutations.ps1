$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$taskRoot=Join-Path $workspace '.task'
$directory=[IO.Path]::GetFullPath((Join-Path $taskRoot ('verified-test-application-finalize-'+[Guid]::NewGuid().ToString('N'))))
$prefix=$taskRoot.TrimEnd([char[]]'\/')+[IO.Path]::DirectorySeparatorChar+'verified-test-application-finalize-'
if(-not $directory.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase)){throw 'Mutation directory escaped workspace'}
foreach($path in @($workspace,$taskRoot)){if(Test-Path -LiteralPath $path){if(((Get-Item -LiteralPath $path -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)-ne 0){throw 'Mutation root reparse point forbidden'}}}
[void](New-Item -ItemType Directory -Path $directory)
$paths=@{service=Join-Path $workspace 'internal/application/finalize.go';tests=Join-Path $workspace 'internal/application/finalize_fence_failures_test.go'}
$original=@{};$hashes=@{}
foreach($key in $paths.Keys){$original[$key]=[IO.File]::ReadAllText($paths[$key]).Replace("`r`n","`n");$hashes[$key]=(Get-FileHash -LiteralPath $paths[$key] -Algorithm SHA256).Hash}
$pattern='^TestFinalizeClosedDuringPreparation|^TestFinalizeMaximumRevision'
$variants=@(
 @{name='closed owner refused independently of Ready state';file='service';before='service.closed || service.draft.Summary.State != contracts.DraftReady';after='service.draft.Summary.State != contracts.DraftReady'},
 @{name='maximum revision refusal precedes admission';file='service';before='if _, err = contracts.Increment(service.draft.Summary.Revision); err != nil {';after='if _, err = contracts.Increment(service.draft.Summary.Revision); err != nil && false {'}
)
$report=[ordered]@{status='running';sourceSHA256=$hashes;killed=@();compileTimeoutInfrastructureKills=0;cleanupVerified=$false}
try{
 Push-Location $workspace
 try{
  $baseline=& go test ./internal/application -run $pattern -count=1 -timeout=30s 2>&1
  if($LASTEXITCODE -ne 0){throw ('Baseline failed: '+($baseline -join "`n"))}
  Write-Output 'Application finalize fence baseline PASS'
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
   $output=& go test -overlay $overlay ./internal/application -run $pattern -count=1 -timeout=30s 2>&1
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
 foreach($key in $paths.Keys){if((Get-FileHash -LiteralPath $paths[$key] -Algorithm SHA256).Hash -ne $hashes[$key]){throw ('Original source changed: '+$key)}}
 if(Test-Path -LiteralPath $directory){$resolved=[IO.Path]::GetFullPath((Get-Item -LiteralPath $directory).FullName);if(-not $resolved.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase)){throw 'Cleanup escaped owned workspace'};if(((Get-Item -LiteralPath $directory).Attributes -band [IO.FileAttributes]::ReparsePoint)-ne 0){throw 'Mutation directory became reparse point'};Remove-Item -LiteralPath $directory -Recurse -Force}
 $report.cleanupVerified= -not (Test-Path -LiteralPath $directory)
 $reportName=if($report.status -eq 'passed'){'application-finalize-fences-mutants.json'}else{'application-finalize-fences-mutants-failed.json'}
 [IO.File]::WriteAllText((Join-Path $taskRoot $reportName),($report|ConvertTo-Json -Depth 8),[Text.UTF8Encoding]::new($false))
}
if ($report.status -eq 'passed') { $global:LASTEXITCODE = 0 }
