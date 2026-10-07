param([switch]$Build)
$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$task=Join-Path $workspace '.task'
if((Get-Item -LiteralPath $task).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Diagnostic task root must not be a reparse point'}
$name='verified-test-round-admission-'+[guid]::NewGuid().ToString('N')
$directory=[IO.Path]::GetFullPath((Join-Path $task $name))
if([IO.Path]::GetDirectoryName($directory) -ne $task -or -not [IO.Path]::GetFileName($directory).StartsWith('verified-test-round-admission-')){throw 'Profile preparation escaped own task directory'}
$null=New-Item -ItemType Directory -Path $directory
$utf8=New-Object Text.UTF8Encoding($false)
$helper=Join-Path $directory 'trace';$null=New-Item -ItemType Directory -Path $helper
foreach($template in @('round_phase.go','round_phase_test.go')){[IO.File]::WriteAllText((Join-Path $helper $template),[IO.File]::ReadAllText((Join-Path $PSScriptRoot ('profile/'+$template+'.txt'))),$utf8)}
$import='producttrace "github.com/porkyx/jackpot/.task/'+$name+'/trace"'
$paths=@('internal/desktop/round.go','internal/desktop/service.go','internal/roundlifecycle/commands.go','internal/roundlifecycle/service.go','internal/storage/sqlite/round_reads.go','internal/storage/sqlite/round_writes.go','internal/storage/sqlite/round_queries.go','internal/storage/sqlite/round_page.go')
$sources=@{};$hashes=@{};$map=@{}
# Preserve every actual production Go source, including untracked manual/page
# producers, as provenance. Only the instrumented subset becomes a Go overlay.
$productionPaths=@('main.go','go.mod','go.sum')+@(& rg --files internal cmd/producte2e | Where-Object {$_ -match '\.go$' -and $_ -notmatch '_test\.go$'} | ForEach-Object {$_.Replace('\','/')})
if($productionPaths -notcontains 'internal/roundlifecycle/manual_snapshot.go' -or $productionPaths -notcontains 'internal/storage/sqlite/round_page.go' -or $productionPaths -notcontains 'internal/application/finalize.go'){throw 'Latest manual/page production provenance missing'}
$productionHashes=@{}
$productionCopy=Join-Path $directory 'sourceSnapshot'
foreach($productionPath in $productionPaths){
 $productionFile=Join-Path $workspace $productionPath
 $productionHashes[$productionPath]=(Get-FileHash -LiteralPath $productionFile -Algorithm SHA256).Hash
 $productionTarget=Join-Path $productionCopy $productionPath
 $null=New-Item -ItemType Directory -Path ([IO.Path]::GetDirectoryName($productionTarget)) -Force
 [IO.File]::WriteAllBytes($productionTarget,[IO.File]::ReadAllBytes($productionFile))
}
foreach($path in $paths){$file=Join-Path $workspace $path;$sources[$path]=[IO.File]::ReadAllText($file);$hashes[$path]=(Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash}
function Rewrite-Function([string]$File,[string]$Signature,[string]$Phase,[switch]$Root,[switch]$Decode,[switch]$BeginTx){
 $text=$sources[$File];$start=$text.IndexOf($Signature,[StringComparison]::Ordinal)
 if($start -lt 0 -or $text.IndexOf($Signature,$start+1,[StringComparison]::Ordinal) -ge 0){throw ('Unique profile function anchor missing: '+$Signature)}
 $end=$text.IndexOf("`nfunc ",$start+$Signature.Length,[StringComparison]::Ordinal);if($end -lt 0){$end=$text.Length}
 $segment=$text.Substring($start,$end-$start)
 $method='Begin';if($Root){$method='Root'}
 $segment=$segment.Replace($Signature,$Signature+"`n`tctx, profileFinish := producttrace."+$method+'(ctx, "'+$Phase+'")'+"`n`tdefer profileFinish()")
 if($Decode){$segment=$segment.Replace('decodeStored(','profileDecode(ctx, ').Replace('rl.ValidateRoundInput(record.Input)','profileInput(ctx, record.Input)').Replace('rl.ValidateOutcome(record.Input, *record.Outcome)','profileOutcome(ctx, record.Input, *record.Outcome)')}
 if($BeginTx){$segment=$segment.Replace('store.db.BeginTx(ctx,','profileBeginTx(ctx, store.db,')}
 $sources[$File]=$text.Substring(0,$start)+$segment+$text.Substring($end)
}
Rewrite-Function 'internal/desktop/round.go' 'func (service *RoundService) Rerun(ctx context.Context, request contracts.CollectionCommandRequest) (contracts.CollectionResponse, error) {' 'desktop.Rerun' -Root
Rewrite-Function 'internal/desktop/round.go' 'func (service *RoundService) CreateCollection(ctx context.Context, request contracts.CreateCollectionRequest) (contracts.CollectionResponse, error) {' 'desktop.CreateCollection' -Root
Rewrite-Function 'internal/desktop/round.go' 'func (service *RoundService) collection(ctx context.Context, id contracts.CollectionID) (contracts.CollectionData, error) {' 'desktop.collection'
Rewrite-Function 'internal/desktop/round.go' 'func (service *RoundService) collectionPage(ctx context.Context, id contracts.CollectionID, query rl.CollectionPageQuery) (contracts.CollectionData, error) {' 'desktop.collectionPage'
Rewrite-Function 'internal/desktop/service.go' 'func (service *Service) Bootstrap(ctx context.Context) (contracts.BootstrapResponse, error) {' 'desktop.Bootstrap' -Root
Rewrite-Function 'internal/roundlifecycle/commands.go' 'func (service *Service) Rerun(ctx context.Context, request RerunRequest) (RoundRecord, error) {' 'lifecycle.Rerun'
Rewrite-Function 'internal/roundlifecycle/commands.go' 'func (service *Service) Recover(ctx context.Context, _ RecoverRequest) (RecoveryReport, error) {' 'lifecycle.Recover' -Root
$servicePath='internal/roundlifecycle/service.go';$service=$sources[$servicePath]
$signature='func (service *Service) ExecuteAdmission(_ context.Context, admission Admission) (RoundRecord, error) {'
$start=$service.IndexOf($signature,[StringComparison]::Ordinal);if($start -lt 0){throw 'Execution profile inheritance anchor missing'}
$end=$service.IndexOf("`nfunc ",$start+$signature.Length,[StringComparison]::Ordinal)
$segment=$service.Substring($start,$end-$start)
$segment=$segment.Replace($signature,'func (service *Service) ExecuteAdmission(caller context.Context, admission Admission) (RoundRecord, error) {')
$segment=$segment.Replace('ctx := service.options.ExecutionContext','ctx := producttrace.Inherit(service.options.ExecutionContext, caller)'+"`n`tctx, profileFinish := producttrace.Begin(ctx, " + '"lifecycle.ExecuteAdmission")'+"`n`tdefer profileFinish()")
$sources[$servicePath]=$service.Substring(0,$start)+$segment+$service.Substring($end)
Rewrite-Function 'internal/storage/sqlite/round_writes.go' 'func txOperation(ctx context.Context, tx *sql.Tx, id contracts.OperationID) (rl.OperationRecord, error) {' 'sqlite.txOperation'
Rewrite-Function 'internal/storage/sqlite/round_writes.go' 'func txRound(ctx context.Context, tx *sql.Tx, id contracts.RoundID) (rl.RoundRecord, error) {' 'sqlite.txRound' -Decode
Rewrite-Function 'internal/storage/sqlite/round_writes.go' 'func txValidateCollection(ctx context.Context, tx *sql.Tx, id contracts.CollectionID) error {' 'sqlite.allpast'
Rewrite-Function 'internal/storage/sqlite/round_writes.go' 'func (store *Store) AdmitRound(ctx context.Context, request rl.AdmitRoundRequest) (rl.Admission, error) {' 'sqlite.AdmitRound' -BeginTx
# Fixed block observations only; original per-row SQL and all return paths remain.
$admitPath='internal/storage/sqlite/round_writes.go'
$admitSignature='func (store *Store) AdmitRound(ctx context.Context, request rl.AdmitRoundRequest) (rl.Admission, error) {'
$admitText=$sources[$admitPath];$admitStart=$admitText.IndexOf($admitSignature,[StringComparison]::Ordinal);$admitEnd=$admitText.IndexOf("`nfunc ",$admitStart+$admitSignature.Length,[StringComparison]::Ordinal)
$script:admitSegment=$admitText.Substring($admitStart,$admitEnd-$admitStart)
$script:admitSegment=$script:admitSegment.Replace('defer profileFinish()',"defer profileFinish()`n admissionStages := newProfileAdmissionStages(ctx)`n defer admissionStages.Finish()")
function Add-AdmissionStage([string]$Before,[string]$Name){
 $position=$script:admitSegment.IndexOf($Before,[StringComparison]::Ordinal)
 if($position -lt 0 -or $script:admitSegment.IndexOf($Before,$position+1,[StringComparison]::Ordinal) -ge 0){throw ('Private admission unique block anchor missing: '+$Name)}
 $script:admitSegment=$script:admitSegment.Substring(0,$position)+'admissionStages.Next("'+$Name+'")'+"`n"+$script:admitSegment.Substring($position)
}
Add-AdmissionStage 'tx, err := profileBeginTx(ctx, store.db, nil)' 'admit.beginTx'
$script:admitSegment=$script:admitSegment.Replace('defer tx.Rollback()',"defer profileAdmissionRollback(ctx, tx)`n defer admissionStages.Finish()`n admissionStages.Next("+'"admit.historyAndReplayLookup")')
Add-AdmissionStage 'round, err := txRound(ctx, tx, existing.RoundID)' 'admit.replayRoundRead'
Add-AdmissionStage 'revision := contracts.Revision(1)' 'admit.revisionOrCollection'
Add-AdmissionStage 'statement, err := tx.PrepareContext(ctx, "INSERT INTO participants' 'admit.participantPrepare'
Add-AdmissionStage 'for position, participant := range participants {' 'admit.participantExecLoop'
$close='if err = statement.Close(); err != nil {'
if(([regex]::Matches($script:admitSegment,[regex]::Escape($close))).Count -ne 2){throw 'Both original statement close boundaries required'}
$position=$script:admitSegment.IndexOf($close,[StringComparison]::Ordinal)
$script:admitSegment=$script:admitSegment.Substring(0,$position)+'admissionStages.Next("admit.participantClose")'+"`n"+$script:admitSegment.Substring($position)
Add-AdmissionStage 'if err = insertOperation(ctx, tx, request.Operation' 'admit.operationInsert'
Add-AdmissionStage 'raw, err := json.Marshal(request.Input)' 'admit.inputMarshal'
Add-AdmissionStage 'version := contracts.RoundVersion(1)' 'admit.roundDecision'
Add-AdmissionStage 'if _, err = tx.ExecContext(ctx, "INSERT INTO rounds(' 'admit.roundInsert'
Add-AdmissionStage 'statement, err := tx.PrepareContext(ctx, "INSERT INTO round_candidates' 'admit.candidatePrepare'
Add-AdmissionStage 'for position, id := range request.Input.CandidateIDs {' 'admit.candidateExecLoop'
$position=$script:admitSegment.LastIndexOf($close,[StringComparison]::Ordinal)
$script:admitSegment=$script:admitSegment.Substring(0,$position)+'admissionStages.Next("admit.candidateClose")'+"`n"+$script:admitSegment.Substring($position)
Add-AdmissionStage 'for position, prize := range request.Input.Prizes {' 'admit.prizeInsertLoop'
Add-AdmissionStage 'if request.Operation.Kind == "ExecuteDue" {' 'admit.dueClaim'
Add-AdmissionStage 'if request.Operation.Kind != "ExecuteDue" {' 'admit.attemptInsert'
Add-AdmissionStage 'round, err := txRound(ctx, tx, request.RoundID)' 'admit.finalRoundRead'
Add-AdmissionStage 'operation, err := txOperation(ctx, tx, request.Operation.ID)' 'admit.finalOperationRead'
$commit='if err = tx.Commit(); err != nil {'
if(([regex]::Matches($script:admitSegment,[regex]::Escape($commit))).Count -ne 2){throw 'Both original commit boundaries required'}
$script:admitSegment=$script:admitSegment.Replace($commit,'admissionStages.Next("admit.commit")'+"`n"+$commit)
$sources[$admitPath]=$admitText.Substring(0,$admitStart)+$script:admitSegment+$admitText.Substring($admitEnd)

Rewrite-Function 'internal/storage/sqlite/round_writes.go' 'func (store *Store) ClaimAttempt(ctx context.Context, request rl.ClaimRequest) (*rl.ClaimToken, error) {' 'sqlite.ClaimAttempt' -BeginTx
Rewrite-Function 'internal/storage/sqlite/round_writes.go' 'func (store *Store) CommitOutcome(ctx context.Context, request rl.CommitOutcomeRequest) (rl.RoundRecord, error) {' 'sqlite.CommitOutcome' -BeginTx
Rewrite-Function 'internal/storage/sqlite/round_reads.go' 'func (store *Store) ReadRound(ctx context.Context, id contracts.RoundID) (rl.RoundRecord, error) {' 'sqlite.ReadRound' -BeginTx
Rewrite-Function 'internal/storage/sqlite/round_reads.go' 'func (store *Store) ReadCollection(ctx context.Context, id contracts.CollectionID) (rl.FrozenCollection, error) {' 'sqlite.ReadCollection' -BeginTx -Decode
Rewrite-Function 'internal/storage/sqlite/round_queries.go' 'func (store *Store) ListRounds(ctx context.Context, id contracts.CollectionID) ([]rl.RoundRecord, error) {' 'sqlite.ListRounds' -BeginTx
Rewrite-Function 'internal/storage/sqlite/round_queries.go' 'func (store *Store) ListRecoverableRounds(ctx context.Context) ([]rl.RoundRecord, error) {' 'sqlite.ListRecoverableRounds' -BeginTx
Rewrite-Function 'internal/storage/sqlite/round_queries.go' 'func txCollection(ctx context.Context, tx *sql.Tx, id contracts.CollectionID) (rl.FrozenCollection, contracts.Revision, error) {' 'sqlite.txCollection' -Decode
Rewrite-Function 'internal/storage/sqlite/round_queries.go' 'func (store *Store) ReadCollectionState(ctx context.Context, id contracts.CollectionID) (rl.FrozenCollection, []rl.RoundRecord, contracts.Revision, error) {' 'sqlite.ReadCollectionState' -BeginTx
Rewrite-Function 'internal/storage/sqlite/round_page.go' 'func (store *Store) ReadCollectionPage(ctx context.Context, id contracts.CollectionID, query rl.CollectionPageQuery) (rl.CollectionPage, error) {' 'sqlite.ReadCollectionPage' -BeginTx
foreach($path in $paths){
 $text=$sources[$path];$anchor="import (`n";if(-not $text.Contains($anchor)){throw ('Go import anchor missing: '+$path)}
 $text=$text.Replace($anchor,$anchor+"`t"+$import+"`n")
 $destination=Join-Path $directory ($path.Replace('/','_'));[IO.File]::WriteAllText($destination,$text,$utf8)
 $map[(Join-Path $workspace $path)]=$destination
}
$sqliteHelper=[IO.File]::ReadAllText((Join-Path $PSScriptRoot 'profile/sqlite_profile_helpers.go.txt')).Replace('PRODUCT_TRACE_IMPORT',$import)
$helperFile=Join-Path $directory 'sqlite_profile_helpers.go';[IO.File]::WriteAllText($helperFile,$sqliteHelper,$utf8)
$map[(Join-Path $workspace 'internal/storage/sqlite/round_profile_private.go')]=$helperFile
$roundSQLMatch=[regex]::Matches([IO.File]::ReadAllText((Join-Path $workspace 'internal/storage/sqlite/round_writes.go')),'"(SELECT r\.collection_id,r\.id,[^\r\n"]+WHERE r\.id=\?)"')
if($roundSQLMatch.Count -ne 1){throw 'Actual txRound query must occur once'}
foreach($template in @('admission_stages.go','admission_stages_test.go')){
 $content=[IO.File]::ReadAllText((Join-Path $PSScriptRoot ('profile/'+$template+'.txt'))).Replace('PRODUCT_TRACE_IMPORT',$import).Replace('ACTUAL_ROUND_SQL',$roundSQLMatch[0].Groups[1].Value)
 $destination=Join-Path $directory $template;[IO.File]::WriteAllText($destination,$content,$utf8)
 $virtual='internal/storage/sqlite/round_admission_profile_private.go';if($template.EndsWith('_test.go')){$virtual='internal/storage/sqlite/round_admission_profile_private_test.go'}
 $map[(Join-Path $workspace $virtual)]=$destination
}

$overlay=Join-Path $directory 'overlay.json';[IO.File]::WriteAllText($overlay,(@{Replace=$map}|ConvertTo-Json -Depth 5),$utf8)
$assetHTML=Join-Path $workspace 'frontend/dist/index.html'
$assetSHA=$null;if(Test-Path -LiteralPath $assetHTML){$assetSHA=(Get-FileHash -LiteralPath $assetHTML -Algorithm SHA256).Hash}
$manifest=@{status='prepared';directory=$directory;overlay=$overlay;originalSourceSHA256=$hashes;productionSourceSHA256=$productionHashes;productionSourceCopy=$productionCopy;manualAndPageSourceIncluded=$true;frontendAssetsPending=$true;frontendHTMLAtPreparation=$assetSHA;helperPackage='./.task/'+$name+'/trace';goSourceChanged=$false;productionAssetsChanged=$false;privateContextOnly=$true;profileENV='JACKPOT_PHASE_PROFILE=1 child only';admissionBlockObserversPerRow=0;admissionStageLabels='closed static set only';idsPasswordPayloadLogged=$false;queryPlansAndPragmas='owned actualSQLite fixture; no performance timing';formalPerformanceAcceptance=$false;buildExecuted=$false;actualMeasurementExecuted=$false;createdUTC=[DateTime]::UtcNow.ToString('o')}
Push-Location $workspace
try{
 if($Build){
  $gofmtPaths=@($map.Values)+@((Join-Path $helper 'round_phase.go'),(Join-Path $helper 'round_phase_test.go'))
  & gofmt -w @gofmtPaths
  if($LASTEXITCODE -ne 0){throw 'Private admission generated source format failed'}
  $priorProfile=$env:JACKPOT_PHASE_PROFILE
  try {
   $env:JACKPOT_PHASE_PROFILE='1'
   & go test -race $manifest.helperPackage -count=1 -timeout 30s *> (Join-Path $directory 'observer-control.log')
   if($LASTEXITCODE -ne 0){throw 'Private profiler observer controls failed'}
   & go test ./internal/storage/sqlite -run '^TestRoundReadOptimizationPreserves|^TestRoundBuildEquivalenceActualSQLiteThreeTwoOneTrace' -count=1 -timeout 60s *> (Join-Path $directory 'baseline-contracts.log')
   if($LASTEXITCODE -ne 0){throw 'Original SQLite baseline fixture failed'}
   & go test '-overlay' $overlay -run '^TestAdmissionPrivate|^TestRoundReadOptimizationPreserves|^TestRoundBuildEquivalenceActualSQLiteThreeTwoOneTrace' -race -count=1 -v -timeout 90s ./internal/storage/sqlite *> (Join-Path $directory 'overlay-controls-and-queryplans.log')
   if($LASTEXITCODE -ne 0){throw 'Private admission control/invariant fixture failed'}
  } finally {$env:JACKPOT_PHASE_PROFILE=$priorProfile}
  $exe=Join-Path $directory 'product-admission.exe'
  & go build -tags production '-overlay' $overlay -o $exe ./cmd/producte2e
  if($LASTEXITCODE -ne 0){throw 'Private profiler host build failed'}
  $manifest.buildExecuted=$true;$manifest.status='built';$manifest.executable=$exe;$manifest.executableSHA256=(Get-FileHash -LiteralPath $exe -Algorithm SHA256).Hash
 }
}finally{
 Pop-Location
 foreach($path in $productionPaths){if((Get-FileHash -LiteralPath (Join-Path $workspace $path) -Algorithm SHA256).Hash -ne $productionHashes[$path]){throw ('Original production source changed during private profile preparation: '+$path)}}
 [IO.File]::WriteAllText((Join-Path $directory 'manifest.json'),($manifest|ConvertTo-Json -Depth 7),$utf8)
 [IO.File]::WriteAllText((Join-Path $task 'round-admission-profile-prepared.json'),($manifest|ConvertTo-Json -Depth 7),$utf8)
}
Write-Output ('Private admission phase profile '+$manifest.status+': '+$directory+'; no native run / no original source, assets, binding or shared host write')