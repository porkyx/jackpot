param(
 [switch]$Run,
 [ValidateRange(20,50)][int]$SamplesPerWorkload=20,
 [string]$HostPath='',
 [string]$ExpectedHostSHA256='',
 [string]$NodePath=(Join-Path $env:USERPROFILE 'AppData/Local/Volta/tools/image/node/22.16.0/node.exe')
)
$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$task=Join-Path $workspace '.task'
$native=Join-Path $workspace 'frontend/tests/native'
$driver=Join-Path $native 'product.draw-performance.mjs'
$helper=Join-Path $native 'product.draw-performance.helpers.mjs'
$utf8=New-Object Text.UTF8Encoding($false)
if(-not [IO.Path]::IsPathRooted($NodePath) -or -not(Test-Path -LiteralPath $NodePath -PathType Leaf)){throw 'Explicit absolute pinned Node22.16.0 path required'}
if((Get-Item -LiteralPath $NodePath).VersionInfo.ProductVersion -ne '22.16.0'){throw 'Pinned Node22.16.0 required'}

function Assert-PlainTree([string]$Path){
 foreach($item in @((Get-Item -LiteralPath $Path)) + @(Get-ChildItem -LiteralPath $Path -Recurse -Force)){
  if($item.Attributes -band [IO.FileAttributes]::ReparsePoint){throw ('Reparse alias rejected: '+$item.FullName)}
 }
}
function File-Records([string]$Base,[object[]]$Files){
 @($Files|Sort-Object FullName|ForEach-Object{
  [ordered]@{path=$_.FullName.Substring($Base.Length+1).Replace('\','/');sha256=(Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash}
 })
}
function Source-Records {
 $files=@(Get-Item -LiteralPath (Join-Path $workspace 'main.go'),(Join-Path $workspace 'go.mod'),(Join-Path $workspace 'go.sum'))
 foreach($name in @('internal','cmd/producte2e')){
  $directory=Join-Path $workspace $name;Assert-PlainTree $directory
  $files+=@(Get-ChildItem -LiteralPath $directory -File -Recurse -Filter '*.go')
 }
 foreach($name in @('frontend/src','frontend/bindings')){
  $directory=Join-Path $workspace $name;Assert-PlainTree $directory
  $files+=@(Get-ChildItem -LiteralPath $directory -File -Recurse)
 }
 File-Records $workspace $files
}

Push-Location $workspace
try {
 & $NodePath --test (Join-Path $native 'product.draw-performance.helpers.test.mjs') (Join-Path $native 'stress.metrics.test.mjs') (Join-Path $native 'webview-recovery.guards.test.mjs')
 if($LASTEXITCODE -ne 0){throw 'Independent draw timing/state/ownership guards failed'}
 & $NodePath --check $driver
 if($LASTEXITCODE -ne 0){throw 'Native draw driver syntax failed'}
 & $NodePath --check $helper
 if($LASTEXITCODE -ne 0){throw 'Native draw helper syntax failed'}
 if(-not $Run){Write-Output 'Draw timing harness prepared: narrow tests/syntax PASS; Go build/native launch0. -Run builds one owned host and measures two20+ fresh-collection workloads.';return}

 $directory=[IO.Path]::GetFullPath((Join-Path $task ('draw-performance-'+[guid]::NewGuid().ToString('N'))))
 if([IO.Path]::GetDirectoryName($directory) -ine $task -or [IO.Path]::GetFileName($directory) -notmatch '^draw-performance-[a-f0-9]{32}$'){throw 'Evidence directory escaped workspace'}
 $null=New-Item -ItemType Directory -Path $directory
 $manifestPath=Join-Path $directory 'manifest.json'
 $manifest=[ordered]@{version=1;status='preparing';workspace=$workspace;preparedAt=[DateTime]::UtcNow.ToString('o');samplesPerWorkload=$SamplesPerWorkload;nativeStarted=$false;runtimePolicy='default; no GOMEMLIMIT/GOGC/browser diagnostic arguments';sourceFiles=@();assetsFiles=@();bindingFiles=@();harnessFiles=@()}
 try {
  $manifest.sourceFiles=@(Source-Records)
  $dist=Join-Path $workspace 'frontend/dist';Assert-PlainTree $dist
  $manifest.assetsFiles=@(File-Records $dist @(Get-ChildItem -LiteralPath $dist -File -Recurse))
  if(-not($manifest.assetsFiles|Where-Object{$_.path -eq 'index.html'})){throw 'Production frontend assets required'}
  $assets=Join-Path $directory 'assets';$null=New-Item -ItemType Directory -Path $assets
  foreach($record in $manifest.assetsFiles){$destination=Join-Path $assets $record.path;$null=New-Item -ItemType Directory -Path (Split-Path $destination -Parent) -Force;Copy-Item -LiteralPath (Join-Path $dist $record.path) -Destination $destination}
  $bindingRoot=Join-Path $workspace 'frontend/bindings';$manifest.bindingFiles=@(File-Records $bindingRoot @(Get-ChildItem -LiteralPath $bindingRoot -File -Recurse))
  foreach($record in $manifest.bindingFiles){$destination=Join-Path $directory ('bindings/'+$record.path);$null=New-Item -ItemType Directory -Path (Split-Path $destination -Parent) -Force;Copy-Item -LiteralPath (Join-Path $bindingRoot $record.path) -Destination $destination}
  $harness=@($driver,$helper,(Join-Path $native 'product.draw-performance.helpers.test.mjs'),(Join-Path $native 'stress.metrics.mjs'),(Join-Path $native 'reservationrestart.invariants.mjs'),(Join-Path $native 'webview-recovery.guards.mjs'),$PSCommandPath)
  $manifest.harnessFiles=@(File-Records $workspace @(Get-Item -LiteralPath $harness))
  $manifest.assets=$assets;$manifest.executable=Join-Path $directory 'product-draw-performance.exe'
  [IO.File]::WriteAllText($manifestPath,($manifest|ConvertTo-Json -Depth 8),$utf8)
  if($HostPath){
   $hostAbsolute=[IO.Path]::GetFullPath($HostPath)
   if(-not [IO.Path]::IsPathRooted($HostPath) -or [IO.Path]::GetDirectoryName($hostAbsolute) -ine $task -or [IO.Path]::GetFileName($hostAbsolute) -notin @('producte2e.exe','product-stress.exe')){throw 'Existing isolated test host must be exact workspace .task/producte2e.exe or product-stress.exe'}
   if((Get-Item -LiteralPath $hostAbsolute).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Host alias rejected'}
   if($ExpectedHostSHA256 -notmatch '^[A-Fa-f0-9]{64}$' -or (Get-FileHash -LiteralPath $hostAbsolute -Algorithm SHA256).Hash -ine $ExpectedHostSHA256){throw 'Coordinator-supplied exact existing host SHA required'}
   Copy-Item -LiteralPath $hostAbsolute -Destination $manifest.executable
   $manifest.hostProvenance=[ordered]@{kind='exact existing producte2e host copied; coordinator must attest current Go source build';path=$hostAbsolute;expectedSHA256=$ExpectedHostSHA256.ToUpperInvariant()}
  }else{
   $buildOutput=@(& go build -tags production -o $manifest.executable ./cmd/producte2e 2>&1);$buildExit=$LASTEXITCODE
   [IO.File]::WriteAllText((Join-Path $directory 'build.log'),($buildOutput -join [Environment]::NewLine),$utf8)
   if($buildExit -ne 0){throw 'Owned optimized native host build failed'}
   $manifest.hostProvenance=[ordered]@{kind='go build -tags production from pre/post identical source';path=$manifest.executable}
  }
  $after=@(Source-Records)
  if(($manifest.sourceFiles|ConvertTo-Json -Depth 4 -Compress) -cne ($after|ConvertTo-Json -Depth 4 -Compress)){throw 'Source file set/hash changed during build'}
  foreach($record in $manifest.assetsFiles){if((Get-FileHash -LiteralPath (Join-Path $assets $record.path) -Algorithm SHA256).Hash -cne $record.sha256){throw 'Copied asset changed'}}
  $manifest.executableSHA256=(Get-FileHash -LiteralPath $manifest.executable -Algorithm SHA256).Hash
  $manifest.goVersion=(& go version|Out-String).Trim();$manifest.goEnvironment=(& go env GOOS GOARCH GOVERSION GOFLAGS CGO_ENABLED|Out-String).Trim()
  if((& go env GOFLAGS|Out-String).Trim()){throw 'Default optimized Go build requires empty GOFLAGS'}
  $manifest.status='built';$manifest.nativeStarted=$true
  [IO.File]::WriteAllText($manifestPath,($manifest|ConvertTo-Json -Depth 8),$utf8)
  & $NodePath $driver $manifestPath
  $driverExit=$LASTEXITCODE
  if($driverExit -ne 0){throw ('Fresh-collection draw failed; preserved '+(Join-Path $directory 'report.json'))}
  $report=[IO.File]::ReadAllText((Join-Path $directory 'report.json'))|ConvertFrom-Json
  if($report.status -ne 'passed' -or $report.workloads.Count -ne 2 -or $report.pinsVerifiedBefore -ne $true -or $report.pinsVerifiedAfter -ne $true -or $report.ownedCleanupComplete -ne $true -or $report.thresholds.both20Plus -ne $true){throw 'Incomplete native timing/state/source/cleanup evidence'}
  foreach($workload in $report.workloads){if($workload.samples.Count -ne $SamplesPerWorkload -or $workload.uniqueCollections -ne $SamplesPerWorkload -or $workload.uniqueDrafts -ne $SamplesPerWorkload){throw 'Fresh equivalent sample set incomplete'}}
  Write-Output ('Native draw evidence: '+(Join-Path $directory 'report.json')+';100/200 comment workloads each'+$SamplesPerWorkload+' fresh collections; functional/cleanupPASS;1s p95='+$report.thresholds.drawP95Under1s+'. Quiet acceptance requires coordinator confirmation.')
 } catch {
  [IO.File]::WriteAllText((Join-Path $directory 'wrapper-failure.txt'),($_|Out-String),$utf8)
  throw
 }
} finally {Pop-Location}
