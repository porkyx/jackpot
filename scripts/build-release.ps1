param([string]$NodePath='node',[string]$Version='0.1.0')
$ErrorActionPreference='Stop'
if($Version -ne '0.1.0'){throw 'This source candidate has application version 0.1.0; package metadata must match'}
$projectRoot=[IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent))
$releaseDirectory=Join-Path $projectRoot ('dist/Jackpot-'+$Version+'-windows-x64')
Push-Location $projectRoot
try {
    Push-Location (Join-Path $projectRoot 'frontend')
    try {
        & $NodePath node_modules/typescript/bin/tsc --noEmit
        if($LASTEXITCODE -ne 0){throw 'Release TypeScript failed'}
        & $NodePath scripts/policy.mjs
        if($LASTEXITCODE -ne 0){throw 'Release import policy failed'}
        & $NodePath node_modules/vite/bin/vite.js build
        if($LASTEXITCODE -ne 0){throw 'Release assets failed'}
    } finally {Pop-Location}
    if((& go env GOOS) -ne 'windows' -or (& go env GOARCH) -ne 'amd64'){throw 'This package supports Windows x64 only'}
    New-Item -ItemType Directory -Path $releaseDirectory -Force | Out-Null
    $executable=Join-Path $releaseDirectory 'Jackpot.exe'
    & go build -tags production -trimpath -ldflags '-H=windowsgui' -o $executable .
    if($LASTEXITCODE -ne 0){throw 'Windows embedded-assets executable failed'}
    Copy-Item -LiteralPath (Join-Path $projectRoot 'frontend/public/licenses/Pretendard-OFL.txt') -Destination (Join-Path $releaseDirectory 'Pretendard-OFL.txt') -Force
    $thirdPartyDirectory=Join-Path $projectRoot 'third_party'
    Copy-Item -LiteralPath (Join-Path $thirdPartyDirectory 'licenses') -Destination $releaseDirectory -Recurse -Force
    Copy-Item -LiteralPath (Join-Path $thirdPartyDirectory 'THIRD-PARTY-NOTICES.md') -Destination $releaseDirectory -Force
    $lines=@(
        'Jackpot Windows x64 portable package',
        ('Version: '+$Version),
        'Requirements: Windows 11 x64 and installed WebView2 runtime.',
        'The executable includes frontend assets. Node and a development server are not required.',
        'User database: %APPDATA%/Jackpot/jackpot.sqlite3',
        'Reservations execute while Jackpot is running; overdue reservations execute once at next startup.',
        'This is a local unsigned verification candidate. It has not been published or approved for release.'
    )
    [IO.File]::WriteAllLines((Join-Path $releaseDirectory 'README.txt'),$lines,[Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllLines((Join-Path $releaseDirectory 'README.md'),$lines,[Text.UTF8Encoding]::new($false))
    Copy-Item -LiteralPath (Join-Path $projectRoot 'CHANGELOG.md') -Destination $releaseDirectory -Force
    $manifest=[ordered]@{version=$Version;target='windows/amd64';candidateStatus='verification-only';go=(& go version);sha256=(Get-FileHash -LiteralPath $executable -Algorithm SHA256).Hash;sizeBytes=(Get-Item -LiteralPath $executable).Length;frontendIndexSHA256=(Get-FileHash -LiteralPath frontend/dist/index.html -Algorithm SHA256).Hash;requires=@('Windows 11 x64','WebView2');embeddedAssets=$true;nodeRequired=$false;published=$false}
    [IO.File]::WriteAllText((Join-Path $releaseDirectory 'manifest.json'),($manifest|ConvertTo-Json -Depth 4),[Text.UTF8Encoding]::new($false))
    $checksums=@(Get-ChildItem -LiteralPath $releaseDirectory -File -Recurse|Where-Object {$_.Name -ne 'SHA256SUMS.txt'}|Sort-Object FullName|ForEach-Object {(Get-FileHash -LiteralPath $_.FullName).Hash+'  '+$_.FullName.Substring($releaseDirectory.Length+1).Replace('\','/')})
    [IO.File]::WriteAllLines((Join-Path $releaseDirectory 'SHA256SUMS.txt'),$checksums,[Text.UTF8Encoding]::new($false))
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zipPath=$releaseDirectory+'.zip'
    if(Test-Path -LiteralPath $zipPath){Remove-Item -LiteralPath $zipPath -Force}
    [IO.Compression.ZipFile]::CreateFromDirectory($releaseDirectory,$zipPath,[IO.Compression.CompressionLevel]::Optimal,$true)
    [IO.File]::WriteAllText(($zipPath+'.sha256'),((Get-FileHash -LiteralPath $zipPath).Hash+'  '+[IO.Path]::GetFileName($zipPath)+[Environment]::NewLine),[Text.UTF8Encoding]::new($false))
    Write-Output $releaseDirectory
} finally {Pop-Location}
