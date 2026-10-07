// Actual production assets, public Wails calls and real SQLite. This functional
// probe neither changes product source nor relaxes the original stress driver.
import assert from "node:assert/strict";
import { spawn, execFile } from "node:child_process";
import { mkdtemp, readFile, writeFile, copyFile, cp, access } from "node:fs/promises";
import { createServer } from "node:net";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { createHash, randomUUID } from "node:crypto";
import { chromium, expect } from "@playwright/test";
const root = fileURLToPath(new URL("../../../", import.meta.url)), task = resolve(root, ".task");
const sourceHost = resolve(root, process.env.JACKPOT_ROUND_PAGE_HOST ?? ".task/producte2e.exe");
await access(sourceHost); await access(resolve(root, "frontend/dist/index.html"));
const work = await mkdtemp(resolve(task, "product-run-round-page-"));
const assets = await mkdtemp(resolve(task, "product-round-page-assets-"));
assert.equal(dirname(work), task); assert.equal(dirname(assets), task);
const exe = resolve(assets, "product-round-page.exe");
await copyFile(sourceHost, exe); await cp(resolve(root, "frontend/dist"), assets, { recursive: true });
const digest = async path => createHash("sha256").update(await readFile(path)).digest("hex");
const jsonHash = value => createHash("sha256").update(JSON.stringify(value)).digest("hex");
// Round.revision is the current collection projection; immutable roundVersion/input/outcome stay in this oracle.
const immutableRound = ({ revision: _currentCollectionRevision, ...snapshot }) => snapshot;
const reportPath = resolve(task, "product-round-page-report.json");
const report = { status: "running", startedAt: new Date().toISOString(), checks: [], boundary: { host: "owned actual Windows Wails/WebView2", database: "actual SQLite", commands: "public generated method IDs; unique operation per intentional command", initialDraw: "actual UI reservation/collector/commit; public crypto draw on rounds51/52", PNG: "actual Canvas/atomic file; owned save path", clipboard: "capture seam; OS clipboard unchanged", timingAcceptance: false },
 assets: { directory: assets, sourceHost, hostSHA256: await digest(exe), htmlSHA256: await digest(resolve(assets, "index.html")) } };
