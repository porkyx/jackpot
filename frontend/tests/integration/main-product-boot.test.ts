import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { Deferred, Effect } from "effect";
import type { BackendError, PendingOperationsReply, BootstrapReply } from "../../src/contracts/backend";
import type { Draft, ProductReply } from "../../src/contracts/product";

import type { CreateWorkspace } from "../../src/screens/create/productOwner";
import type { backendHarness } from "../helpers/productBackend";

const native = vi.hoisted(() => ({
  recoveryBlocked: true, holdCreate: false, bootstrapStarted: 0, bootstrapGate: null as Deferred.Deferred<BootstrapReply,BackendError> | null,
  page: null as Deferred.Deferred<PendingOperationsReply,BackendError> | null,
  create: null as Deferred.Deferred<ProductReply<Draft>,BackendError> | null,
  runtimeRelease: null as Deferred.Deferred<void> | null,
  runtimeStarted: null as Deferred.Deferred<void> | null,
  owner: undefined as {readonly ready:Promise<unknown>;readonly dispose:()=>Promise<void>;readonly phase:()=>string}|undefined,
  port: undefined as ReturnType<typeof backendHarness>|undefined,
  workspace: undefined as CreateWorkspace|undefined,
  activeNotices:0,activePage:0,cancelledPage:0,activeCreate:0,cancelledCreate:0,
  ensureRuns:0,queued:0,executed:new Map<number,number>(),
  hash:"#/create",
}));

// The actual application, bootstrap/recovery owners, Coordinator, workspace,
// Runtime and DOM execute. Only the external Wails backend is supplied by a
// controlled boundary; fixture results are never imported by production.
vi.mock("../../src/platform/wails",async()=>{
  const {Effect,Exit,Layer}=await import("effect");
  const {Backend,ProtocolError}=await import("../../src/contracts/backend");
  const {backendHarness,bootstrap,reply}=await import("../helpers/productBackend");
  const {draft}=await import("../helpers/productCommands");
  return {WailsResultExportPorts:{savePng:()=>Effect.succeed("cancelled"),copyText:()=>Effect.void},openWailsArticle:()=>Effect.void,WailsBackendLive:Layer.sync(Backend,()=>{
    const empty=draft({summary:{...draft().summary,state:"empty"},article:null,participants:0,included:0,excluded:0});
    const port=backendHarness(empty);native.port=port;
    port.override("createDraft",Effect.scoped(Effect.gen(function*(){
      let completed=false;
      yield* Effect.acquireRelease(Effect.sync(()=>{native.activeCreate++;}),()=>Effect.sync(()=>{native.activeCreate--;if(!completed)native.cancelledCreate++;}));
      const answer=yield* (native.holdCreate?Deferred.await(native.create!):Effect.succeed(reply(empty))).pipe(Effect.onExit((exit)=>Effect.sync(()=>{completed=Exit.isSuccess(exit);})));return answer;
    })));
    return {
      product:port.backend,
      bootstrap:()=>native.bootstrapGate===null?Effect.succeed({...bootstrap(null),data:{...bootstrap(null).data,pendingCursor:native.recoveryBlocked?"cursor":null}}):Effect.sync(()=>{native.bootstrapStarted++;}).pipe(Effect.andThen(Deferred.await(native.bootstrapGate))),
      listPendingOperations:()=>Effect.scoped(Effect.gen(function*(){let completed=false;yield* Effect.acquireRelease(Effect.sync(()=>{native.activePage++;}),()=>Effect.sync(()=>{native.activePage--;if(!completed)native.cancelledPage++;}));return yield* Deferred.await(native.page!).pipe(Effect.onExit((exit)=>Effect.sync(()=>{completed=Exit.isSuccess(exit);})));})),
      getOperation:()=>Effect.fail(new ProtocolError()),
      subscribeStateChanges:()=>Effect.asVoid(Effect.acquireRelease(Effect.sync(()=>{native.activeNotices++;}),()=>Effect.sync(()=>{native.activeNotices--;}))),
    };
  })};
});

