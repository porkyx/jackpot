// Actual scheduled Go exit/restart with production Wails UI, parser, entropy and SQLite.
import assert from "node:assert/strict";
import { spawn, execFile } from "node:child_process";
import { mkdtemp, readFile, writeFile, access } from "node:fs/promises";
import { createServer } from "node:net";
import { resolve, basename } from "node:path";
import { fileURLToPath } from "node:url";
import { createHash } from "node:crypto";
import { chromium, expect } from "@playwright/test";
import { ownedWorkPath, requireOwnedWebView, psQuote } from "./webview-recovery.guards.mjs";
import {normalizeReservationReport,immutableReservationRound,requireDistinctReservationWinners} from "./reservationrestart.invariants.mjs";

const root=fileURLToPath(new URL("../../../",import.meta.url)),task=resolve(root,".task");
const exe=resolve(task,"product-reservation-restart.exe"),assets=resolve(root,"frontend/dist"),reportPath=resolve(task,"reservation-restart-report.json");
const report={status:"running",checks:[],runs:[],boundaries:{native:"actual Windows Wails/WebView2 and unmodified production dist",parser:"production DCInside collector parser",network:"deterministic HTTP RoundTripper fixture; external network0",database:"actual isolated SQLite reused across two real Go processes",entropy:"real OS crypto/rand",clock:"first ephemeral offset+5 then30s schedule; new process offset0 then controlled Advance40",stop:"normal E2E Probe.Stop; production close-warning hook has separate evidence",failureFallback:"only exact owned executable/command/CreationDate/StartTime handle kill; no forced kill claimed on normal success",OSsleep:"not exercised",performance:"functional E2E; no timing acceptance sample"}};
let work,active,phase="preflight";const owned=new Map();
const runPS=command=>new Promise((ok,fail)=>execFile("powershell.exe",["-NoProfile","-NonInteractive","-Command",command],{windowsHide:true,timeout:35000,maxBuffer:1024*1024},(error,out,err)=>error?fail(new Error("Owned reservation command failed: "+err.slice(-1200),{cause:error})):ok(out)));
const check=async(name,action)=>{phase=name;console.log("reservation restart phase "+name);await action();report.checks.push(name);await writeFile(reportPath,JSON.stringify(report,null,2));};
const visible=name=>active.page.getByLabel(name,{exact:true}).and(active.page.locator(":visible"));
const button=name=>active.page.getByRole("button",{name,exact:true});
const call=(method,...args)=>active.page.evaluate(async({method,args})=>{const dispatch=window._wails.dispatchWailsEvent;const{Call}=await import("/wails/runtime.js");window._wails.dispatchWailsEvent=dispatch;return Call.ByName("main.Probe."+method,...args)},{method,args});
const snapshot=async()=>normalizeReservationReport(await call("Report"));
function counts(state){const raw=state.Counts??state.counts;assert.ok(raw&&typeof raw==="object");return Object.fromEntries(Object.entries(raw).map(([key,value])=>{assert.ok(Number.isSafeInteger(value)&&value>=0);return[key[0].toLowerCase()+key.slice(1),value]}));}
function latest(state){assert.equal(state.latest.length,1);return state.latest[0];}
function winners(collection){return requireDistinctReservationWinners(collection);}
async function freePort(){const server=createServer();await new Promise((ok,fail)=>{server.once("error",fail);server.listen(0,"127.0.0.1",ok)});const port=server.address().port;await new Promise((ok,fail)=>server.close(error=>error?fail(error):ok()));return port;}
async function nativeRecords(){
 const out=await runPS("$ErrorActionPreference='Stop'; $proof="+psQuote(work)+"; $entries=@(Get-CimInstance Win32_Process -Filter \"Name='msedgewebview2.exe'\" | Where-Object {$_.CommandLine -like ('*'+$proof+'*')} | ForEach-Object { $entry=$_; $process=Get-Process -Id $entry.ProcessId -ErrorAction SilentlyContinue; if($process){try{$started=$process.StartTime;if($null -ne $started){@{id=[int]$entry.ProcessId;name=$entry.Name;command=$entry.CommandLine;created=$entry.CreationDate.ToUniversalTime().ToString('o');started=$started.ToUniversalTime().ToString('o')}}}finally{$process.Dispose()}} }); ConvertTo-Json -InputObject $entries -Depth 4 -Compress");
 const records=JSON.parse(out);assert.ok(Array.isArray(records));for(const value of records){const record=requireOwnedWebView(value,work,root);owned.set(record.id+":"+record.started,record);}return records;
}
async function goRecord(instance){
 const out=await runPS("$ErrorActionPreference='Stop'; $entry=Get-CimInstance Win32_Process -Filter 'ProcessId="+instance.child.pid+"'; if(-not $entry){throw 'Owned Go process absent'}; $process=Get-Process -Id $entry.ProcessId -ErrorAction Stop; try { @{id=[int]$entry.ProcessId;name=$entry.Name;exe=$entry.ExecutablePath;command=$entry.CommandLine;created=$entry.CreationDate.ToUniversalTime().ToString('o');started=$process.StartTime.ToUniversalTime().ToString('o')}|ConvertTo-Json -Compress } finally {$process.Dispose()}");
 const value=JSON.parse(out);assert.equal(value.id,instance.child.pid);assert.equal(value.name.toLowerCase(),basename(exe).toLowerCase());assert.equal(value.exe.toLowerCase(),exe.toLowerCase());assert.ok(value.command.includes(work));assert.ok(Number.isFinite(Date.parse(value.created))&&Number.isFinite(Date.parse(value.started)));return value;
}
async function killOwnedGo(instance){
 const proof=instance.proof??await goRecord(instance);assert.equal(proof.id,instance.child.pid);
 await runPS("$ErrorActionPreference='Stop'; $entry=Get-CimInstance Win32_Process -Filter 'ProcessId="+proof.id+"'; if(-not $entry){exit 0}; if($entry.ExecutablePath -ine "+psQuote(exe)+" -or $entry.CommandLine -cne "+psQuote(proof.command)+" -or $entry.CreationDate.ToUniversalTime().ToString('o') -cne "+psQuote(proof.created)+"){throw 'Go kill ownership changed'}; $process=Get-Process -Id $entry.ProcessId -ErrorAction Stop; try{if($process.StartTime.ToUniversalTime().ToString('o') -cne "+psQuote(proof.started)+"){throw 'Go PID reused'}; $process.Kill();if(-not $process.WaitForExit(10000)){throw 'Owned Go did not exit'}}finally{$process.Dispose()}");
 report.failureFallbackUsed=true;
}
async function waitExit(instance){let timer;try{return await Promise.race([instance.exit,new Promise((_,fail)=>{timer=setTimeout(()=>fail(new Error("Owned Go exit timeout")),15000)})]);}finally{clearTimeout(timer)}}
async function stop({failure=false}={}){
 if(!active)return;const instance=active;
 await nativeRecords();
 let exited;
 try{await call("Stop");exited=await waitExit(instance);}catch(error){if(!failure)throw error;if(instance.child.exitCode===null)await killOwnedGo(instance);exited=await waitExit(instance);}
 if(!failure)assert.deepEqual(exited,{code:0,signal:null});
 await instance.browser?.close().catch(()=>{});active=undefined;
 await nativeRecords();
 const proof=[...owned.values()];
 await runPS("$ErrorActionPreference='Stop'; $proofs="+psQuote(JSON.stringify(proof))+"|ConvertFrom-Json; $deadline=[DateTime]::UtcNow.AddSeconds(20); foreach($proof in $proofs){$process=Get-Process -Id ([int]$proof.id) -ErrorAction SilentlyContinue;if($process){try{$started=$process.StartTime;if($null -ne $started -and $started.ToUniversalTime().ToString('o') -ceq $proof.started){$remaining=[int]($deadline-[DateTime]::UtcNow).TotalMilliseconds;if($remaining -le 0 -or -not $process.WaitForExit($remaining)){throw 'Owned WebView process did not exit'}}}finally{$process.Dispose()}}};exit 0");
 assert.deepEqual(await nativeRecords(),[]);
 instance.run.exit=exited;instance.run.ownedProcessInstancesGone=true;
 await runPS("$ErrorActionPreference='Stop'; Get-ChildItem -LiteralPath "+psQuote(work)+" -File -Recurse|ForEach-Object{$handle=[IO.File]::Open($_.FullName,[IO.FileMode]::Open,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None);$handle.Dispose()};exit 0");
 instance.run.dbProfileExclusiveOpen=true;
}
async function launch(){
 const cdp=await freePort();const child=spawn(exe,["-assets",assets,"-work-dir",work,"-cdp-port",String(cdp)],{cwd:root,windowsHide:true,stdio:["ignore","pipe","pipe"]});
 const instance={child,logs:"",stderr:"",run:{pid:child.pid,cdpPort:cdp}};active=instance;report.runs.push(instance.run);
 instance.exit=new Promise((ok,fail)=>{child.once("error",fail);child.once("exit",(code,signal)=>ok({code,signal}))});void instance.exit.catch(()=>{});
 child.stdout.on("data",value=>{instance.logs=(instance.logs+value).slice(-10000)});child.stderr.on("data",value=>{instance.stderr=(instance.stderr+value).slice(-4000)});
 instance.proof=await goRecord(instance);instance.run.creationTime=instance.proof.created;instance.run.startTime=instance.proof.started;
 await expect.poll(()=>{if(child.exitCode!==null)throw new Error("Owned host exited before ready: "+instance.stderr);return instance.logs.includes("JACKPOT_PRODUCT_E2E_READY")},{timeout:30000}).toBe(true);
 await expect.poll(async()=>{if(child.exitCode!==null)throw new Error("Owned host exited before CDP");try{return(await(await fetch("http://127.0.0.1:"+cdp+"/json/list")).json()).some(target=>target.type==="page")}catch{return false}},{timeout:20000}).toBe(true);
 instance.browser=await chromium.connectOverCDP("http://127.0.0.1:"+cdp,{isWebView:true,timeout:20000});const pages=instance.browser.contexts().flatMap(context=>context.pages());assert.equal(pages.length,1);instance.page=pages[0];instance.page.setDefaultTimeout(15000);
 const session=await instance.page.context().newCDPSession(instance.page);try{await session.send("Emulation.setFocusEmulationEnabled",{enabled:true});}finally{await session.detach();}
 await expect(instance.page.getByRole("heading",{name:"Jackpot",exact:true})).toBeVisible({timeout:30000});await nativeRecords();
 return instance;
}
async function result(id){await active.page.evaluate(value=>{location.hash="#/results/"+encodeURIComponent(value)},id);await expect(active.page.getByRole("heading",{name:"추첨 결과",exact:true})).toBeVisible();await expect(active.page.locator('select[name="resultRound"]')).toBeEnabled();}
async function clockProof(offset){const before=Date.now();const state=await snapshot();const after=Date.now();assert.ok(Date.parse(state.now)>=before+offset*1000-1000&&Date.parse(state.now)<=after+offset*1000+1000,"Go ephemeral clock offset mismatch");return state;}

