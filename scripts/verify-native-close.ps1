param([string]$Candidate='.task/jackpot-close-check.exe')
$ErrorActionPreference='Stop'
$projectRoot=[IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent))
$taskRoot=Join-Path $projectRoot '.task'
$runRoot=Join-Path $taskRoot ('native-close-run-'+[Guid]::NewGuid().ToString('N'))
$reportPath=Join-Path $taskRoot 'native-close-report.json'
$executable=[IO.Path]::GetFullPath((Join-Path $projectRoot $Candidate))
$children=New-Object 'System.Collections.Generic.List[System.Diagnostics.Process]'
$ownedBrowserPids=New-Object 'System.Collections.Generic.HashSet[int]'
$checks=New-Object 'System.Collections.Generic.List[string]'
$report=[ordered]@{status='running';candidateSHA256='';checks=@();native='actual production main close hook and Windows MessageBox with isolated SQLite reservation';nodeAvailableToChild=$false;developmentServer=$false;physicalIME=$false;cleanupVerified=$false}
$active=$null
Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes
function Remember-Browsers {
 foreach($process in Get-CimInstance Win32_Process -Filter "Name='msedgewebview2.exe'") {
  if($process.CommandLine -like ('*'+$runRoot+'*')){[void]$ownedBrowserPids.Add([int]$process.ProcessId)}
 }
}
function Start-Candidate {
 $start=New-Object System.Diagnostics.ProcessStartInfo
 $start.FileName=$executable;$start.WorkingDirectory=$runRoot;$start.UseShellExecute=$false
 $start.CreateNoWindow=$true;$start.WindowStyle=[Diagnostics.ProcessWindowStyle]::Hidden
 $start.RedirectStandardError=$true;$start.RedirectStandardOutput=$true
 # Go UserConfigDir and pinned WebView2 default profile both use child AppData.
 $start.EnvironmentVariables['APPDATA']=$runRoot
 $start.EnvironmentVariables['LOCALAPPDATA']=$runRoot
 $start.EnvironmentVariables['PATH']=(Join-Path $env:SystemRoot 'System32')+';'+$env:SystemRoot
 $process=[Diagnostics.Process]::Start($start);$children.Add($process);[void]$process.Handle
 return $process
}
function Find-OwnedWindow {
 $condition=New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::ProcessIdProperty,$active.Id)
 $windows=[System.Windows.Automation.AutomationElement]::RootElement.FindAll([System.Windows.Automation.TreeScope]::Children,$condition)
 foreach($window in $windows){if($window.Current.Name -eq 'Jackpot'){return $window}}
 return $null
}
function Wait-Control([string]$name,$type=$null) {
 $deadline=[DateTime]::UtcNow.AddSeconds(30)
 do {
  $active.Refresh();if($active.HasExited){throw ('Production candidate exited before '+$name+': '+$active.StandardError.ReadToEnd())}
  Remember-Browsers;$window=Find-OwnedWindow
  if($null -ne $window) {
   $a=New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::NameProperty,$name)
   $condition=$a
   if($null -ne $type){$b=New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::ControlTypeProperty,$type);$condition=New-Object System.Windows.Automation.AndCondition($a,$b)}
   $control=$window.FindFirst([System.Windows.Automation.TreeScope]::Descendants,$condition)
   if($null -ne $control -and -not $control.Current.IsOffscreen -and $control.Current.IsEnabled){return $control}
  }
  Start-Sleep -Milliseconds 50
 }while([DateTime]::UtcNow -lt $deadline)
 if($null -ne $window){$nodes=$window.FindAll([System.Windows.Automation.TreeScope]::Descendants,[System.Windows.Automation.Condition]::TrueCondition);$report['controls']=@($nodes|ForEach-Object{@{name=$_.Current.Name;type=$_.Current.ControlType.ProgrammaticName}})}
 throw ('Production accessibility control did not become available: '+$name)
}
function Read-DatabaseDigest([string]$path) {
 $stream=[IO.File]::Open($path,[IO.FileMode]::Open,[IO.FileAccess]::Read,([IO.FileShare]::ReadWrite -bor [IO.FileShare]::Delete))
 $sha=[Security.Cryptography.SHA256]::Create()
 try{return ([BitConverter]::ToString($sha.ComputeHash($stream))).Replace('-','')}finally{$sha.Dispose();$stream.Dispose()}
}
function Invoke-Control($control){$pattern=$control.GetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern);$pattern.Invoke()}
function Close-Candidate {
 Remember-Browsers;$window=Find-OwnedWindow
 if($null -eq $window){throw 'Production candidate window missing before normal close'}
 $pattern=$window.GetCurrentPattern([System.Windows.Automation.WindowPattern]::Pattern);$pattern.Close()
 if(-not $active.WaitForExit(15000)){throw 'Production candidate did not exit after native window close'}
 if($active.ExitCode -ne 0){throw ('Production normal close failed: '+$active.StandardError.ReadToEnd())}
 foreach($id in $ownedBrowserPids){$browser=Get-Process -Id $id -ErrorAction SilentlyContinue;if($browser){try{if(-not $browser.WaitForExit(10000)){throw 'Owned WebView process remained after normal close'}}finally{$browser.Dispose()}}}
}
Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;
public static class JackpotOwnedClose {
 [DllImport("user32.dll")] private static extern uint GetWindowThreadProcessId(IntPtr hwnd,out uint pid);
 [DllImport("user32.dll",SetLastError=true)] private static extern bool PostMessage(IntPtr hwnd,uint message,IntPtr wparam,IntPtr lparam);
 public static void Request(int handle,int expectedPid){uint pid;GetWindowThreadProcessId(new IntPtr(handle),out pid);if(pid!=(uint)expectedPid||pid==0)throw new InvalidOperationException("Close request is not for the owned process");if(!PostMessage(new IntPtr(handle),0x0010,IntPtr.Zero,IntPtr.Zero))throw new InvalidOperationException("Owned WM_CLOSE failed");}
}
"@
function Request-OwnedClose {
 $window=Find-OwnedWindow
 if(-not $window){throw 'Original production window missing before close request'}
 [JackpotOwnedClose]::Request($window.Current.NativeWindowHandle,$active.Id)
}
function Wait-CloseQuestion {
 $deadline=[DateTime]::UtcNow.AddSeconds(15)
 $a=New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::ProcessIdProperty,$active.Id)
 $b=New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::NameProperty,'Jackpot 종료')
 $condition=New-Object System.Windows.Automation.AndCondition($a,$b)
 do {
  $active.Refresh();if($active.HasExited){throw 'Product closed before required reservation notice'}
  Remember-Browsers
  $window=Find-OwnedWindow
  $question=if($window){$window.FindFirst([System.Windows.Automation.TreeScope]::Descendants,$condition)}else{$null}
  if(-not $question){$question=[System.Windows.Automation.AutomationElement]::RootElement.FindFirst([System.Windows.Automation.TreeScope]::Children,$condition)}
  if($question -and -not $question.Current.IsOffscreen){return $question}
  Start-Sleep -Milliseconds 50
 }while([DateTime]::UtcNow -lt $deadline)
 $window=Find-OwnedWindow
 if($window){$report['ownedControls']=@($window.FindAll([System.Windows.Automation.TreeScope]::Descendants,[System.Windows.Automation.Condition]::TrueCondition)|ForEach-Object{@{name=$_.Current.Name;type=$_.Current.ControlType.ProgrammaticName;automationId=$_.Current.AutomationId}})}
 throw 'Actual scheduled native close question did not appear'
}
function Invoke-NativeChoice($question,[string]$id) {
 $condition=New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::AutomationIdProperty,$id)
 $button=$question.FindFirst([System.Windows.Automation.TreeScope]::Descendants,$condition)
 if(-not $button){throw ('Native MessageBox choice missing: '+$id)}
 Invoke-Control $button
}
Push-Location $projectRoot
try {
 if(-not(Test-Path -LiteralPath $executable -PathType Leaf)){throw 'Build the production main executable first'}
 $seed=Join-Path $taskRoot 'native-close-seed.exe'
 if(-not(Test-Path -LiteralPath $seed -PathType Leaf)){throw 'Build cmd/nativecloseseed first'}
 $report.candidateSHA256=(Get-FileHash -LiteralPath $executable -Algorithm SHA256).Hash
 New-Item -ItemType Directory -Path $runRoot|Out-Null
 $seedJSON=& $seed -data-root $runRoot
 if($LASTEXITCODE -ne 0){throw 'Actual SQLite reservation fixture failed'}
 $seedState=$seedJSON|ConvertFrom-Json
 if($seedState.scheduled -ne 1){throw 'Fixture has no scheduled reservation'}
 $report['fixture']=$seedState
 $checks.Add('actual-RoundLifecycle-SQLite-reservation-seeded')
 $active=Start-Candidate
 Wait-Control '디시인사이드 게시글 주소' ([System.Windows.Automation.ControlType]::Edit)|Out-Null
 $database=Join-Path $runRoot 'Jackpot/jackpot.sqlite3'
 $before=Read-DatabaseDigest $database
 Request-OwnedClose
 $question=Wait-CloseQuestion
 $texts=$question.FindAll([System.Windows.Automation.TreeScope]::Descendants,[System.Windows.Automation.Condition]::TrueCondition)
 $policy=($texts|ForEach-Object{$_.Current.Name}) -join "`n"
 if(-not $policy.Contains('예정 예약이 1건 있습니다.') -or -not $policy.Contains('앱을 종료하면 예약은 실행되지 않습니다.') -or -not $policy.Contains('다음 시작 시 만료 예약을 한 번씩 지연 실행합니다.')){throw 'Actual native close dialog omitted count or recovery policy'}
 $report['dialogPolicy']=$policy
 $checks.Add('actual-Windows-close-dialog-shows-authoritative-one-reservation-and-delay-policy')
 Invoke-NativeChoice $question '7'
 $deadline=[DateTime]::UtcNow.AddSeconds(10)
 do {$window=Find-OwnedWindow;if($window -and $window.Current.IsEnabled){break};Start-Sleep -Milliseconds 50}while([DateTime]::UtcNow -lt $deadline)
 $active.Refresh();if($active.HasExited -or -not $window -or -not $window.Current.IsEnabled){throw 'No choice did not retain the live application'}
 if((Read-DatabaseDigest $database) -ne $before){throw 'Native cancel changed durable database bytes'}
 $checks.Add('native-No-retains-application-and-reservation-database-bytes')
 Request-OwnedClose
 $question=Wait-CloseQuestion
 Remember-Browsers
 Invoke-NativeChoice $question '6'
 if(-not $active.WaitForExit(15000) -or $active.ExitCode -ne 0){throw 'Native Yes did not close original production main cleanly'}
 foreach($id in $ownedBrowserPids){$browser=Get-Process -Id $id -ErrorAction SilentlyContinue;if($browser){try{if(-not $browser.WaitForExit(10000)){throw 'Native close left owned WebView process'}}finally{$browser.Dispose()}}}
 $checks.Add('native-Yes-exits-original-main-and-all-owned-WebView-processes')
 $report.status='passed'} catch {$report.status='failed';$report['failure']=$_.Exception.Message;$report['line']=$_.InvocationInfo.ScriptLineNumber;throw}
