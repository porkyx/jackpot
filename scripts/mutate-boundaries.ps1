param([ValidateSet('clock','entropy','recovery','descriptor','pending','desktop','reads','export','lookup','lookupservice','dependency')][string]$Scope='recovery',[ValidateRange(0,1000)][int]$Skip=0)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
$definitions = @{
    lookup = @('internal/contracts/operation_lookup.go', './internal/contracts', @(
        @('unknown kind','observation.Kind != nil','false'),
        @('unknown collection','observation.CollectionID != nil','false'),
        @('unknown round','observation.RoundID != nil','false'),
        @('unknown revision','observation.Revision != nil','false'),
        @('unknown failure','observation.FailureCode != nil','false'),
        @('known kind','observation.Kind == nil','false'),
        @('known collection','observation.CollectionID == nil','false'),
        @('known round','observation.RoundID == nil','false'),
        @('known revision','observation.Revision == nil','false'),
        @('failed code','observation.FailureCode == nil','false'),
        @('unknown state','observation.State != OperationPending && observation.State != OperationSucceeded && observation.State != OperationFailed','false'),
        @('target validation','descriptor.ValidatePending(); err != nil','descriptor.ValidatePending(); err != nil && false')
    ))
    lookupservice = @('internal/desktop/recovery.go', './internal/desktop', @(
        @('reader dependency','nilDependency(reader)','false'),
        @('context','ctx == nil','false'),
        @('pre-cancel','ctx.Err(); err != nil','ctx.Err(); err != nil && false'),
        @('request','request.Validate(); err != nil','request.Validate(); err != nil && false'),
        @('unknown private kind','record.Operation.Kind != ""','false'),
        @('unknown collection','record.CollectionID != ""','false'),
        @('unknown round','record.RoundID != ""','false'),
        @('unknown revision','record.Revision != 0','false'),
        @('unknown failure','record.FailureCode != ""','false'),
        @('unknown fingerprint','record.Operation.PublicFingerprint != [32]byte{}','false'),
        @('identity match','observation.OperationID != request.OperationID','false'),
        @('observation validation','observation.Validate() != nil','false')
    ))
    dependency = @('internal/desktop/dependencies.go', './internal/desktop', @(
        @('nil interface','value == nil','false'),
        @('typed nil','return reflected.IsNil()','return false'),
        @('present value','return false','return true')
    ))
    export = @('internal/platform/export.go', './internal/platform', @(
        @('absolute saved path','!filepath.IsAbs(outcome.Path)','filepath.IsAbs(outcome.Path)'),
        @('cancelled path','outcome.Path != ""','outcome.Path == ""'),
        @('unknown status','default:','case "unknown":')
    ))
    reads = @('internal/storage/sqlite/round_reads.go', './internal/storage/sqlite', @(
        @('nil context','ctx == nil','false'),
        @('empty identity','id == ""','false'),
        @('input size','len(raw) > 100<<20','false','TestStoredDecoder'),
        @('input null','strings.TrimSpace(raw) == "null"','strings.TrimSpace(raw) == " "','TestStoredDecoder'),
        @('input unknown fields','decoder.DisallowUnknownFields()','// removed strict fields','TestStoredDecoder'),
        @('input trailing value','decoder.Decode(new(json.RawMessage)) != io.EOF','decoder.Decode(new(json.RawMessage)) != io.EOF && false','TestStoredDecoder'),
        @('time absence','!raw.Valid','raw.Valid','TestStoredUTC'),
        @('time calendar','NewResponseHeader("storage", value); err != nil','NewResponseHeader("storage", value); err != nil && false','TestStoredUTC'),
        @('time UTC','offset != 0','offset != 0 && false','TestStoredUTC'),
        @('missing targets','errors.Is(err, sql.ErrNoRows)','false'),
        @('fingerprint','len(fingerprint) != 32','false'),
        @('operation failure code','record.FailureCode != ""','false'),
        @('failed code','record.FailureCode.Validate() != nil','false'),
        @('state','record.State.Validate() != nil','false'),
        @('version safe','contracts.ValidateCounter(record.Version) != nil','false'),
        @('revision safe','contracts.ValidateCounter(record.Revision) != nil','false'),
        @('version positive','record.Version == 0','false'),
        @('revision positive','record.Revision == 0','false'),
        @('round positive','record.Number == 0','false'),
        @('attempt positive','record.Attempt == 0','false'),
        @('claim collection','record.Claim.CollectionID != record.CollectionID','false'),
        @('claim round','record.Claim.RoundID != record.ID','false'),
        @('claim attempt','record.Claim.Attempt != record.Attempt','false'),
        @('claim session','record.Claim.Session == ""','false'),
        @('claim operation','record.Claim.OperationID != activeOperation','false'),
        @('claim version positive','record.Claim.Version == 0','false'),
        @('claim version order','record.Claim.Version > record.Version','false'),
        @('outcome commit','(record.State == contracts.Completed) != outcome.Valid','false'),
        @('schedule time','record.ScheduledAt == nil','false'),
        @('schedule timezone','record.Timezone != "Asia/Seoul"','false'),
        @('collection ID','result.ID != id','false'),
        @('source draft','result.SourceDraftID != source','false'),
        @('embedded participants','result.Participants != nil','false'),
        @('participant count','len(result.Participants) >= 100000','len(result.Participants) > 100000','TestFrozenCollectionParticipantCount'),
        @('participant ID','participant.ID != participantID','false'),
        @('participant inclusion','participant.Included != included','false'),
        @('readonly snapshot','ReadOnly: true','ReadOnly: false','TestTypedReadDuringUncommitted'),
        @('transaction cleanup','defer tx.Rollback()','// removed transaction cleanup','TestFrozenCollectionReadRejects')
    ))
    clock = @('internal/clock/clock.go', './internal/clock', @(
        @('nil context','ctx == nil','ctx != nil'),
        @('nil clock','clock == nil','clock != nil'),
        @('negative delay','duration < 0','duration > 0'),
        @('zero delay','duration == 0','duration != 0'),
        @('pre-cancel','ctx.Err(); err != nil','ctx.Err(); err == nil'),
        @('nil timer','timer == nil','timer != nil'),
        @('cleanup','defer timer.Stop()','// removed timer cleanup'),
        @('nil channel','channel == nil','channel != nil')
    ))
    entropy = @('internal/platform/entropy.go', './internal/platform', @(
        @('nil reader','reader == nil','reader != nil'),
        @('nil context','ctx == nil','ctx != nil'),
        @('zero bound','bound == 0','bound != 0'),
        @('context checks','ctx.Err(); err != nil','ctx.Err(); err == nil'),
        @('entropy failure','if err != nil {','if err == nil {'),
        @('read limit','reader.remaining == 0','reader.remaining < 0'),
        @('negative read','n < 0','false'),
        @('oversize read','n > len(buffer)','false')
    ))
    recovery = @('internal/contracts/recovery.go', './internal/contracts', @(
        @('nil limit','request.Limit == nil','request.Limit != nil'),
        @('minimum limit','*request.Limit < 1','*request.Limit < 0'),
        @('maximum limit','*request.Limit > 64','*request.Limit > 65'),
        @('empty input cursor','*request.Cursor == ""','*request.Cursor != ""'),
        @('nil page','page.Operations == nil','false'),
        @('page cap','len(page.Operations) > 64','len(page.Operations) > 65'),
        @('empty page cursor','*page.Cursor == ""','*page.Cursor != ""'),
        @('descriptor validation','descriptor.ValidatePending(); err != nil','descriptor.ValidatePending(); err == nil'),
        @('response validation','Envelope[PendingOperationsPage](response).Validate()','nil')
    ))
    descriptor = @('internal/contracts/types.go', './internal/contracts', @(
        @('operation identity','ValidateID(descriptor.OperationID); err != nil','ValidateID(descriptor.OperationID); err == nil'),
        @('kind','case "CreateCollection",','case "unknown",'),
        @('collection identity','ValidateID(descriptor.CollectionID); err != nil','ValidateID(descriptor.CollectionID); err == nil'),
        @('round identity','ValidateID(descriptor.RoundID); err != nil','ValidateID(descriptor.RoundID); err == nil'),
        @('pending status','descriptor.Status != OperationPending','descriptor.Status == OperationPending'),
        @('revision','ValidateCounter(descriptor.Revision)','nil')
    ))
    pending = @('internal/storage/sqlite/pending.go', './internal/storage/sqlite', @(
        @('cursor length','len(raw) != 23','len(raw) == 23'),
        @('cursor version','bytes[0] != 1','bytes[0] == 1'),
        @('cursor minimum','after < 1','false'),
        @('cursor order','after > through','false'),
        @('cursor overflow','through > math.MaxInt64','through > math.MaxInt64-1'),
        @('readonly transaction','ReadOnly: true','ReadOnly: false'),
        @('lookahead','*request.Limit+1','*request.Limit'),
        @('pending filter',"status='pending' AND",'1=1 AND'),
        @('key lower bound','creation_key>?','creation_key>=?'),
        @('high water','creation_key<=?','creation_key<=?+100'),
        @('key order','ORDER BY creation_key LIMIT','ORDER BY creation_key DESC LIMIT'),
        @('next page','uint32(len(page.Operations)) == *request.Limit','false'),
        @('validated descriptor','descriptor.ValidatePending(); err != nil','descriptor.ValidatePending(); err == nil')
    ))
    desktop = @('internal/desktop/service.go', './internal/desktop', @(
        @('reader dependency','nilDependency(pending)','!nilDependency(pending)'),
        @('context dependency','ctx == nil','ctx != nil'),
        @('pre-cancellation','ctx.Err(); err != nil','ctx.Err(); err == nil'),
        @('query validation','request.Validate(); err != nil','request.Validate(); err == nil'),
        @('page validation','page.Validate(); err != nil','page.Validate(); err == nil'),
        @('known fault','errors.As(err, &fault)','!errors.As(err, &fault)'),
        @('fault code','fault.Code.Validate() == nil','fault.Code.Validate() != nil')
    ))
}
$relativePath,$testPackage,$mutations = $definitions[$Scope]
$sourcePath = Join-Path $projectRoot $relativePath
$source = [IO.File]::ReadAllText($sourcePath)
$mutationRoot = Join-Path $projectRoot ".task/mutation-boundaries/$Scope"
New-Item -ItemType Directory -Force $mutationRoot | Out-Null
$survived = @()
Push-Location $projectRoot
try {
    foreach ($mutation in ($mutations | Select-Object -Skip $Skip)) {
        $name,$from,$to = $mutation[0],$mutation[1],$mutation[2]
        if (-not $source.Contains($from)) { throw "Mutation target missing: $Scope/$name" }
        $mutantPath = Join-Path $mutationRoot 'mutant.go'
        [IO.File]::WriteAllText($mutantPath,$source.Replace($from,$to))
        $overlayPath = Join-Path $mutationRoot 'overlay.json'
        [IO.File]::WriteAllText($overlayPath,(@{Replace=@{$sourcePath=$mutantPath}} | ConvertTo-Json -Depth 4))
        $testArgs=@('-timeout=30s',"-overlay=$overlayPath")
        if($Scope -eq 'pending'){$testArgs+='-run=TestPending|TestCursor|FuzzPendingCursor'}
        if($Scope -eq 'export'){$testArgs+='-run=TestSave'}
        if($Scope -eq 'reads'){
            $pattern='TestCommittedTyped|TestReadOperationUnknown|TestAllStored|TestOperationReadRejects|TestRoundReadRejects|TestFrozenCollectionReadRejects|TestStoredDecoder|TestStoredUTC'
            if($mutation.Count -gt 3){$pattern=$mutation[3]}
            $testArgs+="-run=$pattern"
        }
        $output = & go test @testArgs $testPackage 2>&1 | Out-String
        $exitCode = $LASTEXITCODE
        if ($output.Contains('[build failed]')) { throw "Invalid compile mutation: $Scope/$name" }
        if ($output.Contains('test timed out')) { throw "Mutation harness timeout: $Scope/$name" }
        $killed = $exitCode -ne 0 -and $output.Contains('--- FAIL:')
        if ($killed) { Write-Output "killed: $Scope/$name" } else { $survived += $name; Write-Output "SURVIVED: $Scope/$name" }
    }
} finally { Pop-Location }
if ($survived.Count -gt 0) { throw "Surviving mutations: $Scope/$($survived -join ', ')" }
$global:LASTEXITCODE = 0
