// Real Windows Wails, collector, crypto entropy, SQLite and production assets.
// Only HTTP delivery, save path and clipboard are isolated test seams.
import assert from "node:assert/strict";
import { spawn, execFile } from "node:child_process";
import { mkdtemp, readFile, writeFile, access } from "node:fs/promises";
import { createServer } from "node:net";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { createHash } from "node:crypto";
import { chromium, expect } from "@playwright/test";
const root=fileURLToPath(new URL("../../../",import.meta.url)),task=resolve(root,".task");
const exe=resolve(task,"producte2e.exe"),assets=resolve(root,"frontend/dist");
const reportPath=resolve(task,"product-badges-report.json"),work=await mkdtemp(resolve(task,"product-run-badges-"));
assert.equal(dirname(work),task);await access(exe);await access(resolve(assets,"index.html"));
const digest=async(path)=>createHash("sha256").update(await readFile(path)).digest("hex");
const report={status:"running",checks:[],boundary:{native:"actual Windows Wails/WebView2",collector:"real parser; six observed marker HTTP fixtures",random:"real OS crypto",storage:"actual isolated SQLite",clipboard:"capture seam; user clipboard untouched",PNG:"actual Canvas and atomic save; owned path selection",DPI:"renderer viewport only; Windows physical settings unchanged"},hostSHA256:await digest(exe),htmlSHA256:await digest(resolve(assets,"index.html"))};
const groups=[{id:"fixed",field:"fixed",label:"고닉"},{id:"semi_fixed",field:"semiFixed",label:"비고닉"},{id:"main_manager",field:"mainManager",label:"주딱"},{id:"sub_manager",field:"subManager",label:"파딱"},{id:"new_account",field:"newAccount",label:"깡계"},{id:"anonymous",field:"anonymous",label:"유동닉"}];
const runPS=(command)=>new Promise((ok,fail)=>execFile("powershell.exe",["-NoProfile","-NonInteractive","-Command",command],{windowsHide:true,timeout:30000},(error,stdout)=>error?fail(error):ok(stdout)));
const bounded=async(promise,ms)=>{let timer;try{return await Promise.race([promise,new Promise((_,fail)=>{timer=setTimeout(()=>fail(new Error("OwnedProcessDeadline")),ms);})]);}finally{clearTimeout(timer);}};
let instance,phase="setup",owned=[];
const check=async(name,action)=>{phase=name;await action();report.checks.push(name);console.log("badges PASS "+name);};
async function launch(){
 const server=createServer();await new Promise((ok,fail)=>{server.once("error",fail);server.listen(0,"127.0.0.1",ok);});const port=server.address().port;await new Promise((ok,fail)=>server.close(error=>error?fail(error):ok()));
 const child=spawn(exe,["-assets",assets,"-work-dir",work,"-cdp-port",String(port),"-badge-fixture"],{cwd:root,windowsHide:true,stdio:["ignore","pipe","pipe"]});
 const exit=new Promise((ok,fail)=>{child.once("error",fail);child.once("exit",(code,signal)=>ok({code,signal}));});void exit.catch(()=>{});let logs="",stderr="";child.stdout.on("data",part=>logs=(logs+part).slice(-4000));child.stderr.on("data",part=>stderr=(stderr+part).slice(-2000));
 instance={child,exit};owned.push(child.pid);
 await expect.poll(()=>{if(child.exitCode!==null)throw new Error("Owned host exited: "+stderr);return logs.includes("JACKPOT_PRODUCT_E2E_READY");},{timeout:30000}).toBe(true);
 await expect.poll(async()=>{try{return(await(await fetch(`http://127.0.0.1:${port}/json/list`)).json()).some(target=>target.type==="page");}catch{return false;}},{timeout:20000}).toBe(true);
 const browser=await chromium.connectOverCDP(`http://127.0.0.1:${port}`,{timeout:20000,isWebView:true});instance.browser=browser;
 const pages=browser.contexts().flatMap(context=>context.pages());assert.equal(pages.length,1);const page=pages[0];page.setDefaultTimeout(15000);instance.page=page;
 const renderer=await page.context().newCDPSession(page);instance.renderer=renderer;await renderer.send("Emulation.setFocusEmulationEnabled",{enabled:true});
 const session=await browser.newBrowserCDPSession();const info=await session.send("SystemInfo.getProcessInfo");await session.detach();owned=[...new Set([...owned,...info.processInfo.map(process=>process.id)])];
 await expect(page.getByRole("heading",{name:"추첨 만들기",exact:true})).toBeVisible();await expect(page.getByLabel("디시인사이드 게시글 주소",{exact:true})).toBeEnabled();
 report.engine=browser.version();
}
const call=(method,...args)=>instance.page.evaluate(async({method,args})=>{const dispatch=window._wails.dispatchWailsEvent;const {Call}=await import("/wails/runtime.js");window._wails.dispatchWailsEvent=dispatch;return typeof method==="number"?Call.ByID(method,...args):Call.ByName("main.Probe."+method,...args);},{method,args});
const draft=()=>call("DraftState");
const snapshot=()=>call("Report");
const button=(name)=>instance.page.getByRole("button",{name,exact:true});
const visible=(name)=>instance.page.getByLabel(name,{exact:true}).and(instance.page.locator(":visible"));
async function participants(){const state=await draft(),s=state.summary;const reply=await call(1442270765,{backendSessionId:s.backendSessionId,draftId:s.draftId,revision:s.revision,articleGeneration:s.articleGeneration,query:"",offset:0,limit:100,group:""});assert.equal(reply.ok,true);return reply.data.rows;}
async function filterReady(){await expect(instance.page.locator(".create-product")).not.toHaveAttribute("aria-busy","true");await expect(button("추첨 생성")).toBeEnabled();}
async function load(){await expect(visible("디시인사이드 게시글 주소")).toBeEnabled();await visible("디시인사이드 게시글 주소").fill((await snapshot()).fixtureURL);await button("게시글 불러오기").click();await expect.poll(async()=>(await draft()).participants).toBe(12);await filterReady();}
async function weight(group,value){const input=visible(group.label+" 당첨 비율");await input.fill(String(value));await input.blur();await expect.poll(async()=>(await draft()).filters.badgeRules?.[group.field].weight).toBe(value);await filterReady();}
async function stop(){if(!instance)return;const active=instance;instance=null;
 try{if(active.page)await active.page.evaluate(async()=>{const dispatch=window._wails.dispatchWailsEvent;const {Call}=await import("/wails/runtime.js");window._wails.dispatchWailsEvent=dispatch;return Call.ByName("main.Probe.Stop");});}catch{}
 let ended;try{ended=await bounded(active.exit,20000);}catch{if(active.child.exitCode===null)active.child.kill();ended=await bounded(active.exit,10000);report.forcedOwnedStop=true;}
 await active.browser?.close().catch(()=>{});assert.equal(ended.code,0);report.hostExit=ended;
 const ids=[...new Set(owned)];assert.ok(ids.every(id=>Number.isSafeInteger(id)&&id>0));await runPS("$ErrorActionPreference='Stop'; $badgeIds=@("+ids.join(",")+"); $badgeProcesses=Get-Process -Id $badgeIds -ErrorAction SilentlyContinue; if($badgeProcesses){$badgeProcesses|Wait-Process -Timeout 20 -ErrorAction Stop}; if(Get-Process -Id $badgeIds -ErrorAction SilentlyContinue){throw 'owned processes remaining'}");report.ownedPIDExit={count:ids.length,remaining:0};
}
try{
 await launch();await load();
 await check("six-observed-badges-preserve-login-identity-and-default-uniform",async()=>{
  const state=await draft(),rows=await participants();assert.equal(state.included,12);assert.ok(!state.filters.badgeRules?.weightingEnabled);assert.equal(rows.length,12);
  for(const group of groups)assert.equal(rows.filter(row=>row.badgeCategory===group.id).length,2);
  assert.equal(rows.filter(row=>row.kind==="anonymous").length,2);assert.equal(rows.filter(row=>row.kind!=="anonymous").length,10);
  report.initialCategories=Object.fromEntries(groups.map(group=>[group.id,rows.filter(row=>row.badgeCategory===group.id).length]));
  await expect(instance.page.locator('details[data-filter-accordion]')).not.toHaveAttribute("open","");await instance.page.locator('details[data-filter-accordion]>summary').click();
  await expect(instance.page.locator(".create-participants .participant-badge")).toHaveCount(12);
 });
 await check("each-category-exclusion-is-independent-and-reset-restores-defaults",async()=>{
  for(const group of groups){await visible(group.label+" 제외").check();await expect.poll(async()=>(await draft()).included).toBe(10);await filterReady();const rows=await participants();assert.ok(rows.filter(row=>row.badgeCategory===group.id).every(row=>!row.included));assert.ok(rows.filter(row=>row.badgeCategory!==group.id).every(row=>row.included));await visible(group.label+" 제외").uncheck();await expect.poll(async()=>(await draft()).included).toBe(12);await filterReady();}
  await button("필터 초기화").click();await expect.poll(async()=>{const state=await draft();return state.included===12&&!state.filters.badgeRules?.weightingEnabled;}).toBe(true);await filterReady();
 });
 await check("category-manual-override-and-zero-weight-cannot-create-a-draw",async()=>{
  await visible("고닉 제외").check();await expect.poll(async()=>(await draft()).included).toBe(10);await filterReady();await visible("분류").selectOption("excluded");await expect(instance.page.locator(".create-participants>li")).toHaveCount(2);await visible("고닉 참가자 1 참가자 포함").check();await expect.poll(async()=>(await draft()).included).toBe(11);await button("수동 분류 초기화").click();await expect.poll(async()=>(await draft()).included).toBe(10);await visible("고닉 제외").uncheck();await expect.poll(async()=>(await draft()).included).toBe(12);await filterReady();
  await visible("분류").selectOption("unclassified");await visible("분류별 당첨 비율 사용").check();await expect.poll(async()=>(await draft()).filters.badgeRules?.weightingEnabled).toBe(true);await filterReady();
  // Keep one positive group while committing individual fields. The last zero
  // must reject without a collection/result write or any fallback uniform draw.
  for(const group of groups.slice(0,-1))await weight(group,0);
  const before=(await snapshot()).counts;
  await visible("유동닉 당첨 비율").fill("0");await visible("유동닉 당첨 비율").blur();await button("추첨 생성").click();await expect(instance.page.getByRole("alert").filter({visible:true}).first()).toBeVisible();assert.deepEqual((await snapshot()).counts,before);await expect(instance.page.getByRole("heading",{name:"추첨 만들기",exact:true})).toBeVisible();
  await visible("유동닉 당첨 비율").fill("100");await visible("유동닉 당첨 비율").blur();await expect.poll(async()=>(await draft()).filters.badgeRules?.anonymous.weight).toBe(100);await filterReady();
 });
 await check("main-only-ratio-draw-and-PNG-retain-frozen-policy",async()=>{
  await weight(groups[2],100);await weight(groups[5],0);
  const rules=(await draft()).filters.badgeRules;assert.equal(rules.mainManager.weight,100);assert.ok(groups.filter(group=>group.field!=="mainManager").every(group=>rules[group.field].weight===0));
  await visible("상품명 (선택)").fill("배지 테스트 상품");await visible("당첨 인원").fill("1");await visible("당첨 인원").blur();await expect.poll(async()=>(await draft()).prizes.single.name).toBe("배지 테스트 상품");await filterReady();await button("추첨 생성").click();await expect(instance.page.getByRole("heading",{name:"추첨 결과",exact:true})).toBeVisible();await expect(button("PNG 사진 저장")).toBeEnabled();
  const model=(await snapshot()).latest[0];assert.equal(model.latestRound.winners.length,1);assert.equal(model.latestRound.winners[0].participant.badgeCategory,"main_manager");assert.equal(model.remainingCount,1);assert.deepEqual(model.filters.badgeRules,rules);report.frozenPolicy=rules;report.firstWinner=model.latestRound.winners[0].participant.id;report.collectionId=model.collectionId;
  await button("PNG 사진 저장").click();await expect.poll(async()=>(await call("ExportState")).saves).toBe(1);const saved=(await call("ExportState")).savePath,bytes=await readFile(saved);assert.equal(bytes.subarray(0,8).toString("hex"),"89504e470d0a1a0a");assert.equal(bytes.readUInt32BE(16),1200);report.PNG={width:1200,height:bytes.readUInt32BE(20),bytes:bytes.length,sha256:await digest(saved)};
  await button("결과 텍스트 복사").click();await expect.poll(async()=>(await call("ExportState")).copies).toBe(1);assert.ok(!(await call("ExportState")).copiedText.startsWith("Jackpot 로컬 추첨 결과"));
 });
 await check("weighted-rerun-exhausts-only-eligible-category-without-duplicates",async()=>{
  await button("재추첨").click();const dialog=instance.page.getByRole("dialog");await expect(dialog).toBeVisible();await dialog.locator('[name="prizeName1"]').fill("다음 상품");await dialog.getByRole("button",{name:"새 회차 추첨",exact:true}).click();await expect(dialog).toBeHidden();await expect.poll(async()=>(await snapshot()).latest[0].latestRound.number).toBe(2);const model=(await snapshot()).latest[0];assert.equal(model.latestRound.winners[0].participant.badgeCategory,"main_manager");assert.notEqual(model.latestRound.winners[0].participant.id,report.firstWinner);assert.equal(model.remainingCount,0);assert.deepEqual(model.filters.badgeRules,report.frozenPolicy);await expect(button("재추첨")).toBeDisabled();report.secondWinner=model.latestRound.winners[0].participant.id;
 });
 await check("restart-preserves-categories-ratios-and-round-outcomes",async()=>{
  const before=(await snapshot()).latest[0];await stop();await launch();await instance.page.evaluate(id=>{location.hash="#/results/"+encodeURIComponent(id);},report.collectionId);await expect(instance.page.getByRole("heading",{name:"추첨 결과",exact:true})).toBeVisible();await expect(instance.page.locator('[name="resultRound"]')).toBeEnabled();const model=(await snapshot()).latest[0];assert.deepEqual(model,before);assert.equal(model.remainingCount,0);await expect(button("재추첨")).toBeDisabled();await button("확정 참가자 보기").click();await expect(instance.page.getByRole("dialog")).toBeVisible();await expect(instance.page.getByRole("dialog").locator(".participant-badge")).toHaveCount(12);await instance.page.getByRole("dialog").getByRole("button",{name:"명단 닫기",exact:true}).click();
 });
 await check("category-controls-have-labels-and-no-small-window-overflow",async()=>{
  await instance.page.locator('nav a[href="#/create"]').click();await load();const filters=instance.page.locator('details[data-filter-accordion]');if(!(await filters.evaluate(node=>node.open)))await filters.locator("summary").click();
  report.layout=[];for(const width of[1024,800,560]){await instance.renderer.send("Emulation.setDeviceMetricsOverride",{width,height:720,deviceScaleFactor:1,mobile:false});const geometry=await instance.page.evaluate(()=>({width:innerWidth,scrollWidth:document.documentElement.scrollWidth,rows:[...document.querySelectorAll('[data-badge-category]')].filter(node=>node.closest('details')).length,labels:[...document.querySelectorAll('details[data-filter-accordion] input')].every(input=>input.labels?.length>0),weightLabelCount:document.querySelectorAll('.badge-rule-row .create-field:not(:has(input[type="checkbox"]))>label').length,weightLabelsFit:[...document.querySelectorAll('.badge-rule-row .create-field:not(:has(input[type="checkbox"]))>label')].every(label=>label.scrollWidth<=label.clientWidth&&label.getBoundingClientRect().height<=parseFloat(getComputedStyle(label).lineHeight)+1)}));assert.ok(geometry.scrollWidth<=width);assert.equal(geometry.labels,true);assert.equal(geometry.weightLabelCount,6);assert.equal(geometry.weightLabelsFit,true);report.layout.push(geometry);}await instance.page.screenshot({path:resolve(task,"product-badges-small-window.png"),animations:"disabled"});
  await instance.renderer.send("Emulation.setDeviceMetricsOverride",{width:1024,height:900,deviceScaleFactor:1,mobile:false});await visible("분류별 당첨 비율 사용").check();await expect.poll(async()=>(await draft()).filters.badgeRules?.weightingEnabled).toBe(true);await filterReady();await weight(groups[0],70);await weight(groups[1],30);for(const group of groups.slice(2))await weight(group,0);await instance.page.evaluate(()=>scrollTo(0,0));await instance.page.screenshot({path:resolve(task,"product-badges-controls.png"),fullPage:true,animations:"disabled"});await instance.renderer.send("Emulation.clearDeviceMetricsOverride");
 });
 await stop();report.status="passed";
}catch(error){report.status="failed";report.phase=phase;report.error={name:error?.name,message:String(error?.message??error).slice(0,4000),stack:String(error?.stack??"").split("\n").slice(0,5)};process.exitCode=1;if(instance?.page)try{report.visibleAlerts=await instance.page.getByRole("alert").allTextContents();await instance.page.screenshot({path:resolve(task,"product-badges-failure.png"),timeout:3000});}catch{}}
finally{
 try{await stop();}catch(error){report.status="failed";report.cleanupFailure=String(error?.message??error).slice(0,2000);process.exitCode=1;}
 if(!report.cleanupFailure){try{const escaped=work.replaceAll("'","''"),parent=task.replaceAll("'","''");const count=await runPS("$ErrorActionPreference='Stop'; $badgeWork=[IO.Path]::GetFullPath('"+escaped+"'); if((Split-Path -Parent $badgeWork) -ne '"+parent+"' -or (Split-Path -Leaf $badgeWork) -notlike 'product-run-badges-*'){throw 'work scope'}; $badgeFiles=@(Get-ChildItem -LiteralPath $badgeWork -File -Recurse -Force); foreach($badgeFile in $badgeFiles){$badgeStream=[IO.File]::Open($badgeFile.FullName,[IO.FileMode]::Open,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None);$badgeStream.Dispose()}; Remove-Item -LiteralPath $badgeWork -Recurse -Force; Write-Output $badgeFiles.Count");report.profileCleanup={removed:true,exclusiveFiles:Number(count.trim())};}catch(error){report.status="failed";report.cleanupFailure=String(error?.message??error).slice(0,2000);process.exitCode=1;}}
 report.htmlUnchanged=await digest(resolve(assets,"index.html"))===report.htmlSHA256;if(!report.htmlUnchanged){report.status="failed";process.exitCode=1;}report.endedUTC=new Date().toISOString();await writeFile(reportPath,JSON.stringify(report,null,2)+"\n");console.log(JSON.stringify({status:report.status,checks:report.checks.length,phase:report.phase,report:reportPath,cleanupFailure:report.cleanupFailure}));
}
