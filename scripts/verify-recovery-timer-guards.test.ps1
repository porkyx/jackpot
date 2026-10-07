$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$task=Join-Path $workspace '.task'
$scriptPath=Join-Path $PSScriptRoot 'verify-recovery-timer-mutations.ps1'
$scriptSHA=(Get-FileHash -LiteralPath $scriptPath -Algorithm SHA256).Hash
$tokens=$null;$errors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot 'verify-recovery-timer-mutations.ps1'),[ref]$tokens,[ref]$errors)
if($errors.Count -ne 0){throw 'Timer mutation script parse failed'}
$function=$ast.Find({param($node)$node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Assert-OwnedDirectory'},$true)
if($null -eq $function){throw 'Timer cleanup guard missing'}
. ([scriptblock]::Create($function.Extent.Text))
$owned=Join-Path $task ('verified-test-recovery-timer-guards-'+[guid]::NewGuid().ToString('N'))
$passed=0
$rootLink=Join-Path $task ('verified-test-recovery-timer-guard-rootlink-'+[guid]::NewGuid().ToString('N'))
$nestedLink=Join-Path $owned 'junction'
function Remove-ExactGuardLink([string]$Path){
 $full=[IO.Path]::GetFullPath($Path)
 if($full -ne $rootLink -and $full -ne $nestedLink){throw 'Timer guard junction removal escaped exact owned paths'}
 if((Get-Item -LiteralPath $full -Force).Attributes -band [IO.FileAttributes]::ReparsePoint){[IO.Directory]::Delete($full)}else{throw 'Timer guard removal requires own junction'}
}
function Expect-Rejected([string]$Name,[string]$Path){$rejected=$false;try{$null=Assert-OwnedDirectory $Path}catch{$rejected=$true};if(-not $rejected){throw ('Timer cleanup accepted invalid path: '+$Name)};$script:passed++}
try{
 $null=New-Item -ItemType Directory -Path $owned
 $child=Join-Path $owned 'nested';$null=New-Item -ItemType Directory -Path $child
 [IO.File]::WriteAllText((Join-Path $child 'read only evidence.txt'),'unchanged')
 $evidence=Join-Path $child 'read only evidence.txt';$hash=(Get-FileHash -LiteralPath $evidence -Algorithm SHA256).Hash
 $file=Get-Item -LiteralPath $evidence;$file.IsReadOnly=$true
 if((Assert-OwnedDirectory $owned) -ne $owned){throw 'Timer owned directory rejected'};$passed++
 if((Assert-OwnedDirectory ($owned+[IO.Path]::DirectorySeparatorChar)) -ne $owned){throw 'Timer trailing separator rejected'};$passed++
 $absent=Join-Path $task ('verified-test-recovery-timer-absent-'+[guid]::NewGuid().ToString('N'));if((Assert-OwnedDirectory $absent) -ne $absent){throw 'Timer absent owned directory rejected'};$passed++
 $null=New-Item -ItemType Junction -Path $rootLink -Target $child
 Expect-Rejected 'root junction' $rootLink
 Remove-ExactGuardLink $rootLink
 $null=New-Item -ItemType Junction -Path $nestedLink -Target $child
 Expect-Rejected 'nested junction' $owned
 Remove-ExactGuardLink $nestedLink
 Expect-Rejected 'workspace root' $workspace
 Expect-Rejected 'task root' $task
 Expect-Rejected 'nested target' $child
 Expect-Rejected 'different prefix' (Join-Path $task 'unowned-existing')
 Expect-Rejected 'traversal' (Join-Path $owned '../../escaped')
 if((Get-FileHash -LiteralPath $evidence -Algorithm SHA256).Hash -ne $hash -or -not (Get-Item -LiteralPath $evidence).IsReadOnly){throw 'Timer guard mutated read only evidence'};$passed++
}finally{
 foreach($link in @($rootLink,$nestedLink)){if(Test-Path -LiteralPath $link){Remove-ExactGuardLink $link}}
 if(Test-Path -LiteralPath $owned){$safe=Assert-OwnedDirectory $owned;foreach($entry in Get-ChildItem -LiteralPath $safe -Recurse -File){$entry.IsReadOnly=$false};Remove-Item -LiteralPath $safe -Recurse -Force}
 if(Test-Path -LiteralPath $owned){throw 'Timer guard temporary directory leaked'}
}
Write-Output ('Timer exact cleanup guard '+$passed+' independent checks PASS; no Go suite invoked')
if((Get-FileHash -LiteralPath $scriptPath -Algorithm SHA256).Hash -ne $scriptSHA){throw 'Timer actual guard source changed'}
$report=@{status='passed';cases=$passed;loadedActualASTGuardOnly=$true;goSuiteExecuted=$false;powershellVersion=$PSVersionTable.PSVersion.ToString();sourceSHA256=$scriptSHA;sourceUnchanged=$true;temporaryRemoved=-not(Test-Path -LiteralPath $owned);rootAndNestedJunctionRemoved=(-not(Test-Path -LiteralPath $rootLink) -and -not(Test-Path -LiteralPath $nestedLink))}
[IO.File]::WriteAllText((Join-Path $task ('recovery-timer-guards-'+$PSVersionTable.PSVersion.Major+'.'+$PSVersionTable.PSVersion.Minor+'.json')),($report|ConvertTo-Json), (New-Object Text.UTF8Encoding($false)))