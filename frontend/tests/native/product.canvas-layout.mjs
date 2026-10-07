// Actual production assets, public Wails calls and real SQLite. This functional
// probe neither changes product source nor relaxes the original stress driver.
import assert from "node:assert/strict";
import { installCanvasLayoutProbe, verifyCanvasPaintBounds, readResultCanvasFont, findResultSummaryPaint } from "./canvas.layout.mjs";
import { spawn, execFile } from "node:child_process";
import { mkdtemp, readFile, writeFile, copyFile, cp, access } from "node:fs/promises";
import { createServer } from "node:net";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { createHash, randomUUID } from "node:crypto";
import { chromium, expect } from "@playwright/test";
const root = fileURLToPath(new URL("../../../", import.meta.url)), task = resolve(root, ".task");
const sourceHost = resolve(root, process.env.JACKPOT_CANVAS_LAYOUT_HOST ?? ".task/producte2e.exe");
await access(sourceHost); await access(resolve(root, "frontend/dist/index.html"));
const work = await mkdtemp(resolve(task, "product-run-canvas-layout-"));
const assets = await mkdtemp(resolve(task, "product-canvas-layout-assets-"));
assert.equal(dirname(work), task); assert.equal(dirname(assets), task);
const exe = resolve(assets, "product-canvas-layout.exe");
await copyFile(sourceHost, exe); await cp(resolve(root, "frontend/dist"), assets, { recursive: true });
const digest = async path => createHash("sha256").update(await readFile(path)).digest("hex");
const jsonHash = value => createHash("sha256").update(JSON.stringify(value)).digest("hex");
// Round.revision is the current collection projection; immutable roundVersion/input/outcome stay in this oracle.
const immutableRound = ({ revision: _currentCollectionRevision, ...snapshot }) => snapshot;
const reportPath = resolve(task, "product-canvas-layout-report.json");
const report = { status: "running", startedAt: new Date().toISOString(), checks: [], boundary: { host: "owned actual Windows Wails/WebView2", database: "actual SQLite", commands: "public generated method IDs; unique operation per intentional command", initialDraw: "actual UI 100 participants/200 comments; ten named prizes/ten real crypto winners", PNG: "actual structured Canvas/atomic file; owned save path", clipboard: "capture seam; OS clipboard unchanged", timingAcceptance: false },
 assets: { directory: assets, sourceHost, hostSHA256: await digest(exe), htmlSHA256: await digest(resolve(assets, "index.html")) } };