finally {
 foreach($process in $children){$process.Refresh();if(-not $process.HasExited){Stop-Process -Id $process.Id -Force;[void]$process.WaitForExit(5000)}}
 Remember-Browsers
 foreach($id in $ownedBrowserPids){$browser=Get-Process -Id $id -ErrorAction SilentlyContinue;if($browser){try{if(-not $browser.WaitForExit(5000)){$proof=Get-CimInstance Win32_Process -Filter ('ProcessId='+$id);if($proof -and $proof.Name -eq 'msedgewebview2.exe' -and $proof.CommandLine -like ('*'+$runRoot+'*')){Stop-Process -Id $id -Force;[void]$browser.WaitForExit(5000)}}}finally{$browser.Dispose()}}}
 if(Test-Path -LiteralPath $runRoot){
  $target=[IO.Path]::GetFullPath($runRoot);$prefix=[IO.Path]::GetFullPath($taskRoot).TrimEnd([char[]]'\/')+[IO.Path]::DirectorySeparatorChar
  if(-not $target.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase) -or -not [IO.Path]::GetFileName($target).StartsWith('native-close-run-',[StringComparison]::Ordinal)){throw 'Production package cleanup escaped owned directory'}
  foreach($item in Get-ChildItem -LiteralPath $target -Recurse -Force){if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw 'Production package cleanup found unexpected reparse point'};if(-not $item.PSIsContainer){$stream=[IO.File]::Open($item.FullName,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::None);$stream.Dispose()}}
  Remove-Item -LiteralPath $target -Recurse -Force;$report.cleanupVerified=-not(Test-Path -LiteralPath $target)
 }
 $report.checks=$checks.ToArray();$report['ownedBrowserProcessCount']=$ownedBrowserPids.Count
 [IO.File]::WriteAllText($reportPath,($report|ConvertTo-Json -Depth 8),[Text.UTF8Encoding]::new($false))
 foreach($process in $children){$process.Dispose()};Pop-Location
}