const runPS = command => new Promise((ok, fail) => execFile("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", command], { windowsHide: true, timeout: 30000 }, (error, stdout) => error ? fail(error) : ok(stdout)));
const deadline = async (promise, ms) => { let timer; try { return await Promise.race([promise, new Promise((_, fail) => { timer = setTimeout(() => fail(new Error("OwnedHostDeadline")), ms); })]); } finally { clearTimeout(timer); } };
const server = createServer(); await new Promise((ok, fail) => { server.once("error", fail); server.listen(0, "127.0.0.1", ok); });
const cdp = server.address().port; await new Promise((ok, fail) => server.close(error => error ? fail(error) : ok()));
let child, browser, page, renderer, exit, owned = [], stopped = false;
const check = async (name, action) => { report.phase = name; await action(); report.checks.push(name); console.log("round-page PASS " + name); };
const call = (id, ...args) => page.evaluate(async ({ id, args }) => {
 const dispatch = window._wails.dispatchWailsEvent; const { Call } = await import("/wails/runtime.js"); window._wails.dispatchWailsEvent = dispatch;
 return typeof id === "number" ? Call.ByID(id, ...args) : Call.ByName("main.Probe." + id, ...args);
}, { id, args });
const get = (collectionId, options = {}) => call(1947843826, { collectionId, roundOffset: 0, roundLimit: 0, roundId: "", ...options });
const button = name => page.getByRole("button", { name, exact: true });
const visible = name => page.getByLabel(name, { exact: true }).and(page.locator(":visible"));
const route = (collectionId, roundId = null) => "#/results/" + encodeURIComponent(collectionId) + (roundId === null ? "" : "/rounds/" + encodeURIComponent(roundId));
async function navigate(hash) {
 const selection = page.locator('[name="resultRound"]'); const previous = await selection.count() === 0 ? null : await selection.elementHandle();
 try {
  await page.evaluate(value => { window.location.hash = value; }, hash);
  await expect.poll(() => page.evaluate(() => window.location.hash)).toBe(hash);
  if (previous) await expect.poll(() => previous.evaluate(element => element.isConnected)).toBe(false);
  await expect(page.getByRole("heading", { name: "추첨 결과", exact: true })).toBeVisible();
  await expect(page.locator('[name="resultRound"]')).toBeEnabled();
 } finally { await previous?.dispose(); }
}
async function discover() {
 const proof = work.replaceAll("'", "''");
 const rows = await runPS("$ErrorActionPreference='Stop'; $roundProfile='" + proof + "'; Get-CimInstance Win32_Process -Filter \"Name='msedgewebview2.exe'\" | Where-Object {$_.CommandLine -like ('*'+$roundProfile+'*')} | ForEach-Object {$_.ProcessId}");
 const ids = rows.trim() === "" ? [] : rows.trim().split(/\s+/).map(Number); assert.ok(ids.every(id => Number.isSafeInteger(id) && id > 0)); owned = [...new Set([...owned, ...ids])];
}
async function stop() {
 if (stopped || !child) return; stopped = true; await discover();
 try { await call("Stop"); } catch { if (child.exitCode === null) child.kill(); }
 const finished = await deadline(exit, 20000); report.hostExit = finished;
 await renderer?.detach().catch(() => {}); await browser?.close().catch(() => {});
 assert.equal(finished.code, 0); assert.equal(finished.signal, null); assert.ok(owned.length > 0 && owned.every(id => Number.isSafeInteger(id) && id > 0));
 await runPS("$ErrorActionPreference='Stop'; $roundPids=@(" + owned.join(",") + "); $roundProcesses=Get-Process -Id $roundPids -ErrorAction SilentlyContinue; try {if($roundProcesses){$roundProcesses|Wait-Process -Timeout 20 -ErrorAction Stop}}catch{exit 1}; exit 0");
 report.ownedPIDExit = { ids: owned, count: owned.length, remaining: 0 };
}
let first, current, firstPNG, consumedRemaining;
try {
 child = spawn(exe, ["-assets", assets, "-work-dir", work, "-cdp-port", String(cdp)], { cwd: root, windowsHide: true, shell: false, stdio: ["ignore", "pipe", "pipe"] });
 exit = new Promise((ok, fail) => { child.once("error", fail); child.once("exit", (code, signal) => ok({ code, signal })); }); void exit.catch(() => {});
 let stdout = ""; child.stdout.on("data", chunk => { stdout = (stdout + chunk.toString()).slice(-2048); }); child.stderr.resume(); owned = [child.pid];
 await expect.poll(() => { if (child.exitCode !== null) throw new Error("NativeStartup"); return stdout.includes("JACKPOT_PRODUCT_E2E_READY"); }, { timeout: 30000 }).toBe(true);
 await expect.poll(async () => { try { return (await (await fetch(`http://127.0.0.1:${cdp}/json/list`)).json()).some(target => target.type === "page"); } catch { return false; } }, { timeout: 20000 }).toBe(true);
 browser = await chromium.connectOverCDP(`http://127.0.0.1:${cdp}`, { timeout: 20000, isWebView: true });
 const pages = browser.contexts().flatMap(context => context.pages()); assert.equal(pages.length, 1); page = pages[0]; page.setDefaultTimeout(10000);
 renderer = await page.context().newCDPSession(page); await renderer.send("Emulation.setFocusEmulationEnabled", { enabled: true });
 const browserSession = await browser.newBrowserCDPSession(); const processes = await browserSession.send("SystemInfo.getProcessInfo"); await browserSession.detach();
 owned = [...new Set([...owned, ...processes.processInfo.map(process => process.id)])]; await discover(); report.engine = browser.version();
 // Existing media failure fallback stays real; this pagination test requires no external image service.
 await page.route(/^https:\/\/(?:dcimg[1-5]\.dcinside\.com)\//, request => request.abort());
 await check("actual-initial-reservation-with-five-eligible", async () => {
  await expect(visible("디시인사이드 게시글 주소")).toBeEnabled();
  await page.locator('select[name="theme"]').selectOption("light");
  await visible("디시인사이드 게시글 주소").fill("https://gall.dcinside.com/board/view/?id=producte2e&no=1"); await button("게시글 불러오기").click();
  await expect(button("추첨 생성")).toBeEnabled(); await visible("상품명 (선택)").fill("회차 보존"); await visible("상품명 (선택)").press("Enter");
  await expect.poll(async () => (await call("DraftState")).prizes.single.name).toBe("회차 보존"); await visible("추첨 방식").selectOption("reservation");
  await button("추첨 생성").click();
  await expect(page.getByRole("heading", { name: "추첨 결과", exact: true })).toBeVisible({ timeout: 30000 }); await expect(page.locator('[name="resultRound"]')).toBeEnabled();
  const snapshot = await call("Report"); const data = snapshot.latest[0]; assert.ok(data); current = (await get(data.collectionId)).data;
  assert.equal(current.roundTotal, 1); assert.equal(current.selectedCount, 5); assert.equal(current.rounds.length, 1); assert.equal(current.latestRound.state, "pending_schedule");
  assert.equal(snapshot.counts.results, 0); assert.equal(snapshot.counts.winners, 0); consumedRemaining = current.remainingCount; assert.equal(consumedRemaining, 5);
  report.initial = { collectionId: current.collectionId, participantCount: current.participantCount, selectedCount: current.selectedCount, remainingCount: consumedRemaining };
 });
 const session = (await get(current.collectionId)).backendSessionId; const prize = { id: randomUUID(), name: "회차 보존", count: 1 };
 const header = (mode = "reservation") => ({ protocolVersion: 1, backendSessionId: session, operationId: randomUUID(), expectedRevision: current.revision, collectionId: current.collectionId, roundId: current.latestRound.roundId, expectedVersion: current.latestRound.roundVersion, prizes: [prize], message: "", mode, scheduledAt: null, quickDelaySeconds: null });
 let firstCancelled;
 await check("fifty-cancelled-reservations-do-not-consume-any-participant", async () => {
  const initialCancel = await call(3827868161, { ...header(), prizes: [] }); assert.equal(initialCancel.ok, true); current = initialCancel.data; firstCancelled = current.latestRound;
  assert.equal(firstCancelled.state, "cancelled");
  for (let number = 2; number <= 50; number++) {
   const admitted = await call(2028294364, header()); assert.equal(admitted.ok, true); current = admitted.data;
   assert.equal(current.latestRound.state, "pending_schedule"); assert.equal(current.latestRound.number, number); assert.equal(current.remainingCount, consumedRemaining);
   const cancelled = await call(3827868161, { ...header(), prizes: [] }); assert.equal(cancelled.ok, true); current = cancelled.data;
   assert.equal(current.latestRound.state, "cancelled"); assert.equal(current.roundTotal, number); assert.equal(current.roundOffset, 0); assert.equal(current.remainingCount, consumedRemaining);
  }
  const snapshot = await call("Report"); assert.equal(snapshot.counts.rounds, 50); assert.equal(snapshot.counts.results, 0); assert.equal(snapshot.counts.winners, 0);
  report.commandCounts = { initialReservation: 1, reservationReruns: 49, cancellations: 50, counts: snapshot.counts, remainingCount: current.remainingCount };
 });
 await check("round-fifty-one-first-completed-PNG-text-and-frozen-result", async () => {
  const draw = await call(2028294364, header("immediate")); assert.equal(draw.ok, true); current = draw.data; first = current.latestRound;
  assert.equal(first.number, 51); assert.equal(first.state, "completed"); assert.equal(current.remainingCount, 4);
  await page.locator('nav a[href="#/create"]').click(); await expect(page.getByRole("heading", { name: "추첨 만들기", exact: true })).toBeVisible(); await navigate(route(current.collectionId));
  await expect(page.locator('[name="resultRound"] option')).toHaveCount(50); await expect(page.locator('[name="resultRound"]')).toHaveValue(first.roundId);
  await button("PNG 사진 저장").click(); await expect.poll(async () => (await call("ExportState")).saves).toBe(1); firstPNG = await digest((await call("ExportState")).savePath);
  await button("결과 텍스트 복사").click(); await expect.poll(async () => (await call("ExportState")).copies).toBe(1); report.firstTextSHA256 = jsonHash((await call("ExportState")).copiedText);
  report.firstPNG = firstPNG; report.firstCompleted = { number: 51, roundId: first.roundId, SHA256: jsonHash(immutableRound(first)), remainingCount: 4 };
  const snapshot = await call("Report"); assert.equal(snapshot.counts.rounds, 51); assert.equal(snapshot.counts.results, 1); assert.equal(snapshot.counts.winners, 1);
 });
 await check("round-fifty-two-completes-once-and-preserves-first-result", async () => {
  const draw = await call(2028294364, header("immediate")); assert.equal(draw.ok, true); current = draw.data;
  assert.equal(current.roundTotal, 52); assert.equal(current.latestRound.number, 52); assert.equal(current.latestRound.state, "completed"); assert.equal(current.remainingCount, 3);
  assert.deepEqual(immutableRound(current.rounds.find(round => round.roundId === first.roundId)), immutableRound(first)); assert.notEqual(current.latestRound.winners[0].participant.id, first.winners[0].participant.id);
  const snapshot = await call("Report"); assert.equal(snapshot.counts.rounds, 52); assert.equal(snapshot.counts.results, 2); assert.equal(snapshot.counts.winners, 2); report.completedDraws = 2;
 });
 await check("default-newest-fifty-older-two-and-return-latest-UI", async () => {
  await page.locator('nav a[href="#/create"]').click(); await expect(page.getByRole("heading", { name: "추첨 만들기", exact: true })).toBeVisible(); await navigate(route(current.collectionId));
  await expect(page.locator('[name="resultRound"] option')).toHaveCount(50); await expect(page.locator('[name="resultRound"]')).toHaveValue(current.latestRound.roundId);
  await expect(button("더 최근 회차")).toBeDisabled(); await expect(button("재추첨")).toBeEnabled();
  await page.locator('nav a[href="#/create"]').click(); await expect(page.getByRole("heading", { name: "추첨 만들기", exact: true })).toBeVisible();
  await navigate(route(current.collectionId, current.latestRound.roundId)); await expect.poll(() => page.evaluate(() => window.location.hash)).toBe(route(current.collectionId, current.latestRound.roundId));
  await expect(page.locator('[name="resultRound"]')).toHaveValue(current.latestRound.roundId); await expect(page.locator('[name="resultRound"] option')).toHaveCount(50);
  const earlier = await get(current.collectionId, { roundOffset: 50 }); assert.equal(earlier.ok, true); assert.deepEqual(earlier.data.rounds.map(round => round.number), [1, 2]);
  await button("이전 회차 보기").click(); await expect(page.locator('[name="resultRound"] option')).toHaveCount(2); await expect(page.locator('[name="resultRound"]')).toHaveValue(earlier.data.rounds[1].roundId);
  await expect(button("재추첨")).toBeDisabled(); await expect(button("이전 회차 보기")).toBeDisabled(); await expect(button("PNG 사진 저장")).toBeDisabled();
  await button("더 최근 회차").click(); await expect(page.locator('[name="resultRound"] option')).toHaveCount(50); await expect(page.locator('[name="resultRound"]')).toHaveValue(current.latestRound.roundId); await expect(button("재추첨")).toBeEnabled();
 });
 await check("actual-cancelled-round-one-anchor-has-no-fake-export", async () => {
  await navigate(route(current.collectionId, firstCancelled.roundId)); await expect(page.locator('[name="resultRound"] option')).toHaveCount(2); await expect(page.locator('[name="resultRound"]')).toHaveValue(firstCancelled.roundId);
  await expect(button("재추첨")).toBeDisabled(); await expect(button("PNG 사진 저장")).toBeDisabled(); await expect(button("결과 텍스트 복사")).toBeDisabled();
  const anchored = await get(current.collectionId, { roundId: firstCancelled.roundId }); assert.equal(anchored.ok, true); assert.equal(anchored.data.roundOffset, 50); assert.equal(anchored.data.rounds[0].number, 1); assert.equal(anchored.data.latestRound.number, 52);
  report.cancelledAnchor = { URI: route(current.collectionId, firstCancelled.roundId), offset: 50, exportsDisabled: true };
 });
 await check("old-completed-round-fifty-one-anchor-keeps-PNG-text-readonly", async () => {
  await navigate(route(current.collectionId, first.roundId)); await expect(page.locator('[name="resultRound"] option')).toHaveCount(50); await expect(page.locator('[name="resultRound"]')).toHaveValue(first.roundId); await expect(button("재추첨")).toBeDisabled();
  const anchored = await get(current.collectionId, { roundId: first.roundId }); assert.equal(anchored.ok, true); assert.equal(anchored.data.roundOffset, 0); assert.deepEqual(immutableRound(anchored.data.rounds.find(round => round.roundId === first.roundId)), immutableRound(first)); assert.equal(anchored.data.latestRound.number, 52);
  await button("PNG 사진 저장").click(); await expect.poll(async () => (await call("ExportState")).saves).toBe(2); assert.equal(await digest((await call("ExportState")).savePath), firstPNG);
  await button("결과 텍스트 복사").click(); await expect.poll(async () => (await call("ExportState")).copies).toBe(2); assert.equal(jsonHash((await call("ExportState")).copiedText), report.firstTextSHA256);
  report.anchor = { URI: route(current.collectionId, first.roundId), offset: 0, roundSHA256: jsonHash(immutableRound(first)), PNGUnchanged: true, textUnchanged: true };
 });
 await check("public-Go-bypass-invalid-page-and-anchor-offset-fail-without-writes", async () => {
  const before = await get(current.collectionId);
  for (const options of [{ roundLimit: 51 }, { roundId: firstCancelled.roundId, roundOffset: 1 }]) { const response = await get(current.collectionId, options); assert.equal(response.ok, false); assert.equal(response.code, "InvalidInput"); assert.equal(response.messageKey, "InvalidInput"); assert.equal(response.data, undefined); }
  const after = await get(current.collectionId); assert.deepEqual(after.data, before.data); const snapshot = await call("Report"); assert.equal(snapshot.counts.rounds, 52); assert.equal(snapshot.counts.results, 2); assert.equal(snapshot.counts.winners, 2); report.finalCounts = snapshot.counts;
 });
 await check("route-close-removes-old-native-listeners", async () => {
  await page.locator('[name="resultRound"]').evaluate(element => { globalThis.__roundPageRetired = element; });
  await page.locator('nav a[href="#/create"]').click(); await expect(page.getByRole("heading", { name: "추첨 만들기", exact: true })).toBeVisible();
  assert.equal(await page.evaluate(() => globalThis.__roundPageRetired.isConnected), false);
  const remote = await renderer.send("Runtime.evaluate", { expression: "globalThis.__roundPageRetired", returnByValue: false, allowUnsafeEvalBlockedByCSP: false }); assert.ok(remote.result.objectId);
  try { assert.equal((await renderer.send("DOMDebugger.getEventListeners", { objectId: remote.result.objectId })).listeners.length, 0); } finally { await renderer.send("Runtime.releaseObject", { objectId: remote.result.objectId }); }
  await page.evaluate(() => { delete globalThis.__roundPageRetired; });
 });
 await stop(); report.status = "passed";
} catch (error) {
 report.status = "failed"; report.failedStep = report.phase; report.errorClass = error?.name ?? "Unknown"; report.errorLocation = String(error?.stack ?? "").split("\n").filter(line=>line.includes("product.round-page.mjs")).slice(0,3); process.exitCode = 1;
 if (page) try { report.UI = await page.locator('[data-product-screen="result"]').innerText(); await page.screenshot({ path: resolve(task, "product-round-page-failure.png") }); } catch {}
} finally {
 try { await stop(); } catch (error) { report.status = "failed"; report.cleanupFailure = error?.name ?? "Unknown"; process.exitCode = 1; if (child?.exitCode === null) child.kill(); }
 if (!report.cleanupFailure) {
  const escaped = work.replaceAll("'", "''"), ownedRoot = task.replaceAll("'", "''");
  try { const removed = await runPS("$ErrorActionPreference='Stop'; $roundWork=[IO.Path]::GetFullPath('" + escaped + "'); if((Split-Path -Parent $roundWork) -ne '" + ownedRoot + "' -or (Split-Path -Leaf $roundWork) -notlike 'product-run-round-page-*'){throw 'scope'}; $roundFiles=@(Get-ChildItem -LiteralPath $roundWork -File -Recurse -Force); foreach($roundFile in $roundFiles){$roundStream=[IO.File]::Open($roundFile.FullName,[IO.FileMode]::Open,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None);$roundStream.Dispose()}; Remove-Item -LiteralPath $roundWork -Recurse -Force; Write-Output $roundFiles.Count"); report.profileCleanup = { exclusiveFiles: Number(removed.trim()), removed: true }; }
  catch (error) { report.status = "failed"; report.cleanupFailure = error?.name ?? "Unknown"; process.exitCode = 1; }
 }
 report.endedAt = new Date().toISOString(); report.assets.copyHTMLUnchanged = await digest(resolve(assets, "index.html")) === report.assets.htmlSHA256;
 if (!report.assets.copyHTMLUnchanged) { report.status = "failed"; process.exitCode = 1; }
 await writeFile(reportPath, JSON.stringify(report, null, 2) + "\n"); console.log(JSON.stringify({ status: report.status, checks: report.checks.length, phase: report.phase, report: reportPath, cleanupFailure: report.cleanupFailure }));
}
