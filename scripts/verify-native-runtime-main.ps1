# Run only after the team has released its formal performance CPU checkpoint.
$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent));$taskRoot=Join-Path $workspace '.task'
$artifact=Join-Path $taskRoot ('verified-test-runtime-main-'+[Guid]::NewGuid().ToString('N'))
$runRoot=Join-Path $taskRoot ('native-runtime-run-main-'+[Guid]::NewGuid().ToString('N'))
$executable='';$owned=New-Object 'System.Collections.Generic.List[System.Diagnostics.Process]'
$checks=New-Object 'System.Collections.Generic.List[string]';$reportPath=Join-Path $taskRoot 'native-runtime-main-report.json'
$report=[ordered]@{status='running';checks=@();physicalMissing=$false;controlledDetection=$true;automaticDownloadInstall=$false;browserOpened=$false;databaseUnchanged=$false;sidecarLockCreated=$false;cleanupVerified=$false;runs=@();native='actual original main with only official detector result controlled through Go overlay'}
Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes
$dependency=Join-Path $PSScriptRoot 'verify-native-runtime.ps1';$tokens=$null;$parseErrors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile($dependency,[ref]$tokens,[ref]$parseErrors)
if($parseErrors.Count -ne 0){throw 'Native runtime shared verifier parse failure'}
foreach($name in @('Assert-OwnedRuntimeDirectory','Read-FixtureSHA','Start-OwnedRuntime','Wait-OwnedRuntimeDialog','Invoke-RuntimeOK')){
 $definition=$ast.FindAll({param($node)$node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq $name},$true)
 if($definition.Count -ne 1){throw ('Missing independent native runtime function: '+$name)}
 Invoke-Expression $definition[0].Extent.Text
}
$files=@('main.go','go.mod','go.sum','internal/platform/webview_runtime.go','internal/platform/webview_runtime_windows.go','internal/platform/webview_runtime_other.go','frontend/dist/index.html')
$sourceSHA=[ordered]@{};foreach($name in $files){$sourceSHA[$name]=(Get-FileHash -LiteralPath (Join-Path $workspace $name) -Algorithm SHA256).Hash}
$sourcePath=Join-Path $workspace 'internal/platform/webview_runtime_windows.go';$original=[IO.File]::ReadAllText($sourcePath)
$needle='return webviewloader.GetAvailableCoreWebView2BrowserVersionString("")'
if(([regex]::Matches($original,[regex]::Escape($needle))).Count -ne 1){throw 'Official detector overlay target is not unique'}
Push-Location $workspace
try{
 New-Item -ItemType Directory -Path $artifact|Out-Null
 Assert-OwnedRuntimeDirectory $runRoot|Out-Null;New-Item -ItemType Directory -Path $runRoot|Out-Null
 $seed=Join-Path $taskRoot 'native-close-seed.exe'
 if(-not(Test-Path -LiteralPath $seed -PathType Leaf)){throw 'Build existing isolated native close seed first'}
 $seedOutput=& $seed -data-root $runRoot;if($LASTEXITCODE -ne 0){throw 'Actual SQLite fixture seed failed'}
 $report['fixture']=$seedOutput|ConvertFrom-Json
 $before=Read-FixtureSHA;$report['beforeSHA']=$before
 foreach($mode in @('missing','error')){
  $modeRoot=Join-Path $artifact $mode;New-Item -ItemType Directory -Path $modeRoot|Out-Null
  # Keep the real official lookup, then control only its public result. The
  # installed runtime/registry and all main/body/notice code remain unchanged.
  $controlled='if _, err := webviewloader.GetAvailableCoreWebView2BrowserVersionString(""); err != nil { return "", err }; return "", '
  $controlled+=if($mode -eq 'missing'){'nil'}else{'ErrWebView2Unavailable'}
  $overlaySource=Join-Path $modeRoot 'runtime_windows.go';[IO.File]::WriteAllText($overlaySource,$original.Replace($needle,$controlled),[Text.UTF8Encoding]::new($false))
  $replace=@{};$replace[$sourcePath]=$overlaySource;$overlay=Join-Path $modeRoot 'overlay.json';[IO.File]::WriteAllText($overlay,(@{Replace=$replace}|ConvertTo-Json -Compress),[Text.UTF8Encoding]::new($false))
  $executable=Join-Path $modeRoot ('Jackpot-runtime-'+$mode+'.exe')
  & go build '-overlay' $overlay -tags production '-ldflags=-H=windowsgui' -o $executable .
  if($LASTEXITCODE -ne 0){throw 'Actual main controlled detector overlay did not compile; not a mutation kill'}
  $p=Start-OwnedRuntime $mode
  $proof=Get-CimInstance Win32_Process -Filter ('ProcessId='+$p.Id)
  if($null -eq $proof -or $proof.ExecutablePath -ne $executable -or $proof.CommandLine -notlike ('* -mode '+$mode)){throw 'Main overlay PID identity mismatch'}
  $creation=$proof.CreationDate.ToUniversalTime().ToString('o')
  $p|Add-Member -NotePropertyName RuntimeOwnedCreated -NotePropertyValue $creation
  $dialog=Wait-OwnedRuntimeDialog $p
  $text=($dialog.FindAll([Windows.Automation.TreeScope]::Descendants,[Windows.Automation.Condition]::TrueCondition)|ForEach-Object{$_.Current.Name}) -join "`n"
  $expected=if($mode -eq 'missing'){'Jackpot을 실행하려면 Microsoft Edge WebView2 Runtime이 필요합니다.'}else{'Microsoft Edge WebView2 Runtime을 확인하지 못해 Jackpot을 시작할 수 없습니다.'}
  if(-not $text.Contains($expected) -or -not $text.Contains('https://developer.microsoft.com/en-us/microsoft-edge/webview2/') -or -not $text.Contains('Jackpot은 자동으로 다운로드하거나 설치하지 않습니다.')){throw 'Actual original main omitted safe native runtime policy'}
  # One branch acknowledges; the other closes the OS titlebar. Neither may
  # advance main past the preflight or change the durable database.
  if($mode -eq 'missing'){Invoke-RuntimeOK $dialog}else{$pattern=$dialog.GetCurrentPattern([Windows.Automation.WindowPattern]::Pattern);$pattern.Close()}
  if(-not$p.WaitForExit(15000) -or $p.ExitCode -ne 1){throw 'Actual main did not refuse before data startup'}
  $stderr=$p.StandardError.ReadToEnd()
  $expectedError=if($mode -eq 'missing'){'WebView2 Runtime is not installed'}else{'WebView2 Runtime could not be checked'}
  if(-not$stderr.Contains($expectedError)){throw 'Actual main lost typed runtime refusal'}
  if((Read-FixtureSHA) -ne $before){throw 'Actual main runtime failure touched DB/file-set/backup/sidecar/instance lock'}
  if(@(Get-CimInstance Win32_Process -Filter "Name='msedgewebview2.exe'"|Where-Object{$_.CommandLine -like ('*'+$runRoot+'*')}).Count -ne 0){throw 'Main runtime refusal started owned WebView'}
  $report.runs+=@{mode=$mode;pid=$p.Id;creationDate=$creation;exeSHA256=(Get-FileHash -LiteralPath $executable -Algorithm SHA256).Hash;exitCode=$p.ExitCode;notice=$text;stderr=$stderr;originalMainSHA256=$sourceSHA['main.go'];assetIndexSHA256=$sourceSHA['frontend/dist/index.html'];close=if($mode -eq 'missing'){'OK'}else{'native-titlebar-close'}}
  $checks.Add('original-main-'+$mode+'-native-refusal-before-DB-lock-sidecar-zero')
 }
 $report.databaseUnchanged=$true;$report.status='passed'
}catch{$report.status='failed';$report['failure']=$_.Exception.Message;$report['line']=$_.InvocationInfo.ScriptLineNumber;throw}
finally{
 foreach($p in $owned){$p.Refresh();if(-not$p.HasExited){$proof=Get-CimInstance Win32_Process -Filter ('ProcessId='+$p.Id);if($null -eq $proof -or -not$proof.ExecutablePath.StartsWith($artifact+[IO.Path]::DirectorySeparatorChar,[StringComparison]::OrdinalIgnoreCase) -or $proof.CreationDate.ToUniversalTime().ToString('o') -ne $p.RuntimeOwnedCreated){throw 'Main overlay cleanup lost exact child ownership'};$p.Kill();if(-not$p.WaitForExit(5000)){throw 'Owned main overlay failed cleanup'}};$p.Dispose()}
 foreach($name in $files){if((Get-FileHash -LiteralPath (Join-Path $workspace $name) -Algorithm SHA256).Hash -ne $sourceSHA[$name]){throw ('Original source changed during main overlay: '+$name)}}
 if(Test-Path -LiteralPath $runRoot){$target=Assert-OwnedRuntimeDirectory $runRoot;foreach($item in Get-ChildItem -LiteralPath $target -Recurse -File){$f=[IO.File]::Open($item.FullName,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::None);$f.Dispose()};$target=Assert-OwnedRuntimeDirectory $runRoot;Remove-Item -LiteralPath $target -Recurse -Force}
 if(Test-Path -LiteralPath $artifact){$target=[IO.Path]::GetFullPath($artifact);if([IO.Path]::GetDirectoryName($target) -ne $taskRoot -or -not[IO.Path]::GetFileName($target).StartsWith('verified-test-runtime-main-',[StringComparison]::Ordinal) -or ((Get-Item -LiteralPath $target -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw 'Main overlay cleanup escaped own artifact'};foreach($item in Get-ChildItem -LiteralPath $target -Recurse -Force){if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw 'Main overlay artifact contains reparse point'}};Remove-Item -LiteralPath $target -Recurse -Force}
 $report.cleanupVerified=-not(Test-Path -LiteralPath $runRoot) -and -not(Test-Path -LiteralPath $artifact);$report['sourceSHA256']=$sourceSHA;$report.checks=$checks.ToArray();[IO.File]::WriteAllText($reportPath,($report|ConvertTo-Json -Depth 10),[Text.UTF8Encoding]::new($false));Pop-Location
}
if($report.status -ne 'passed' -or -not$report.cleanupVerified){throw 'Original main runtime cleanup evidence incomplete'}
Write-Output 'Original main controlled runtime PASS: missing/error native notice, refusal before actual DB/backup/file-set/instance lock, source SHA unchanged, owned exit1/cleanup'