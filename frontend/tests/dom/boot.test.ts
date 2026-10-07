import { afterEach, expect, test, vi } from "vitest";
import type { BootstrapReply } from "../../src/contracts/backend";

const native = vi.hoisted(() => ({ fail: false, badTheme: false, restoredDraft: false, reads: 0, active: 0 }));
vi.mock("../../src/platform/wails", async () => {
  const { Layer, Effect } = await import("effect");
  const { Backend, ProtocolError } = await import("../../src/contracts/backend");
  return { WailsBackendLive: Layer.succeed(Backend, {
    product: null,
    bootstrap: () => Effect.suspend(() => {
      native.reads++;
      if (native.fail) return Effect.fail(new ProtocolError());
      const reply: BootstrapReply = { protocolVersion: 1, backendSessionId: "session", occurredAt: "2026-10-06T00:00:00Z", data: {
        backendNow: "2026-10-06T00:00:00Z", theme: native.badTheme ? "unknown" : "dark", activeDraft: native.restoredDraft ? { backendSessionId:"session",draftId:"restored-draft",revision:0,articleGeneration:0,state:"empty",snapshot:null,collectionId:null } : null, pendingOperations: [], pendingCursor: null, recentResults: [],
      } };
      return Effect.succeed(reply);
    }),
    listPendingOperations: () => Effect.fail(new ProtocolError()),
    getOperation: () => Effect.die("GetOperation is unused by this boot harness"),
    subscribeStateChanges: () => Effect.asVoid(Effect.acquireRelease(Effect.sync(() => { native.active++; }), () => Effect.sync(() => { native.active--; }))),
  }) };
});
let dispose: (() => Promise<void>) | undefined;
afterEach(async () => {
  await dispose?.(); dispose = undefined;
  document.body.replaceChildren(); window.location.hash = "";
  native.fail = false; native.badTheme = false; native.restoredDraft = false; native.reads = 0; expect(native.active).toBe(0);
  vi.resetModules();
});

test("boot assembles one scoped runtime and Coordinator read before displaying connected shell", async () => {
  const host = document.createElement("main"); host.id = "app"; document.body.append(host);
  const app = await import("../../src/main"); dispose = app.appOwner.dispose;
  expect(app.appOwner.phase()).toBe("running"); expect(native.reads).toBe(1); expect(native.active).toBe(1);
  expect(host.querySelector("h1")?.textContent).toBe("Jackpot");
  expect(host.querySelector('[role="status"]')?.textContent).toContain("저장소 연결됨");
  expect(host.querySelector('[data-appearance="dark"]')).not.toBeNull();
  expect(host.querySelectorAll("nav a")).toHaveLength(1); expect(host.querySelector("label select")).not.toBeNull();
  expect(host.querySelector('nav a[href="#/history"]')).toBeNull();
  expect(document.body.childElementCount).toBe(1);
  await app.appOwner.dispose(); await app.appOwner.dispose(); expect(native.active).toBe(0); expect(host.childElementCount).toBe(0);
});

for (const kind of ["read", "theme"] as const) test(`boot ${kind} protocol failure preserves visible error shell and releases subscription`, async () => {
  const host = document.createElement("main"); host.id = "app"; document.body.append(host);
  native.fail = kind === "read"; native.badTheme = kind === "theme";
  const app = await import("../../src/main"); dispose = app.appOwner.dispose;
  const error = host.querySelector<HTMLElement>('[role="alert"]');
  expect(error?.hidden).toBe(false); expect(error?.textContent).toContain("앱 응답 형식");
  expect(host.querySelector("h1")?.textContent).toBe("Jackpot"); expect(native.active).toBe(0); expect(native.reads).toBe(1);
});

test("native theme control owns theme state and pagehide disposes all app resources", async () => {
  const host = document.createElement("main"); host.id = "app"; document.body.append(host);
  const app = await import("../../src/main"); dispose = app.appOwner.dispose;
  const theme = host.querySelector<HTMLSelectElement>("select"); expect(theme).not.toBeNull();
  theme!.value = "light"; theme!.dispatchEvent(new Event("change"));
  expect(host.querySelector('[data-appearance="light"]')).not.toBeNull();
  window.dispatchEvent(new Event("pagehide")); await app.appOwner.dispose(); expect(app.appOwner.phase()).toBe("stopped"); expect(host.childElementCount).toBe(0);
});

test("boot exposes a missing mount host without adding partial nodes", async () => {
  await expect(import("../../src/main")).rejects.toThrow("앱 표시 영역이 없습니다."); expect(document.body.childElementCount).toBe(0);
});

for(const restored of [false,true]) test(`first Bootstrap existing draft ${restored} owns raw-loss notice without later focus fabrication`,async()=>{
 native.restoredDraft=restored;
 const host=document.createElement("main");host.id="app";document.body.append(host);
 const app=await import("../../src/main");dispose=app.appOwner.dispose;
 const notice=host.querySelector<HTMLElement>('[role="note"][data-draft-recovery]');
 expect(notice).not.toBeNull();expect(notice?.hidden).toBe(!restored);
 if(restored)expect(notice?.textContent).toBe("현재 초안을 다시 불러왔습니다. 이전 화면에서 전송하지 않은 입력은 초기화되었습니다.");
 else expect(notice?.textContent).toBe("");
 native.restoredDraft=!restored;window.dispatchEvent(new Event("focus"));
 const {Effect}=await import("effect");
 for(let turn=0;turn<256&&native.reads<2;turn++)await Effect.runPromise(Effect.yieldNow);
 expect(native.reads).toBe(2);expect(host.querySelector('[data-draft-recovery]')).toBe(notice);expect(notice?.hidden).toBe(!restored);
 await app.appOwner.dispose();expect(host.childElementCount).toBe(0);
});
