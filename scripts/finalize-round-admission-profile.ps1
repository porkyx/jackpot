param([string]$NodePath = (Join-Path $env:LOCALAPPDATA 'Volta/tools/image/node/22.16.0/node.exe'))
$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$task=Join-Path $workspace '.task'
$utf8=New-Object Text.UTF8Encoding($false)
function Assert-NoReparse([string]$Path){
 $queue=New-Object 'Collections.Generic.Queue[string]';$queue.Enqueue($Path)
 while($queue.Count){$item=Get-Item -LiteralPath $queue.Dequeue() -Force;if($item.Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Private profile artifact has a reparse point'};if($item.PSIsContainer){foreach($child in @(Get-ChildItem -LiteralPath $item.FullName -Force)){$queue.Enqueue($child.FullName)}}}
}
if((Get-Item -LiteralPath $task).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Diagnostic task root is a reparse point'}
$manifestPath=Join-Path $task 'round-admission-profile-prepared.json'
$manifest=Get-Content -LiteralPath $manifestPath -Raw|ConvertFrom-Json
$directory=[IO.Path]::GetFullPath($manifest.directory)
if([IO.Path]::GetDirectoryName($directory) -ne $task -or -not [IO.Path]::GetFileName($directory).StartsWith('verified-test-round-admission-',[StringComparison]::Ordinal)){throw 'Profile manifest escaped task directory'}
Assert-NoReparse $directory
if($manifest.status -ne 'built' -or (Get-FileHash -LiteralPath $manifest.executable -Algorithm SHA256).Hash -ne $manifest.executableSHA256){throw 'Private profiler host changed before asset join'}
$sourceCount=@($manifest.productionSourceSHA256.psobject.Properties).Count
if($sourceCount -lt 8 -or -not $manifest.manualAndPageSourceIncluded){throw 'Latest production source provenance missing'}
foreach($property in $manifest.productionSourceSHA256.psobject.Properties){if((Get-FileHash -LiteralPath (Join-Path $workspace $property.Name) -Algorithm SHA256).Hash -ne $property.Value){throw 'Production source changed since private observer controls'}}
$sourceAssets=Join-Path $workspace 'frontend/dist'
Assert-NoReparse $sourceAssets
$html=[IO.File]::ReadAllText((Join-Path $sourceAssets 'index.html'))
$refs=@([regex]::Matches($html,'(?:src|href)="(/?assets/[A-Za-z0-9_.-]+\.(?:js|css))"')|ForEach-Object {$_.Groups[1].Value.TrimStart('/')})
if($refs.Count -ne 2 -or @($refs|Where-Object {$_ -match '\.js$'}).Count -ne 1 -or @($refs|Where-Object {$_ -match '\.css$'}).Count -ne 1){throw 'Exact HTML JS CSS asset join required'}
$assetPaths=@('index.html')+$refs
$assetCopy=[IO.Path]::GetFullPath((Join-Path $task ('stress-assets-admission-'+[guid]::NewGuid().ToString('N'))))
if([IO.Path]::GetDirectoryName($assetCopy) -ne $task -or (Test-Path -LiteralPath $assetCopy)){throw 'Fresh owned profiler asset target required'}
$assetHashes=@{}
foreach($path in $assetPaths){
 $source=Join-Path $sourceAssets $path
 $assetHashes[$path]=(Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash
 $target=Join-Path $assetCopy $path
 $null=New-Item -ItemType Directory -Path ([IO.Path]::GetDirectoryName($target)) -Force
 [IO.File]::WriteAllBytes($target,[IO.File]::ReadAllBytes($source))
 if((Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash -ne $assetHashes[$path]){throw 'Profiler asset copy changed bytes'}
}
$bindingSource=Join-Path $workspace 'frontend/bindings'
Assert-NoReparse $bindingSource
$bindingHashes=@{}
foreach($file in @(Get-ChildItem -LiteralPath $bindingSource -Recurse -File)){
 $relative=$file.FullName.Substring($workspace.Length+1).Replace('\','/')
 $bindingHashes[$relative]=(Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash
 $target=Join-Path $directory ('bindingSnapshot/'+$relative)
 $null=New-Item -ItemType Directory -Path ([IO.Path]::GetDirectoryName($target)) -Force
 [IO.File]::WriteAllBytes($target,[IO.File]::ReadAllBytes($file.FullName))
}
$bootstrap='frontend/bindings/github.com/porkyx/jackpot/internal/desktop/service.ts'
if(-not $bindingHashes.ContainsKey($bootstrap)){throw 'Fresh generated Bootstrap binding missing'}
$beforeHost=$manifest.executableSHA256
Push-Location $workspace
try {
 & go build -tags production -overlay $manifest.overlay -o $manifest.executable ./cmd/producte2e *> (Join-Path $directory 'latest-assets-host-rebuild.log')
 if($LASTEXITCODE -ne 0){throw 'Exact private overlay host rebuild failed'}
}finally{Pop-Location}
$manifest.executableSHA256=(Get-FileHash -LiteralPath $manifest.executable -Algorithm SHA256).Hash
$manifest.frontendAssetsPending=$false
$manifest|Add-Member -NotePropertyName assetFreezeWithdrawn -NotePropertyValue $false -Force
$manifest|Add-Member -NotePropertyName assetSourceSHA256 -NotePropertyValue $assetHashes -Force
$manifest|Add-Member -NotePropertyName assetDirectory -NotePropertyValue $assetCopy -Force
$manifest|Add-Member -NotePropertyName bindingSourceSHA256 -NotePropertyValue $bindingHashes -Force
$manifest|Add-Member -NotePropertyName executableBeforeAssetJoinSHA256 -NotePropertyValue $beforeHost -Force
$manifest|Add-Member -NotePropertyName assetsLoadedFromRuntimeDirFS -NotePropertyValue $true -Force
$manifest|Add-Member -NotePropertyName assetsJoinedUTC -NotePropertyValue ([DateTime]::UtcNow.ToString('o')) -Force
foreach($path in $assetHashes.Keys){if((Get-FileHash -LiteralPath (Join-Path $sourceAssets $path) -Algorithm SHA256).Hash -ne $assetHashes[$path]){throw 'Shared assets changed during profiler join'}}
foreach($property in $manifest.productionSourceSHA256.psobject.Properties){if((Get-FileHash -LiteralPath (Join-Path $workspace $property.Name) -Algorithm SHA256).Hash -ne $property.Value){throw 'Production source changed during profiler rebuild'}}
foreach($path in $bindingHashes.Keys){if((Get-FileHash -LiteralPath (Join-Path $workspace $path) -Algorithm SHA256).Hash -ne $bindingHashes[$path]){throw 'Shared bindings changed during profiler join'}}
foreach($target in @($manifestPath,(Join-Path $directory 'manifest.json'),(Join-Path $directory 'joined-manifest.json'))){[IO.File]::WriteAllText($target,($manifest|ConvertTo-Json -Depth 8),$utf8)}
& (Join-Path $PSScriptRoot 'prepare-round-admission-driver.ps1') -NodePath $NodePath
if($LASTEXITCODE -ne 0){throw 'Private copied driver preparation failed'}
$driverPath=Join-Path $directory 'driver/product.stress.mjs'
$driver=[IO.File]::ReadAllText($driverPath)
$assetAnchor='assets=resolve(process.env.JACKPOT_STRESS_ASSETS??resolve(root,"frontend/dist"))'
if(-not $driver.Contains($assetAnchor)){throw 'Private profiler asset anchor missing'}
$quotedAssets=ConvertTo-Json -InputObject $assetCopy -Compress
$driver=$driver.Replace($assetAnchor,'assets=resolve(process.env.JACKPOT_STRESS_ASSETS??'+$quotedAssets+')')
$bindingAnchor='resolve(root,"'+$bootstrap+'")'
if(-not $driver.Contains($bindingAnchor)){throw 'Private binding copy anchor missing'}
$driver=$driver.Replace($bindingAnchor,'resolve(evidenceRoot,"bindingSnapshot/'+$bootstrap+'")')
$driver=$driver.Replace('const bootstrapBindings=', 'assert.equal(assets,resolve('+$quotedAssets+'),"exact joined profiler assets required");'+"`n"+'const bootstrapBindings=')
if(-not $driver.Contains('const phaseLog=')){throw 'Profile launch guard anchor missing'}
$driver=$driver.Replace('const phaseLog=', 'const exactAssetJoin=JSON.parse(await readFile(resolve(evidenceRoot,"joined-manifest.json"),"utf8"));assert.equal(exactAssetJoin.frontendAssetsPending,false,"fresh frontend asset checkpoint required");assert.equal(exactAssetJoin.assetFreezeWithdrawn,false,"withdrawn frontend asset checkpoint forbids launch");const phaseLog=')
[IO.File]::WriteAllText($driverPath,$driver,$utf8)
if(-not [IO.Path]::IsPathRooted($NodePath) -or -not (Test-Path -LiteralPath $NodePath -PathType Leaf)){throw 'Absolute pinned Node executable required'}; $node=$NodePath
& $node --check $driverPath
if($LASTEXITCODE -ne 0){throw 'Exact asset-bound private driver syntax failed'}
$driverReport=Get-Content -LiteralPath (Join-Path $task 'round-admission-driver-prepared.json') -Raw|ConvertFrom-Json
$driverReport|Add-Member -NotePropertyName assetsDirectory -NotePropertyValue $assetCopy -Force
$driverReport|Add-Member -NotePropertyName assetSourceSHA256 -NotePropertyValue $assetHashes -Force
$driverReport|Add-Member -NotePropertyName bindingSourceSHA256 -NotePropertyValue $bindingHashes -Force
$driverReport|Add-Member -NotePropertyName driverSHA256 -NotePropertyValue ((Get-FileHash -LiteralPath $driverPath -Algorithm SHA256).Hash) -Force
$driverReport|Add-Member -NotePropertyName frontendAssetsPending -NotePropertyValue $false -Force
[IO.File]::WriteAllText((Join-Path $task 'round-admission-driver-prepared.json'),($driverReport|ConvertTo-Json -Depth 8),$utf8)
$global:LASTEXITCODE=0
Write-Output ('Exact assets3 + bindings'+$bindingHashes.Count+' + source'+$sourceCount+' + private host joined; native0; acceptancefalse: '+$directory)