const runPS = command => new Promise((ok, fail) => execFile("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", command], { windowsHide: true, timeout: 30000 }, (error, stdout) => error ? fail(error) : ok(stdout)));
const deadline = async (promise, ms) => { let timer; try { return await Promise.race([promise, new Promise((_, fail) => { timer = setTimeout(() => fail(new Error("OwnedHostDeadline")), ms); })]); } finally { clearTimeout(timer); } };
const server = createServer(); await new Promise((ok, fail) => { server.once("error", fail); server.listen(0, "127.0.0.1", ok); });
const cdp = server.address().port; await new Promise((ok, fail) => server.close(error => error ? fail(error) : ok()));
let child, browser, page, renderer, exit, owned = [], stopped = false;
const check = async (name, action) => { report.phase = name; await action(); report.checks.push(name); console.log("canvas-layout PASS " + name); };
const call = (id, ...args) => page.evaluate(async ({ id, args }) => {
 const dispatch = window._wails.dispatchWailsEvent; const { Call } = await import("/wails/runtime.js"); window._wails.dispatchWailsEvent = dispatch;
 return typeof id === "number" ? Call.ByID(id, ...args) : Call.ByName("main.Probe." + id, ...args);
}, { id, args });
const get = (collectionId, options = {}) => call(1947843826, { collectionId, roundOffset: 0, roundLimit: 0, roundId: "", ...options });
const button = name => page.getByRole("button", { name, exact: true });
const visible = name => page.getByLabel(name, { exact: true }).and(page.locator(":visible"));
const route = (collectionId, roundId = null) => "#/results/" + encodeURIComponent(collectionId) + (roundId === null ? "" : "/rounds/" + encodeURIComponent(roundId));
const prizeNames = Array.from({ length: 10 }, (_, index) => "품목" + index + " " + "한글".repeat(7) + "😀");
assert.ok(prizeNames.every(name => name.length === 20));
let storedCollection, durableBefore, pngSequence = 0;
function verifyStructuredPaint(record) {
 const geometry = verifyCanvasPaintBounds(record);
 assert.equal(record.width,1200);assert.ok(record.height<=8192&&record.width*record.height<=16000000);
 const paints=record.paints,round=storedCollection.latestRound;
 assert.ok(paints.every(paint=>{readResultCanvasFont(paint.font);return paint.baseline==="top";}));
 assert.ok(paints.some(paint=>readResultCanvasFont(paint.font).weight===700));assert.ok(paints.some(paint=>readResultCanvasFont(paint.font).weight===600));
 const title=paints.filter(paint=>readResultCanvasFont(paint.font).weight===700);assert.equal(title.map(paint=>paint.text).join(""),storedCollection.article.title);
 const footer=findResultSummaryPaint(paints,storedCollection.selectedCount,round.winners.length);
 const heading=paints.findIndex(paint=>paint.text===`${round.number}회차 추첨 결과`);assert.ok(heading>=title.length&&heading<footer);
 const body=paints.slice(heading+1,footer),rows=[];let cursor=0;
 // Public metadata precedes badges, then each prize is painted before its own
 // identity fragments. Reassembly checks full Unicode text, not substring/OCR.
 if(round.message!==""){const message=[];while(cursor<body.length&&body[cursor].color!=="#806ab9")message.push(body[cursor++].text);assert.equal(message.join(""),round.message);}
 for(const prize of round.prizes){
  const badge=[];while(cursor<body.length&&body[cursor].color==="#806ab9")badge.push(body[cursor++]);
  const identity=[];while(cursor<body.length&&body[cursor].color!=="#806ab9")identity.push(body[cursor++]);
  assert.ok(badge.length>0&&identity.length>0);assert.equal(badge.map(paint=>paint.text).join(""),prize.name);
  const winners=round.winners.filter(winner=>winner.prizeId===prize.id);assert.equal(winners.length,1);
  const participant=winners[0].participant,kind=participant.kind==="fixed"?"registered":participant.kind==="semi_fixed"?"semi-registered":"anonymous";
  assert.equal(identity.map(paint=>paint.text).join(""),`${participant.nickname} (${participant.publicIdentifier||kind})`);
  assert.ok(badge.every(paint=>paint.x===badge[0].x)&&identity.every(paint=>paint.x===identity[0].x));assert.ok(badge[0].x<identity[0].x);assert.equal(badge[0].y,identity[0].y);
  if(rows.length>0)assert.ok(badge[0].y>rows.at(-1).bottom);
  rows.push({prizeIndex:rows.length,winnerCount:1,labelLines:badge.length,identityLines:identity.length,top:badge[0].y,bottom:Math.max(...badge.map(paint=>paint.bottom),...identity.map(paint=>paint.bottom))});
 }
 assert.equal(cursor,body.length);assert.equal(rows.length,10);assert.equal(new Set(round.winners.map(winner=>winner.participant.id)).size,10);
 const painted=paints.map(paint=>paint.text).join("");
 for(const text of [storedCollection.article.galleryName,storedCollection.article.url,`참가자 ${storedCollection.selectedCount}명 · 당첨자 10명`])assert.ok(painted.includes(text));
 const stamp=value=>new Intl.DateTimeFormat("ko-KR",{timeZone:"Asia/Seoul",dateStyle:"medium",timeStyle:"medium"}).format(Date.parse(value));
 assert.ok(painted.includes(`실행: ${stamp(round.executedAt)} (한국 시간)`));if(round.scheduledAt!==null)assert.ok(painted.includes(`예정: ${stamp(round.scheduledAt)} (한국 시간)`));
 assert.equal(painted.includes(storedCollection.collectionId),false);assert.equal(painted.includes(round.roundId),false);
 return {...geometry,rows,prizes:10,winners:10,opaqueIDsPainted:false};
}
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