try{
 assert.equal(process.platform,"win32");await access(exe);await access(resolve(assets,"index.html"));
 report.driverSHA256=createHash("sha256").update(await readFile(fileURLToPath(import.meta.url))).digest("hex");report.invariantsSHA256=createHash("sha256").update(await readFile(resolve(root,"frontend/tests/native/reservationrestart.invariants.mjs"))).digest("hex");
 report.exeSHA256=createHash("sha256").update(await readFile(exe)).digest("hex");report.assetIndexSHA256=createHash("sha256").update(await readFile(resolve(assets,"index.html"))).digest("hex");
 work=await mkdtemp(resolve(task,"product-run-webview-reservation-"));ownedWorkPath(work,root);report.workDir=work;
 await check("actual-native-empty-boot-and-ephemeral-first-clock-offset",async()=>{await launch();await expect(visible("디시인사이드 게시글 주소")).toBeEnabled({timeout:30000});const empty=await snapshot();assert.equal(counts(empty).collections,0);await call("Advance",5);report.firstClock=await clockProof(5);});
 let scheduled,scheduledCollection;
 await check("actual-collector-local-reservation-has-zero-outcome",async()=>{
  await visible("디시인사이드 게시글 주소").fill(report.firstClock.fixtureURL);await button("게시글 불러오기").click();await expect(button("추첨 생성")).toBeEnabled();await expect(active.page.locator(".create-participants>li")).toHaveCount(5);
  await visible("상품명 (선택)").fill("예약 첫 상품");await visible("당첨 인원").fill("1");await visible("추첨 방식").selectOption("reservation");await button("추첨 생성").click();
  await expect(active.page.getByRole("heading",{name:"추첨 결과",exact:true})).toBeVisible();await expect(button("30초 후")).toBeEnabled();const pending=await snapshot();assert.equal(latest(pending).rounds[0].state,"pending_schedule");assert.equal(counts(pending).results,0);assert.equal(counts(pending).winners,0);
  await button("30초 후").click();await expect(active.page.locator('select[name="resultRound"] option')).toContainText("예약됨");scheduled=await snapshot();scheduledCollection=latest(scheduled);assert.equal(scheduledCollection.rounds[0].state,"scheduled");assert.equal(scheduledCollection.rounds[0].winners.length,0);assert.equal(counts(scheduled).results,0);assert.equal(counts(scheduled).winners,0);assert.equal(counts(scheduled).participants,6);assert.ok(Date.parse(scheduledCollection.rounds[0].scheduledAt)>Date.parse(scheduled.now));assert.equal(scheduled.fixtureCalls,scheduled.fixtureBodiesClosed);assert.ok(scheduled.fixtureCalls>=2);report.scheduled=scheduled;active.run.session=scheduled.session;
 });
 await check("scheduled-Go-process-exits-with-no-result-and-releases-owned-profile",async()=>{await stop();assert.equal(report.runs.length,1);assert.equal(report.runs[0].exit.code,0);assert.equal(report.runs[0].ownedProcessInstancesGone,true);});
 let restored;
 await check("same-DB-new-Go-session-and-original-absolute-deadline",async()=>{
  await launch();await result(scheduledCollection.collectionId);await expect(button("예약 취소")).toBeEnabled();restored=await clockProof(0);active.run.session=restored.session;assert.notEqual(restored.session,scheduled.session);assert.notEqual(active.child.pid,report.runs[0].pid);assert.equal(restored.fixtureCalls,0);assert.deepEqual(counts(restored),counts(scheduled));assert.equal(latest(restored).collectionId,scheduledCollection.collectionId);assert.deepEqual(latest(restored).rounds,scheduledCollection.rounds);assert.equal(latest(restored).rounds[0].state,"scheduled");assert.ok(Date.parse(restored.now)<Date.parse(latest(restored).rounds[0].scheduledAt));await expect(button("재추첨")).toBeDisabled();report.restoredBeforeDue=restored;report.clockOffsetResetWithDeadlineImmutable=true;
 });
 let completed,firstRound;
 await check("controlled-new-Go-clock-jump-completes-approved-local-reservation-once",async()=>{
  await call("Advance",40);await button("다시 조회").click();await expect(active.page.locator('select[name="resultRound"] option')).toContainText("완료");completed=await snapshot();const collection=latest(completed);firstRound=collection.rounds[0];assert.equal(firstRound.state,"completed");assert.equal(firstRound.winners.length,1);assert.equal(counts(completed).results,1);assert.equal(counts(completed).winners,1);assert.equal(firstRound.scheduledAt,scheduledCollection.rounds[0].scheduledAt);assert.ok(Date.parse(firstRound.executedAt)>Date.parse(firstRound.scheduledAt));assert.equal(completed.fixtureCalls,0);
  await call("Advance",0);const duplicate=await snapshot();assert.deepEqual(counts(duplicate),counts(completed));assert.deepEqual(latest(duplicate),collection);report.delayedCompleted=completed;report.delayMilliseconds=Date.parse(firstRound.executedAt)-Date.parse(firstRound.scheduledAt);
 });
 await check("restarted-local-completion-enables-management-without-credentials",async()=>{await expect(button("재추첨")).toBeEnabled();await expect(active.page.locator('input[type="password"]')).toHaveCount(0);await expect(button("관리 잠금 해제")).toHaveCount(0);const restored=await snapshot();assert.deepEqual(counts(restored),counts(completed));assert.deepEqual(latest(restored).rounds,latest(completed).rounds);assert.equal(latest(restored).revision,latest(completed).revision);report.managementRestored=true;});
 await check("actual-Rerun-new-round-two-distinct-winners-and-immutable-scheduled-outcome",async()=>{
  await button("재추첨").click();const dialog=active.page.locator("dialog[open]");await dialog.locator('[name="prizeName1"]').fill("예약 후 재추첨 상품");await button("새 회차 추첨").click();await expect(active.page.locator('select[name="resultRound"] option')).toHaveCount(2);await expect(active.page.locator('select[name="resultRound"] option').last()).toContainText("완료");const final=await snapshot();const collection=latest(final);assert.equal(collection.rounds.length,2);assert.equal(collection.rounds[1].state,"completed");assert.equal(collection.rounds[1].winners.length,1);assert.deepEqual(immutableReservationRound(collection.rounds[0]),immutableReservationRound(firstRound));const ids=winners(collection);assert.equal(ids.length,2);assert.equal(new Set(ids).size,2);assert.equal(counts(final).results,2);assert.equal(counts(final).winners,2);assert.equal(counts(final).rounds,2);assert.equal(final.fixtureCalls,0);assert.equal(collection.selectedCount,latest(completed).selectedCount);assert.equal(collection.remainingCount,latest(completed).remainingCount-1);report.final=final;report.winnerIDs=ids;
 });
 await check("second-Go-normal-exit-all-owned-PID-handles-and-profile-cleanup",async()=>{await stop();assert.equal(report.runs.length,2);assert.ok(report.runs.every(run=>run.exit.code===0&&run.exit.signal===null&&run.ownedProcessInstancesGone&&run.dbProfileExclusiveOpen));report.ownedProcessInstances=[...owned.values()].map(({id,started,role})=>({id,started,role}));});
 report.status="passed";
}catch(error){report.status="failed";report.failedPhase=phase;report.error=error instanceof Error?error.message.slice(0,1600):"unknown";if(active?.page){try{report.failureSnapshot=await snapshot();await active.page.screenshot({path:resolve(task,"reservation-restart-failure.png")});}catch{}};process.exitCode=1;
}finally{
 if(active){try{await stop({failure:true});}catch(error){report.cleanupError=error instanceof Error?error.message:"unknown";process.exitCode=1;report.status="failed";}}
 if(work&&!active&&!report.cleanupError){try{assert.deepEqual(await nativeRecords(),[]);ownedWorkPath(work,root);await runPS("$ErrorActionPreference='Stop';$target=[IO.Path]::GetFullPath("+psQuote(work)+");$expected=[IO.Path]::GetFullPath("+psQuote(task)+");if([IO.Path]::GetDirectoryName($target) -ine $expected -or -not [IO.Path]::GetFileName($target).StartsWith('product-run-webview-reservation-')){throw 'Reservation cleanup escaped workspace'};Get-ChildItem -LiteralPath $target -File -Recurse|ForEach-Object{$handle=[IO.File]::Open($_.FullName,[IO.FileMode]::Open,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None);$handle.Dispose()};Remove-Item -LiteralPath $target -Recurse -Force;if(Test-Path -LiteralPath $target){throw 'Reservation profile cleanup incomplete'};exit 0");report.ownedProcessesExited=true;report.dbProfileExclusiveAndRemoved=true;}catch(error){report.cleanupError=error instanceof Error?error.message:"unknown";report.status="failed";process.exitCode=1;}}
 report.forcedKillClaim=false;if(report.failureFallbackUsed)assert.equal(report.status,"failed");await writeFile(reportPath,JSON.stringify(report,null,2)+"\n");console.log(JSON.stringify({status:report.status,checks:report.checks,failedPhase:report.failedPhase,report:reportPath}));
}