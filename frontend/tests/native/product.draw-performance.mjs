// Dedicated fresh-collection draw timing. Two native sessions (100/200 comments),
// 20+ new drafts/collections in each. No Rerun, PNG, memory sampler or scope loop.
import assert from "node:assert/strict";
import { spawn, execFile } from "node:child_process";
import { readFile, writeFile, mkdtemp, access, readdir } from "node:fs/promises";
import { createServer } from "node:net";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { createHash } from "node:crypto";
import { chromium, expect } from "@playwright/test";
import { ownedWorkPath, requireOwnedWebView, psQuote } from "./webview-recovery.guards.mjs";
import { normalizeReservationReport } from "./reservationrestart.invariants.mjs";
import { drawWorkload, ownedEvidencePath, bindingMethod, boundedPSFailure, ownedProcessWaitRecords, initialDrawState, verifyDrawStored, verifyParticipantSnapshots, summarizeDrawSamples, watchCreateDraw } from "./product.draw-performance.helpers.mjs";

const root = resolve(fileURLToPath(new URL("../../../", import.meta.url))), task = resolve(root, ".task");
assert.equal(process.argv.length, 3, "One absolute prepared manifest argument required");
const manifestPath = resolve(process.argv[2]), evidence = ownedEvidencePath(resolve(manifestPath, ".."), root);
assert.equal(manifestPath, resolve(evidence, "manifest.json"));
const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
assert.equal(manifest.version, 1); assert.equal(manifest.status, "built"); assert.equal(manifest.workspace.toLowerCase(), root.toLowerCase());
const exe = resolve(evidence, "product-draw-performance.exe"), assets = resolve(evidence, "assets");
assert.equal(manifest.executable, exe); assert.equal(manifest.assets, assets);
const reportPath = resolve(evidence, "report.json");
const report = { status: "running", phase: "preflight", startedAt: new Date().toISOString(), workloads: [], sourceManifest: manifestPath, boundaries: {
  native: "actual Wails/WebView2; unmodified production frontend; real SQLite and OS crypto/rand",
  network: "existing deterministic HTTP RoundTripper fixture; production collector/parser; external network0",
  samples: "one app/profile per comment workload; new draft/new collection per sample; DB collection count grows1..20+; no Rerun",
  timing: "trusted capturing Create click -> first completed/latest-selected1-round/10-winner DOM; startup/collection/prize configuration/actionability excluded",
  durableRequery: "separate click -> post-DOM Probe.Report actual readonly SQLite counts/Go collection projection completion; not a commit timestamp",
  verification: "post-timing actual stored public8-field snapshot/order and same bounded draft/frozen previews; winner previews=[] contract, included/winner membership, readonly duplicate queries, all prior outcomes unchanged",
  globalState: "no OS settings, physical input, clipboard, power, forced GC or memory policy changes",
  acceptance: "draw p95 <=1000ms only after both complete20+ workloads, source/asset equality and owned cleanup; quiet execution must be declared by coordinator",
}, performanceAcceptanceClaim: false };
let active;
const sha = bytes => createHash("sha256").update(bytes).digest("hex");
const runPS = command => new Promise((ok, fail) => execFile("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", command], { windowsHide: true, timeout: 35000, maxBuffer: 1024 * 1024 }, (error, out, err) => error ? fail(new Error(boundedPSFailure(error, out, err), { cause: error })) : ok(out)));
const persist = () => writeFile(reportPath, JSON.stringify(report, null, 2) + "\n");
const button = name => active.page.getByRole("button", { name, exact: true });
const field = name => active.page.getByLabel(name, { exact: true }).and(active.page.locator(":visible"));
async function rpc(method, ...args) {
  return active.page.evaluate(async ({ method, args }) => {
    const dispatch = window._wails.dispatchWailsEvent;
    const { Call } = await import("/wails/runtime.js"); window._wails.dispatchWailsEvent = dispatch;
    const request = typeof method === "number" ? Call.ByID(method, ...args) : Call.ByName("main.Probe." + method, ...args);
    let timer;
    try { return await Promise.race([request, new Promise((_, reject) => { timer = window.setTimeout(() => { request.cancel?.(); reject(new Error("Draw verification IPC deadline")); }, 20000); })]); }
    finally { window.clearTimeout(timer); }
  }, { method, args });
}
const snapshot = async () => normalizeReservationReport(await rpc("Report"));
async function sourceFiles(directory, prefix, acceptFile) {
  const result = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    assert.equal(entry.isSymbolicLink(), false, "Source alias rejected");
    const path = resolve(directory, entry.name), name = prefix + "/" + entry.name;
    if (entry.isDirectory()) result.push(...await sourceFiles(path, name, acceptFile));
    else if (entry.isFile() && acceptFile(name)) result.push(name);
  }
  return result;
}
async function verifyPins() {
  const current = ["main.go", "go.mod", "go.sum", ...await sourceFiles(resolve(root, "internal"), "internal", name => name.endsWith(".go")), ...await sourceFiles(resolve(root, "cmd/producte2e"), "cmd/producte2e", name => name.endsWith(".go")), ...await sourceFiles(resolve(root, "frontend/src"), "frontend/src", () => true), ...await sourceFiles(resolve(root, "frontend/bindings"), "frontend/bindings", () => true)].sort();
  assert.deepEqual(current, manifest.sourceFiles.map(value => value.path).sort(), "Exact source file set changed");
  for (const value of manifest.sourceFiles) assert.equal(sha(await readFile(resolve(root, value.path))).toUpperCase(), value.sha256, "Source changed: " + value.path);
  for (const value of manifest.assetsFiles) assert.equal(sha(await readFile(resolve(assets, value.path))).toUpperCase(), value.sha256, "Pinned asset changed");
  const assetNames = (await sourceFiles(assets, "", () => true)).map(value => value.slice(1)).sort();
  assert.deepEqual(assetNames, manifest.assetsFiles.map(value => value.path).sort(), "Pinned asset file set changed");
  for (const value of manifest.harnessFiles) assert.equal(sha(await readFile(resolve(root, value.path))).toUpperCase(), value.sha256, "Harness changed: " + value.path);
  for (const value of manifest.bindingFiles) assert.equal(sha(await readFile(resolve(evidence, "bindings", value.path))).toUpperCase(), value.sha256, "Copied binding changed");
  assert.equal(sha(await readFile(exe)).toUpperCase(), manifest.executableSHA256);
}
async function freePort() {
  const server = createServer(); await new Promise((ok, fail) => { server.once("error", fail); server.listen(0, "127.0.0.1", ok); });
  const port = server.address().port; await new Promise((ok, fail) => server.close(error => error ? fail(error) : ok())); return port;
}
async function webviews(instance) {
  const raw = await runPS("$ErrorActionPreference='Stop';$entries=@(Get-CimInstance Win32_Process -Filter \"Name='msedgewebview2.exe'\"|Where-Object{$_.CommandLine -like ('*'+" + psQuote(instance.work) + "+'*')}|ForEach-Object{$entry=$_;$process=Get-Process -Id $entry.ProcessId -ErrorAction SilentlyContinue;if($process){try{@{id=[int]$entry.ProcessId;name=$entry.Name;command=$entry.CommandLine;created=$entry.CreationDate.ToUniversalTime().ToString('o');started=$process.StartTime.ToUniversalTime().ToString('o')}}finally{$process.Dispose()}}});ConvertTo-Json -InputObject $entries -Depth 4 -Compress");
  const values = JSON.parse(raw); assert.ok(Array.isArray(values));
  for (const value of values) { const record = requireOwnedWebView(value, instance.work, root); instance.owned.set(record.id + ":" + record.started, record); }
  return values;
}
async function launch(workload, entry) {
  const work = await mkdtemp(resolve(task, "product-run-webview-draw-")); ownedWorkPath(work, root);
  const port = await freePort();
  const child = spawn(exe, ["-assets", assets, "-work-dir", work, "-cdp-port", String(port), "-large-fixture", "-fixture-comments", String(workload.comments)], { cwd: root, windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
  const instance = { child, work, stdout: "", stderr: "", owned: new Map(), entry }; active = instance;
  entry.workDir = work; entry.pid = child.pid; entry.cdpPort = port;
  instance.exit = new Promise((ok, fail) => { child.once("error", fail); child.once("exit", (code, signal) => ok({ code, signal })); }); void instance.exit.catch(() => {});
  child.stdout.on("data", bytes => { instance.stdout = (instance.stdout + bytes).slice(-16000); }); child.stderr.on("data", bytes => { instance.stderr = (instance.stderr + bytes).slice(-16000); });
  const proof = JSON.parse(await runPS("$ErrorActionPreference='Stop';$entry=Get-CimInstance Win32_Process -Filter 'ProcessId=" + child.pid + "';if(-not $entry){throw 'Owned host absent'};$process=Get-Process -Id $entry.ProcessId -ErrorAction Stop;try{@{id=[int]$entry.ProcessId;image=$entry.ExecutablePath;command=$entry.CommandLine;created=$entry.CreationDate.ToUniversalTime().ToString('o');started=$process.StartTime.ToUniversalTime().ToString('o')}|ConvertTo-Json -Compress}finally{$process.Dispose()}"));
  assert.equal(proof.id, child.pid); assert.equal(proof.image.toLowerCase(), exe.toLowerCase()); assert.ok(proof.command.includes(work)); instance.proof = proof; entry.creationTime = proof.created;
  await expect.poll(() => { if (child.exitCode !== null) throw new Error("Owned host exited: " + instance.stderr); return instance.stdout.includes("JACKPOT_PRODUCT_E2E_READY"); }, { timeout: 30000 }).toBe(true);
  await expect.poll(async () => { if (child.exitCode !== null) throw new Error("Owned host exited before CDP"); try { const response = await fetch("http://127.0.0.1:" + port + "/json/list", { signal: AbortSignal.timeout(1500) }); return (await response.json()).some(value => value.type === "page"); } catch { return false; } }, { timeout: 20000 }).toBe(true);
  instance.browser = await chromium.connectOverCDP("http://127.0.0.1:" + port, { isWebView: true, timeout: 20000 });
  const pages = instance.browser.contexts().flatMap(context => context.pages()); assert.equal(pages.length, 1); instance.page = pages[0]; instance.page.setDefaultTimeout(15000);
  const session = await instance.page.context().newCDPSession(instance.page); try { await session.send("Emulation.setFocusEmulationEnabled", { enabled: true }); } finally { await session.detach(); }
  await expect(instance.page.getByRole("heading", { name: "추첨 만들기", exact: true })).toBeVisible({ timeout: 30000 }); await expect(field("디시인사이드 게시글 주소")).toBeEnabled();
  await webviews(instance); assert.ok(instance.owned.size > 0); return instance;
}
async function waitExit(instance) {
  let timer; try { return await Promise.race([instance.exit, new Promise((_, fail) => { timer = setTimeout(() => fail(new Error("Owned host exit deadline")), 15000); })]); } finally { clearTimeout(timer); }
}
async function killOwned(instance) {
  const proof = instance.proof; assert.ok(proof, "Missing Go ownership proof; no blind kill");
  await runPS("$ErrorActionPreference='Stop';$entry=Get-CimInstance Win32_Process -Filter 'ProcessId=" + proof.id + "';if(-not $entry){exit 0};if($entry.ExecutablePath -ine " + psQuote(exe) + " -or $entry.CommandLine -cne " + psQuote(proof.command) + " -or $entry.CreationDate.ToUniversalTime().ToString('o') -cne " + psQuote(proof.created) + "){throw 'Host identity changed'};$process=Get-Process -Id $entry.ProcessId -ErrorAction Stop;try{if($process.StartTime.ToUniversalTime().ToString('o') -cne " + psQuote(proof.started) + "){throw 'Host PID reused'};$process.Kill();if(-not $process.WaitForExit(10000)){throw 'Owned host did not exit'}}finally{$process.Dispose()}");
  instance.entry.failureFallbackKill = true;
}
async function stop({ failure = false } = {}) {
  const instance = active; if (!instance) return;
  await instance.page?.evaluate(() => { window.__jackpotCreateTimer?.dispose(); delete window.__jackpotCreateTimer; }).catch(() => {});
  await webviews(instance);
  let outcome;
  try { if (instance.child.exitCode === null) await rpc("Stop"); outcome = await waitExit(instance); }
  catch (error) { if (!failure) throw error; if (instance.child.exitCode === null) await killOwned(instance); outcome = await waitExit(instance); }
  if (!failure) assert.deepEqual(outcome, { code: 0, signal: null });
  instance.entry.exit = outcome;
  await instance.browser?.close().catch(() => {}); await webviews(instance);
  const records = [...instance.owned.values()]; instance.entry.ownedProcesses = records.map(({ id, started, role }) => ({ id, started, role }));
  const waitProof = JSON.stringify(ownedProcessWaitRecords(records));
  const waitCommand = "$ErrorActionPreference='Stop';$records=" + psQuote(waitProof) + "|ConvertFrom-Json;$deadline=[DateTime]::UtcNow.AddSeconds(20);foreach($record in $records){$process=Get-Process -Id ([int]$record.id) -ErrorAction SilentlyContinue;if($process){try{if($process.StartTime.ToUniversalTime().ToString('o') -ceq $record.started){$remaining=[int]($deadline-[DateTime]::UtcNow).TotalMilliseconds;if($remaining -le 0 -or -not $process.WaitForExit($remaining)){throw 'Owned WebView did not exit'}}}finally{$process.Dispose()}}};exit 0";
  instance.entry.processWaitPayload = { records: records.length, fullRecordJSONChars: JSON.stringify(records).length, projectedJSONChars: waitProof.length, commandChars: waitCommand.length };
  await runPS(waitCommand);
  assert.deepEqual(await webviews(instance), []);
  const preserved = resolve(evidence, "failed-" + instance.entry.comments + ".sqlite3");
  const command = "$ErrorActionPreference='Stop';$target=[IO.Path]::GetFullPath(" + psQuote(instance.work) + ");if([IO.Path]::GetDirectoryName($target) -ine " + psQuote(task) + " -or -not [IO.Path]::GetFileName($target).StartsWith('product-run-webview-draw-')){throw 'Cleanup escaped workspace'};$item=Get-Item -LiteralPath $target;if($item.Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Cleanup alias rejected'};foreach($item in Get-ChildItem -LiteralPath $target -Recurse -Force){if($item.Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Nested alias rejected'}};foreach($file in Get-ChildItem -LiteralPath $target -File -Recurse -Force){$stream=[IO.File]::Open($file.FullName,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::None);$stream.Dispose()};" + (failure ? "$db=Join-Path $target 'product.sqlite3';if(Test-Path -LiteralPath $db){Copy-Item -LiteralPath $db -Destination " + psQuote(preserved) + ";foreach($suffix in @('-wal','-shm')){$side=$db+$suffix;if(Test-Path -LiteralPath $side){Copy-Item -LiteralPath $side -Destination (" + psQuote(preserved) + "+$suffix)}}};" : "") + "Remove-Item -LiteralPath $target -Recurse -Force;if(Test-Path -LiteralPath $target){throw 'Owned profile remains'}";
  await runPS(command);
  instance.entry.ownedProcessesExited = true; instance.entry.dbProfileExclusiveAndRemoved = true; instance.entry.stdoutTail = instance.stdout; instance.entry.stderrTail = instance.stderr;
  if (failure) { try { instance.entry.preservedDB = { path: preserved, sha256: sha(await readFile(preserved)) }; } catch (error) { if (error.code !== "ENOENT") throw error; } }
  active = undefined;
}

try {
  assert.equal(process.platform, "win32"); assert.ok(!process.env.GOMEMLIMIT && !process.env.GOGC && !process.env.WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS, "Default runtime policy required; no diagnostic inherited flags");
  await access(exe); await verifyPins(); report.pinsVerifiedBefore = true; report.exeSHA256 = manifest.executableSHA256; report.assetsFiles = manifest.assetsFiles;
  const bindings = resolve(evidence, "bindings/github.com/porkyx/jackpot/internal/desktop");
  const draftMethod = bindingMethod(await readFile(resolve(bindings, "draftservice.ts"), "utf8"), "QueryParticipants");
  const frozenMethod = bindingMethod(await readFile(resolve(bindings, "roundservice.ts"), "utf8"), "QueryFrozenParticipants");
  for (const comments of [100, 200]) {
    const workload = drawWorkload(comments, manifest.samplesPerWorkload);
    const entry = { comments, participants: workload.participants, samples: [], phase: "boot", session: null }; report.workloads.push(entry);
    report.phase = "boot-" + comments; await persist(); await launch(workload, entry);
    let before = await snapshot(); assert.deepEqual(before.counts, { collections: 0, operations: 0, rounds: 0, attempts: 0, results: 0, winners: 0, participants: 0 });
    entry.session = before.session; assert.equal(before.assetSHA256.toUpperCase(), manifest.assetsFiles.find(value => value.path === "index.html").sha256);
    let initialJSON; const drafts = new Set(), collections = new Set();
    for (let index = 0; index < workload.samples; index++) {
      report.phase = "comments" + comments + "-sample" + (index + 1); entry.phase = "collect-and-configure"; await persist();
      if (index > 0) { await active.page.locator('nav a[href="#/create"]').click(); await expect(active.page.getByRole("heading", { name: "추첨 만들기", exact: true })).toBeVisible(); await expect(field("디시인사이드 게시글 주소")).toBeEnabled(); }
      await field("디시인사이드 게시글 주소").fill(before.fixtureURL); await button("게시글 불러오기").click();
      await expect(button("추첨 생성")).toBeEnabled(); await expect(active.page.locator(".create-participants>li")).toHaveCount(workload.participants);
      await field("추첨 방식").selectOption("immediate"); await field("상품 구성").selectOption("single");
      await field("상품명 (선택)").fill("즉시 추첨 검증"); await field("당첨 인원").fill("10"); await field("당첨 인원").blur();
      await expect.poll(async () => { const state = await rpc("DraftState"); return { mode: state.prizes.mode, drawMode: state.prizes.drawMode, name: state.prizes.single.name, count: state.prizes.single.count }; }, { timeout: 15000 }).toEqual({ mode: "single", drawMode: "immediate", name: "즉시 추첨 검증", count: 10 });
      const draft = await rpc("DraftState"); assert.equal(draft.summary.backendSessionId, entry.session); assert.equal(drafts.has(draft.summary.draftId), false); drafts.add(draft.summary.draftId);
      const context = { backendSessionId: entry.session, draftId: draft.summary.draftId, revision: draft.summary.revision, articleGeneration: draft.summary.articleGeneration };
      const initialReply = await rpc(draftMethod, { ...context, query: "", group: "", offset: 0, limit: 100 }); assert.equal(initialReply.ok, true);
      const state = JSON.stringify(initialDrawState(draft, initialReply.data, workload)); if (initialJSON === undefined) initialJSON = state; else assert.equal(state, initialJSON, "Each fresh collection must have equivalent inputs");
      before = await snapshot(); assert.equal(before.counts.collections, index); assert.equal(before.counts.rounds, index); assert.equal(before.fixtureCalls, (index + 1) * 3); assert.equal(before.fixtureCalls, before.fixtureBodiesClosed);
      await expect(button("추첨 생성")).toBeEnabled(); await expect(active.page.locator('input[type="password"]')).toHaveCount(0);
      entry.phase = "timed-create"; await active.page.evaluate(watchCreateDraw, { timeoutMs: 20000 }); await button("추첨 생성").click();
      const timing = await active.page.evaluate(() => window.__jackpotCreateTimer.promise); assert.equal(timing.ok, true, "Exact completed result DOM deadline");
      const after = await snapshot(); const clickToDurableRequeryMs = await active.page.evaluate(() => window.__jackpotCreateTimer.verifiedElapsed());
      const fresh = after.latest.filter(value => !collections.has(value.collectionId)); assert.equal(fresh.length, 1);
      const frozenReply = await rpc(frozenMethod, { collectionId: fresh[0].collectionId, expectedRevision: fresh[0].revision, query: "", group: "", offset: 0, limit: 100 }); assert.equal(frozenReply.ok, true);
      const verified = verifyDrawStored(after, before, draft, frozenReply.data, workload, index + 1, collections);
      verifyParticipantSnapshots(initialReply.data.rows, frozenReply.data.rows);
      for (const old of before.latest) assert.deepEqual(after.latest.find(value => value.collectionId === old.collectionId), old, "New collection cannot alter earlier results");
      const duplicate = await snapshot(); assert.deepEqual(duplicate.counts, after.counts); assert.deepEqual(duplicate.latest, after.latest); assert.equal(duplicate.fixtureCalls, after.fixtureCalls);
      collections.add(verified.collection.collectionId);
      entry.samples.push({ sample: index + 1, clickToResultMs: timing.elapsedMs, clickToDurableRequeryMs, verified: true, initialStateEqual: true, initialStateSHA256: sha(state), includedSnapshotSHA256: sha(JSON.stringify(frozenReply.data.rows)), winnerCount: 10, roundCount: 1, DBcollections: after.counts.collections, counts: after.counts });
      await active.page.evaluate(() => { window.__jackpotCreateTimer.dispose(); delete window.__jackpotCreateTimer; });
      entry.phase = "verified"; before = after; await persist();
    }
    entry.summary = summarizeDrawSamples(entry.samples, workload.samples); entry.uniqueDrafts = drafts.size; entry.uniqueCollections = collections.size;
    await stop(); entry.phase = "cleaned"; await persist();
  }
  await verifyPins(); report.pinsVerifiedAfter = true; report.status = "passed";
  report.thresholds = { drawP95Under1s: report.workloads.every(value => value.summary.clickToResultP95Ms <= 1000), both20Plus: report.workloads.every(value => value.samples.length >= 20) };
} catch (error) {
  report.status = "failed"; report.failedPhase = report.phase; report.error = error instanceof Error ? error.message.slice(0,1800) : "unknown"; process.exitCode = 1;
  if (active?.page) { try { await active.page.screenshot({ path: resolve(evidence, "failure.png") }); } catch {} }
} finally {
  if (active) { try { await stop({ failure: true }); } catch (error) { report.status = "failed"; report.cleanupError = String(error).slice(0,1500); report.retainedWorkDir = active.work; process.exitCode = 1; } }
  report.finishedAt = new Date().toISOString(); report.ownedCleanupComplete = report.workloads.every(value => value.ownedProcessesExited === true && value.dbProfileExclusiveAndRemoved === true);
  if (!report.ownedCleanupComplete) { report.status = "failed"; process.exitCode = 1; }
  await persist(); console.log(JSON.stringify({ status: report.status, thresholds: report.thresholds, workloads: report.workloads.map(value => ({ comments: value.comments, samples: value.samples.length, ...value.summary })), report: reportPath }));
}
