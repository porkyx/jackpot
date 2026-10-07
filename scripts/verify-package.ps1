param([string]$Candidate='dist/Jackpot-0.1.0-windows-x64/Jackpot.exe')
$ErrorActionPreference='Stop'
$projectRoot=[IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent))
$taskRoot=Join-Path $projectRoot '.task'
$runRoot=Join-Path $taskRoot ('package-run-'+[Guid]::NewGuid().ToString('N'))
$reportPath=Join-Path $taskRoot 'package-e2e-report.json'
$executable=[IO.Path]::GetFullPath((Join-Path $projectRoot $Candidate))
$children=New-Object 'System.Collections.Generic.List[System.Diagnostics.Process]'
$ownedBrowserPids=New-Object 'System.Collections.Generic.HashSet[int]'
$checks=New-Object 'System.Collections.Generic.List[string]'
$report=[ordered]@{status='running';candidateSHA256='';checks=@();native='actual production Windows GUI executable and UI Automation';nodeAvailableToChild=$false;developmentServer=$false;physicalIME=$false;cleanupVerified=$false}
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
function Assert-NoHistoryNavigation {
 $window=Find-OwnedWindow
 if($null -eq $window){throw 'Owned window missing while checking retired navigation'}
 $name=New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::NameProperty,'내역')
 $type=New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::ControlTypeProperty,[System.Windows.Automation.ControlType]::Hyperlink)
 $condition=New-Object System.Windows.Automation.AndCondition($name,$type)
 if($window.FindAll([System.Windows.Automation.TreeScope]::Descendants,$condition).Count -ne 0){throw 'Retired history navigation remains in production UI'}
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
Push-Location $projectRoot
try {
 if(-not(Test-Path -LiteralPath $executable -PathType Leaf)){throw 'Build the Windows portable candidate first'}
 $report.candidateSHA256=(Get-FileHash -LiteralPath $executable -Algorithm SHA256).Hash
 New-Item -ItemType Directory -Path $runRoot|Out-Null
 $active=Start-Candidate
 $input=Wait-Control '디시인사이드 게시글 주소' ([System.Windows.Automation.ControlType]::Edit)
 if(-not $input.Current.IsEnabled){throw 'Actual production bootstrap/draft admission did not enable input'}
 $database=Join-Path $runRoot 'Jackpot/jackpot.sqlite3'
 if(-not(Test-Path -LiteralPath $database -PathType Leaf)){throw 'Production database escaped child AppData'}
 $header=New-Object byte[] 16;$stream=[IO.File]::Open($database,[IO.FileMode]::Open,[IO.FileAccess]::Read,([IO.FileShare]::ReadWrite -bor [IO.FileShare]::Delete));try{$headerRead=$stream.Read($header,0,16)}finally{$stream.Dispose()};if($headerRead -lt 16 -or [Text.Encoding]::ASCII.GetString($header,0,15) -ne 'SQLite format 3'){throw 'Production user database header is invalid'}
 if(Test-Path -LiteralPath (Join-Path (Split-Path $executable -Parent) 'jackpot.sqlite3')){throw 'Candidate wrote database beside executable'}
 $checks.Add('embedded-assets-bootstrap-real-bridge-no-Node-or-dev-server')
 # The portable gate uses native accessibility navigation. Text entry and
 # physical Windows IME remain separate gates with their own failure evidence.
 Assert-NoHistoryNavigation
 $beforeNavigation=Read-DatabaseDigest $database
 $inputValue=$input.GetCurrentPattern([System.Windows.Automation.ValuePattern]::Pattern)
 if($inputValue.Current.IsReadOnly){throw 'Production create input unexpectedly read-only'}
 $originalInput=$inputValue.Current.Value
 Invoke-Control (Wait-Control '만들기' ([System.Windows.Automation.ControlType]::Hyperlink))
 $input=Wait-Control '디시인사이드 게시글 주소' ([System.Windows.Automation.ControlType]::Edit)
 $inputValue=$input.GetCurrentPattern([System.Windows.Automation.ValuePattern]::Pattern)
 if($inputValue.Current.IsReadOnly -or $inputValue.Current.Value -cne $originalInput){throw 'No-history create navigation changed or disabled input'}
 if((Read-DatabaseDigest $database) -cne $beforeNavigation){throw 'No-history create navigation mutated saved database'}
 Assert-NoHistoryNavigation
 $checks.Add('production-no-history-public-navigation-readonly')
 $before=(Read-DatabaseDigest $database)
 $duplicate=Start-Candidate
 if(-not $duplicate.WaitForExit(10000) -or $duplicate.ExitCode -eq 0){throw 'Duplicate production process did not reject same data directory'}
 if((Read-DatabaseDigest $database) -ne $before){throw 'Duplicate launch mutated existing user database'}
 $checks.Add('actual-production-duplicate-launch-no-database-mutation')
 Close-Candidate;$checks.Add('native-window-close-exits-main-and-WebView-processes')
 $active=Start-Candidate
 $input=Wait-Control '디시인사이드 게시글 주소' ([System.Windows.Automation.ControlType]::Edit)
 if(-not $input.Current.IsEnabled){throw 'Production restart did not rebuild session/draft'}
 Assert-NoHistoryNavigation
 Close-Candidate;$checks.Add('production-restart-with-owned-profile-and-database')
 $report.status='passed'
} catch {$report.status='failed';$report['failure']=$_.Exception.Message;$report['line']=$_.InvocationInfo.ScriptLineNumber;throw}
finally {
 foreach($process in $children){$process.Refresh();if(-not $process.HasExited){Stop-Process -Id $process.Id -Force;[void]$process.WaitForExit(5000)}}
 Remember-Browsers
 foreach($id in $ownedBrowserPids){$browser=Get-Process -Id $id -ErrorAction SilentlyContinue;if($browser){try{if(-not $browser.WaitForExit(5000)){$proof=Get-CimInstance Win32_Process -Filter ('ProcessId='+$id);if($proof -and $proof.Name -eq 'msedgewebview2.exe' -and $proof.CommandLine -like ('*'+$runRoot+'*')){Stop-Process -Id $id -Force;[void]$browser.WaitForExit(5000)}}}finally{$browser.Dispose()}}}
 if(Test-Path -LiteralPath $runRoot){
  $target=[IO.Path]::GetFullPath($runRoot);$prefix=[IO.Path]::GetFullPath($taskRoot).TrimEnd([char[]]'\/')+[IO.Path]::DirectorySeparatorChar
  if(-not $target.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase) -or -not [IO.Path]::GetFileName($target).StartsWith('package-run-',[StringComparison]::Ordinal)){throw 'Production package cleanup escaped owned directory'}
  foreach($item in Get-ChildItem -LiteralPath $target -Recurse -Force){if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw 'Production package cleanup found unexpected reparse point'};if(-not $item.PSIsContainer){$stream=[IO.File]::Open($item.FullName,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::None);$stream.Dispose()}}
  Remove-Item -LiteralPath $target -Recurse -Force;$report.cleanupVerified=-not(Test-Path -LiteralPath $target)
 }
 $report.checks=$checks.ToArray();$report['ownedBrowserProcessCount']=$ownedBrowserPids.Count
 [IO.File]::WriteAllText($reportPath,($report|ConvertTo-Json -Depth 8),[Text.UTF8Encoding]::new($false))
 foreach($process in $children){$process.Dispose()};Pop-Location
}