$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd([char[]]'\/')
$taskRoot=Join-Path $workspace '.task'
$directory=[IO.Path]::GetFullPath((Join-Path $taskRoot ('verified-test-lifecycle-command-'+[Guid]::NewGuid().ToString('N'))))
$prefix=$taskRoot.TrimEnd([char[]]'\/')+[IO.Path]::DirectorySeparatorChar+'verified-test-lifecycle-command-'
if(-not $directory.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase)){throw 'Mutation directory escaped workspace'}
foreach($path in @($workspace,$taskRoot)){if(Test-Path -LiteralPath $path){if(((Get-Item -LiteralPath $path -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)-ne 0){throw 'Mutation root reparse point forbidden'}}}
[void](New-Item -ItemType Directory -Path $directory)
$paths=@{service=Join-Path $workspace 'internal/roundlifecycle/service.go';commands=Join-Path $workspace 'internal/roundlifecycle/commands.go';state=Join-Path $workspace 'internal/roundlifecycle/state.go';tests=Join-Path $workspace 'internal/roundlifecycle/command_port_failures_test.go'}
$original=@{};$hashes=@{}
foreach($key in $paths.Keys){$original[$key]=[IO.File]::ReadAllText($paths[$key]).Replace("`r`n","`n");$hashes[$key]=(Get-FileHash -LiteralPath $paths[$key] -Algorithm SHA256).Hash}
# The separately race-covered100MiB tests run once, never once per mutation.
$pattern='^TestCommand(Replay|Rerun|PreparePreconditions|PrepareGate|PreparedAdmission|ExecuteAdmission|CommitThen|DuplicateLive|RecoveryRead|RecoveryCompleted|RecoveryDue|RecoveryExecuting|CanceledOrNil|RecoveryWaiting|Continuous|Closing|RecoveryCancellation)'
$variants=@(
 @{name='replay operation failure remains error';file='commands';before="operation, err := service.options.Storage.ReadOperation(ctx, identity.ID)`n`tif err != nil {`n`t`treturn nil, storageError(err)";after="operation, err := service.options.Storage.ReadOperation(ctx, identity.ID)`n`tif err != nil {`n`t`treturn nil, nil"},
 @{name='replay round failure remains error';file='commands';before="round, err := service.options.Storage.ReadRound(ctx, operation.RoundID)`n`tif err != nil {`n`t`treturn nil, storageError(err)";after="round, err := service.options.Storage.ReadRound(ctx, operation.RoundID)`n`tif err != nil {`n`t`treturn nil, nil"},
 @{name='empty history cannot become success';file='commands';before="if len(rounds) == 0 {`n`t`treturn RoundRecord{}, contracts.NewFault(contracts.InvalidState)";after="if len(rounds) == 0 {`n`t`treturn RoundRecord{}, nil"},
 @{name='past completed outcome validation';file='commands';before="for _, round := range rounds {`n`t`tif round.State == contracts.Completed {`n`t`t`tif round.Outcome == nil || ValidateOutcome(round.Input, *round.Outcome) != nil";after="for _, round := range rounds {`n`t`tif round.State == contracts.Completed {`n`t`t`tif round.Outcome == nil"},
 @{name='past winner reuse refused';file='commands';before='if used[winner.ParticipantID] {';after='if false {'},
 @{name='prepare session fence';file='service';before='if request.Context.BackendSessionID != service.options.Session {';after='if false {'},
 @{name='prepared current snapshot digest fence';file='service';before='if err != nil || hash != prepared.fingerprint {';after='if err != nil || hash != hash {'},
 @{name='replay race never grants new admission';file='service';before="if admission.Replay {`n`t`treturn Admission{}, ErrOperationConflict";after="if admission.Replay {`n`t`treturn Admission{}, nil"},
 @{name='claim session independently required';file='service';before='claim.Session != service.options.Session || claim.OperationID != admission.Operation.Operation.ID';after='claim.OperationID != admission.Operation.Operation.ID'},
 @{name='claim operation independently required';file='service';before='claim.Session != service.options.Session || claim.OperationID != admission.Operation.Operation.ID';after='claim.Session != service.options.Session'},
 @{name='observed executing state independently required';file='service';before='round.State != contracts.Executing || round.Claim == nil || *round.Claim != *claim';after='round.Claim == nil || *round.Claim != *claim'},
 @{name='observed claim presence independently required';file='service';before='round.State != contracts.Executing || round.Claim == nil || *round.Claim != *claim';after='round.State != contracts.Executing || (round.Claim != nil && *round.Claim != *claim)'},
 @{name='observed exact claim independently required';file='service';before='round.State != contracts.Executing || round.Claim == nil || *round.Claim != *claim';after='round.State != contracts.Executing || round.Claim == nil'},
 @{name='duplicate live claim consumes no additional entropy';file='service';before='if service.executingClaims[*claim] {';after='if false {'},
 @{name='execution claim ownership always released';file='service';before='delete(service.executingClaims, *claim)';after='_ = claim'},
 @{name='durable failure write error overrides computed outcome';file='service';before="if writeErr != nil {`n`t`t`t`t`treturn RoundRecord{}, storageError(writeErr)";after="if writeErr != nil {`n`t`t`t`t`treturn RoundRecord{}, nil"},
 @{name='recovery preserves verified completed IDs';file='commands';before='report.Completed = append(report.Completed, round.ID)';after='_ = round.ID'},
 @{name='recovery read error preserves partial report';file='commands';before="round, err := service.options.Storage.ReadRound(ctx, snapshot.ID)`n`t`tif err != nil {`n`t`t`treturn report, storageError(err)";after="round, err := service.options.Storage.ReadRound(ctx, snapshot.ID)`n`t`tif err != nil {`n`t`t`treturn RecoveryReport{}, storageError(err)"},
 @{name='recovery records attempted due ID before failing requery';file='commands';before='report.Due = append(report.Due, round.ID)';after='_ = round.ID'},
 @{name='recovery skips unrelated terminal states';file='commands';before="if round.State != contracts.Executing {`n`t`t`tcontinue";after="if false {`n`t`t`tcontinue"},
 @{name='recovery stale revision is a competing owner noop';file='commands';before='errors.As(err, &fault) && fault.Code == contracts.StaleRevision';after='errors.As(err, &fault) && fault.Code == contracts.InvalidState'},
 @{name='recovery failure publish follows successful commit';file='commands';before='report.Failed = append(report.Failed, failed.ID)';after='_ = failed.ID'},
 @{name='recovery changed state is not failed';file='commands';before="if round.State != contracts.Executing {`n`t`treturn nil, `"`", nil";after="if round.State != contracts.Executing {`n`t`treturn &round, `"`", nil"}
)
$report=[ordered]@{status='running';sourceSHA256=$hashes;killed=@();compileTimeoutInfrastructureKills=0;cleanupVerified=$false}
try{
 Push-Location $workspace
 try{
  $baseline=& go test ./internal/roundlifecycle -run $pattern -count=1 -timeout=30s 2>&1
  if($LASTEXITCODE -ne 0){throw ('Baseline failed: '+($baseline -join "`n"))}
  Write-Output 'Lifecycle command observable branch baseline PASS'
  $ordinal=0
  foreach($variant in $variants){
   $ordinal++
   $text=$original[$variant.file]
   if(-not $text.Contains($variant.before)){throw ('Missing target: '+$variant.name)}
   $changed=$text.Replace($variant.before,$variant.after)
   $file=Join-Path $directory ('mutant-'+$ordinal+'.go')
   [IO.File]::WriteAllText($file,$changed,[Text.UTF8Encoding]::new($false))
   $mapping=@{};$mapping[$paths[$variant.file]]=$file
   $overlay=Join-Path $directory ('overlay-'+$ordinal+'.json')
   [IO.File]::WriteAllText($overlay,(@{Replace=$mapping}|ConvertTo-Json -Depth 4),[Text.UTF8Encoding]::new($false))
   $output=& go test -overlay $overlay ./internal/roundlifecycle -run $pattern -count=1 -timeout=30s 2>&1
   $code=$LASTEXITCODE;$joined=$output -join "`n"
   [IO.File]::WriteAllText((Join-Path $directory ('mutant-'+$ordinal+'.log')),$joined,[Text.UTF8Encoding]::new($false))
   if($code -eq 0){throw ('Surviving mutation: '+$variant.name)}
   if($joined -match 'panic:|build failed|timed out|no test files' -or $joined -notmatch '--- FAIL: Test'){throw ('Only observable assertion failures count: '+$variant.name+"`n"+$joined)}
   $report.killed+=@([ordered]@{name=$variant.name;exitCode=$code});Write-Output ('KILLED '+$variant.name)
  }
  $report.status='passed'
 }finally{Pop-Location}
}catch{
 $report.status='failed';$report.failure=$_.Exception.Message;throw
}finally{
 foreach($key in $paths.Keys){if((Get-FileHash -LiteralPath $paths[$key] -Algorithm SHA256).Hash -ne $hashes[$key]){throw ('Original source changed: '+$key)}}
 if(Test-Path -LiteralPath $directory){$resolved=[IO.Path]::GetFullPath((Get-Item -LiteralPath $directory).FullName);if(-not $resolved.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase)){throw 'Cleanup escaped owned workspace'};if(((Get-Item -LiteralPath $directory).Attributes -band [IO.FileAttributes]::ReparsePoint)-ne 0){throw 'Mutation directory became reparse point'};Remove-Item -LiteralPath $directory -Recurse -Force}
 $report.cleanupVerified= -not (Test-Path -LiteralPath $directory)
 $reportName=if($report.status -eq 'passed'){'lifecycle-command-mutants.json'}else{'lifecycle-command-mutants-failed.json'}
 [IO.File]::WriteAllText((Join-Path $taskRoot $reportName),($report|ConvertTo-Json -Depth 8),[Text.UTF8Encoding]::new($false))
}
if ($report.status -eq 'passed') { $global:LASTEXITCODE = 0 }
