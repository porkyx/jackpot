param([switch]$KeepArtifacts)
$ErrorActionPreference = 'Stop'
$workspace = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$directory = [IO.Path]::GetFullPath((Join-Path $workspace ('.task/verified-test-frozen-' + [guid]::NewGuid().ToString('N'))))
if (-not $directory.StartsWith($workspace + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw 'Mutation artifact escaped workspace' }
$null = New-Item -ItemType Directory -Path $directory
$original = [IO.File]::ReadAllText((Join-Path $workspace 'internal/desktop/frozen_participants.go'))
$mutations = @(
 @{ Name='revision fence'; Before='actual != *expected'; After='actual == *expected' },
 @{ Name='participant limit 100'; Before='query.Limit > 100'; After='query.Limit > 101' },
 @{ Name='comment limit 50'; Before='query.Limit > 50'; After='query.Limit > 51' },
 @{ Name='global identifier search'; Before='strings.Contains(strings.ToLower(participant.PublicIdentifier), needle)'; After='strings.Contains(strings.ToLower(participant.Nickname), needle)' },
 @{ Name='classification group'; Before='if query.Group != "" && participant.Classification != query.Group {'; After='if false {' },
 @{ Name='participant offset'; Before='index < query.Offset'; After='index > query.Offset' },
 @{ Name='matched total'; Before='page.Matched++'; After='page.Matched += 0' },
 @{ Name='participant payload cap'; Before='uint32(len(page.Rows)) >= query.Limit'; After='uint32(len(page.Rows)) > query.Limit' },
 @{ Name='comment offset'; Before='uint32(index) < query.Offset'; After='uint32(index) > query.Offset' },
 @{ Name='https media only'; Before='media.Scheme != "https"'; After='media.Scheme == "https"' },
 @{ Name='preview 140 unicode scalars'; Before='runes[:140]'; After='runes[:139]' },
 @{ Name='detached parent pointer'; Before='data.ParentID = &parent'; After='data.ParentID = comment.ParentID; _ = parent' },
 @{ Name='detached timestamp pointer'; Before='data.PostedAt = &stamp'; After='data.PostedAt = comment.PostedAt; _ = stamp' },
 @{ Name='empty participant rows array'; Before='Rows: make([]contracts.ParticipantData, 0, query.Limit)'; After='Rows: nil' },
 @{ Name='empty comment rows array'; Before='Rows: make([]contracts.CommentData, 0, query.Limit)'; After='Rows: nil' }
)
$results = @()
try {
 $null=New-Item -ItemType Directory -Path (Join-Path $directory 'baseline')
 Get-ChildItem -LiteralPath (Join-Path $workspace 'internal/desktop') -Filter '*.go' -File | ForEach-Object { Copy-Item -LiteralPath $_.FullName -Destination (Join-Path $directory 'baseline') }
 Push-Location $workspace
 try {
  $baselinePath='./.task/' + [IO.Path]::GetFileName($directory) + '/baseline'
  $baseline = & go test $baselinePath -run TestFrozen -count=1 -timeout=60s 2>&1
  if ($LASTEXITCODE -ne 0) { throw ('Baseline failed: ' + ($baseline -join "`n")) }
  Write-Output 'Frozen mutation baseline PASS'
  $index=0
  foreach($mutation in $mutations) {
   $index++
   if (-not $original.Contains($mutation.Before)) { throw ('Missing mutation target: ' + $mutation.Name) }
   $mutant = Join-Path $directory ('mutant-' + $index)
   $null=New-Item -ItemType Directory -Path $mutant
   Get-ChildItem -LiteralPath (Join-Path $directory 'baseline') -Filter '*.go' -File | ForEach-Object {Copy-Item -LiteralPath $_.FullName -Destination $mutant}
   [IO.File]::WriteAllText((Join-Path $mutant 'frozen_participants.go'), $original.Replace($mutation.Before, $mutation.After))
   $testPath='./.task/' + [IO.Path]::GetFileName($directory) + '/mutant-' + $index
   $output = & go test $testPath -run TestFrozen -count=1 -timeout=60s 2>&1
   $exitCode=$LASTEXITCODE
   [IO.File]::WriteAllText((Join-Path $mutant 'test.log'),($output -join "`n"))
   if ($exitCode -eq 0) { throw ('Surviving mutation: ' + $mutation.Name) }
   if (($output -join "`n") -notmatch 'FAIL: TestFrozen') { throw ('Mutation did not fail an observable behavior test: ' + $mutation.Name + "`n" + ($output -join "`n")) }
   $results += $mutation.Name
   Write-Output ('KILLED ' + $mutation.Name)
  }
 } finally { Pop-Location }
 Write-Output ('Frozen mutation gate PASS: ' + $results.Count + '/' + $mutations.Count)
} finally {
 # Deletion uses one native PowerShell shell, with a freshly verified absolute
 # workspace target. Original source, user data and unrelated artifacts stay out.
 $resolved = [IO.Path]::GetFullPath($directory)
 $expectedPrefix=Join-Path $workspace '.task/verified-test-frozen-'
 if (-not $resolved.StartsWith($expectedPrefix,[StringComparison]::OrdinalIgnoreCase)) { throw 'Cleanup target escaped frozen artifacts' }
 if (-not $KeepArtifacts -and (Test-Path -LiteralPath $resolved)) { Remove-Item -LiteralPath $resolved -Recurse -Force }
 if (-not $KeepArtifacts -and (Test-Path -LiteralPath $resolved)) { throw 'Mutation artifact cleanup failed' }
}