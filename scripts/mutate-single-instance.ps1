$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
$mutationRoot = Join-Path $projectRoot '.task/mutation-single-instance'
New-Item -ItemType Directory -Force $mutationRoot | Out-Null
$mutations = @(
    @('nil lease close', 'instance.go', 'if l == nil {', 'if l != nil {', 'TestCloseNil|TestConcurrent'),
    @('missing close owner', 'instance.go', 'if l.release != nil {', 'if l.release == nil {', 'TestConcurrent'),
    @('nil context', 'instance.go', 'if ctx == nil {', 'if ctx != nil {', 'TestAcquire'),
    @('pre-cancelled context', 'instance.go', "if err := ctx.Err(); err != nil {`n`t`treturn nil, err`n`t}", "if err := ctx.Err(); err == nil {`n`t`treturn nil, err`n`t}", 'TestAcquire'),
    @('post-acquire cancellation guard', 'instance.go', "if err := ctx.Err(); err != nil {`n`t`treturn nil, errors.Join", "if err := ctx.Err(); err == nil {`n`t`treturn nil, errors.Join", 'TestAcquireCancellationAfterLock|TestAcquireCanonicalizes'),
    @('post-acquire cancellation cleanup', 'instance.go', 'return nil, errors.Join(err, lease.Close())', 'return nil, err', 'TestAcquireCancellationAfterLock'),
    @('OS error propagated', 'instance.go', "if err != nil {`n`t`treturn nil, err`n`t}`n`tif release", "if err == nil {`n`t`treturn nil, err`n`t}`n`tif release", 'TestAcquirePropagates'),
    @('maximum path size', 'instance.go', 'len(databasePath) > 32767', 'len(databasePath) >= 32767', 'TestAcquire'),
    @('UTF8 path', 'instance.go', '!utf8.ValidString(databasePath)', 'utf8.ValidString(databasePath)', 'TestAcquire'),
    @('NUL path', 'instance.go', 'strings.IndexByte(databasePath, 0) >= 0', 'strings.IndexByte(databasePath, 0) < 0', 'TestAcquire'),
    @('absolute path', 'instance.go', '!filepath.IsAbs(databasePath)', 'filepath.IsAbs(databasePath)', 'TestAcquire'),
    @('root file path', 'instance.go', 'filepath.Dir(cleaned) == cleaned', 'filepath.Dir(cleaned) != cleaned', 'TestAcquire'),
    @('successful owner invariant', 'instance.go', 'if release == nil {', 'if release != nil {', 'TestSuccessful|TestAcquire'),
    @('directory error guard', 'instance_windows.go', 'os.MkdirAll(filepath.Dir(path), 0700); err != nil', 'os.MkdirAll(filepath.Dir(path), 0700); err == nil', 'TestRealOSDirectoryCreation|TestRealOSLock'),
    @('open error guard', 'instance_windows.go', "if err != nil {`n`t`treturn nil, fmt.Errorf", "if err == nil {`n`t`treturn nil, fmt.Errorf", 'TestRealOSOpenFailure|TestRealOSLock'),
    @('OS lock error guard', 'instance_windows.go', "if err != nil {`n`t`tcloseErr := file.Close()", "if err == nil {`n`t`tcloseErr := file.Close()", 'TestRealOSLock|TestRealOSClosedHandle'),
    @('exclusive OS lock', 'instance_windows.go', 'windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY', 'windows.LOCKFILE_FAIL_IMMEDIATELY', 'TestRealOSLock|TestRealOSAcquireContention'),
    @('nonblocking OS lock', 'instance_windows.go', 'windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY', 'windows.LOCKFILE_EXCLUSIVE_LOCK', 'TestRealProcessesRejectDuplicate'),
    @('lock range', 'instance_windows.go', 'windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0', 'windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 0, 0', 'TestRealOSLock'),
    @('duplicate error identity', 'instance_windows.go', 'if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {', 'if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {', 'TestRealOSLock|TestRealOSClosedHandle'),
    @('failed lock closes handle', 'instance_windows.go', 'closeErr := file.Close()', 'var closeErr error', 'TestRealOSLock|TestRealOSRepeated|TestRealOSClosedHandle'),
    @('normal close closes handle', 'instance_windows.go', 'return errors.Join(unlockErr, file.Close())', 'return unlockErr', 'TestRealOSLock|TestRealOSCloseInvalidates|TestRealOSRepeated'),
    @('explicit unlock failure preserved', 'instance_windows.go', 'windows.UnlockFileEx(handle, 0, 1, 0, &windows.Overlapped{})', 'error(nil)', 'TestRealOSUnlockFailure')
)
$survived = @()
Push-Location $projectRoot
try {
    foreach ($mutation in $mutations) {
        $name, $filename, $from, $to, $tests = $mutation
        $sourcePath = Join-Path $projectRoot "internal/singleinstance/$filename"
        $source = [IO.File]::ReadAllText($sourcePath)
        if (-not $source.Contains($from)) { throw "Mutation target missing: $name" }
        $mutantPath = Join-Path $mutationRoot $filename
        [IO.File]::WriteAllText($mutantPath, $source.Replace($from, $to))
        $overlayPath = Join-Path $mutationRoot 'overlay.json'
        $overlay = @{ Replace = @{ $sourcePath = $mutantPath } } | ConvertTo-Json -Depth 4
        [IO.File]::WriteAllText($overlayPath, $overlay)
        $output = & go test "-overlay=$overlayPath" "-run=$tests" '-timeout=45s' ./internal/singleinstance 2>&1 | Out-String
        if ($output.Contains('[build failed]') -or $output.Contains('panic: test timed out')) {
            throw "Invalid build/whole-suite timeout, not a killed mutation: $name`n$output"
        }
        $killed = $LASTEXITCODE -ne 0 -and $output.Contains('--- FAIL:')
        if ($killed) {
            Write-Output "killed: $name"
        } else {
            $survived += $name
            Write-Output "SURVIVED: $name"
        }
    }
} finally { Pop-Location }
if ($survived.Count -gt 0) { throw "Surviving mutations: $($survived -join ', ')" }
$global:LASTEXITCODE = 0
