$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent));$taskRoot=Join-Path $workspace '.task'
$root=Join-Path $taskRoot ('verified-test-runtime-mutant-'+[Guid]::NewGuid().ToString('N'))
$sourcePath=Join-Path $workspace 'internal/platform/webview_runtime.go';$original=[IO.File]::ReadAllText($sourcePath);$sha=(Get-FileHash -LiteralPath $sourcePath -Algorithm SHA256).Hash
$cases=@(
 @{name='nil dependencies fail unavailable';from='return ErrWebView2Unavailable';to='return ErrWebView2Missing'},
 @{name='independent nil notice';from='probe == nil || inform == nil';to='probe == nil'},
 @{name='installed nonempty version';from='detectionErr == nil && version != ""';to='detectionErr == nil && version == ""'},
 @{name='detector error gates nonempty and empty versions';from='if detectionErr != nil {';to='if detectionErr != nil && false {'},
 @{name='missing safe notice';from='failure, message := ErrWebView2Missing, WebView2RuntimeMissingMessage';to='failure, message := ErrWebView2Missing, WebView2RuntimeUnavailableMessage'},
 @{name='native notice failure observed';from='return errors.Join(failure, ErrWebView2Notice)';to='return failure'},
 @{name='zero native notice failure';from='if result == 0 {';to='if result != 0 {'}
)
Push-Location $workspace
try{
 New-Item -ItemType Directory -Path $root|Out-Null
 $baseline=& go test -count=1 -run '^TestWebView2Runtime' ./internal/platform 2>&1
 if($LASTEXITCODE -ne 0){throw ('Runtime baseline failed: '+($baseline -join "`n"))}
 $killed=0
 foreach($case in $cases){
  if(([regex]::Matches($original,[regex]::Escape($case.from))).Count -ne 1){throw ('Runtime mutant source match not unique: '+$case.name)}
  $mutant=Join-Path $root 'runtime.go';[IO.File]::WriteAllText($mutant,$original.Replace($case.from,$case.to),[Text.UTF8Encoding]::new($false))
  $replace=@{};$replace[$sourcePath]=$mutant;$overlay=Join-Path $root 'overlay.json';[IO.File]::WriteAllText($overlay,(@{Replace=$replace}|ConvertTo-Json -Compress),[Text.UTF8Encoding]::new($false))
  $output=& go test '-overlay' $overlay -count=1 -run '^TestWebView2Runtime' ./internal/platform 2>&1
  $code=$LASTEXITCODE;$joined=$output -join "`n"
  if($code -eq 0 -or $joined -notmatch '--- FAIL: TestWebView2Runtime' -or $joined -match '\[build failed\]'){throw ('Runtime branch survived or compile-only failure: '+$case.name+"`n"+$joined)}
  $killed++;Write-Output ('KILLED '+$case.name)
 }
 if($killed -ne 7){throw 'Runtime mutation count mismatch'}
 Write-Output 'Runtime preflight mutation PASS: 7/7 assertion failures'
}finally{
 if((Get-FileHash -LiteralPath $sourcePath -Algorithm SHA256).Hash -ne $sha){throw 'Runtime mutation changed original production source'}
 if(Test-Path -LiteralPath $root){$target=[IO.Path]::GetFullPath($root);if([IO.Path]::GetDirectoryName($target) -ne $taskRoot -or -not [IO.Path]::GetFileName($target).StartsWith('verified-test-runtime-mutant-',[StringComparison]::Ordinal)){throw 'Runtime mutation cleanup escaped unique artifact'};foreach($item in Get-ChildItem -LiteralPath $target -Recurse -Force){if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw 'Runtime mutation cleanup found reparse point'}};Remove-Item -LiteralPath $target -Recurse -Force}
 Pop-Location
}
Write-Output 'Runtime original SHA and unique overlay cleanup PASS'