// Delaying delivery of an already-created real Runtime isolates the interval
// when views enqueue work but main has not installed the app-owned dispatcher.
vi.mock("../../src/app/runtime",async()=>{
  const actual=await vi.importActual<typeof import("../../src/app/runtime")>("../../src/app/runtime");
  return {...actual,createAppRuntimeWithHmr:async(...args:Parameters<typeof actual.createAppRuntimeWithHmr>)=>{
    const owner=await actual.createAppRuntimeWithHmr(...args);native.owner=owner;Deferred.doneUnsafe(native.runtimeStarted!,Effect.void);
    if(native.runtimeRelease!==null)await Effect.runPromise(Deferred.await(native.runtimeRelease));
    return owner;
  }};
});
vi.mock("../../src/screens/create/productOwner",async()=>{
  const actual=await vi.importActual<typeof import("../../src/screens/create/productOwner")>("../../src/screens/create/productOwner");
  return {...actual,makeCreateWorkspace:(commands:Parameters<typeof actual.makeCreateWorkspace>[0],dispatch:Parameters<typeof actual.makeCreateWorkspace>[1])=>actual.makeCreateWorkspace(commands,(effect)=>{
    const token=++native.queued;dispatch(Effect.sync(()=>{native.executed.set(token,(native.executed.get(token)??0)+1);}).pipe(Effect.andThen(effect)));
  }).pipe(Effect.map((workspace)=>{const observed={...workspace,ensure:Effect.sync(()=>{native.ensureRuns++;}).pipe(Effect.andThen(workspace.ensure))};native.workspace=observed;return observed;}))};
});

let host:HTMLElement;
let gatedImport:Promise<typeof import("../../src/main")>|undefined;
let frameToken=0;
const frames=new Map<number,FrameRequestCallback>();
async function settle(predicate:()=>boolean){for(let turn=0;turn<256&&!predicate();turn++){await Effect.runPromise(Effect.yieldNow);const queued=[...frames.values()];frames.clear();for(const frame of queued)frame(0);}expect(predicate(),"controlled runnable fibers must settle within256 turns").toBe(true);}
function url(){return host.querySelector<HTMLInputElement>('input[type="url"]');}
function load(){return [...host.querySelectorAll("button")].find((element)=>element.textContent==="게시글 불러오기");}
function recoveryPage():PendingOperationsReply{return {protocolVersion:1,backendSessionId:"session",occurredAt:"2026-10-06T00:00:00Z",data:{operations:[],cursor:null}};}

beforeEach(()=>{
  host=document.createElement("main");host.id="app";document.body.append(host);
  vi.spyOn(window.location,"hash","get").mockImplementation(()=>native.hash);
  vi.spyOn(window,"requestAnimationFrame").mockImplementation((callback)=>{frames.set(++frameToken,callback);return frameToken;});
  vi.spyOn(window,"cancelAnimationFrame").mockImplementation((token)=>{frames.delete(token);});
  native.runtimeStarted=Deferred.makeUnsafe<void>();native.page=Deferred.makeUnsafe<PendingOperationsReply,BackendError>();native.create=Deferred.makeUnsafe<ProductReply<Draft>,BackendError>();
});
afterEach(async()=>{
  try{await native.owner?.dispose();if(native.runtimeRelease!==null)await Effect.runPromise(Deferred.succeed(native.runtimeRelease,undefined));await gatedImport;expect(native.activeNotices).toBe(0);expect(native.activePage).toBe(0);expect(native.activeCreate).toBe(0);expect(frames.size).toBe(0);expect(host.childElementCount).toBe(0);}
  finally{vi.restoreAllMocks();vi.resetModules();document.body.replaceChildren();frames.clear();frameToken=0;native.hash="#/create";native.recoveryBlocked=true;native.holdCreate=false;native.bootstrapStarted=0;native.bootstrapGate=null;native.page=null;native.create=null;native.runtimeRelease=null;native.runtimeStarted=null;gatedImport=undefined;native.owner=undefined;native.port=undefined;native.workspace=undefined;native.activeNotices=0;native.activePage=0;native.cancelledPage=0;native.activeCreate=0;native.cancelledCreate=0;native.ensureRuns=0;native.queued=0;native.executed.clear();}
});

