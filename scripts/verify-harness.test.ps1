param([switch]$Mutations)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$workspaceRoot = [IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent)).TrimEnd([char[]]'\/')
$taskDirectory = Join-Path $workspaceRoot '.task'
$fixtureRoot = Join-Path $taskDirectory ('verified-test-' + [Guid]::NewGuid().ToString('N'))
$verifyPath = Join-Path $PSScriptRoot 'verify.ps1'
$parseTokens = $null
$parseErrors = $null
$verifyAst = [Management.Automation.Language.Parser]::ParseFile($verifyPath, [ref]$parseTokens, [ref]$parseErrors)
if ($parseErrors.Count -ne 0) { throw 'Verifier source has parse errors; no code was loaded' }
$definitions = @($verifyAst.FindAll({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Assert-SameFiles' }, $true))
if ($definitions.Count -ne 1) { throw 'Verifier must contain exactly one Assert-SameFiles function' }
$originalDefinition = $definitions[0].Extent.Text

function Assert-True([bool]$Condition, [string]$Message) {
    if (-not $Condition) { throw "HARNESS ASSERTION: $Message" }
}
function Assert-OwnedPath([string]$Path) {
    $absolute = [IO.Path]::GetFullPath($Path).TrimEnd([char[]]'\/')
    $boundary = $fixtureRoot.TrimEnd([char[]]'\/') + [IO.Path]::DirectorySeparatorChar
    if ($absolute -ne $fixtureRoot -and -not $absolute.StartsWith($boundary, [StringComparison]::OrdinalIgnoreCase)) { throw "Unsafe fixture path: $absolute" }
    $workspaceBoundary = $workspaceRoot + [IO.Path]::DirectorySeparatorChar
    if (-not $absolute.StartsWith($workspaceBoundary, [StringComparison]::OrdinalIgnoreCase)) { throw "Fixture escaped workspace: $absolute" }
    $cursor = $absolute
    while ($cursor.Length -ge $workspaceRoot.Length) {
        if (Test-Path -LiteralPath $cursor) {
            $item = Get-Item -LiteralPath $cursor -Force
            if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw "Reparse fixture ancestor is forbidden: $cursor" }
        }
        if ($cursor -eq $workspaceRoot) { break }
        $cursor = Split-Path $cursor -Parent
    }
    return $absolute
}
function Remove-OwnedTree([string]$Path) {
    $absolute = Assert-OwnedPath $Path
    if (-not (Test-Path -LiteralPath $absolute)) { return }
    foreach ($item in @(Get-ChildItem -LiteralPath $absolute -Recurse -Force)) {
        if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw "Reparse fixture descendant is forbidden: $($item.FullName)" }
    }
    Remove-Item -LiteralPath $absolute -Recurse -Force
    Assert-True (-not (Test-Path -LiteralPath $absolute)) "Cleanup left fixture $absolute"
}
function Write-Fixture([string]$Root, [hashtable]$Files, [bool]$ReadOnly) {
    New-Item -ItemType Directory -Path (Assert-OwnedPath $Root) -Force | Out-Null
    foreach ($name in $Files.Keys) {
        $file = Assert-OwnedPath (Join-Path $Root $name)
        New-Item -ItemType Directory -Path (Split-Path $file -Parent) -Force | Out-Null
        $contents = $Files[$name]
        [byte[]]$bytes = @()
        if ($contents -is [byte[]]) { $bytes = $contents } else { $bytes = [Text.Encoding]::UTF8.GetBytes([string]$contents) }
        [IO.File]::WriteAllBytes($file, [byte[]]$bytes)
        if ($ReadOnly) { [IO.File]::SetAttributes($file, ([IO.File]::GetAttributes($file) -bor [IO.FileAttributes]::ReadOnly)) }
    }
}
function Snapshot-Fixture([string]$Root) {
    $rootPath = [IO.Path]::GetFullPath($Root).TrimEnd([char[]]'\/')
    $entries = @(Get-ChildItem -LiteralPath $rootPath -Force -Recurse | Sort-Object FullName | ForEach-Object {
        $relative = $_.FullName.Substring($rootPath.Length + 1)
        if ($_.PSIsContainer) { "directory|$relative|$([int]$_.Attributes)" }
        else { "file|$relative|$($_.Length)|$($_.LastWriteTimeUtc.Ticks)|$([int]$_.Attributes)|$((Get-FileHash -LiteralPath $_.FullName).Hash)" }
    })
    return ($entries -join "`n")
}
function Invoke-ComparisonCases {
    $cases = @(
        @{ Name = 'same single'; Expected = @{ 'a.txt' = 'same' }; Actual = @{ 'a.txt' = 'same' }; Error = $null },
        @{ Name = 'same nested'; Expected = @{ 'nested/deeper/a.txt' = 'a'; 'b.txt' = 'b' }; Actual = @{ 'b.txt' = 'b'; 'nested/deeper/a.txt' = 'a' }; Error = $null },
        @{ Name = 'different names same count'; Expected = @{ 'a.txt' = 'same' }; Actual = @{ 'b.txt' = 'same' }; Error = 'Contract file set differs:' },
        @{ Name = 'extra actual file'; Expected = @{ 'a.txt' = 'same' }; Actual = @{ 'a.txt' = 'same'; 'extra.txt' = 'extra' }; Error = 'Contract file set differs:' },
        @{ Name = 'missing actual file'; Expected = @{ 'a.txt' = 'a'; 'b.txt' = 'b' }; Actual = @{ 'a.txt' = 'a' }; Error = 'Contract file set differs:' },
        @{ Name = 'both empty'; Expected = @{}; Actual = @{}; Error = 'Contract file set differs:' },
        @{ Name = 'empty expected'; Expected = @{}; Actual = @{ 'a.txt' = 'a' }; Error = 'Contract file set differs:' },
        @{ Name = 'empty actual'; Expected = @{ 'a.txt' = 'a' }; Actual = @{}; Error = 'Contract file set differs:' },
        @{ Name = 'different first contents'; Expected = @{ 'a.txt' = 'first' }; Actual = @{ 'a.txt' = 'other' }; Error = 'Contract content differs: a.txt' },
        @{ Name = 'different Nth contents'; Expected = @{ 'a.txt' = 'same'; 'b.txt' = 'before' }; Actual = @{ 'a.txt' = 'same'; 'b.txt' = 'after' }; Error = 'Contract content differs: b.txt' },
        @{ Name = 'empty file contents'; Expected = @{ 'empty.txt' = [byte[]]@() }; Actual = @{ 'empty.txt' = [byte[]]@() }; Error = $null },
        @{ Name = 'binary contents'; Expected = @{ 'binary.dat' = [byte[]]@(0, 255, 128, 13, 10, 0) }; Actual = @{ 'binary.dat' = [byte[]]@(0, 255, 128, 13, 10, 0) }; Error = $null },
        @{ Name = 'expected trailing separator'; Expected = @{ 'a.txt' = 'same' }; Actual = @{ 'a.txt' = 'same' }; ExpectedSuffix = '\'; Error = $null },
        @{ Name = 'actual trailing separator'; Expected = @{ 'a.txt' = 'same' }; Actual = @{ 'a.txt' = 'same' }; ActualSuffix = '/'; Error = $null },
        @{ Name = 'both repeated trailing separators'; Expected = @{ 'nested/a.txt' = 'same' }; Actual = @{ 'nested/a.txt' = 'same' }; ExpectedSuffix = '\//'; ActualSuffix = '//\'; Error = $null },
        @{ Name = 'literal brackets and spaces'; Expected = @{ '[literal] folder/[a].txt' = 'same' }; Actual = @{ '[literal] folder/[a].txt' = 'same' }; Error = $null },
        @{ Name = 'relative paths'; Expected = @{ 'a.txt' = 'same' }; Actual = @{ 'a.txt' = 'same' }; Relative = $true; Error = $null },
        @{ Name = 'readonly repeated comparison'; Expected = @{ 'nested/a.txt' = 'same'; 'b.txt' = 'same' }; Actual = @{ 'nested/a.txt' = 'same'; 'b.txt' = 'same' }; ReadOnly = $true; Repeat = 20; Error = $null }
    )
    $passed = 0
    foreach ($case in $cases) {
        $caseRoot = Assert-OwnedPath (Join-Path $fixtureRoot $case.Name)
        $expected = Join-Path $caseRoot 'expected tree'
        $actual = Join-Path $caseRoot 'actual tree'
        try {
            $readOnly = $case.ContainsKey('ReadOnly') -and $case.ReadOnly
            Write-Fixture $expected $case.Expected $readOnly
            Write-Fixture $actual $case.Actual $readOnly
            $beforeExpected = Snapshot-Fixture $expected
            $beforeActual = Snapshot-Fixture $actual
            $expectedArgument = $expected
            $actualArgument = $actual
            if ($case.ContainsKey('Relative')) {
                $expectedArgument = $expected.Substring($workspaceRoot.Length + 1)
                $actualArgument = $actual.Substring($workspaceRoot.Length + 1)
            }
            if ($case.ContainsKey('ExpectedSuffix')) { $expectedArgument += $case.ExpectedSuffix }
            if ($case.ContainsKey('ActualSuffix')) { $actualArgument += $case.ActualSuffix }
            $repeat = if ($case.ContainsKey('Repeat')) { $case.Repeat } else { 1 }
            for ($iteration = 0; $iteration -lt $repeat; $iteration++) {
                $failure = $null
                try { Assert-SameFiles $expectedArgument $actualArgument } catch { $failure = $_ }
                if ($null -eq $case.Error) {
                    Assert-True ($null -eq $failure) "[$($case.Name)] Expected success, got $failure"
                } else {
                    Assert-True ($null -ne $failure) "[$($case.Name)] Expected rejection, got success"
                    Assert-True ($failure.Exception.Message.StartsWith($case.Error, [StringComparison]::Ordinal)) "[$($case.Name)] Expected '$($case.Error)', got '$($failure.Exception.Message)'"
                }
                Assert-True ((Snapshot-Fixture $expected) -ceq $beforeExpected) "[$($case.Name)] Expected files were modified"
                Assert-True ((Snapshot-Fixture $actual) -ceq $beforeActual) "[$($case.Name)] Actual files were modified"
            }
            # Closed Get-FileHash handles must allow immediate directory moves.
            $moved = Assert-OwnedPath (Join-Path $caseRoot 'moved expected tree')
            Assert-OwnedPath $expected | Out-Null
            Move-Item -LiteralPath $expected -Destination $moved
            Move-Item -LiteralPath $moved -Destination $expected
            Assert-True ((Snapshot-Fixture $expected) -ceq $beforeExpected) "[$($case.Name)] Rename changed files"
            $passed++
        } finally {
            Remove-OwnedTree $caseRoot
            Remove-OwnedTree $caseRoot
        }
    }
    return $passed
}

$workspaceBoundary = $workspaceRoot + [IO.Path]::DirectorySeparatorChar
if (-not ([IO.Path]::GetFullPath($fixtureRoot).StartsWith($workspaceBoundary, [StringComparison]::OrdinalIgnoreCase))) { throw 'Fixture root escaped workspace' }
Assert-OwnedPath $fixtureRoot | Out-Null
Push-Location $workspaceRoot
try {
    New-Item -ItemType Directory -Path $fixtureRoot -Force | Out-Null
    # AST extent includes exactly this definition. No top-level verifier code,
    # Invoke-Check, Go suite, fixture generator or existing contract tree runs.
    . ([ScriptBlock]::Create($originalDefinition))
    $passed = Invoke-ComparisonCases
    Write-Host "Assert-SameFiles: $passed cases PASS on PowerShell $($PSVersionTable.PSVersion)"
    if ($Mutations) {
        $variants = @(
            @{ Name = 'empty expected guard'; From = '$expectedNames.Count -eq 0'; To = '$expectedNames.Count -lt 0' },
            @{ Name = 'file-set comparison'; From = '(Compare-Object $expectedNames $actualNames)'; To = '$false' },
            @{ Name = 'content condition'; From = '.Hash -ne '; To = '.Hash -eq ' },
            @{ Name = 'content traversal'; From = 'foreach ($name in $expectedNames)'; To = 'foreach ($name in @())' },
            @{ Name = 'expected trailing normalization'; From = '[System.IO.Path]::GetFullPath($Expected).TrimEnd([char[]]''\/'')'; To = '[System.IO.Path]::GetFullPath($Expected)' },
            @{ Name = 'actual trailing normalization'; From = '[System.IO.Path]::GetFullPath($Actual).TrimEnd([char[]]''\/'')'; To = '[System.IO.Path]::GetFullPath($Actual)' },
            @{ Name = 'nested expected traversal'; From = '-LiteralPath $expectedRoot -File -Recurse'; To = '-LiteralPath $expectedRoot -File' },
            @{ Name = 'relative expected prefix'; From = '$_.FullName.Substring($expectedRoot.Length + 1)'; To = '$_.FullName.Substring($expectedRoot.Length + 2)' }
        )
        $killed = 0
        foreach ($variant in $variants) {
            if (-not $originalDefinition.Contains($variant.From)) { throw "Missing mutation target: $($variant.Name)" }
            $mutated = $originalDefinition.Replace($variant.From, $variant.To)
            $mutantTokens = $null
            $mutantErrors = $null
            [Management.Automation.Language.Parser]::ParseInput($mutated, [ref]$mutantTokens, [ref]$mutantErrors) | Out-Null
            if ($mutantErrors.Count -ne 0) { throw "Invalid parse mutation: $($variant.Name)" }
            . ([ScriptBlock]::Create($mutated))
            $caught = $null
            try { Invoke-ComparisonCases | Out-Null } catch { $caught = $_ }
            if ($null -eq $caught -or -not $caught.Exception.Message.StartsWith('HARNESS ASSERTION:', [StringComparison]::Ordinal)) { throw "Survived or invalid mutation: $($variant.Name): $caught" }
            $killed++
            Write-Host "killed: $($variant.Name)"
        }
        . ([ScriptBlock]::Create($originalDefinition))
        Assert-True ((Invoke-ComparisonCases) -eq $passed) 'Restored in-memory function must pass the same cases'
        Write-Host "$killed/$($variants.Count) comparator mutations killed by assertions"
    }
} finally {
    try {
        Remove-OwnedTree $fixtureRoot
        Remove-OwnedTree $fixtureRoot
    } finally { Pop-Location }
}
Assert-True (-not (Test-Path -LiteralPath $fixtureRoot)) 'Top-level fixture cleanup failed'
Write-Host 'Cleanup PASS; existing verifier/source/contracts/database files were not changed'