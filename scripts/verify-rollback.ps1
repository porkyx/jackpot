param([switch]$KeepArtifacts,[switch]$RunCompatibleDesktop)
$ErrorActionPreference='Stop'
$commit='bca1e845493f04fab07a21838efdc618bf08f712'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$directory=[IO.Path]::GetFullPath((Join-Path $workspace ('.task/verified-test-rollback-'+[guid]::NewGuid().ToString('N'))))
$prefix=Join-Path $workspace '.task/verified-test-rollback-'
if(-not $directory.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase)){throw 'Rollback directory escaped workspace'}
$null=New-Item -ItemType Directory -Path $directory
$owned=@()
if($RunCompatibleDesktop){
 Add-Type -TypeDefinition @"
using System;
using System.Diagnostics;
using System.Runtime.InteropServices;
public static class RollbackWindowBarrier {
 delegate bool EnumCallback(IntPtr h,IntPtr p);
 delegate void WinEvent(IntPtr hook,uint e,IntPtr h,int obj,int child,uint thread,uint time);
 [StructLayout(LayoutKind.Sequential)] struct Point { public int X,Y; }
 [StructLayout(LayoutKind.Sequential)] struct Message { public IntPtr H; public uint ID; public IntPtr W,L; public uint Time; public Point P; }
 [DllImport("user32.dll")] static extern bool EnumWindows(EnumCallback cb,IntPtr p);
 [DllImport("user32.dll")] static extern bool IsWindowVisible(IntPtr h);
 [DllImport("user32.dll")] static extern int GetWindowTextLength(IntPtr h);
 [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr h,out uint pid);
 [DllImport("user32.dll")] static extern IntPtr SetWinEventHook(uint min,uint max,IntPtr mod,WinEvent cb,uint pid,uint tid,uint flags);
 [DllImport("user32.dll")] static extern bool UnhookWinEvent(IntPtr hook);
 [DllImport("user32.dll")] static extern uint MsgWaitForMultipleObjects(uint count,IntPtr[] handles,bool all,uint timeout,uint mask);
 [DllImport("user32.dll")] static extern bool PeekMessage(out Message m,IntPtr h,uint min,uint max,uint remove);
 [DllImport("user32.dll")] static extern bool TranslateMessage(ref Message m);
 [DllImport("user32.dll")] static extern IntPtr DispatchMessage(ref Message m);
 public static IntPtr Wait(Process process,int milliseconds) {
  IntPtr found=IntPtr.Zero;
  EnumCallback inspect=(h,p)=>{uint pid;GetWindowThreadProcessId(h,out pid);if(pid==(uint)process.Id && IsWindowVisible(h) && GetWindowTextLength(h)>0){found=h;return false;}return true;};
  WinEvent observe=(hook,e,h,obj,child,tid,time)=>{if(h!=IntPtr.Zero && obj==0 && child==0)inspect(h,IntPtr.Zero);};
  IntPtr registration=SetWinEventHook(0x8000,0x8002,IntPtr.Zero,observe,(uint)process.Id,0,0);
  if(registration==IntPtr.Zero)throw new InvalidOperationException("Window observer registration failed");
  var timer=Stopwatch.StartNew();
  try{
   while(timer.ElapsedMilliseconds<milliseconds){
    EnumWindows(inspect,IntPtr.Zero);
    if(found!=IntPtr.Zero)return found;
    process.Refresh();if(process.HasExited)throw new InvalidOperationException("Historical process exited before window creation");
    uint remaining=(uint)Math.Max(1,milliseconds-timer.ElapsedMilliseconds);
    uint result=MsgWaitForMultipleObjects(1,new[]{process.Handle},false,remaining,0x04FF);
    if(result==0)throw new InvalidOperationException("Historical process exited while waiting for window");
    if(result==258)break;
    if(result!=1)throw new InvalidOperationException("Window barrier wait failed");
    Message message;while(PeekMessage(out message,IntPtr.Zero,0,0,1)){TranslateMessage(ref message);DispatchMessage(ref message);}
   }
   throw new TimeoutException("Historical process did not create a visible window");
  }finally{if(!UnhookWinEvent(registration))throw new InvalidOperationException("Window observer cleanup failed");GC.KeepAlive(observe);GC.KeepAlive(inspect);}
 }
}
"@
}
function Native-Argument([string]$Value){
 if($Value -notmatch '[\s"]'){return $Value}
 return '"'+([regex]::Replace($Value,'(\\*)"','$1$1\"') -replace '(\\+)$','$1$1')+'"'
}
function Start-Owned([string]$Executable,[string[]]$Arguments,[string]$WorkingDirectory,[hashtable]$Environment){
 $info=New-Object Diagnostics.ProcessStartInfo
 $info.FileName=$Executable
 $info.Arguments=($Arguments|ForEach-Object{Native-Argument $_}) -join ' '
 $info.WorkingDirectory=$WorkingDirectory
 $info.UseShellExecute=$false
 $info.CreateNoWindow=$true
 $info.WindowStyle=[Diagnostics.ProcessWindowStyle]::Hidden
 $info.RedirectStandardOutput=$true
 $info.RedirectStandardError=$true
 if($Environment){foreach($key in $Environment.Keys){$info.EnvironmentVariables[$key]=[string]$Environment[$key]}}
 $process=New-Object Diagnostics.Process
 $process.StartInfo=$info
 if(-not $process.Start()){throw 'Failed to start owned rollback process'}
 $item=[pscustomobject]@{Process=$process;PID=$process.Id;Stdout=$process.StandardOutput.ReadToEndAsync();Stderr=$process.StandardError.ReadToEndAsync();Executable=$Executable}
 $script:owned+=$item
 return $item
}
function Invoke-Owned([string]$Executable,[string[]]$Arguments,[string]$WorkingDirectory,[hashtable]$Environment,[switch]$ExpectFailure){
 $item=Start-Owned $Executable $Arguments $WorkingDirectory $Environment
 if(-not $item.Process.WaitForExit(60000)){$item.Process.Kill();$item.Process.WaitForExit();throw 'Owned rollback subprocess exceeded 60 seconds'}
 $result=[pscustomobject]@{PID=$item.PID;ExitCode=$item.Process.ExitCode;Stdout=$item.Stdout.Result;Stderr=$item.Stderr.Result}
 if((-not $ExpectFailure -and $result.ExitCode -ne 0) -or ($ExpectFailure -and $result.ExitCode -eq 0)){throw ("Unexpected exit $($result.ExitCode): "+$result.Stdout+$result.Stderr)}
 return $result
}
function File-Signatures([string]$Path){
 $files=@(Get-ChildItem -LiteralPath $Path -File | Sort-Object Name | ForEach-Object{[pscustomobject]@{Name=$_.Name;SHA256=(Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash;Length=$_.Length}})
 return ConvertTo-Json -InputObject $files -Compress
}
function Assert-Database($Report,[int]$Version){
 if($Report.userVersion -ne $Version -or $Report.marker -ne 'rollback 한글 preserved' -or $Report.operationCount -ne 0 -or $Report.sequence -ne 7 -or $Report.integrity -ne 'ok' -or $Report.foreignKeyViolations -ne 0){throw ('Database invariant failed: '+($Report|ConvertTo-Json -Compress))}
}
$probeSource=[IO.File]::ReadAllText((Join-Path $PSScriptRoot 'rollback/probe.go.txt'))
$manifest=[ordered]@{requestedInitialCommit='824b28282b5e4c20bb48166394dd9848e23b0da0';previousSourceCommit=$commit;previousSchema=1;currentSchema=2;compatibleDesktopRequested=[bool]$RunCompatibleDesktop}
try{
 Push-Location $workspace
 try{
  $headBefore=(& git rev-parse HEAD).Trim()
  $archive=Join-Path $directory 'previous-source.zip'
  $result=Invoke-Owned 'git' @('archive','--format=zip','--output',$archive,$commit) $workspace @{}
  $oldSource=Join-Path $directory 'previous-source'
  Expand-Archive -LiteralPath $archive -DestinationPath $oldSource
  $historicalFiles=@(Get-ChildItem -LiteralPath $oldSource -Recurse -File -Force | ForEach-Object{[pscustomobject]@{Path=$_.FullName;Relative=$_.FullName.Substring($oldSource.Length+1);SHA256=(Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash}})
  $manifest.sourceArchiveSHA256=(Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash
  Write-Output ('Rollback exact source: '+$commit+' schema1')
  $oldProbeSource=Join-Path $oldSource 'cmd/rollbackprobe'
  $currentProbeSource=Join-Path $directory 'current-probe'
  $null=New-Item -ItemType Directory -Path $oldProbeSource,$currentProbeSource
  [IO.File]::WriteAllText((Join-Path $oldProbeSource 'main.go'),$probeSource.Replace('@@LABEL@@','historical-storage-probe').Replace('@@COMMIT@@',$commit),(New-Object Text.UTF8Encoding($false)))
  [IO.File]::WriteAllText((Join-Path $currentProbeSource 'main.go'),$probeSource.Replace('@@LABEL@@','current-migration-probe').Replace('@@COMMIT@@',$headBefore+'+working-tree'),(New-Object Text.UTF8Encoding($false)))
  $assetBuilder=Join-Path $directory 'build-previous-assets.mjs'
  Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'rollback/build-assets.mjs') -Destination $assetBuilder
  $result=Invoke-Owned 'node' @($assetBuilder,$workspace,$oldSource) $workspace @{}
  Write-Output $result.Stdout.Trim()
  $oldBinary=Join-Path $directory 'previous-jackpot.exe'
  $oldProbe=Join-Path $directory 'previous-storage-probe.exe'
  $currentProbe=Join-Path $directory 'current-migration-probe.exe'
  $null=Invoke-Owned 'go' @('build','-buildvcs=false','-o',$oldBinary,'.') $oldSource @{}
  $null=Invoke-Owned 'go' @('build','-buildvcs=false','-o',$oldProbe,'./cmd/rollbackprobe') $oldSource @{}
  $currentPath='./.task/'+[IO.Path]::GetFileName($directory)+'/current-probe'
  $null=Invoke-Owned 'go' @('build','-buildvcs=false','-o',$currentProbe,$currentPath) $workspace @{}
  Write-Output 'Original historical main and two actual storage probes built'
  $activeBase=Join-Path $directory 'active 한글'
  $activeDB=Join-Path $activeBase 'Jackpot/jackpot.sqlite3'
  $seed=(Invoke-Owned $oldProbe @('-mode','seed','-path',$activeDB) $directory @{}).Stdout|ConvertFrom-Json
  Assert-Database $seed 1
  $migrated=(Invoke-Owned $currentProbe @('-mode','migrate','-path',$activeDB) $directory @{}).Stdout|ConvertFrom-Json
  Assert-Database $migrated 2
  if(-not $migrated.backup -or -not (Test-Path -LiteralPath $migrated.backup)){throw 'Actual migration did not return backup'}
  $backup=(Invoke-Owned $oldProbe @('-mode','inspect','-path',$migrated.backup) $directory @{}).Stdout|ConvertFrom-Json
  Assert-Database $backup 1
  $futureBefore=File-Signatures (Split-Path $activeDB)
  $local=Join-Path $directory 'isolated-local'
  $temp=Join-Path $directory 'isolated-temp'
  $null=New-Item -ItemType Directory -Path $local,$temp
  $environment=@{APPDATA=$activeBase;LOCALAPPDATA=$local;TEMP=$temp;TMP=$temp}
  $futureRuns=@()
  for($attempt=0;$attempt -lt 3;$attempt++){
   $rejected=Invoke-Owned $oldBinary @() $directory $environment -ExpectFailure
   if($rejected.Stderr -notmatch 'database schema is newer than this application'){throw ('Historical original main failed for wrong reason: '+$rejected.Stderr)}
   if($futureBefore -ne (File-Signatures (Split-Path $activeDB))){throw 'Historical future rejection changed durable bytes/sidecars'}
   $futureRuns+=$rejected
  }
  if(Test-Path -LiteralPath (Join-Path $activeBase ([IO.Path]::GetFileName($oldBinary)))){throw 'Future rejection initialized historical WebView profile'}
  Write-Output 'Original previous main rejects schema2 before WebView; 3 repeats, bytes/sidecars preserved'
  $restoredBase=Join-Path $directory 'restored 한글 복원'
  $restoredDir=Join-Path $restoredBase 'Jackpot'
  $null=New-Item -ItemType Directory -Path $restoredDir
  $restoredDB=Join-Path $restoredDir 'jackpot.sqlite3'
  Copy-Item -LiteralPath $migrated.backup -Destination $restoredDB
  $backupHash=(Get-FileHash -LiteralPath $migrated.backup -Algorithm SHA256).Hash
  if((Get-FileHash -LiteralPath $restoredDB -Algorithm SHA256).Hash -ne $backupHash){throw 'Restored file is not exact actual migration backup'}
  $resumed=(Invoke-Owned $oldProbe @('-mode','resume','-path',$restoredDB) $directory @{}).Stdout|ConvertFrom-Json
  Assert-Database $resumed 1
  if($resumed.backup){throw 'Historical compatible resume unexpectedly migrated'}
  Write-Output 'Actual backup restored and historical schema1 storage binary resumes marker/sequence/integrity/FK'
  $desktopResult=$null
  if($RunCompatibleDesktop){
   $environment.APPDATA=$restoredBase
   $item=Start-Owned $oldBinary @() $directory $environment
   $windowHandle=[RollbackWindowBarrier]::Wait($item.Process,20000)
   $item.Process.Refresh()
   if($item.Process.HasExited -or $windowHandle -eq [IntPtr]::Zero){throw 'Historical compatible original main did not create a window'}
   $children=@(Get-CimInstance Win32_Process -Filter "Name='msedgewebview2.exe'" | Where-Object{$_.CommandLine -and $_.CommandLine.Contains($restoredBase)})
   $desktopResult=[pscustomobject]@{PID=$item.PID;WindowTitle=$item.Process.MainWindowTitle;ChildPIDs=@($children|ForEach-Object{$_.ProcessId});WindowHandle=[string]$windowHandle}
   if(-not $item.Process.CloseMainWindow()){throw 'Failed to request owned historical window close'}
   if(-not $item.Process.WaitForExit(20000)){throw 'Historical original main did not close cleanly'}
   if($item.Process.ExitCode -ne 0){throw ('Historical original main exit failure: '+$item.Stderr.Result)}
   foreach($child in $children){
    $live=Get-CimInstance Win32_Process -Filter ('ProcessId='+$child.ProcessId)
    if($live -and $live.CreationDate -eq $child.CreationDate -and $live.CommandLine.Contains($restoredBase)){
     $process=Get-Process -Id $child.ProcessId -ErrorAction SilentlyContinue
     if($process -and -not $process.WaitForExit(5000)){throw 'Owned historical WebView child remained after clean close'}
    }
   }
   $after=(Invoke-Owned $oldProbe @('-mode','inspect','-path',$restoredDB) $directory @{}).Stdout|ConvertFrom-Json
   Assert-Database $after 1
   Write-Output 'Original historical desktop accepts restored schema1, creates owned window and exits cleanly'
  }
  $currentAfter=(Invoke-Owned $currentProbe @('-mode','inspect','-path',$activeDB) $directory @{}).Stdout|ConvertFrom-Json
  Assert-Database $currentAfter 2
  if($futureBefore -ne (File-Signatures (Split-Path $activeDB))){throw 'Original new database changed after rollback rehearsal'}
  foreach($file in $historicalFiles){if((Get-FileHash -LiteralPath $file.Path -Algorithm SHA256).Hash -ne $file.SHA256){throw ('Historical source modified: '+$file.Relative)}}
  if((& git rev-parse HEAD).Trim() -ne $headBefore){throw 'Shared Git HEAD changed during rollback rehearsal'}
  $manifest.head=$headBefore
  $manifest.originalHistoricalMainSHA256=(Get-FileHash -LiteralPath (Join-Path $oldSource 'main.go') -Algorithm SHA256).Hash
  $manifest.previousBinarySHA256=(Get-FileHash -LiteralPath $oldBinary -Algorithm SHA256).Hash
  $manifest.previousProbeSHA256=(Get-FileHash -LiteralPath $oldProbe -Algorithm SHA256).Hash
  $manifest.currentProbeSHA256=(Get-FileHash -LiteralPath $currentProbe -Algorithm SHA256).Hash
  $manifest.actualBackupSHA256=$backupHash
  $manifest.futureDirectorySignatures=$futureBefore|ConvertFrom-Json
  $manifest.seed=$seed;$manifest.migrated=$migrated;$manifest.backup=$backup;$manifest.resumed=$resumed;$manifest.futureRuns=$futureRuns;$manifest.compatibleDesktop=$desktopResult
  $manifest.historicalFiles=$historicalFiles|ForEach-Object{[pscustomobject]@{Relative=$_.Relative;SHA256=$_.SHA256}}
  $manifest.processes=$owned|ForEach-Object{[pscustomobject]@{PID=$_.PID;Executable=$_.Executable;Exited=$_.Process.HasExited}}
  $manifestPath=Join-Path $directory 'rollback-manifest.json'
  [IO.File]::WriteAllText($manifestPath,($manifest|ConvertTo-Json -Depth 12),(New-Object Text.UTF8Encoding($false)))
  Write-Output ('Rollback rehearsal PASS; manifest='+$manifestPath)
  if(-not $RunCompatibleDesktop){Write-Output 'Compatible original desktop startup remains separate; storage probe is explicitly a boundary harness.'}
 }finally{Pop-Location}
}finally{
 foreach($item in $owned){
  if(-not $item.Process.HasExited){$item.Process.Kill();$item.Process.WaitForExit()}
  $item.Process.Dispose()
 }
 $children=@(Get-CimInstance Win32_Process -Filter "Name='msedgewebview2.exe'" | Where-Object{$_.CommandLine -and $_.CommandLine.Contains($directory)})
 foreach($child in $children){
  $live=Get-CimInstance Win32_Process -Filter ('ProcessId='+$child.ProcessId)
  if($live -and $live.CreationDate -eq $child.CreationDate -and $live.CommandLine.Contains($directory)){Stop-Process -Id $child.ProcessId -Force}
 }
 $remaining=@(Get-CimInstance Win32_Process -Filter "Name='msedgewebview2.exe'" | Where-Object{$_.CommandLine -and $_.CommandLine.Contains($directory)})
 if($remaining.Count){throw 'Owned rollback WebView PID cleanup failed'}
 $resolved=[IO.Path]::GetFullPath($directory)
 if(-not $resolved.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase)){throw 'Rollback cleanup escaped workspace'}
 if(-not $KeepArtifacts -and (Test-Path -LiteralPath $resolved)){Remove-Item -LiteralPath $resolved -Recurse -Force}
 if(-not $KeepArtifacts -and (Test-Path -LiteralPath $resolved)){throw 'Rollback artifact cleanup failed'}
 if(-not $KeepArtifacts){Write-Output 'Rollback own PID and artifact cleanup PASS'}
}