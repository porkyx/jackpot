param([switch]$Build)
$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$task=Join-Path $workspace '.task'
if((Get-Item -LiteralPath $task).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Diagnostic task root must not be a reparse point'}
$name='verified-test-round-phase-'+[guid]::NewGuid().ToString('N')
$directory=[IO.Path]::GetFullPath((Join-Path $task $name))
if([IO.Path]::GetDirectoryName($directory) -ne $task -or -not [IO.Path]::GetFileName($directory).StartsWith('verified-test-round-phase-')){throw 'Profile preparation escaped own task directory'}
$null=New-Item -ItemType Directory -Path $directory
$utf8=New-Object Text.UTF8Encoding($false)
$helper=Join-Path $directory 'trace';$null=New-Item -ItemType Directory -Path $helper
foreach($template in @('round_phase.go','round_phase_test.go')){[IO.File]::WriteAllText((Join-Path $helper $template),[IO.File]::ReadAllText((Join-Path $PSScriptRoot ('profile/'+$template+'.txt'))),$utf8)}
$import='producttrace "github.com/porkyx/jackpot/.task/'+$name+'/trace"'
$paths=@('internal/desktop/round.go','internal/desktop/service.go','internal/roundlifecycle/commands.go','internal/roundlifecycle/service.go','internal/storage/sqlite/round_reads.go','internal/storage/sqlite/round_writes.go','internal/storage/sqlite/round_queries.go')
$sources=@{};$hashes=@{};$map=@{}
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
Rewrite-Function 'internal/storage/sqlite/round_writes.go' 'func (store *Store) ClaimAttempt(ctx context.Context, request rl.ClaimRequest) (*rl.ClaimToken, error) {' 'sqlite.ClaimAttempt' -BeginTx
Rewrite-Function 'internal/storage/sqlite/round_writes.go' 'func (store *Store) CommitOutcome(ctx context.Context, request rl.CommitOutcomeRequest) (rl.RoundRecord, error) {' 'sqlite.CommitOutcome' -BeginTx
Rewrite-Function 'internal/storage/sqlite/round_reads.go' 'func (store *Store) ReadRound(ctx context.Context, id contracts.RoundID) (rl.RoundRecord, error) {' 'sqlite.ReadRound' -BeginTx
Rewrite-Function 'internal/storage/sqlite/round_reads.go' 'func (store *Store) ReadCollection(ctx context.Context, id contracts.CollectionID) (rl.FrozenCollection, error) {' 'sqlite.ReadCollection' -BeginTx -Decode
Rewrite-Function 'internal/storage/sqlite/round_queries.go' 'func (store *Store) ListRounds(ctx context.Context, id contracts.CollectionID) ([]rl.RoundRecord, error) {' 'sqlite.ListRounds' -BeginTx
Rewrite-Function 'internal/storage/sqlite/round_queries.go' 'func (store *Store) ListRecoverableRounds(ctx context.Context) ([]rl.RoundRecord, error) {' 'sqlite.ListRecoverableRounds' -BeginTx
Rewrite-Function 'internal/storage/sqlite/round_queries.go' 'func txCollection(ctx context.Context, tx *sql.Tx, id contracts.CollectionID) (rl.FrozenCollection, contracts.Revision, error) {' 'sqlite.txCollection' -Decode
Rewrite-Function 'internal/storage/sqlite/round_queries.go' 'func (store *Store) ReadCollectionState(ctx context.Context, id contracts.CollectionID) (rl.FrozenCollection, []rl.RoundRecord, contracts.Revision, error) {' 'sqlite.ReadCollectionState' -BeginTx
foreach($path in $paths){
 $text=$sources[$path];$anchor="import (`n";if(-not $text.Contains($anchor)){throw ('Go import anchor missing: '+$path)}
 $text=$text.Replace($anchor,$anchor+"`t"+$import+"`n")
 $destination=Join-Path $directory ($path.Replace('/','_'));[IO.File]::WriteAllText($destination,$text,$utf8)
 $map[(Join-Path $workspace $path)]=$destination
}
$sqliteHelper=[IO.File]::ReadAllText((Join-Path $PSScriptRoot 'profile/sqlite_profile_helpers.go.txt')).Replace('PRODUCT_TRACE_IMPORT',$import)
$helperFile=Join-Path $directory 'sqlite_profile_helpers.go';[IO.File]::WriteAllText($helperFile,$sqliteHelper,$utf8)
$map[(Join-Path $workspace 'internal/storage/sqlite/round_profile_private.go')]=$helperFile
$overlay=Join-Path $directory 'overlay.json';[IO.File]::WriteAllText($overlay,(@{Replace=$map}|ConvertTo-Json -Depth 5),$utf8)
$manifest=@{status='prepared';directory=$directory;overlay=$overlay;originalSourceSHA256=$hashes;helperPackage='./.task/'+$name+'/trace';goSourceChanged=$false;productionAssetsChanged=$false;privateContextOnly=$true;profileENV='JACKPOT_PHASE_PROFILE=1 child only';formalPerformanceAcceptance=$false;buildExecuted=$false;actualMeasurementExecuted=$false;createdUTC=[DateTime]::UtcNow.ToString('o')}
Push-Location $workspace
try{
 if($Build){
  & go test -race $manifest.helperPackage -count=1
  if($LASTEXITCODE -ne 0){throw 'Private profiler helper tests failed'}
  & go test '-overlay' $overlay -run '^TestRoundReadOptimizationPreserves|^TestRoundBuildEquivalence' -count=1 ./internal/storage/sqlite
  if($LASTEXITCODE -ne 0){throw 'Profile overlay invariant fixture failed'}
  $exe=Join-Path $directory 'product-phase.exe'
  & go build -tags production '-overlay' $overlay -o $exe ./cmd/producte2e
  if($LASTEXITCODE -ne 0){throw 'Private profiler host build failed'}
  $manifest.buildExecuted=$true;$manifest.status='built';$manifest.executable=$exe;$manifest.executableSHA256=(Get-FileHash -LiteralPath $exe -Algorithm SHA256).Hash
 }
}finally{
 Pop-Location
 foreach($path in $paths){if((Get-FileHash -LiteralPath (Join-Path $workspace $path) -Algorithm SHA256).Hash -ne $hashes[$path]){throw ('Original source changed during private profile preparation: '+$path)}}
 [IO.File]::WriteAllText((Join-Path $directory 'manifest.json'),($manifest|ConvertTo-Json -Depth 7),$utf8)
 [IO.File]::WriteAllText((Join-Path $task 'round-phase-profile-prepared.json'),($manifest|ConvertTo-Json -Depth 7),$utf8)
}
Write-Output ('Private phase profile '+$manifest.status+': '+$directory+'; no native run / no original source, assets, binding or shared host write')