$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$task=Join-Path $workspace '.task'
$manifest=Get-Content -LiteralPath (Join-Path $task 'round-phase-profile-prepared.json') -Raw|ConvertFrom-Json
$directory=[IO.Path]::GetFullPath($manifest.directory)
if([IO.Path]::GetDirectoryName($directory) -ne $task -or -not [IO.Path]::GetFileName($directory).StartsWith('verified-test-round-phase-') -or (Get-Item -LiteralPath $directory).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Diagnostic profile driver path escaped own task root'}
if($manifest.status -ne 'built' -or (Get-FileHash -LiteralPath $manifest.executable -Algorithm SHA256).Hash -ne $manifest.executableSHA256){throw 'Exact profiler host not built or changed'}
$driverDirectory=Join-Path $directory 'driver';$null=New-Item -ItemType Directory -Path $driverDirectory -Force
$utf8=New-Object Text.UTF8Encoding($false)
$native=Join-Path $workspace 'frontend/tests/native'
$playwright=([Uri](Join-Path $workspace 'frontend/node_modules/@playwright/test/index.mjs')).AbsoluteUri
$sourceHashes=@{}
foreach($name in @('product.stress.mjs','stress.metrics.mjs','stress.trace.mjs','stress.media.mjs','stress.preview.mjs','stress.noimage.mjs')){
 $file=Join-Path $native $name;$sourceHashes[$name]=(Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash
 $content=[IO.File]::ReadAllText($file).Replace('from "@playwright/test"','from "'+$playwright+'"')
 if($name -eq 'product.stress.mjs'){
  $rootAnchor='const root=fileURLToPath(new URL("../../../",import.meta.url)),task=resolve(root,".task");'
  if(-not $content.Contains($rootAnchor)){throw 'Profile driver root anchor missing'}
  $content=$content.Replace($rootAnchor,$rootAnchor+"`n"+'const evidenceRoot=resolve(task,"'+[IO.Path]::GetFileName($directory)+'");const phaseLog=createWriteStream(resolve(evidenceRoot,"go-phase.jsonl"),{flags:"w"});')
  $content=$content.Replace('resolve(task,','resolve(evidenceRoot,')
  $content=$content.Replace('const evidenceRoot=resolve(evidenceRoot,','const evidenceRoot=resolve(task,')
  $content=$content.Replace('const exe=resolve(evidenceRoot,"product-stress.exe")','const exe=resolve(evidenceRoot,"product-phase.exe")')
  $content=$content.Replace('const work=await mkdtemp(resolve(evidenceRoot,"product-run-stress-"))','const work=await mkdtemp(resolve(task,"product-run-phase-"))')
  $content=$content.Replace('product-run-stress-','product-run-phase-')
  $content=$content.Replace('env:{...process.env,','env:{...process.env,JACKPOT_PHASE_PROFILE:"1",')
  $stderr='child.stderr.on("data",()=>{});'
  $replacement='let phaseBuffer="";report.phaseRecords=0;report.phaseRecordNames={};child.stderr.on("data",chunk=>{phaseBuffer+=chunk.toString();if(phaseBuffer.length>1024*1024){report.phaseLogFailure="bounded stderr overflow";phaseBuffer="";return}let index;while((index=phaseBuffer.indexOf("\n"))>=0){const line=phaseBuffer.slice(0,index).trim();phaseBuffer=phaseBuffer.slice(index+1);if(!line.startsWith("JACKPOT_PHASE "))continue;try{const phase=JSON.parse(line.slice(14));assert.ok(Number.isFinite(phase.elapsedMs)&&phase.elapsedMs>=0);assert.ok(Object.keys(phase.phases).length<=128);assert.equal(phase.truncated,false);phaseLog.write(JSON.stringify({...phase,observedAt:Date.now(),uiPhase:report.phase})+"\n");report.phaseRecords++;report.phaseRecordNames[phase.name]=(report.phaseRecordNames[phase.name]??0)+1}catch{report.phaseLogFailure="invalid/truncated phase record"}}});'
  if(-not $content.Contains($stderr)){throw 'Profile stderr anchor missing'};$content=$content.Replace($stderr,$replacement)
  $start=$content.IndexOf(' await phase("native-frozen-comment-media");',[StringComparison]::Ordinal)
  $end=$content.IndexOf(' assert.equal(Boolean(report.metricSamplerFailure),false);report.status="passed";}',[StringComparison]::Ordinal)
  if($start -lt 0 -or $end -lt $start){throw 'Profile draw-only completion anchors missing'}
  $completion=' report.phaseProfileDiagnostic=true;report.formalPerformanceAcceptance=false;report.phase="draw-profile-complete";report.final=await snapshot();assert.equal(report.final.counts.collections,1);assert.equal(report.final.counts.results,drawSampleCount+1);assert.equal(report.final.counts.winners,(drawSampleCount+1)*10);assert.equal(report.final.counts.participants,10000);report.targets={diagnosticMode:true,drawP95Under1s:false,peakUnder500MiB:false};'+"`n"
  $content=$content.Substring(0,$start)+$completion+$content.Substring($end)
  $finalAnchor=' await new Promise(ok=>memoryLog.end(ok));finalizePeak(report);'
  if(-not $content.Contains($finalAnchor)){throw 'Profile log/resource finalization anchor missing'}
  $content=$content.Replace($finalAnchor,' await new Promise(ok=>phaseLog.end(ok));if(report.phaseLogFailure||report.phaseRecords===0||report.phaseRecordNames?.["desktop.Rerun"]!==report.drawSampleCount){report.status="failed";process.exitCode=1};'+$finalAnchor)
  # Diagnostics never satisfy formal acceptance, including finalizePeak recomputation.
  $content=$content.Replace('finalizePeak(report);await writeFile(reportPath','finalizePeak(report);if(report.targets){report.targets.peakUnder500MiB=false;report.targets.drawP95Under1s=false;report.targets.diagnosticMode=true}report.formalPerformanceAcceptance=false;await writeFile(reportPath')
 }
 [IO.File]::WriteAllText((Join-Path $driverDirectory $name),$content,$utf8)
}
$node=Join-Path $env:LOCALAPPDATA 'Volta/tools/image/node/22.16.0/node.exe'
& $node --check (Join-Path $driverDirectory 'product.stress.mjs');if($LASTEXITCODE -ne 0){throw 'Private profile driver syntax failed'}
foreach($name in $sourceHashes.Keys){if((Get-FileHash -LiteralPath (Join-Path $native $name) -Algorithm SHA256).Hash -ne $sourceHashes[$name]){throw 'Original stress driver/helper changed'}}
$report=@{status='prepared';driver=Join-Path $driverDirectory 'product.stress.mjs';directory=$directory;executable=$manifest.executable;executableSHA256=$manifest.executableSHA256;originalDriverSHA256=$sourceHashes;originalDriverUnchanged=$true;sharedHostAndAssetsUnchanged=$true;nativeRunStarted=$false;formalPerformanceAcceptance=$false;workDirectory='workspace .task/product-run-phase-*';reports='owned profile directory only';drawOnly=$true;noPreviewPNGOr100Cycles=$true}
[IO.File]::WriteAllText((Join-Path $task 'round-phase-driver-prepared.json'),($report|ConvertTo-Json -Depth 6),$utf8)
Write-Output ('Private draw-only profiler driver prepared: '+$report.driver+'; node syntax PASS / native run0 / original SHA unchanged')