try {
 child = spawn(exe, ["-assets", assets, "-work-dir", work, "-cdp-port", String(cdp), "-large-fixture"], { cwd: root, windowsHide: true, shell: false, stdio: ["ignore", "pipe", "pipe"] });
 exit = new Promise((ok, fail) => { child.once("error", fail); child.once("exit", (code, signal) => ok({ code, signal })); }); void exit.catch(() => {});
 let stdout = ""; child.stdout.on("data", chunk => { stdout = (stdout + chunk.toString()).slice(-2048); }); child.stderr.resume(); owned = [child.pid];
 await expect.poll(() => { if (child.exitCode !== null) throw new Error("NativeStartup"); return stdout.includes("JACKPOT_PRODUCT_E2E_READY"); }, { timeout: 30000 }).toBe(true);
 await expect.poll(async () => { try { return (await (await fetch(`http://127.0.0.1:${cdp}/json/list`)).json()).some(target => target.type === "page"); } catch { return false; } }, { timeout: 20000 }).toBe(true);
 browser = await chromium.connectOverCDP(`http://127.0.0.1:${cdp}`, { timeout: 20000, isWebView: true });
 const pages = browser.contexts().flatMap(context => context.pages()); assert.equal(pages.length, 1); page = pages[0]; page.setDefaultTimeout(10000);
 renderer = await page.context().newCDPSession(page); await renderer.send("Emulation.setFocusEmulationEnabled", { enabled: true });
 const browserSession = await browser.newBrowserCDPSession(); const processes = await browserSession.send("SystemInfo.getProcessInfo"); await browserSession.detach();
 owned = [...new Set([...owned, ...processes.processInfo.map(process => process.id)])]; await discover(); report.engine = browser.version(); report.processRoles=Object.fromEntries(processes.processInfo.map(item=>[item.id,item.type]));report.processRoles[child.pid]="go-main";await renderer.send("Performance.enable");
 // Existing media failure fallback stays real; PNG layout needs no external image service.
 await page.route(/^https:\/\/(?:dcimg[1-5]\.dcinside\.com)\//, request => request.abort());
 await check("actual-large-fixed-title-and-ten-winners", async () => {
  await expect(visible("디시인사이드 게시글 주소")).toBeEnabled();
  const fixture = (await call("Report")).fixtureURL;
  await visible("디시인사이드 게시글 주소").fill(fixture); await button("게시글 불러오기").click();
  await expect(button("추첨 생성")).toBeEnabled();
  await expect(page.locator(".create-participants>li")).toHaveCount(100);
  await visible("상품 구성").selectOption("multiple");
  for(let index=1;index<10;index++){await button("상품 추가").click();await expect(visible("상품명 (선택)")).toHaveCount(index+1);}
  for(let index=0;index<10;index++)await visible("상품명 (선택)").nth(index).fill(prizeNames[index]);
  await visible("상품명 (선택)").nth(9).blur();
  await expect.poll(async()=>(await call("DraftState")).prizes.multiple.map(prize=>prize.name)).toEqual(prizeNames);
  const drawMode=visible("추첨 방식");
  // A redundant change event still submits an asynchronous prize edit. Avoid
  // manufacturing that extra command when the authoritative default is correct.
  if(await drawMode.inputValue()!=="immediate")await drawMode.selectOption("immediate");
  await drawMode.blur();
  await expect.poll(async()=>{const draft=await call("DraftState");return{mode:draft.prizes.mode,drawMode:draft.prizes.drawMode,prizes:draft.prizes.multiple.map(prize=>({name:prize.name,count:prize.count}))};},{timeout:15000}).toEqual({mode:"multiple",drawMode:"immediate",prizes:prizeNames.map(name=>({name,count:1}))});
  await expect(page.locator(".create-product")).not.toHaveAttribute("aria-busy","true");await expect(drawMode).toBeEnabled();await expect(button("추첨 생성")).toBeEnabled();
  report.createBarrier={authoritativeMode:"immediate",authoritativePrizeMode:"multiple",prizes:10,winners:10,UIReady:true};
  await button("추첨 생성").click();

  await expect(page.getByRole("heading",{name:"추첨 결과",exact:true})).toBeVisible({timeout:30000});await expect(button("PNG 사진 저장")).toBeEnabled();
  const state=await call("Report"),observed=state.latest[0];assert.equal(observed.latestRound.winners.length,10);assert.equal(observed.participantCount,100);assert.equal(observed.latestRound.prizes.length,10);assert.deepEqual(observed.latestRound.prizes.map(prize=>prize.name),prizeNames);storedCollection=observed;durableBefore=state.counts;
  assert.ok(observed.article.title.length>0);report.fixture={participants:observed.participantCount,winners:10,titleUTF16:observed.article.title.length,collectionId:observed.collectionId,roundId:observed.latestRound.roundId};
  await button("결과 텍스트 복사").click();await expect.poll(async()=>(await call("ExportState")).copies).toBe(1);const copied=(await call("ExportState")).copiedText;assert.ok(copied.includes(observed.collectionId)&&copied.includes(observed.latestRound.roundId));report.modelTextSHA256=jsonHash(copied);report.clipboardTextRetainsOpaqueIDs=true;
 });
 const savePNG=async()=>{const previous=(await call("ExportState")).saves;const start=performance.now();await button("PNG 사진 저장").click();await expect.poll(async()=>(await call("ExportState")).saves).toBe(previous+1);const state=await call("ExportState"),path=resolve(assets,"canvas-layout-"+(pngSequence++)+".png");await copyFile(state.savePath,path);const bytes=await readFile(path);assert.equal(bytes.subarray(0,8).toString("hex"),"89504e470d0a1a0a");assert.equal(bytes.readUInt32BE(16),1200);return{path,sha256:await digest(path),bytes:bytes.length,width:bytes.readUInt32BE(16),height:bytes.readUInt32BE(20),actionMs:performance.now()-start};};
 const memoryCheckpoint=async(label)=>{
  const metrics=Object.fromEntries((await renderer.send("Performance.getMetrics")).metrics.map(item=>[item.name,item.value]));
  const pids=owned.join(",");const raw=await runPS("$metricIds=@("+pids+"); $metricParts=@(); foreach($metricId in $metricIds){$metricProcess=Get-Process -Id $metricId -ErrorAction SilentlyContinue;if($metricProcess){try{$metricProcess.Refresh();$metricParts+=@{id=$metricId;workingSet64=$metricProcess.WorkingSet64;privateBytes64=$metricProcess.PrivateMemorySize64;handles=$metricProcess.HandleCount}}finally{$metricProcess.Dispose()}}}; ConvertTo-Json -InputObject $metricParts -Compress");
  const parts=JSON.parse(raw);report.memory??=[];report.memory.push({label,observedUTC:new Date().toISOString(),JSHeapUsedSize:metrics.JSHeapUsedSize,JSHeapTotalSize:metrics.JSHeapTotalSize,Nodes:metrics.Nodes,Documents:metrics.Documents,JSEventListeners:metrics.JSEventListeners,processes:parts.map(part=>({...part,role:report.processRoles[part.id]??"owned-other"})),go:await call("Memory")});
 };
 const baseline=await savePNG();report.baselinePNG=baseline;await memoryCheckpoint("baseline-after-uninstrumented-save");
 await check("bundled-font-loaded-offline-regular-and-bold",async()=>{
  report.fonts=await page.evaluate(()=>({faces:[...document.fonts].filter(face=>face.family.replaceAll('"',"")==="Jackpot Result").map(face=>({family:face.family,weight:face.weight,status:face.status})),checks:[400,600,700].map(weight=>({weight,loaded:document.fonts.check(`${weight} 35px "Jackpot Result"`,"가나다")})),resources:performance.getEntriesByType("resource").filter(entry=>/\.woff2(?:\?|$)/.test(entry.name)).map(entry=>({url:entry.name,local:new URL(entry.name).origin===location.origin}))}));
  assert.ok(report.fonts.faces.length>0&&report.fonts.faces.every(face=>face.status==="loaded"));assert.ok(report.fonts.checks.every(check=>check.loaded));assert.ok(report.fonts.resources.length>0&&report.fonts.resources.every(resource=>resource.local));
 });
 await page.evaluate(installCanvasLayoutProbe,{capturePaint:true});
 report.measurements=[];
 await check("passive-original-layout-PNG-repeated-without-cache",async()=>{
  for(let cycle=0;cycle<3;cycle++){
   const saved=await savePNG();assert.equal(saved.sha256,baseline.sha256);report.measurements.push({cycle,action:"save",...saved});
   await button("PNG 미리보기").click();const image=page.getByAltText("Jackpot 로컬 추첨 결과 미리보기");await expect(image).toBeVisible();await expect.poll(()=>image.evaluate(element=>element.naturalWidth),{timeout:20000}).toBe(1200);
   await button("미리보기 닫기").click();await expect(image).toBeHidden();assert.equal(await image.getAttribute("src"),null);
   const stats=await page.evaluate(()=>window.__jackpotCanvasLayoutProbe.snapshot());assert.equal(stats.observerFaults,0);assert.equal(stats.totalContexts,(cycle+1)*2);assert.equal(stats.records.length,(cycle+1)*2);
   for(const record of stats.records){assert.equal(record.pixelsReleased,true);assert.equal(record.trackingTruncated,false);assert.equal(record.measureFailures,0);assert.equal(record.fillFailures,0);assert.equal(record.toBlobFailures,0);assert.equal(record.callbacks,1);assert.ok(record.calls>0);assert.equal(record.blobBytes,baseline.bytes);assert.equal(record.blobType,"image/png");record.geometry=verifyStructuredPaint(record);}
   report.probe=stats;await memoryCheckpoint("after-save-preview-"+String(cycle+1));
  }
 });
 await check("same-snapshot-route-reopen-recomputes-layout-and-disposes-observer",async()=>{
  const id=report.fixture.collectionId;await page.locator('nav a[href="#/create"]').click();await expect(page.getByRole("heading",{name:"추첨 만들기",exact:true})).toBeVisible();await navigate(route(id));
  const saved=await savePNG();assert.equal(saved.sha256,baseline.sha256);report.measurements.push({cycle:3,action:"route-reopen-save",...saved});
  const stats=await page.evaluate(()=>window.__jackpotCanvasLayoutProbe.snapshot());assert.equal(stats.totalContexts,7);assert.ok(stats.records.every(record=>record.pixelsReleased));for(const record of stats.records)record.geometry=verifyStructuredPaint(record);report.probe=stats;
  await memoryCheckpoint("after-route-reopen-save");
 });
 await check("same-stored-model-dark-DPR2-exports-identical-PNG",async()=>{
  const theme=page.locator('select[name="theme"]'),previous=await theme.inputValue();await theme.selectOption("dark");await expect(page.locator(".shell")).toHaveAttribute("data-appearance","dark");await renderer.send("Emulation.setDeviceMetricsOverride",{width:1024,height:720,deviceScaleFactor:2,mobile:false});await expect.poll(()=>page.evaluate(()=>devicePixelRatio)).toBe(2);await expect(button("PNG 사진 저장")).toBeEnabled();
  const saved=await savePNG();assert.equal(saved.sha256,baseline.sha256);report.measurements.push({action:"dark-renderer-DPR2",...saved});
  const stats=await page.evaluate(()=>window.__jackpotCanvasLayoutProbe.snapshot());assert.equal(stats.observerFaults,0);assert.equal(stats.totalContexts,8);for(const record of stats.records){assert.equal(record.pixelsReleased,true);record.geometry=verifyStructuredPaint(record);}report.probe=stats;
  await renderer.send("Emulation.clearDeviceMetricsOverride");await theme.selectOption(previous);await expect(button("PNG 사진 저장")).toBeEnabled();
  const response=await get(storedCollection.collectionId);assert.equal(response.ok,true);assert.deepEqual(response.data,storedCollection);const after=await call("Report");assert.deepEqual(after.counts,durableBefore);assert.deepEqual(after.latest[0],storedCollection);report.readonlySnapshotUnchanged=true;
  await page.evaluate(()=>{window.__jackpotCanvasLayoutProbe.dispose();});assert.equal(await page.evaluate(()=>window.__jackpotCanvasLayoutProbe===undefined),true);report.observerDisposed=true;
 });
 await stop();report.status="passed";
} catch(error) {
 report.status="failed";report.failedStep=report.phase;report.errorClass=error?.name??"Unknown";report.errorMessage=String(error?.message??error).slice(0,4000);report.errorLocation=String(error?.stack??"").split("\n").filter(line=>line.includes("product.canvas-layout.mjs")).slice(0,3);process.exitCode=1;
 if(page)try{report.probe=await page.evaluate(()=>window.__jackpotCanvasLayoutProbe?.snapshot());report.visibleAlerts=await page.locator('[role="alert"]:visible').allTextContents();report.failureScreenshot=resolve(task,"product-canvas-layout-failure.png");await page.screenshot({path:report.failureScreenshot});}catch{}
 if(page)try{const draft=await call("DraftState");report.failedDraft={summary:draft.summary,prizes:draft.prizes,included:draft.included};}catch{}
} finally {
 if(page)try{await page.evaluate(()=>window.__jackpotCanvasLayoutProbe?.dispose());}catch{}
 try{await stop();}catch(error){report.status="failed";report.cleanupFailure=error?.name??"Unknown";report.cleanupFailureMessage=String(error?.message??error).slice(0,2000);process.exitCode=1;if(child?.exitCode===null)child.kill();}
 if(!report.cleanupFailure){const escaped=work.replaceAll("'","''"),ownedRoot=task.replaceAll("'","''");try{const removed=await runPS("$ErrorActionPreference='Stop'; $layoutWork=[IO.Path]::GetFullPath('"+escaped+"'); if((Split-Path -Parent $layoutWork) -ne '"+ownedRoot+"' -or (Split-Path -Leaf $layoutWork) -notlike 'product-run-canvas-layout-*'){throw 'scope'}; $layoutFiles=@(Get-ChildItem -LiteralPath $layoutWork -File -Recurse -Force); foreach($layoutFile in $layoutFiles){$layoutStream=[IO.File]::Open($layoutFile.FullName,[IO.FileMode]::Open,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None);$layoutStream.Dispose()}; Remove-Item -LiteralPath $layoutWork -Recurse -Force; Write-Output $layoutFiles.Count");report.profileCleanup={exclusiveFiles:Number(removed.trim()),removed:true};}catch(error){report.status="failed";report.cleanupFailure=error?.name??"Unknown";process.exitCode=1;}}
 report.endedAt=new Date().toISOString();report.assets.copyHTMLUnchanged=await digest(resolve(assets,"index.html"))===report.assets.htmlSHA256;if(!report.assets.copyHTMLUnchanged){report.status="failed";process.exitCode=1;}
 report.boundary={...report.boundary,initialDraw:"actual 100 participant/200 comment fixture UI/collector/commit, one completed round/ten prizes/ten winners",commands:"actual UI; no raw retry/candidate caching",measurement:"test-only passive Canvas forwarding/count/timing plus exact native paint metrics; bounded public fixture text capture; not memory/performance acceptance",DPI:"one isolated renderer DPR2 comparison; Windows desktop DPI unchanged",content:"painted title/gallery/URL/round/time/counts/prize/full winner identity; opaque IDs retained in clipboard text only",cache:"no cache/no production changes"};
 await writeFile(reportPath,JSON.stringify(report,null,2)+"\n");console.log(JSON.stringify({status:report.status,checks:report.checks.length,phase:report.phase,report:reportPath,cleanupFailure:report.cleanupFailure}));
}
