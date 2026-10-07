param([switch]$SkipBuild,[switch]$KeepArtifacts)
$ErrorActionPreference='Stop'
$projectRoot=[IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent))
$taskRoot=Join-Path $projectRoot '.task'
$runRoot=Join-Path $taskRoot ('native-run-export-'+[Guid]::NewGuid().ToString('N'))
$reportPath=Join-Path $taskRoot 'native-export-report.json'
$driverReport=Join-Path $taskRoot 'native-export-driver.json'
$executable=Join-Path $taskRoot 'nativeexportcheck.exe'
$child=$null
$ownedBrowserPids=New-Object 'System.Collections.Generic.HashSet[int]'
$trace=New-Object 'System.Collections.Generic.List[object]'
$cleanupVerified=$false
Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes
function Find-OwnedWindows([int]$ownerPid) {
 $condition=New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::ProcessIdProperty,$ownerPid)
 return [System.Windows.Automation.AutomationElement]::RootElement.FindAll([System.Windows.Automation.TreeScope]::Children,$condition)
}
function Find-ById($element,[string]$id) {
 $condition=New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::AutomationIdProperty,$id)
 return $element.FindFirst([System.Windows.Automation.TreeScope]::Descendants,$condition)
}
function Find-ButtonId($element,[string]$id) {
 $a=New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::AutomationIdProperty,$id)
 $b=New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::ControlTypeProperty,[System.Windows.Automation.ControlType]::Button)
 $condition=New-Object System.Windows.Automation.AndCondition($a,$b)
 return $element.FindFirst([System.Windows.Automation.TreeScope]::Descendants,$condition)
}
function Invoke-Control($element) {
 if($null -eq $element){throw 'Owned dialog control was not found'}
 $pattern=$element.GetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern)
 $pattern.Invoke()
}
function Remember-Browsers {
 foreach($process in Get-CimInstance Win32_Process -Filter "Name='msedgewebview2.exe'") {
  if($process.CommandLine -like ('*'+$runRoot+'*')){[void]$ownedBrowserPids.Add([int]$process.ProcessId)}
 }
}
function Wait-Dialog([string]$phase) {
 $deadline=[DateTime]::UtcNow.AddSeconds(20)
 do {
  $child.Refresh();if($child.HasExited){throw ('Native export exited before '+$phase)}
  Remember-Browsers
  $phaseFile=Join-Path $runRoot 'phase'
  if(Test-Path -LiteralPath $phaseFile) {
   $current=[IO.File]::ReadAllText($phaseFile)
   if($current -eq $phase) {
    foreach($window in Find-OwnedWindows $child.Id) {
     if($window.Current.ClassName -eq '#32770' -and $null -ne (Find-ById $window 'FileNameControlHost')){return $window}
    }
   }
  }
  Start-Sleep -Milliseconds 50
 }while([DateTime]::UtcNow -lt $deadline)
 foreach($window in Find-OwnedWindows $child.Id) {
  $controls=$window.FindAll([System.Windows.Automation.TreeScope]::Descendants,[System.Windows.Automation.Condition]::TrueCondition)
  $nodes=@();foreach($control in $controls){$nodes+=@{id=$control.Current.AutomationId;type=$control.Current.ControlType.ProgrammaticName;pid=$control.Current.ProcessId}}
  $trace.Add(@{phase=$phase;ownedWindow=$window.Current.Name;controls=$nodes})
 }
 throw ('Timed out waiting for owned SaveFileDialog: '+$phase)
}
function Confirm-Overwrite {
 $deadline=[DateTime]::UtcNow.AddSeconds(5)
 do {
  foreach($window in Find-OwnedWindows $child.Id) {
   $yes=Find-ButtonId $window '6'
   if($null -eq $yes){$yes=Find-ButtonId $window 'CommandButton_6'}
   if($null -eq $yes) {
    foreach($caption in @('예(Y)','예(&Y)','예','Yes','Yes (Y)')) {
     $a=New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::NameProperty,$caption)
     $b=New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::ControlTypeProperty,[System.Windows.Automation.ControlType]::Button)
     $condition=New-Object System.Windows.Automation.AndCondition($a,$b)
     $yes=$window.FindFirst([System.Windows.Automation.TreeScope]::Descendants,$condition)
     if($null -ne $yes){break}
    }
   }
   if($null -ne $yes -and $yes.Current.ControlType -eq [System.Windows.Automation.ControlType]::Button) {
    $confirmationId=$yes.Current.AutomationId;$confirmationName=$yes.Current.Name
    Invoke-Control $yes
    $trace.Add(@{kind='overwrite-confirm';pid=$child.Id;control=$confirmationId;name=$confirmationName})
    return
   }
  }
  $child.Refresh();if($child.HasExited){return}
  Start-Sleep -Milliseconds 50
 }while([DateTime]::UtcNow -lt $deadline)
 throw 'Owned overwrite confirmation was not found'
}
Push-Location $projectRoot
try {
 if(-not $SkipBuild) {
  & go build -o $executable ./cmd/nativeexportcheck
  if($LASTEXITCODE -ne 0){throw 'Native export harness failed to build'}
 }
 New-Item -ItemType Directory -Path $runRoot | Out-Null
 if(Test-Path -LiteralPath $reportPath){Remove-Item -LiteralPath $reportPath}
 $stdout=Join-Path $taskRoot ([IO.Path]::GetFileName($runRoot)+'.stdout.log');$stderr=Join-Path $taskRoot ([IO.Path]::GetFileName($runRoot)+'.stderr.log')
 $arguments=@('-work-dir',('"'+$runRoot+'"'),'-out',('"'+$reportPath+'"'))
 $child=Start-Process -FilePath $executable -ArgumentList $arguments -WorkingDirectory $projectRoot -WindowStyle Hidden -PassThru -RedirectStandardOutput $stdout -RedirectStandardError $stderr
 $ownedProcessHandle=$child.Handle
 $dialog=Wait-Dialog 'cancel'
 Invoke-Control (Find-ButtonId $dialog '2')
 $trace.Add(@{phase='cancel';pid=$child.Id;control='2';native=$true})
 $destination=Join-Path $runRoot '한글 결과 공백.png'
 foreach($phase in @('save','replace-blocked','replace')) {
  $dialog=Wait-Dialog $phase
  $hostControl=Find-ById $dialog 'FileNameControlHost'
  $condition=New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::ControlTypeProperty,[System.Windows.Automation.ControlType]::Edit)
  $edit=$hostControl.FindFirst([System.Windows.Automation.TreeScope]::Descendants,$condition)
  if($null -eq $edit){throw 'Owned filename edit field was not found'}
  $value=$edit.GetCurrentPattern([System.Windows.Automation.ValuePattern]::Pattern)
  $value.SetValue($destination)
  Invoke-Control (Find-ButtonId $dialog '1')
  $trace.Add(@{phase=$phase;pid=$child.Id;filenameValuePattern=$true;saveInvokePattern=$true})
  if($phase -ne 'save'){Confirm-Overwrite}
 }
 if(-not $child.WaitForExit(15000)){throw 'Native export process did not exit after the final save'}
 $trace.Add(@{kind='process-exit';exitCode=$child.ExitCode})
 if($null -eq $child.ExitCode -or $child.ExitCode -ne 0){throw ('Native export verification failed; exitCode='+$child.ExitCode)}
 $report=[IO.File]::ReadAllText($reportPath)|ConvertFrom-Json
 if(-not $report.cancelled -or -not $report.saved -or -not $report.failedReplacePreserved -or -not $report.replaced -or -not $report.stageFilesClean -or $report.width -ne 4 -or $report.height -ne 2 -or $report.failure -ne ''){throw 'Native export report invariant failed'}
 Copy-Item -LiteralPath $destination -Destination (Join-Path $taskRoot 'native-export-verified.png')
 Write-Output ([IO.File]::ReadAllText($reportPath))
} catch {
 $trace.Add(@{failure=$_.Exception.Message;line=$_.InvocationInfo.ScriptLineNumber})
 throw
} finally {
 if($null -ne $child) {
  $child.Refresh()
  if(-not $child.HasExited){Stop-Process -Id $child.Id -Force;$child.WaitForExit(5000)|Out-Null}
 }
 foreach($browserPid in $ownedBrowserPids) {
  $browser=Get-Process -Id $browserPid -ErrorAction SilentlyContinue
  if($null -ne $browser -and -not $browser.WaitForExit(5000)){
   $proof=Get-CimInstance Win32_Process -Filter ("ProcessId="+$browserPid)
   if($null -ne $proof -and $proof.Name -eq 'msedgewebview2.exe' -and $proof.CommandLine -like ('*'+$runRoot+'*')){Stop-Process -Id $browserPid -Force;$browser.WaitForExit(5000)|Out-Null}
  }
 }
 if(Test-Path -LiteralPath $runRoot) {
  $target=[IO.Path]::GetFullPath($runRoot);$prefix=[IO.Path]::GetFullPath($taskRoot).TrimEnd([char[]]'\/')+[IO.Path]::DirectorySeparatorChar
  if(-not $target.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase)-or -not [IO.Path]::GetFileName($target).StartsWith('native-run-export-',[StringComparison]::Ordinal)){throw 'Native export cleanup target escaped its owned directory'}
  # Exclusive opens prove that no WebView profile lock remains before deletion.
  foreach($file in Get-ChildItem -LiteralPath $target -File -Recurse) {
   $stream=[IO.File]::Open($file.FullName,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::None);$stream.Dispose()
  }
  if(-not $KeepArtifacts){Remove-Item -LiteralPath $target -Recurse -Force;$cleanupVerified=-not(Test-Path -LiteralPath $target)}
 }
 @{ownedPid=if($null -eq $child){$null}else{$child.Id};ownedBrowserPids=($ownedBrowserPids|ForEach-Object{$_});cleanupVerified=$cleanupVerified;physicalInput=$false;clipboardTouched=$false;trace=$trace.ToArray()}|ConvertTo-Json -Depth 12|Set-Content -LiteralPath $driverReport -Encoding UTF8
 if($null -ne $child){$child.Dispose()}
 Pop-Location
}
