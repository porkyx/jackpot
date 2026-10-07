$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$task=Join-Path $workspace '.task'
$source=Join-Path $workspace 'scripts/verify-round-read-optimization.ps1'
$sourceHash=(Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash
$tokens=$null;$errors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile($source,[ref]$tokens,[ref]$errors)
if($errors.Count -ne 0){throw 'Read optimization harness parse failed'}
$function=$ast.Find({param($node)$node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Assert-OwnedDirectory'},$true)
if($null -eq $function){throw 'Actual cleanup guard missing'}
Invoke-Expression $function.Extent.Text
$prefix='verified-test-read-opt-harness-'
$directory=Join-Path $task ($prefix+[guid]::NewGuid().ToString('N')+' own space')
$target=Join-Path $task ($prefix+[guid]::NewGuid().ToString('N')+' junction target')
$null=New-Item -ItemType Directory -Path $directory
$null=New-Item -ItemType Directory -Path $target
$sentinel=Join-Path $target 'sentinel.txt'
[IO.File]::WriteAllText($sentinel,'owned-only target remains unchanged')
$sentinelHash=(Get-FileHash -LiteralPath $sentinel -Algorithm SHA256).Hash
$passed=@()
function Reject([string]$Name,[string]$Path){$rejected=$false;try{$null=Assert-OwnedDirectory $Path $prefix}catch{$rejected=$true};if(-not $rejected){throw ('Cleanup guard accepted '+$Name)};$script:passed+=$Name}
function Delete-ExactOwnedJunction([string]$Path){
 $resolved=[IO.Path]::GetFullPath($Path).TrimEnd([char[]]'\/')
 if(-not $resolved.StartsWith($task+[IO.Path]::DirectorySeparatorChar,[StringComparison]::OrdinalIgnoreCase)){throw 'Guard junction cleanup escaped task'}
 $entry=Get-Item -LiteralPath $resolved -Force
 if(($entry.Attributes -band [IO.FileAttributes]::ReparsePoint) -eq 0){throw 'Guard cleanup target is not the exact own junction'}
 [IO.Directory]::Delete($resolved)
}
$rootLink=Join-Path $task ($prefix+[guid]::NewGuid().ToString('N'))
$nestedLink=Join-Path $directory 'nested junction'
try{
 if((Assert-OwnedDirectory $directory $prefix) -ne $directory){throw 'Guard changed owned path with spaces'};$passed+='owned space path'
 if((Assert-OwnedDirectory ($directory+[IO.Path]::DirectorySeparatorChar) $prefix) -ne $directory){throw 'Guard changed trailing separator'};$passed+='trailing separator'
 $nested=Join-Path $directory 'nested normal directory';$null=New-Item -ItemType Directory -Path $nested
 $file=Join-Path $nested 'readonly.txt';[IO.File]::WriteAllText($file,'readonly content')
 [IO.File]::SetAttributes($file,[IO.FileAttributes]::ReadOnly)
 $hash=(Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash
 $null=Assert-OwnedDirectory $directory $prefix
 if((Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash -ne $hash -or ((Get-Item -LiteralPath $file).Attributes -band [IO.FileAttributes]::ReadOnly) -eq 0){throw 'Guard mutated readonly content or attributes'};$passed+='nested readonly nonmutation'
 Reject 'outside workspace' ([IO.Path]::GetFullPath((Join-Path $workspace ('../'+$prefix+'escape'))))
 Reject 'wrong task prefix' (Join-Path $task 'existing-user-data')
 Reject 'nested own-name directory' (Join-Path $directory ($prefix+'nested'))
 Reject 'dotdot escape' (Join-Path $directory '../outside-owned-prefix')
 Reject 'task root itself' $task
 $missing=Join-Path $task ($prefix+[guid]::NewGuid().ToString('N'))
 if((Assert-OwnedDirectory $missing $prefix) -ne $missing -or (Test-Path -LiteralPath $missing)){throw 'Guard created or rejected new owned path'};$passed+='missing owned path nonmutation'
 $null=New-Item -ItemType Junction -Path $rootLink -Target $target
 Reject 'actual root junction' $rootLink
 Delete-ExactOwnedJunction $rootLink
 $null=New-Item -ItemType Junction -Path $nestedLink -Target $target
 Reject 'actual nested junction' $directory
 Delete-ExactOwnedJunction $nestedLink
 if((Get-FileHash -LiteralPath $sentinel -Algorithm SHA256).Hash -ne $sentinelHash){throw 'Cleanup guard changed junction target sentinel'};$passed+='junction target unchanged'
}finally{
 foreach($link in @($nestedLink,$rootLink)){if(Test-Path -LiteralPath $link){Delete-ExactOwnedJunction $link}}
 foreach($path in @($directory,$target)){$resolved=Assert-OwnedDirectory $path $prefix;if(Test-Path -LiteralPath $resolved){Remove-Item -LiteralPath $resolved -Recurse -Force};if(Test-Path -LiteralPath $resolved){throw 'Owned guard fixture cleanup failed'}}
 if((Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash -ne $sourceHash){throw 'Original harness source changed'}
}
$report=@{status='passed';cases=$passed;count=$passed.Count;powershellVersion=$PSVersionTable.PSVersion.ToString();loadedActualASTGuardOnly=$true;goSuiteExecuted=$false;ownedDirectoriesRemoved=$true;rootAndNestedJunctionsRemoved=$true;sourceSHAUnchanged=$true}
[IO.File]::WriteAllText((Join-Path $task 'read-optimization-guards-report.json'),($report|ConvertTo-Json -Depth 5),(New-Object Text.UTF8Encoding($false)))
Write-Output ('Actual AST read optimization cleanup guard PASS '+$passed.Count+' cases / owned directory and junction cleanup')