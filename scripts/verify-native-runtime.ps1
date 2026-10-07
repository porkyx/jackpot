param([switch]$SkipBuild)
$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent))
$taskRoot=Join-Path $workspace '.task'
$runRoot=Join-Path $taskRoot ('native-runtime-run-'+[Guid]::NewGuid().ToString('N'))
$executable=Join-Path $taskRoot 'native-runtime-check.exe'
$seed=Join-Path $taskRoot 'native-close-seed.exe'
$reportPath=Join-Path $taskRoot 'native-runtime-report.json'
$owned=New-Object 'System.Collections.Generic.List[System.Diagnostics.Process]'
$checks=New-Object 'System.Collections.Generic.List[string]'
$report=[ordered]@{status='running';checks=@();physicalMissing=$false;controlledMissing=$true;automaticDownloadInstall=$false;browserOpened=$false;databaseUnchanged=$false;cleanupVerified=$false;helperSHA256='';runs=@();native='actual Windows MessageBox with production preflight; controlled missing/error probes and installed official loader'}
Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes
function Assert-OwnedRuntimeDirectory([string]$path){
 $target=[IO.Path]::GetFullPath($path)
 if([IO.Path]::GetDirectoryName($target) -ne $taskRoot -or -not [IO.Path]::GetFileName($target).StartsWith('native-runtime-run-',[StringComparison]::Ordinal) -or $target -ne $runRoot){throw 'Runtime cleanup escaped unique owned directory'}
 if(Test-Path -LiteralPath $target){if(((Get-Item -LiteralPath $target -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw 'Runtime fixture root is a reparse point'};foreach($item in Get-ChildItem -LiteralPath $target -Recurse -Force){if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw 'Runtime fixture contains reparse point'}}}
 return $target
}
function Read-FixtureSHA {
 $result=[ordered]@{}
 foreach($item in Get-ChildItem -LiteralPath $runRoot -Recurse -File){$result[$item.FullName.Substring($runRoot.Length)]=(Get-FileHash -LiteralPath $item.FullName -Algorithm SHA256).Hash}
 return ($result|ConvertTo-Json -Compress)
}
function Start-OwnedRuntime([string]$mode){
 $start=New-Object Diagnostics.ProcessStartInfo
 $start.FileName=$executable;$start.Arguments='-mode '+$mode;$start.WorkingDirectory=$runRoot
 $start.UseShellExecute=$false;$start.CreateNoWindow=$true;$start.WindowStyle=[Diagnostics.ProcessWindowStyle]::Hidden
 if($mode -ne 'installed'){$start.WindowStyle=[Diagnostics.ProcessWindowStyle]::Normal}
 $start.RedirectStandardError=$true;$start.RedirectStandardOutput=$true
 $start.EnvironmentVariables['APPDATA']=$runRoot;$start.EnvironmentVariables['LOCALAPPDATA']=$runRoot
 $start.EnvironmentVariables['WEBVIEW2_BROWSER_EXECUTABLE_FOLDER']=(Join-Path $runRoot 'nonexistent-runtime')
 $p=[Diagnostics.Process]::Start($start);[void]$p.Handle;$owned.Add($p)
 return $p
}
function Wait-OwnedRuntimeDialog($p){
 $deadline=[DateTime]::UtcNow.AddSeconds(15)
 $pidCondition=New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::ProcessIdProperty,$p.Id)
 do {
  $p.Refresh();if($p.HasExited){throw ('Native notice exited early: '+$p.StandardError.ReadToEnd())}
  $windows=[Windows.Automation.AutomationElement]::RootElement.FindAll([Windows.Automation.TreeScope]::Children,$pidCondition)
  foreach($window in $windows){if($window.Current.Name -eq 'Jackpot 실행 안내' -and -not $window.Current.IsOffscreen){return $window}}
  Start-Sleep -Milliseconds 25
 }while([DateTime]::UtcNow -lt $deadline)
 throw 'Owned native runtime notice did not appear'
}
function Invoke-RuntimeOK($dialog){
 $buttonCondition=New-Object Windows.Automation.PropertyCondition([Windows.Automation.AutomationElement]::ControlTypeProperty,[Windows.Automation.ControlType]::Button)
 $button=$dialog.FindFirst([Windows.Automation.TreeScope]::Children,$buttonCondition)
 if($null -eq $button -or -not $button.Current.IsEnabled){throw 'Native runtime OK button missing'}
 $pattern=$button.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern);$pattern.Invoke()
}
Push-Location $workspace
try{
 if(-not $SkipBuild){& go build -o $executable ./cmd/nativeruntimecheck;if($LASTEXITCODE -ne 0){throw 'Runtime host build failed'}}
 if(-not(Test-Path -LiteralPath $executable -PathType Leaf) -or -not(Test-Path -LiteralPath $seed -PathType Leaf)){throw 'Build native runtime check and existing native close seed first'}
 $report.helperSHA256=(Get-FileHash -LiteralPath $executable -Algorithm SHA256).Hash
 Assert-OwnedRuntimeDirectory $runRoot|Out-Null
 New-Item -ItemType Directory -Path $runRoot|Out-Null
 $seedOutput=& $seed -data-root $runRoot
 if($LASTEXITCODE -ne 0){throw 'Actual isolated SQLite fixture failed'}
 $report['fixture']=($seedOutput|ConvertFrom-Json)
 $fixtureSHA=Read-FixtureSHA
 $report['beforeSHA']=$fixtureSHA
 $parentOverride=$env:WEBVIEW2_BROWSER_EXECUTABLE_FOLDER
 foreach($mode in @('installed','missing','error')){
  $p=Start-OwnedRuntime $mode
  $proof=Get-CimInstance Win32_Process -Filter ('ProcessId='+$p.Id)
  $p.Refresh()
  if($null -ne $proof){
   if($proof.ExecutablePath -ne $executable -or $proof.CommandLine -notlike ('* -mode '+$mode)){throw 'Owned native helper identity mismatch'}
   $creation=$proof.CreationDate.ToUniversalTime().ToString('o')
  }elseif($mode -eq 'installed' -and $p.HasExited){
   # The fast successful local lookup can exit before CIM observes it. The
   # retained Process handle belongs to our exact ProcessStartInfo child.
   $creation='already-exited-owned-child-handle'
  }else{throw 'Owned native helper missing before controlled notice'}
  $dialogText=''
  if($mode -ne 'installed'){
   $dialog=Wait-OwnedRuntimeDialog $p
   $nodes=$dialog.FindAll([Windows.Automation.TreeScope]::Descendants,[Windows.Automation.Condition]::TrueCondition)
   $dialogText=($nodes|ForEach-Object{$_.Current.Name}) -join "`n"
   $expected=if($mode -eq 'missing'){'Jackpot을 실행하려면 Microsoft Edge WebView2 Runtime이 필요합니다.'}else{'Microsoft Edge WebView2 Runtime을 확인하지 못해 Jackpot을 시작할 수 없습니다.'}
   if(-not $dialogText.Contains($expected) -or -not $dialogText.Contains('https://developer.microsoft.com/en-us/microsoft-edge/webview2/') -or -not $dialogText.Contains('Jackpot은 자동으로 다운로드하거나 설치하지 않습니다.') -or $dialogText.Contains('private registry')){throw 'Native runtime notice omitted safe Korean policy or exposed raw detector detail'}
   $report['lastDialogControls']=@($nodes|ForEach-Object{@{name=$_.Current.Name;id=$_.Current.AutomationId;type=$_.Current.ControlType.ProgrammaticName}})
   $buttons=@($nodes|Where-Object{$_.Current.ControlType -eq [Windows.Automation.ControlType]::Button -and $_.Current.AutomationId -ne 'Close'})
   if($buttons.Count -ne 1 -or $buttons[0].Current.Name -notin @('확인','OK')){throw 'Native runtime notice added an installation/browser action'}
   Invoke-RuntimeOK $dialog
  }
  if(-not $p.WaitForExit(15000) -or $p.ExitCode -ne 0){throw 'Native runtime helper did not finish its expected decision'}
  $result=$p.StandardOutput.ReadToEnd()|ConvertFrom-Json
  if([string]::IsNullOrEmpty($result.installedVersion) -or $result.physicalMissing -ne $false -or $result.networkInstall -ne $false){throw 'Incorrect installed/controlled boundary claim'}
  $expectedDecision=if($mode -eq 'error'){'unavailable'}else{$mode}
  if($result.decision -ne $expectedDecision -or $result.allowedDataOpen -ne ($mode -eq 'installed') -or $result.nativeNotices -ne [int]($mode -ne 'installed')){throw 'Native preflight failed the expected decision'}
  if((Read-FixtureSHA) -ne $fixtureSHA){throw 'Native preflight changed real database or sidecar bytes'}
  $report.runs+=@{pid=$p.Id;creationDate=$creation;mode=$mode;decision=$result;dialogText=$dialogText;exitCode=$p.ExitCode}
  $checks.Add('actual-native-'+$mode+'-preflight-and-durable-byte-preservation')
 }
 if($env:WEBVIEW2_BROWSER_EXECUTABLE_FOLDER -ne $parentOverride){throw 'Native helper changed parent runtime override'}
 if(@(Get-CimInstance Win32_Process -Filter "Name='msedgewebview2.exe'"|Where-Object{$_.CommandLine -like ('*'+$runRoot+'*')}).Count -ne 0){throw 'Runtime preflight unexpectedly started an owned browser'}
 $checks.Add('child-invalid-runtime-override-does-not-simulate-physical-missing-parent-unchanged-browser0')
 $report.databaseUnchanged=$true;$report.status='passed'
}catch{$report.status='failed';$report['failure']=$_.Exception.Message;$report['line']=$_.InvocationInfo.ScriptLineNumber;throw}
finally{
 foreach($p in $owned){$p.Refresh();if(-not $p.HasExited){$proof=Get-CimInstance Win32_Process -Filter ('ProcessId='+$p.Id);if($null -eq $proof -or $proof.ExecutablePath -ne $executable -or $proof.CommandLine -notlike '* -mode *'){throw 'Runtime cleanup cannot prove child ownership'};$p.Kill();if(-not $p.WaitForExit(5000)){throw 'Owned runtime child failed cleanup'}};$p.Dispose()}
 if(Test-Path -LiteralPath $runRoot){$target=Assert-OwnedRuntimeDirectory $runRoot;foreach($item in Get-ChildItem -LiteralPath $target -Recurse -File){$stream=[IO.File]::Open($item.FullName,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::None);$stream.Dispose()};$target=Assert-OwnedRuntimeDirectory $runRoot;Remove-Item -LiteralPath $target -Recurse -Force;$report.cleanupVerified=-not(Test-Path -LiteralPath $target)}
 $report.checks=$checks.ToArray();[IO.File]::WriteAllText($reportPath,($report|ConvertTo-Json -Depth 12),[Text.UTF8Encoding]::new($false));Pop-Location
}
if($report.status -ne 'passed' -or -not $report.cleanupVerified){throw 'Native runtime preflight cleanup gate failed'}
Write-Output 'Native runtime preflight PASS: installed official loader, controlled missing/error Korean OK notice, real SQLite unchanged, download/install/browser0, owned child/profile cleanup'