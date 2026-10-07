$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent));$taskRoot=Join-Path $workspace '.task'
$runRoot=Join-Path $taskRoot ('native-runtime-run-guard-'+[Guid]::NewGuid().ToString('N'))
$path=Join-Path $PSScriptRoot 'verify-native-runtime.ps1'
$bytes=[IO.File]::ReadAllBytes($path)
if($bytes.Length -lt 3 -or $bytes[0] -ne 0xEF -or $bytes[1] -ne 0xBB -or $bytes[2] -ne 0xBF){throw 'PS5.1 Korean runtime selectors require explicit UTF8 BOM'}
$tokens=$null;$errors=$null;$ast=[Management.Automation.Language.Parser]::ParseFile($path,[ref]$tokens,[ref]$errors)
if($errors.Count -ne 0){throw 'Native runtime verifier parse error'}
$function=$ast.FindAll({param($node)$node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Assert-OwnedRuntimeDirectory'},$true)
if($function.Count -ne 1){throw 'Runtime ownership guard missing or duplicate'}
Invoke-Expression $function[0].Extent.Text
function Assert-Rejected([string]$candidate){$caught=$false;try{Assert-OwnedRuntimeDirectory $candidate|Out-Null}catch{$caught=$true};if(-not$caught){throw ('Runtime ownership guard accepted foreign path: '+$candidate)}}
try{
 if((Assert-OwnedRuntimeDirectory $runRoot) -ne $runRoot){throw 'Runtime guard rejected its exact owned path'}
 foreach($candidate in @('',$workspace,$taskRoot,(Join-Path $taskRoot 'wrong-prefix'),(Join-Path $taskRoot 'native-runtime-run-other'),(Join-Path $runRoot 'nested'))){Assert-Rejected $candidate}
 New-Item -ItemType Directory -Path $runRoot|Out-Null
 $fixture=Join-Path $runRoot 'read-only.txt';[IO.File]::WriteAllText($fixture,'runtime guard leaves original bytes',[Text.UTF8Encoding]::new($false));$before=(Get-FileHash -LiteralPath $fixture -Algorithm SHA256).Hash
 [IO.File]::SetAttributes($fixture,[IO.FileAttributes]::ReadOnly)
 Assert-OwnedRuntimeDirectory $runRoot|Out-Null
 if((Get-FileHash -LiteralPath $fixture -Algorithm SHA256).Hash -ne $before -or ([IO.File]::GetAttributes($fixture) -band [IO.FileAttributes]::ReadOnly) -eq 0){throw 'Runtime ownership read guard changed fixture'}
 [IO.File]::SetAttributes($fixture,[IO.FileAttributes]::Normal)
 $junctionTarget=Join-Path $runRoot 'junction-target';New-Item -ItemType Directory -Path $junctionTarget|Out-Null
 $marker=Join-Path $junctionTarget 'marker.txt';[IO.File]::WriteAllText($marker,'owned junction target stays intact')
 $junction=Join-Path $runRoot 'junction';New-Item -ItemType Junction -Path $junction -Value $junctionTarget|Out-Null
 try{Assert-Rejected $runRoot}finally{
  $link=[IO.Path]::GetFullPath($junction)
  if([IO.Path]::GetDirectoryName($link) -ne $runRoot -or [IO.Path]::GetFileName($link) -ne 'junction' -or ([IO.File]::GetAttributes($link) -band [IO.FileAttributes]::ReparsePoint) -eq 0){throw 'Guard junction cleanup escaped its exact owned link'}
  [IO.Directory]::Delete($link)
 }
 if([IO.File]::ReadAllText($marker) -ne 'owned junction target stays intact'){throw 'Guard junction changed its owned target'}
 $originalRun=$runRoot;$rootLink=Join-Path $taskRoot ('native-runtime-run-guard-rootlink-'+[Guid]::NewGuid().ToString('N'))
 New-Item -ItemType Junction -Path $rootLink -Value $originalRun|Out-Null
 try{$runRoot=$rootLink;Assert-Rejected $runRoot}finally{
  $link=[IO.Path]::GetFullPath($rootLink)
  if([IO.Path]::GetDirectoryName($link) -ne $taskRoot -or -not [IO.Path]::GetFileName($link).StartsWith('native-runtime-run-guard-rootlink-',[StringComparison]::Ordinal) -or ([IO.File]::GetAttributes($link) -band [IO.FileAttributes]::ReparsePoint) -eq 0){throw 'Guard root junction cleanup escaped owned link'}
  [IO.Directory]::Delete($link);$runRoot=$originalRun
 }
 Write-Output 'Native runtime ownership guards PASS: exact path, six rejected boundaries, PS5.1 UTF8 BOM, read-only bytes and attributes unchanged'
}finally{
 if(Test-Path -LiteralPath $runRoot){$target=[IO.Path]::GetFullPath($runRoot);if([IO.Path]::GetDirectoryName($target) -ne $taskRoot -or -not [IO.Path]::GetFileName($target).StartsWith('native-runtime-run-guard-',[StringComparison]::Ordinal)){throw 'Guard test cleanup escaped owned path'};foreach($item in Get-ChildItem -LiteralPath $target -Recurse -Force){if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw 'Guard test cleanup encountered reparse point'}};Remove-Item -LiteralPath $target -Recurse -Force;if(Test-Path -LiteralPath $target){throw 'Guard test artifact left behind'}}
}