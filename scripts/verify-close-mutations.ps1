$ErrorActionPreference='Stop'
$projectRoot=[IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent))
$taskRoot=Join-Path $projectRoot '.task'
$runRoot=Join-Path $taskRoot ('close-mutations-'+[Guid]::NewGuid().ToString('N'))
$sourcePath=Join-Path $projectRoot 'internal/desktop/scheduled_close.go'
$testPath=Join-Path $projectRoot 'internal/desktop/scheduled_close_test.go'
$sourceHash=(Get-FileHash -LiteralPath $sourcePath -Algorithm SHA256).Hash
$testHash=(Get-FileHash -LiteralPath $testPath -Algorithm SHA256).Hash
$original=[IO.File]::ReadAllText($sourcePath)
$cases=@(
 @{name='count-dependency';old='count == nil || confirm == nil';new='confirm == nil';test='TestScheduledCloseRejects'},
 @{name='confirmation-dependency';old='count == nil || confirm == nil';new='count == nil';test='TestScheduledCloseRejects'},
 @{name='cancelled-context';old='ctx == nil || ctx.Err() != nil';new='ctx == nil';test='TestScheduledCloseRejects'},
 @{name='duplicate-close-choice';old="if !guard.mu.TryLock() {`n`t`treturn false";new="if !guard.mu.TryLock() {`n`t`treturn true";test='TestScheduledCloseConcurrentEvents'},
 @{name='error-independent-of-zero';old='err == nil && count == 0';new='count == 0';test='TestScheduledCloseErrorWithZero'},
 @{name='zero-count';old='err == nil && count == 0';new='err == nil && count != 0';test='TestScheduledCloseZeroCount'},
 @{name='failed-count-number';old="if err == nil {`n`t`tmessage =";new="if err != nil {`n`t`tmessage =";test='TestScheduledCloseFirstNthContinuous'},
 @{name='user-decision';old='return guard.confirm(message)';new='return !guard.confirm(message)';test='TestScheduledCloseShowsExactCount'}
)
$results=New-Object 'System.Collections.Generic.List[object]'
Push-Location $projectRoot
try {
 New-Item -ItemType Directory -Path $runRoot|Out-Null
 & go test ./internal/desktop -run TestScheduledClose -race -count=1 '-coverprofile=.task/close-guard.cover'
 if($LASTEXITCODE -ne 0){throw 'Close mutation baseline failed'}
 foreach($case in $cases){
  $normal=$original.Replace("`r`n","`n")
  if(([regex]::Matches($normal,[regex]::Escape($case.old))).Count -ne 1){throw ('Close mutation source marker must occur once: '+$case.name)}
  $mutant=Join-Path $runRoot ($case.name+'.go')
  [IO.File]::WriteAllText($mutant,$normal.Replace($case.old,$case.new),[Text.UTF8Encoding]::new($false))
  $overlay=Join-Path $runRoot 'overlay.json'
  $replace=@{};$replace[$sourcePath]=$mutant
  [IO.File]::WriteAllText($overlay,(@{Replace=$replace}|ConvertTo-Json -Depth 3 -Compress),[Text.UTF8Encoding]::new($false))
  $output=& go test -overlay $overlay ./internal/desktop -run $case.test -count=1 -timeout 15s 2>&1
  $exit=$LASTEXITCODE;$text=$output -join "`n"
  if($exit -eq 0 -or $text -notmatch '--- FAIL:' -or $text -match 'build failed|undefined:|timed out|panic:|fatal error:'){throw ('Close mutant did not produce an independent assertion failure: '+$case.name+"`n"+$text)}
  $results.Add(@{name=$case.name;status='assertion-killed'})
  Write-Host ('ASSERTION KILL '+$case.name)
 }
 & go tool cover '-func=.task/close-guard.cover'
 if($LASTEXITCODE -ne 0){throw 'Close coverage analysis failed'}
} finally {
 try {
  if((Get-FileHash -LiteralPath $sourcePath -Algorithm SHA256).Hash -ne $sourceHash -or (Get-FileHash -LiteralPath $testPath -Algorithm SHA256).Hash -ne $testHash){throw 'Close source or regression test changed during overlay harness'}
  if(Test-Path -LiteralPath $runRoot){
   $target=[IO.Path]::GetFullPath($runRoot);$prefix=[IO.Path]::GetFullPath($taskRoot).TrimEnd([char[]]'\/')+[IO.Path]::DirectorySeparatorChar
   if(-not $target.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase) -or -not [IO.Path]::GetFileName($target).StartsWith('close-mutations-')){throw 'Close mutant cleanup escaped owned directory'}
   foreach($item in Get-ChildItem -LiteralPath $target -Recurse -Force){if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw 'Close mutant cleanup found reparse point'}}
   Remove-Item -LiteralPath $target -Recurse -Force
  }
  [IO.File]::WriteAllText((Join-Path $taskRoot 'close-guard-mutations.json'),(@{results=$results.ToArray();sourceSHA256=$sourceHash;testSHA256=$testHash;sourceUnchanged=$true;temporaryRemoved=-not(Test-Path -LiteralPath $runRoot)}|ConvertTo-Json -Depth 5),[Text.UTF8Encoding]::new($false))
 } finally {Pop-Location}
}