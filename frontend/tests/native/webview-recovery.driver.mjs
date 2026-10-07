// Actual isolated native process failures; no page.reload(), HTTP product server or mocked Go state.
import assert from "node:assert/strict";
import { spawn, execFile } from "node:child_process";
import { mkdtemp, readFile, writeFile, access } from "node:fs/promises";
import { createServer } from "node:net";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { createHash, randomUUID } from "node:crypto";
import { chromium, expect } from "@playwright/test";
import { ownedWorkPath, requireOwnedWebView, selectCrashProcess, requireUnchangedGoState, requireFreshBoot, psQuote } from "./webview-recovery.guards.mjs";
const root=fileURLToPath(new URL("../../../",import.meta.url)), task=resolve(root,".task");
const exe=resolve(task,"product-webview-recovery.exe"), assets=resolve(root,"frontend/dist"), reportPath=resolve(task,"webview-recovery-report.json");
const report={status:"running",checks:[],recoveries:[],boundaries:{network:"deterministic HTTP RoundTripper fixture with production DCInside parser; no network access",native:"actual Wails beta.28 Windows WebView2",rendererFailure:"owned renderer Process.Kill; Wails native existing controller renavigation",browserFailure:"owned browser Process.Kill; Wails native new Chromium/controller on existing HWND",GoState:"same live Go service/session/SQLite/draft/management authority",OSPowerSleep:"not exercised",nativeNewWindowObject:"not exercised; existing Wails Window/controller recovery is distinguished",resources:"CDP window/document native listener counts and owned process/profile cleanup; no JS cleanup callback inferred after abrupt process death"}};
const guide="현재 초안을 다시 불러왔습니다. 이전 화면에서 전송하지 않은 입력은 초기화되었습니다.";
let work,child,exit,browser,page,cdpPort,phase="preflight",logs="",stderr="",bootMethod;const browsers=[],owned=new Map();
const check=async(name,fn)=>{phase=name;console.log("recovery phase "+name);await fn();report.checks.push(name);await writeFile(reportPath,JSON.stringify(report,null,2));};
const runPS=command=>new Promise((ok,fail)=>execFile("powershell.exe",["-NoProfile","-NonInteractive","-Command",command],{windowsHide:true,timeout:35000,maxBuffer:1024*1024},(error,stdout,err)=>error?fail(new Error("Owned native command failed: "+err.slice(-1500),{cause:error})):ok(stdout)));
async function freePort(){const server=createServer();await new Promise((ok,fail)=>{server.once("error",fail);server.listen(0,"127.0.0.1",ok)});const value=server.address().port;await new Promise((ok,fail)=>server.close(err=>err?fail(err):ok()));return value;}
async function nativeRecords(){
 const raw=await runPS("$ErrorActionPreference='Stop'; $proof="+psQuote(work)+"; $entries=@(Get-CimInstance Win32_Process -Filter \"Name='msedgewebview2.exe'\" | Where-Object {$_.CommandLine -like ('*'+$proof+'*')} | ForEach-Object { $entry=$_; $process=Get-Process -Id $entry.ProcessId -ErrorAction Stop; try { @{id=[int]$entry.ProcessId;name=$entry.Name;command=$entry.CommandLine;created=$entry.CreationDate.ToUniversalTime().ToString('o');started=$process.StartTime.ToUniversalTime().ToString('o')} } finally {$process.Dispose()} }); ConvertTo-Json -InputObject $entries -Depth 4 -Compress");
 const records=JSON.parse(raw);assert.ok(Array.isArray(records));
 for(const record of records){const valid=requireOwnedWebView(record,work,root);owned.set(valid.id+":"+valid.started,valid);}return records;
}
async function systemProcesses(){const session=await browser.newBrowserCDPSession();try{return(await session.send("SystemInfo.getProcessInfo")).processInfo;}finally{await session.detach();}}
async function connect(){
 await expect.poll(async()=>{if(child.exitCode!==null)throw new Error("Go process exited during WebView recovery");try{const response=await fetch("http://127.0.0.1:"+cdpPort+"/json/list");return(await response.json()).some(target=>target.type==="page")}catch{return false}},{timeout:30000}).toBe(true);
 browser=await chromium.connectOverCDP("http://127.0.0.1:"+cdpPort,{isWebView:true,timeout:20000});browsers.push(browser);
 await expect.poll(()=>browser.contexts().flatMap(context=>context.pages()).length,{timeout:20000}).toBe(1);page=browser.contexts().flatMap(context=>context.pages())[0];page.setDefaultTimeout(15000);
 const session=await page.context().newCDPSession(page);try{await session.send("Emulation.setFocusEmulationEnabled",{enabled:true});}finally{await session.detach();}
 await expect(page.getByRole("heading",{name:"Jackpot",exact:true})).toBeVisible({timeout:30000});await nativeRecords();
}
const button=name=>page.getByRole("button",{name,exact:true});
const visible=name=>page.getByLabel(name,{exact:true}).and(page.locator(":visible"));
const call=(method,...args)=>page.evaluate(async({method,args})=>{const dispatch=window._wails.dispatchWailsEvent;const{Call}=await import("/wails/runtime.js");window._wails.dispatchWailsEvent=dispatch;return typeof method==="number"?Call.ByID(method,...args):Call.ByName(method,...args)},{method,args});
const snapshot=()=>call("main.Probe.Report"), draftState=()=>call("main.Probe.DraftState"), bootstrap=()=>call(bootMethod);
async function readyCreate(){await expect(page.getByRole("heading",{name:"추첨 만들기",exact:true})).toBeVisible({timeout:30000});await expect(visible("디시인사이드 게시글 주소")).toBeEnabled({timeout:30000});}
async function result(collectionId){await page.evaluate(id=>{location.hash="#/results/"+encodeURIComponent(id)},collectionId);await expect(page.getByRole("heading",{name:"추첨 결과",exact:true})).toBeVisible();await expect(page.locator('select[name="resultRound"]')).toBeEnabled();}
async function newDraft(){await page.locator('nav a[href="#/create"]').click();await readyCreate();await expect(visible("디시인사이드 게시글 주소")).toBeEnabled();await visible("디시인사이드 게시글 주소").fill((await snapshot()).fixtureURL);await button("게시글 불러오기").click();await expect(button("추첨 생성")).toBeEnabled();assert.equal((await draftState()).participants,6);await expect(page.locator(".create-participants>li")).toHaveCount(5);}
async function globalResources(){
 // Warm Playwright's main-world utility listeners before the baseline.
 await page.locator("html").evaluate(element=>element.nodeName);
 const session=await page.context().newCDPSession(page);try{
  const listeners=await session.send("Runtime.evaluate",{expression:"JSON.stringify({window:Object.fromEntries(Object.entries(getEventListeners(window)).map(([key,entries])=>[key,entries.length])),document:Object.fromEntries(Object.entries(getEventListeners(document)).map(([key,entries])=>[key,entries.length])),activeDialogs:document.querySelectorAll('dialog[open]').length,passwords:document.querySelectorAll('input[type=password]').length})",includeCommandLineAPI:true,returnByValue:true});assert.equal(listeners.exceptionDetails,undefined);return JSON.parse(listeners.result.value);
 }finally{await session.detach();}
}
async function killOwned(record){
 requireOwnedWebView(record,work,root,["browser","renderer"]);
 const command="$ErrorActionPreference='Stop'; $targetId="+record.id+"; $entry=Get-CimInstance Win32_Process -Filter ('ProcessId='+$targetId); if(-not $entry -or $entry.Name -ne 'msedgewebview2.exe' -or $entry.CommandLine -cne "+psQuote(record.command)+" -or $entry.CreationDate.ToUniversalTime().ToString('o') -cne "+psQuote(record.created)+"){throw 'Native process ownership changed'}; $process=Get-Process -Id $targetId -ErrorAction Stop; try { if($process.StartTime.ToUniversalTime().ToString('o') -cne "+psQuote(record.started)+"){throw 'PID reused'}; $process.Kill(); if(-not $process.WaitForExit(20000)){throw 'Owned crash target did not exit'} } finally {$process.Dispose()}";
 await runPS(command);
}
async function recover(role,label){
 const before=await snapshot(),goDraft=await draftState(),boot=await bootstrap();
 const nonce=randomUUID();await page.evaluate(value=>{window.__webviewRecoveryNonce=value},nonce);
 const records=await nativeRecords(),processes=await systemProcesses(),target=selectCrashProcess(records,processes,role,work,root),oldBrowser=selectCrashProcess(records,processes,"browser",work,root);
 const evidence={label,role,targetPID:target.id,targetCreated:target.created,oldBrowserPID:oldBrowser.id,goPID:child.pid};report.recoveries.push(evidence);
 console.log("native kill "+role+" PID "+target.id);await writeFile(reportPath,JSON.stringify(report,null,2));await killOwned(target);console.log("native target exit observed; connecting fresh CDP");
 // A renderer failure keeps the controller/CDP server; a browser failure must create a new server/controller.
 // Playwright keeps the original Page's crashed flag after native recovery. A new CDP connection observes the live native target, without initiating navigation.
 await connect();
 await readyCreate();assert.equal(child.exitCode,null);assert.equal(await page.evaluate(()=>window.__webviewRecoveryNonce),undefined);
 await expect(page.getByText(guide,{exact:true})).toBeVisible();
 const after=await snapshot();requireUnchangedGoState(before,after);assert.deepEqual(await draftState(),goDraft);requireFreshBoot(boot,await bootstrap(),before.activeDraft);
 const newRecords=await nativeRecords(),newProcesses=await systemProcesses(),newRenderer=selectCrashProcess(newRecords,newProcesses,"renderer",work,root),newBrowser=selectCrashProcess(newRecords,newProcesses,"browser",work,root);
 assert.notEqual(newRenderer.id,target.id);assert.equal(role==="renderer"?newBrowser.id===oldBrowser.id:newBrowser.id!==oldBrowser.id,true);
 evidence.newRendererPID=newRenderer.id;evidence.newBrowserPID=newBrowser.id;evidence.sameGoSession=true;evidence.sameDurableState=true;evidence.sameGoDraft=true;evidence.rawDiscardGuide=true;evidence.documentNonceDiscarded=true;
 return {before,after,evidence};
}
async function clean(){
 if(child&&child.exitCode===null){try{if(page)await call("main.Probe.Stop");}catch{};try{await (async()=>{let timer;try{return await Promise.race([exit,new Promise((_,fail)=>{timer=setTimeout(()=>fail(new Error("Go stop deadline")),15000)})])}finally{clearTimeout(timer)}})();}catch{if(child.exitCode===null)child.kill();await exit;}}
 if(child){const state=await exit;report.goExit=state;}
 for(const connection of browsers)await connection.close().catch(()=>{});
 // Re-discover only our unique profile. Then wait on handles whose StartTime is still the captured instance.
 if(work){await nativeRecords();for(const record of owned.values()){
  await runPS("$ErrorActionPreference='Stop'; $process=Get-Process -Id "+record.id+" -ErrorAction SilentlyContinue; if($process){try{if($process.StartTime.ToUniversalTime().ToString('o') -ceq "+psQuote(record.started)+" -and -not $process.WaitForExit(20000)){throw 'Owned engine did not exit'}}finally{$process.Dispose()}}; exit 0");
 }assert.deepEqual(await nativeRecords(),[]);report.ownedProcessesExited=true;
 ownedWorkPath(work,root);
 await runPS("$ErrorActionPreference='Stop'; $target=[IO.Path]::GetFullPath("+psQuote(work)+"); $expectedParent=[IO.Path]::GetFullPath("+psQuote(task)+"); if([IO.Path]::GetDirectoryName($target) -ine $expectedParent -or -not [IO.Path]::GetFileName($target).StartsWith('product-run-webview-')){throw 'Cleanup target escaped workspace'}; Get-ChildItem -LiteralPath $target -File -Recurse | ForEach-Object { $stream=[IO.File]::Open($_.FullName,[IO.FileMode]::Open,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None); $stream.Dispose() }; Remove-Item -LiteralPath $target -Recurse -Force; if(Test-Path -LiteralPath $target){throw 'Cleanup incomplete'}; exit 0");report.profileExclusiveOpenAndRemoved=true;
 }
}
try {
 assert.equal(process.platform,"win32");await access(exe);await access(resolve(assets,"index.html"));
 const binding=await readFile(resolve(root,"frontend/bindings/github.com/porkyx/jackpot/internal/desktop/service.ts"),"utf8");bootMethod=Number(/export function Bootstrap[\s\S]*?return\s+\$Call\.ByID\((\d+)\)/.exec(binding)?.[1]);assert.ok(Number.isSafeInteger(bootMethod)&&bootMethod>0);
 report.bootstrapMethodId=bootMethod;report.assetIndexSHA256=createHash("sha256").update(await readFile(resolve(assets,"index.html"))).digest("hex");report.executableSHA256=createHash("sha256").update(await readFile(exe)).digest("hex");
 work=await mkdtemp(resolve(task,"product-run-webview-"));report.workDir=work;ownedWorkPath(work,root);cdpPort=await freePort();child=spawn(exe,["-assets",assets,"-work-dir",work,"-cdp-port",String(cdpPort)],{cwd:root,windowsHide:true,stdio:["ignore","pipe","pipe"]});report.goPID=child.pid;
 exit=new Promise((ok,fail)=>{child.once("error",fail);child.once("exit",(code,signal)=>ok({code,signal}))});void exit.catch(()=>{});child.stdout.on("data",chunk=>{logs=(logs+chunk).slice(-10000)});child.stderr.on("data",chunk=>{stderr=(stderr+chunk).slice(-4000)});
 await check("native-production-boot",async()=>{await expect.poll(()=>{if(child.exitCode!==null)throw new Error("Host exited before ready");return logs.includes("JACKPOT_PRODUCT_E2E_READY")},{timeout:30000}).toBe(true);await connect();await readyCreate();assert.equal((await snapshot()).latest.length,0);});
 let confirmed;
 await check("actual-Go-result-management-and-new-Go-draft",async()=>{
  await visible("디시인사이드 게시글 주소").fill((await snapshot()).fixtureURL);await button("게시글 불러오기").click();await expect(button("추첨 생성")).toBeEnabled();await button("추첨 생성").click();await expect(page.getByRole("heading",{name:"추첨 결과",exact:true})).toBeVisible();confirmed=(await snapshot()).latest[0];assert.equal(confirmed.rounds.length,1);assert.equal(confirmed.rounds[0].winners.length,1);await expect(button("재추첨")).toBeEnabled();await newDraft();const state=await snapshot();assert.notEqual(state.activeDraft.collectionId,confirmed.collectionId);assert.equal(state.activeDraft.state,"ready");report.collectionId=confirmed.collectionId;report.session=state.session;report.draftId=state.activeDraft.draftId;
 });
 await check("actual-OS-creation-and-start-time-guards-reject-before-kill",async()=>{
  const records=await nativeRecords(),processes=await systemProcesses(),target=selectCrashProcess(records,processes,"renderer",work,root);
  await assert.rejects(killOwned({...target,created:"2000-01-01T00:00:00.0000000Z"}));
  await assert.rejects(killOwned({...target,started:"2000-01-01T00:00:00.0000000Z"}));
  const still=selectCrashProcess(await nativeRecords(),await systemProcesses(),"renderer",work,root);assert.equal(still.id,target.id);assert.equal(still.started,target.started);await readyCreate();report.actualRejectedKillGuards=2;
 });
 let baseline;
 await check("renderer-crash-discards-invalid-count-and-unsent-URL-keyword",async()=>{
  baseline=await globalResources();await visible("디시인사이드 게시글 주소").fill("https://gall.dcinside.com/board/view/?id=unsent&no=999");await visible("포함 단어").fill("미전송 keyword");await visible("당첨 인원").fill("-");
  const GoBefore=await draftState();assert.equal(GoBefore.prizes.single.count,1);assert.deepEqual(GoBefore.filters.includeKeywords,[]);
  await recover("renderer","raw-input-failure");await expect(visible("당첨 인원")).toHaveValue("1");await expect(visible("포함 단어")).toHaveValue("");await expect(visible("디시인사이드 게시글 주소")).toHaveValue(GoBefore.article.url);report.invalidCountDiscarded=true;report.unsentURLKeywordDiscarded=true;
 });
 await check("browser-crash-rebuilds-controller-discards-unsent-URL-keeps-result-and-draft",async()=>{
  const confirmedDraft=await draftState();await visible("디시인사이드 게시글 주소").fill("https://gall.dcinside.com/board/view/?id=unsent&no=999");
  await recover("browser","controller-replacement");await expect(page.locator("dialog[open]")).toHaveCount(0);await expect(page.locator('input[type="password"]')).toHaveCount(0);await expect(visible("디시인사이드 게시글 주소")).toHaveValue(confirmedDraft.article.url);report.unsentURLDiscardedAfterBrowserCrash=true;
  await result(confirmed.collectionId);await expect(button("재추첨")).toBeEnabled();await expect(button("관리 잠금 해제")).toHaveCount(0);assert.deepEqual((await snapshot()).latest[0],confirmed);await page.locator('nav a[href="#/create"]').click();await readyCreate();
 });
 await check("two-more-native-renderer-recoveries-return-global-listener-baseline",async()=>{
  for(let iteration=0;iteration<2;iteration++){await recover("renderer","repeat-"+iteration);const current=await globalResources();assert.deepEqual(current,baseline,"Fresh runtime must return native global-listener/dialog/credential-input baseline");report.recoveries.at(-1).globalResources=current;}report.listenerBaseline=baseline;
 });
 await check("durable-Go-bootstrap-and-result-remain-offline-with-no-automatic-execution",async()=>{const state=await snapshot();assert.equal(state.latest[0].rounds.length,1);assert.equal(state.latest[0].rounds[0].winners.length,1);assert.equal(state.activeDraft.draftId,report.draftId);report.finalCounts=state.Counts??state.counts;const restored=await bootstrap();assert.equal(restored.ok,true);assert.deepEqual(restored.data.pendingOperations,[]);assert.equal(restored.data.pendingCursor,null);report.authoritativePending=0;report.finalSession=state.session;});
 report.status="passed";
} catch(error){report.status="failed";report.failedPhase=phase;report.error=String(error);report.errorStack=error.stack;process.exitCode=1;}
finally {
 report.nativeDiagnosticTail=(logs+"\n"+stderr).slice(-8000);report.nativeRecoveryLogs={rendererFailureCount:(stderr.match(/process failed \(kind=1\)/g)||[]).length,browserRebuildCount:(logs.match(/rebuilding controller after browser process exit/g)||[]).length};
 try{await clean();}catch(error){report.status="failed";report.cleanupError=String(error);process.exitCode=1;}
 if(report.goExit?.code!==0){report.status="failed";report.cleanupError??="Native host did not exit cleanly";process.exitCode=1;}report.ownedProcessInstances=[...owned.values()].map(({id,role,started})=>({id,role,started}));await writeFile(reportPath,JSON.stringify(report,null,2));console.log(JSON.stringify({status:report.status,checks:report.checks.length,recoveries:report.recoveries.length,report:reportPath,cleanup:report.ownedProcessesExited}));
}