test("actual product main has no history menu and reads a result without changing the draft",async()=>{
  native.recoveryBlocked=false;
  const app=await import("../../src/main");await settle(()=>url()?.disabled===false);
  const before=JSON.stringify(native.port!.current());
  expect(host.querySelector('nav a[href="#/history"]')).toBeNull();
  expect([...host.querySelectorAll("nav a")].map(link=>link.textContent)).toEqual(["만들기"]);
  native.hash="#/history";window.dispatchEvent(new Event("hashchange"));
  await settle(()=>host.querySelector("h2")?.textContent==="화면을 찾을 수 없습니다");
  expect(host.querySelector('[data-product-screen="history"]')).toBeNull();
  native.hash="#/results/collection";window.dispatchEvent(new Event("hashchange"));
  await settle(()=>host.querySelector('[data-product-screen="result"]')!==null&&native.port!.count("getCollection")===1);
  expect(host.querySelector("h2")?.textContent).toBe("추첨 결과");
  expect(JSON.stringify(native.port!.current())).toBe(before);
  expect(native.port!.count("createDraft")).toBe(1);
  await app.appOwner.dispose();expect(host.childElementCount).toBe(0);
});

test("actual main recovery refresh latches duplicate clicks and releases after one bootstrap",async()=>{
  const app=await import("../../src/main");await settle(()=>native.activePage===1);
  const {ProtocolError}=await import("../../src/contracts/backend");
  await Effect.runPromise(Deferred.fail(native.page!,new ProtocolError()));
  await settle(()=>native.activePage===0&&host.textContent?.includes("처리 결과를 확인하지 못했습니다")===true);
  const button=[...host.querySelectorAll("button")].find(node=>node.textContent==="처리 결과 다시 확인")!;
  await settle(()=>!button.disabled);
  native.bootstrapGate=Deferred.makeUnsafe<BootstrapReply,BackendError>();
  button.click();button.click();await settle(()=>native.bootstrapStarted===1);
  expect(button.disabled).toBe(true);
  const {bootstrap}=await import("../helpers/productBackend");const gate=native.bootstrapGate;native.bootstrapGate=null;
  await Effect.runPromise(Deferred.succeed(gate,bootstrap(null)));
  await settle(()=>url()?.disabled===false);
  expect(native.bootstrapStarted).toBe(1);expect(native.port!.count("createDraft")).toBe(1);
  await app.appOwner.dispose();expect(native.activePage).toBe(0);
});

test("actual main projects a rejected bootstrap and disables all product mutations",async()=>{
  const {BackendRejected}=await import("../../src/contracts/backend");
  native.bootstrapGate=Deferred.makeUnsafe<BootstrapReply,BackendError>();
  await Effect.runPromise(Deferred.fail(native.bootstrapGate,new BackendRejected({code:"StorageUnavailable",messageKey:"StorageUnavailable"})));
  const app=await import("../../src/main");await app.appOwner.ready;
  await settle(()=>host.querySelector('[role="alert"]')?.textContent?.includes("기록 저장소")===true);
  expect(native.port!.count("createDraft")).toBe(0);expect(native.activeNotices).toBe(0);
  expect(url()===null||url()!.disabled).toBe(true);await app.appOwner.dispose();
});

test("actual main retries rejected initial draft ensure when durable enumeration becomes ready",async()=>{
  const app=await import("../../src/main");await settle(()=>native.activePage===1&&native.ensureRuns===1);
  expect(app.appOwner.phase()).toBe("running");expect(url()?.disabled).toBe(true);expect(load()?.disabled).toBe(true);expect(native.port?.count("createDraft")).toBe(0);
  await Effect.runPromise(Deferred.succeed(native.page!,recoveryPage()));await settle(()=>url()?.disabled===false);
  expect(load()?.disabled).toBe(false);expect(native.ensureRuns).toBe(2);expect(native.port?.count("createDraft")).toBe(1);expect(native.workspace?.read().editor?.identity.draftId).toBe("draft");expect(native.executed.size).toBeGreaterThan(0);expect([...native.executed.values()].every((count)=>count===1)).toBe(true);
  await app.appOwner.dispose();await app.appOwner.dispose();expect(native.workspace?.read().closed).toBe(true);
});

test("actual main executes a view effect queued before dispatcher readiness exactly once",async()=>{
  native.recoveryBlocked=false;native.runtimeRelease=Deferred.makeUnsafe<void>();
  const importing=import("../../src/main");gatedImport=importing;await Effect.runPromise(Deferred.await(native.runtimeStarted!));await native.owner!.ready;await settle(()=>native.queued>0&&url()!==null);
  expect(native.executed.size).toBe(0);expect(native.ensureRuns).toBe(0);expect(native.port?.count("createDraft")).toBe(0);expect(url()?.disabled).toBe(true);
  await Effect.runPromise(Deferred.succeed(native.runtimeRelease,undefined));const app=await importing;await settle(()=>url()?.disabled===false);
  expect(native.executed.size).toBeGreaterThan(0);expect([...native.executed.values()].every((count)=>count===1)).toBe(true);expect(native.port?.count("createDraft")).toBe(1);expect(load()?.disabled).toBe(false);await app.appOwner.dispose();
});

