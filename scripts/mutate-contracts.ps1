param([ValidateSet('types','protocol')][string]$Scope='types')
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
$sourcePath = Join-Path $projectRoot "internal/contracts/$Scope.go"
$source = [IO.File]::ReadAllText($sourcePath)
$mutationRoot = Join-Path $projectRoot ".task/mutation-contracts/$Scope"
New-Item -ItemType Directory -Force $mutationRoot | Out-Null
$mutations = @(
    @('empty ID', 'id == ""', 'id != ""'),
    @('safe bound', 'uint64(value) > MaxSafeInteger', 'uint64(value) >= MaxSafeInteger'),
    @('increment bound', 'uint64(value) >= MaxSafeInteger', 'uint64(value) > MaxSafeInteger'),
    @('session identity', 'current.BackendSessionID != expected.BackendSessionID', 'current.BackendSessionID == expected.BackendSessionID'),
    @('draft identity', 'current.DraftID != expected.DraftID', 'current.DraftID == expected.DraftID'),
    @('article generation', 'current.ArticleGeneration != expected.ArticleGeneration', 'current.ArticleGeneration == expected.ArticleGeneration'),
    @('revision equality', 'current.Revision != expected.Revision', 'current.Revision == expected.Revision'),
    @('completed gate', 'state != Completed', 'state == Completed'),
    @('cancelled gate', 'state != Cancelled', 'state == Cancelled'),
    @('pending to scheduled', 'to == Scheduled', 'to != Scheduled'),
    @('cancellation transition', 'to == Cancelled', 'to != Cancelled'),
    @('execution transition', 'to == Executing', 'to != Executing'),
    @('completion transition', 'to == Completed', 'to != Completed'),
    @('failure transition', 'to == Failed', 'to != Failed'),
    @('invalid transition', 'if !allowed {', 'if allowed {'),
    @('current context validation', 'current.Validate(); err != nil', 'current.Validate(); err == nil'),
    @('expected context validation', 'expected.Validate(); err != nil', 'expected.Validate(); err == nil'),
    @('session validation', 'ValidateID(context.BackendSessionID); err != nil', 'ValidateID(context.BackendSessionID); err == nil'),
    @('draft validation', 'ValidateID(context.DraftID); err != nil', 'ValidateID(context.DraftID); err == nil'),
    @('revision validation', 'ValidateCounter(context.Revision); err != nil', 'ValidateCounter(context.Revision); err == nil'),
    @('from state validation', 'from.Validate(); err != nil', 'from.Validate(); err == nil'),
    @('to state validation', 'to.Validate(); err != nil', 'to.Validate(); err == nil'),
    @('gate state validation', 'state.Validate(); err != nil', 'state.Validate(); err == nil')
)
if ($Scope -eq 'protocol') {
    $mutations = @(
        @('protocol version', 'header.ProtocolVersion != ProtocolVersion', 'header.ProtocolVersion == ProtocolVersion'),
        @('response session', 'ValidateID(header.BackendSessionID); err != nil', 'ValidateID(header.BackendSessionID); err == nil'),
        @('zero UTC time', 'header.OccurredAt.IsZero()', '!header.OccurredAt.IsZero()'),
        @('minimum year', 'header.OccurredAt.Year() < 1', 'header.OccurredAt.Year() <= 1'),
        @('maximum year', 'header.OccurredAt.Year() > 9999', 'header.OccurredAt.Year() >= 9999'),
        @('UTC offset', 'offset != 0', 'offset == 0'),
        @('mutation operation ID', 'ValidateID(header.OperationID); err != nil', 'ValidateID(header.OperationID); err == nil'),
        @('required revision', 'header.ExpectedRevision == nil', 'header.ExpectedRevision != nil'),
        @('draft header', 'header.MutationHeader.Validate(); err != nil', 'header.MutationHeader.Validate(); err == nil'),
        @('draft identity', 'ValidateID(header.DraftID); err != nil', 'ValidateID(header.DraftID); err == nil'),
        @('required generation', 'header.ArticleGeneration == nil', 'header.ArticleGeneration != nil'),
        @('envelope header', 'envelope.ResponseHeader.Validate(); err != nil', 'envelope.ResponseHeader.Validate(); err == nil'),
        @('optional operation', 'envelope.OperationID != nil', 'envelope.OperationID == nil'),
        @('operation validation', 'ValidateID(*envelope.OperationID); err != nil', 'ValidateID(*envelope.OperationID); err == nil'),
        @('optional revision', 'envelope.Revision != nil', 'envelope.Revision == nil'),
        @('revision validation', 'ValidateCounter(*envelope.Revision); err != nil', 'ValidateCounter(*envelope.Revision); err == nil'),
        @('success discriminant', 'if envelope.OK {', 'if !envelope.OK {'),
        @('success data', 'envelope.Data == nil', 'envelope.Data != nil'),
        @('success error code', 'envelope.Code != ""', 'envelope.Code == ""'),
        @('success message', 'envelope.MessageKey != ""', 'envelope.MessageKey == ""'),
        @('failure data', 'envelope.Data != nil', 'envelope.Data == nil'),
        @('failure code', 'envelope.Code.Validate(); err != nil', 'envelope.Code.Validate(); err == nil'),
        @('failure message', 'envelope.MessageKey != string(envelope.Code)', 'envelope.MessageKey == string(envelope.Code)'),
        @('serialization guard', 'envelope.Validate(); err != nil', 'envelope.Validate(); err == nil')
    )
}
$survived = @()
Push-Location $projectRoot
try {
    foreach ($mutation in $mutations) {
        $name, $from, $to = $mutation
        if (-not $source.Contains($from)) { throw "Mutation target missing: $name" }
        $mutantPath = Join-Path $mutationRoot 'types.go'
        # Replace every occurrence of a predicate; transition pair tests observe each allowed edge.
        [IO.File]::WriteAllText($mutantPath, $source.Replace($from, $to))
        $overlayPath = Join-Path $mutationRoot 'overlay.json'
        $overlay = @{Replace = @{$sourcePath = $mutantPath}} | ConvertTo-Json -Depth 4
        [IO.File]::WriteAllText($overlayPath, $overlay)
        $output = & go test "-overlay=$overlayPath" ./internal/contracts 2>&1 | Out-String
        $killed = $LASTEXITCODE -ne 0 -and $output.Contains('--- FAIL:')
        if ($output.Contains('[build failed]')) { throw "Invalid compile mutation: $name" }
        if ($killed) { Write-Output "killed: $name" } else { $survived += $name; Write-Output "SURVIVED: $name" }
    }
} finally { Pop-Location }
if ($survived.Count -gt 0) { throw "Surviving mutations: $($survived -join ', ')" }
$global:LASTEXITCODE = 0
