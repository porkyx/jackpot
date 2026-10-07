// Actual isolated Wails/WebView2 product UI; HTTP and OS selection seams are marked.
import assert from "node:assert/strict";
import { spawn, execFile } from "node:child_process";
import { mkdtemp, readFile, writeFile, rm, access } from "node:fs/promises";
import { createServer } from "node:net";
import { resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium, expect } from "@playwright/test";
const root=fileURLToPath(new URL("../../../",import.meta.url));
const task=resolve(root,".task"); const exe=resolve(task,"producte2e.exe"); const assets=resolve(root,"frontend/dist");
const reportPath=resolve(task,"product-e2e-report.json");const work=await mkdtemp(resolve(task,"product-run-"));
assert.ok(work.startsWith(task+sep)&&work.includes("product-run-"));await access(exe);await access(resolve(assets,"index.html"));
const report={status:"running",checks:[],boundaryModes:{native:"actual Windows Wails/WebView2",database:"actual SQLite",collector:"actual DC parser with deterministic HTTP fixture",random:"OS crypto",PNG:"actual browser Canvas + Go atomic file write",fileDialog:"test selection seam",clipboard:"capture seam preserving user clipboard",externalBrowser:"capture seam"}};
let instance;let step="boot";
const check=async(name,fn)=>{step=name;await fn();report.checks.push(name)};
async function port(){const server=createServer();await new Promise((ok,fail)=>{server.once("error",fail);server.listen(0,"127.0.0.1",ok)});const number=server.address().port;await new Promise((ok,fail)=>server.close(e=>e?fail(e):ok()));return number}
async function launch(){
 const cdp=await port();const child=spawn(exe,["-assets",assets,"-work-dir",work,"-cdp-port",String(cdp)],{cwd:root,windowsHide:true,stdio:["ignore","pipe","pipe"]});
 const exit=new Promise((ok,fail)=>{child.once("error",fail);child.once("exit",(code,signal)=>ok({code,signal}))});void exit.catch(()=>{});let logs="";child.stdout.on("data",chunk=>{logs=(logs+chunk.toString()).slice(-4000)});let stderr="";child.stderr.on("data",chunk=>{stderr=(stderr+chunk.toString()).slice(-1500)});let browser;try{
 await expect.poll(()=>{if(child.exitCode!==null)throw new Error("Native host exited before ready: "+stderr);return logs.includes("JACKPOT_PRODUCT_E2E_READY")},{timeout:30000}).toBe(true);
 await expect.poll(async()=>{try{const response=await fetch(`http://127.0.0.1:${cdp}/json/list`);const targets=await response.json();return targets.some(target=>target.type==="page")}catch{return false}},{timeout:20000}).toBe(true);
 browser=await chromium.connectOverCDP(`http://127.0.0.1:${cdp}`,{timeout:20000,isWebView:true});const pages=browser.contexts().flatMap(context=>context.pages());assert.equal(pages.length,1);const page=pages[0];page.setDefaultTimeout(10000);const renderer=await browser.contexts()[0].newCDPSession(page);await renderer.send("Emulation.setFocusEmulationEnabled",{enabled:true});
 const session=await browser.newBrowserCDPSession();const processes=await session.send("SystemInfo.getProcessInfo");await session.detach();
 const call=(method,...args)=>page.evaluate(async({method,args})=>{const dispatch=window._wails.dispatchWailsEvent;const {Call}=await import("/wails/runtime.js");window._wails.dispatchWailsEvent=dispatch;return await Call.ByName("main.Probe."+method,...args)},{method,args});
 instance={child,exit,browser,page,call,processes:processes.processInfo.map(p=>p.id)};
 await expect(page.getByRole("heading",{name:"Jackpot",exact:true})).toBeVisible();return instance;}catch(error){if(child.exitCode===null)child.kill();await exit.catch(()=>{});await browser?.close().catch(()=>{});throw error;}
}
async function stop(){if(!instance)return;const active=instance;instance=undefined;try{await active.call("Stop")}catch{};const exited=await active.exit;assert.equal(exited.code,0);await active.browser.close().catch(()=>{});
 const ids=active.processes;assert.ok(ids.every(id=>Number.isSafeInteger(id)&&id>0));
 const command="$ErrorActionPreference='Stop'; $e2ePids=@("+ids.join(",")+"); $e2eProcesses=Get-Process -Id $e2ePids -ErrorAction SilentlyContinue; try { if($e2eProcesses){$e2eProcesses | Wait-Process -Timeout 20 -ErrorAction Stop} } catch { exit 1 }; exit 0";
 await new Promise((ok,fail)=>execFile("powershell.exe",["-NoProfile","-NonInteractive","-Command",command],{windowsHide:true,timeout:25000},e=>e?fail(e):ok()));
}
const button=(page,name)=>page.getByRole("button",{name,exact:true});
const visible=(page,name)=>page.getByLabel(name,{exact:true}).and(page.locator(":visible"));
const snapshot=async()=>{const raw=await instance.call("Report");const counts=raw.counts??raw.Counts??{};const normalized={...raw};delete normalized.FixtureCalls;return {...normalized,collections:counts.collections??counts.Collections??raw.collections,results:counts.results??counts.Results??raw.results,winners:counts.winners??counts.Winners??raw.winners,fixtureCalls:raw.fixtureCalls??raw.FixtureCalls,latest:Array.isArray(raw.latest)?raw.latest[0]:raw.latest};};
async function create(mode="immediate"){
 const {page}=instance;await page.locator('nav a[href="#/create"]').click();
 await expect(visible(page,"디시인사이드 게시글 주소")).toBeEnabled();
 await visible(page,"디시인사이드 게시글 주소").fill("https://gall.dcinside.com/board/view/?id=producte2e&no=1");await button(page,"게시글 불러오기").click();
 await expect(button(page,"추첨 생성")).toBeEnabled();await expect(page.locator(".create-participants li").first()).toBeVisible();
 await visible(page,"상품명 (선택)").fill("E2E 상품");await visible(page,"당첨 인원").fill("1");await visible(page,"추첨 방식").selectOption(mode);
 await expect.poll(async()=>{const draft=await instance.call("DraftState");return draft.prizes.drawMode===mode&&draft.prizes.single.name==="E2E 상품"&&draft.prizes.single.count===1;}).toBe(true);
 await expect(button(page,"추첨 생성")).toBeEnabled();
 await button(page,"추첨 생성").click();await expect(page.locator('input[type="password"]')).toHaveCount(0);
 await expect(page.getByRole("heading",{name:"추첨 결과",exact:true})).toBeVisible();await expect(page.locator('select[name="resultRound"]')).toBeEnabled();return (await snapshot()).latest;
}
try{
 await launch();
 await check("actual-native-boot-create-isolation",async()=>{await expect(visible(instance.page,"디시인사이드 게시글 주소")).toBeEnabled();const state=await snapshot();assert.equal(state.collections,0);assert.equal(state.fixtureCalls,0)});
 await check("actual-native-CSP-blocks-inline-handler-blob-script-and-eval",async()=>{
  const page=instance.page;
  const evidence=await page.evaluate(async()=>{
   const meta=document.querySelector('meta[http-equiv="Content-Security-Policy"]');
   if(!meta)throw new Error("Production CSP meta is absent");
   const policy=meta.content;
   const key="__jackpotCspBoundaryProbe";globalThis[key]=0;
   const violations=[];const nodes=[];let blobURL;let timer;
   let resolveViolations;const ready=new Promise(resolve=>{resolveViolations=resolve});
   const listener=event=>{if(event.effectiveDirective==="script-src-elem"||event.effectiveDirective==="script-src-attr"){
    violations.push({directive:event.effectiveDirective,blocked:event.blockedURI});
    if(violations.length===3)resolveViolations();
   }};
   document.addEventListener("securitypolicyviolation",listener);
   try{
    const inline=document.createElement("script");inline.textContent='globalThis.__jackpotCspBoundaryProbe += 1';nodes.push(inline);document.body.append(inline);
    const handler=document.createElement("button");handler.setAttribute("onclick",'globalThis.__jackpotCspBoundaryProbe += 1');nodes.push(handler);document.body.append(handler);handler.click();
    blobURL=URL.createObjectURL(new Blob(['globalThis.__jackpotCspBoundaryProbe += 1'],{type:"text/javascript"}));
    const blob=document.createElement("script");blob.src=blobURL;nodes.push(blob);document.body.append(blob);
    await Promise.race([ready,new Promise((_,reject)=>{timer=setTimeout(()=>reject(new Error("CSP violation observations timed out")),10000)})]);
    return {policy,executions:globalThis[key],violations};
   }finally{
    clearTimeout(timer);document.removeEventListener("securitypolicyviolation",listener);nodes.forEach(node=>node.remove());if(blobURL)URL.revokeObjectURL(blobURL);delete globalThis[key];
   }
  });
  assert.equal(evidence.executions,0);assert.equal(evidence.violations.length,3);
  assert.deepEqual(evidence.violations.map(value=>value.directive).sort(),["script-src-attr","script-src-elem","script-src-elem"]);
  assert.equal(evidence.violations.filter(value=>value.blocked==="inline").length,2);
  assert.ok(evidence.violations.some(value=>value.blocked==="blob"||value.blocked.startsWith("blob:")));
  assert.ok(evidence.policy.includes("script-src 'self'")&&!evidence.policy.includes("'unsafe-eval'")&&!evidence.policy.includes("'unsafe-inline'"));
  const evaluation=await instance.browser.contexts()[0].newCDPSession(page);
  try{
   const result=await evaluation.send("Runtime.evaluate",{expression:"(0,eval)('globalThis.__jackpotCspEvalExecuted = true')",allowUnsafeEvalBlockedByCSP:false,returnByValue:true});
   assert.ok(result.exceptionDetails,"Explicit CSP-respecting Runtime evaluation unexpectedly allowed eval");
   assert.equal(await page.evaluate(()=>globalThis.__jackpotCspEvalExecuted===true),false);
   evidence.evalBlocked=true;
  }finally{await evaluation.detach();await page.evaluate(()=>{delete globalThis.__jackpotCspEvalExecuted})}
  report.contentSecurity=evidence;
 });
 await check("invalid-URL-never-collects-or-persists",async()=>{const page=instance.page;await visible(page,"디시인사이드 게시글 주소").fill("https://evil.invalid/secret");await button(page,"게시글 불러오기").click();await expect(page.getByText("게시글 수집에 실패했습니다. 주소와 연결 상태를 확인해 주세요.",{exact:true})).toBeVisible();assert.equal((await snapshot()).collections,0)});
 await check("filter-manual-search-and-independent-resets-match-Go",async()=>{
  const page=instance.page;
  await visible(page,"디시인사이드 게시글 주소").fill("https://gall.dcinside.com/board/view/?id=producte2e&no=1");
  await button(page,"게시글 불러오기").click();
  const count=async(expected)=>{
   await expect.poll(async()=>(await instance.call("DraftState")).included).toBe(expected);
   await expect(page.getByText(`전체 6명 · 포함 ${expected}명 · 제외 ${6-expected}명`,{exact:true})).toBeVisible();
   const state=await instance.call("DraftState");assert.equal(state.participants,6);assert.equal(state.excluded,6-expected);
   assert.equal((await snapshot()).collections,0);return state;
  };
  await count(5);
  const filters=page.locator('details[data-filter-accordion]');
  await expect(filters).not.toHaveAttribute("open","");
  await filters.locator("summary").click();await expect(filters).toHaveAttribute("open","");
  await visible(page,"유동닉 제외").check();await count(4);
  await visible(page,"디시콘만 작성한 참가자 제외").check();await count(3);
  await visible(page,"포함 단어").fill("hello");await visible(page,"포함 단어").press("Enter");await count(3);
  await visible(page,"분류").selectOption("included");
  const fixed=page.getByRole("checkbox",{name:"고정 참가자 가 참가자 포함",exact:true});await expect(fixed).toBeVisible();await fixed.uncheck();await count(2);
  await visible(page,"참가자 검색").fill("참가자 가");await visible(page,"참가자 검색").press("Enter");await expect(page.locator(".create-participants>li")).toHaveCount(1);
  const filtered=await instance.call("DraftState");const preservedPrizes=filtered.prizes;
  await button(page,"필터 초기화").click();const reset=await count(5);
  assert.equal(reset.filters.excludeAuthor,false);assert.equal(reset.filters.excludeAnonymous,false);assert.equal(reset.filters.excludeDcconOnly,false);
  assert.deepEqual(reset.filters.includeKeywords,[]);assert.deepEqual(reset.prizes,preservedPrizes);
  await button(page,"수동 분류 초기화").click();await count(6);
  await visible(page,"분류").selectOption("unclassified");await visible(page,"참가자 검색").fill("참가자 가");await visible(page,"참가자 검색").press("Enter");await expect(page.locator(".create-participants>li")).toHaveCount(1);
  await button(page,"미분류 모두 제외").click();await count(0);
  await button(page,"미분류 모두 포함").click();await count(6);
  const after=await instance.call("DraftState");assert.equal(after.summary.articleGeneration,filtered.summary.articleGeneration);
  assert.equal(after.filters.excludeAuthor,false);assert.deepEqual(after.prizes,preservedPrizes);
  report.selectionFlow={participants:6,includedCounts:[5,4,3,3,2,5,6,0,6],searchRows:1,bulkIgnoresSearch:true,resetsIndependent:true};
 });
 await check("malicious-article-and-comment-render-as-literal-text",async()=>{
  const page=instance.page;const injected='<img src=x onerror=window.__jackpotInjected=true><script>window.__jackpotInjected=true</script> &nbsp;';
  const draft=await instance.call("DraftState");assert.equal(draft.article.title,"한글 Alpha 로컬 추첨 검증 "+injected);
  await expect(page.getByText("한글 Alpha 로컬 추첨 검증 "+injected+" · E2E 테스트 갤러리",{exact:true})).toBeVisible();
  await visible(page,"참가자 검색").fill("");await visible(page,"참가자 검색").press("Enter");await expect(page.locator(".create-participants>li")).toHaveCount(6);
  const author=page.locator(".create-participants>li").filter({has:page.getByRole("checkbox",{name:"작성자 참가자 포함",exact:true})});
  await author.getByRole("button",{name:"댓글 보기",exact:true}).click();const dialog=page.locator("dialog[open]");
  await expect(dialog).toContainText("작성자 댓글 "+injected);assert.equal(await dialog.locator("script,img,iframe,object").count(),0);
  assert.equal(await page.locator(".create-product script,.create-product iframe,.create-product object").count(),0);
  assert.equal(await page.evaluate(()=>globalThis.__jackpotInjected===true),false);
  await dialog.getByRole("button",{name:"닫기",exact:true}).click();await expect(dialog).toHaveCount(0);
  const after=await instance.call("DraftState");assert.deepEqual(after,draft);assert.equal((await snapshot()).collections,0);
  report.literalContent={article:true,comment:true,executions:0,readonly:true};
 });
 let first;
 await check("real-collector-local-immediate-draw",async()=>{first=await create();assert.equal(first.rounds.length,1);assert.equal(first.rounds[0].state,"completed");assert.equal(first.rounds[0].winners.length,1);assert.equal(first.remainingCount,first.selectedCount-1);report.selectedCount=first.selectedCount;report.collectionId=first.collectionId});
 await check("frozen-participants-comments-readonly",async()=>{const before=await snapshot();const page=instance.page;await button(page,"확정 참가자 보기").click();const dialog=page.locator("dialog[open]");await expect(dialog).toBeVisible();const rows=dialog.getByRole("list",{name:"확정 참가자",exact:true}).locator("li");await expect(rows).toHaveCount(6);await rows.first().getByRole("button").click();await expect(dialog.getByRole("list",{name:"저장된 댓글",exact:true}).locator("li")).toHaveCount(1);await button(page,"명단 닫기").click();await expect(dialog).toHaveCount(0);const after=await snapshot();assert.equal(after.results,before.results);assert.equal(after.latest.revision,before.latest.revision)});
 await check("actual-Canvas-PNG-safe-export-and-text",async()=>{const page=instance.page;await button(page,"PNG 미리보기").click();const image=page.getByRole("img",{name:"Jackpot 로컬 추첨 결과 미리보기"});await expect(image).toBeVisible();await expect.poll(()=>image.evaluate(img=>img.naturalWidth>0)).toBe(true);await button(page,"PNG 사진 저장").click();await expect.poll(async()=>Boolean((await snapshot()).savePath)).toBe(true);const state=await snapshot();const bytes=await readFile(state.savePath);assert.ok(bytes.length>100);await writeFile(resolve(task,"product-e2e-export.png"),bytes);assert.equal(bytes.subarray(0,8).toString("hex"),"89504e470d0a1a0a");await button(page,"결과 텍스트 복사").click();await expect.poll(async()=>(await snapshot()).copiedText.startsWith(first.article.title+"\n")).toBe(true);const text=(await snapshot()).copiedText;assert.ok(!text.includes("Jackpot 로컬 추첨 결과"));assert.ok(text.includes(first.rounds[0].winners[0].participant.nickname));assert.ok(text.includes("E2E 상품")&&!text.includes(work));await button(page,"미리보기 닫기").click();await expect(image).toBeHidden()});
 await check("no-history-navigation-retired-route-and-results-readonly",async()=>{const page=instance.page;const before=await snapshot();await expect(page.locator('nav a[href="#/history"]')).toHaveCount(0);await expect(page.locator('[data-product-screen="history"]')).toHaveCount(0);await page.evaluate(()=>{window.location.hash="#/history"});await expect(page.getByRole("heading",{name:"화면을 찾을 수 없습니다",exact:true})).toBeVisible();await expect(page.locator('[data-product-screen="history"]')).toHaveCount(0);await page.locator('nav a[href="#/create"]').click();await expect(visible(page,"디시인사이드 게시글 주소")).toBeEnabled();await page.evaluate(id=>{window.location.hash="#/results/"+encodeURIComponent(id)},first.collectionId);await expect(page.getByRole("heading",{name:"추첨 결과",exact:true})).toBeVisible();await expect(page.locator('select[name="resultRound"]')).toBeEnabled();const after=await snapshot();assert.equal(after.collections,before.collections);assert.equal(after.results,before.results);assert.equal(after.winners,before.winners);assert.deepEqual(after.latest,before.latest)});
 await check("rerun-remaining-union-and-original-round-immutable",async()=>{const page=instance.page;await button(page,"재추첨").click();const dialog=page.locator("dialog[open]");await dialog.locator('[name="prizeName1"]').fill("재추첨 상품");await button(page,"새 회차 추첨").click();await expect(page.locator('select[name="resultRound"] option')).toHaveCount(2);const state=await snapshot();const rounds=state.latest.rounds;assert.equal(rounds.length,2);assert.equal(rounds[1].state,"completed");const identities=rounds.flatMap(round=>round.winners.map(w=>w.participant.id));assert.equal(new Set(identities).size,identities.length);assert.deepEqual(rounds[0].winners,first.rounds[0].winners);await page.locator('select[name="resultRound"]').selectOption(rounds[0].roundId);await expect(button(page,"재추첨")).toBeDisabled();await page.locator('select[name="resultRound"]').selectOption(rounds[1].roundId)});
 let scheduled;
 await check("selected-round-change-releases-native-preview",async()=>{
  const page=instance.page,before=await snapshot(),rounds=before.latest.rounds;
  assert.equal(rounds.length,2);const select=page.locator('select[name="resultRound"]');
  const image=page.getByRole("img",{name:"Jackpot 로컬 추첨 결과 미리보기",includeHidden:true});
  for(const round of [rounds[0],rounds[1]]){
   await button(page,"PNG 미리보기").click();await expect(image).toBeVisible();await expect.poll(()=>image.evaluate(img=>img.naturalWidth>0)).toBe(true);
   await select.selectOption(round.roundId);await expect(image).toBeHidden();await expect(image).not.toHaveAttribute("src",/.+/);
   await expect(button(page,"미리보기 닫기")).toBeHidden();await expect(button(page,"PNG 미리보기")).toBeEnabled();
  }
  const after=await snapshot();assert.equal(after.results,before.results);assert.equal(after.winners,before.winners);assert.deepEqual(after.latest.rounds,before.latest.rounds);
 });
 await check("new-draft-after-finalize-and-reservation-acceptance-clock",async()=>{scheduled=await create("reservation");assert.equal(scheduled.rounds[0].state,"pending_schedule");await button(instance.page,"30초 후").click();await expect(instance.page.locator('select[name="resultRound"] option')).toContainText("예약됨");scheduled=(await snapshot()).latest;assert.equal(scheduled.rounds[0].state,"scheduled");assert.equal(scheduled.rounds[0].winners.length,0)});
 await check("scheduler-clock-forward-once-and-delayed-time",async()=>{await instance.call("Advance",40);await button(instance.page,"다시 조회").click();await expect(instance.page.locator('select[name="resultRound"] option')).toContainText("완료");const state=await snapshot();assert.equal(state.latest.rounds[0].state,"completed");assert.equal(state.latest.rounds[0].winners.length,1);assert.ok(Date.parse(state.latest.rounds[0].executedAt)>=Date.parse(state.latest.rounds[0].scheduledAt));await instance.call("Advance",0);assert.equal((await snapshot()).results,state.results)});
 let beforeRestart;
 await check("native-exit-and-restart-offline-results-direct-management",async()=>{beforeRestart=await snapshot();await stop();await launch();assert.ok(beforeRestart.latest.collectionId);await instance.page.evaluate(id=>{window.location.hash="#/results/"+encodeURIComponent(id)},beforeRestart.latest.collectionId);await expect(instance.page.getByRole("heading",{name:"추첨 결과",exact:true})).toBeVisible();await expect(button(instance.page,"재추첨")).toBeEnabled();const state=await snapshot();assert.equal(state.results,beforeRestart.results);assert.equal(state.fixtureCalls,0);assert.equal(state.latest.rounds[0].state,"completed");await expect(instance.page.locator('input[type="password"]')).toHaveCount(0)});
 await check("restored-latest-management-keeps-saved-results-immutable",async()=>{const page=instance.page;const before=await snapshot();await expect(button(page,"재추첨")).toBeEnabled();await expect(button(page,"관리 잠금 해제")).toHaveCount(0);await expect(page.locator("dialog[open]")).toHaveCount(0);const after=await snapshot();assert.equal(after.results,before.results);assert.equal(after.winners,before.winners);assert.deepEqual(after.latest.rounds,before.latest.rounds)});
 await check("native-minimum-window-and-final-resource-exit",async()=>{const page=instance.page;await page.evaluate(async()=>{const {Window}=await import("/wails/runtime.js");await Window.SetSize(1024,720)});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth),true);await page.screenshot({path:resolve(task,"product-e2e-result.png")});report.final=await snapshot();await stop()});
 report.status="passed";
}catch(error){report.status="failed";report.failedStep=step;report.failure=error instanceof Error?error.message.slice(0,1500):"Unknown failure";if(instance){try{report.snapshot=await snapshot();report.ui=await instance.page.locator(".screen").innerText();await instance.page.screenshot({path:resolve(task,"product-e2e-failure.png")})}catch{}};process.exitCode=1}
finally{if(instance){try{await stop()}catch{instance?.child.kill();report.cleanupFailure=true;process.exitCode=1}};if(!report.cleanupFailure){await rm(work,{recursive:true,force:true});report.cleanup="isolated process/profile/DB removed"};await writeFile(reportPath,JSON.stringify(report,null,2)+"\n");console.log(JSON.stringify({status:report.status,checks:report.checks,failedStep:report.failedStep,report:reportPath}))}