test("closing real Runtime before dispatcher publication discards queued view work without IO",async()=>{
  native.recoveryBlocked=false;native.runtimeRelease=Deferred.makeUnsafe<void>();
  const importing=import("../../src/main");gatedImport=importing;await Effect.runPromise(Deferred.await(native.runtimeStarted!));await native.owner!.ready;await settle(()=>native.queued>0&&url()!==null);
  await native.owner!.dispose();await Effect.runPromise(Deferred.succeed(native.runtimeRelease,undefined));const app=await importing;
  expect(app.appOwner.phase()).toBe("stopped");expect(native.executed.size).toBe(0);expect(native.ensureRuns).toBe(0);expect(native.port?.count("createDraft")).toBe(0);expect(host.childElementCount).toBe(0);
});

test("closing actual product app cancels admitted draft creation and late reply cannot restore DOM",async()=>{
  native.recoveryBlocked=false;native.holdCreate=true;
  const app=await import("../../src/main");await settle(()=>native.activeCreate===1);expect(native.port?.count("createDraft")).toBe(1);expect(url()?.disabled).toBe(true);
  await app.appOwner.dispose();expect(native.activeCreate).toBe(0);expect(native.cancelledCreate).toBe(1);expect(native.workspace?.read().closed).toBe(true);
  const {draft}=await import("../helpers/productCommands");const {reply}=await import("../helpers/productBackend");await Effect.runPromise(Deferred.succeed(native.create!,reply(draft())));
  expect(host.childElementCount).toBe(0);expect(native.port?.count("createDraft")).toBe(1);expect(frames.size).toBe(0);
});

test("closing actual main during pending recovery cancels read and never admits a new draft",async()=>{
  const app=await import("../../src/main");await settle(()=>native.activePage===1&&native.ensureRuns===1);
  await app.appOwner.dispose();expect(native.cancelledPage).toBe(1);expect(native.port?.count("createDraft")).toBe(0);expect(native.workspace?.read().closed).toBe(true);
  await Effect.runPromise(Deferred.succeed(native.page!,recoveryPage()));expect(host.childElementCount).toBe(0);expect(frames.size).toBe(0);
});
test("actual window focus resync disables load without dropping raw and releases it after current bootstrap",async()=>{
 native.recoveryBlocked=false;const app=await import("../../src/main");await settle(()=>url()?.disabled===false);
 const input=url()!;input.value="https://evil.invalid/secret";input.dispatchEvent(new Event("input"));
 expect(native.workspace?.read().editor?.url.raw).toBe("https://evil.invalid/secret");
 native.bootstrapGate=Deferred.makeUnsafe<BootstrapReply,BackendError>();window.dispatchEvent(new Event("focus"));
 await settle(()=>native.bootstrapStarted===1&&host.textContent?.includes("변경 기능은 아직 사용할 수 없습니다.")===true);
 await settle(()=>load()?.disabled===true&&url()?.disabled===false);expect(load()?.disabled).toBe(true);expect(url()?.disabled).toBe(false);expect(native.port?.count("loadArticle")).toBe(0);
 load()?.click();await settle(()=>native.port?.count("loadArticle")===0);expect(native.workspace?.read().editor?.url.raw).toBe("https://evil.invalid/secret");
 const {bootstrap}=await import("../helpers/productBackend");const refreshed=bootstrap(native.port!.current());const gate=native.bootstrapGate;native.bootstrapGate=null;
 await Effect.runPromise(Deferred.succeed(gate,refreshed));await settle(()=>load()?.disabled===false);
 expect(url()).toBe(input);expect(input.value).toBe("https://evil.invalid/secret");expect(native.workspace?.read().error).toBeNull();
 load()!.click();await settle(()=>native.port!.count("loadArticle")===1);expect(native.port!.calls.get("loadArticle")?.[0]).toHaveProperty("url","https://evil.invalid/secret");
 await app.appOwner.dispose();expect(native.workspace?.read().closed).toBe(true);
});
