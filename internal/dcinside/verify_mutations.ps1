param([int]$Start=0,[int]$End=999)
$ErrorActionPreference='Stop'
$root=(Get-Location).Path
$manifest=Get-Content -LiteralPath (Join-Path $PSScriptRoot 'mutation-cases.json') -Raw | ConvertFrom-Json
$dir=Join-Path $root '.task/dcinside-mutations'
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$results=@()
for($index=$Start;$index -lt [Math]::Min($End,$manifest.Count);$index++){
 $item=$manifest[$index]
 $sourcePath=Join-Path $root $item.file
 $source=[IO.File]::ReadAllText($sourcePath)
 $matches=[regex]::Matches($source,[regex]::Escape($item.old)).Count
 if($matches -lt 1){throw "missing mutation anchor: $($item.name)"}
 $mutantPath=Join-Path $dir ($item.name+'.go')
 [IO.File]::WriteAllText($mutantPath,$source.Replace($item.old,$item.replacement),[Text.UTF8Encoding]::new($false))
 $replacement=@{};$replacement[$sourcePath]=$mutantPath
 $overlayPath=Join-Path $dir ($item.name+'.json')
 [IO.File]::WriteAllText($overlayPath,(@{Replace=$replacement}|ConvertTo-Json -Depth 4),[Text.UTF8Encoding]::new($false))
 $logPath=Join-Path $dir ($item.name+'.log')
 $arguments=@('test','./internal/dcinside','-count=1','-timeout=12s',('-overlay='+$overlayPath),('-run='+$item.run))
 & go @arguments 2>&1 | Out-File -LiteralPath $logPath -Encoding utf8
 $status=$LASTEXITCODE
 $log=[IO.File]::ReadAllText($logPath)
 $compileFailure=$log.Contains('[build failed]') -or $log.Contains('syntax error')
 $result=[ordered]@{name=$item.name;matches=$matches;killed=($status -ne 0 -and !$compileFailure);compileFailure=$compileFailure;exit=$status}
 $results+=$result
 $result|ConvertTo-Json -Compress|Write-Output
}
[IO.File]::WriteAllText((Join-Path $dir ('results-'+$Start+'.json')),($results|ConvertTo-Json -Depth 4),[Text.UTF8Encoding]::new($false))
if(($results|Where-Object {!$_.killed}).Count -gt 0){exit 1}
