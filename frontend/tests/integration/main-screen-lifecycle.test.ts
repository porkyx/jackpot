import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { Deferred, Effect, Scope } from "effect";
import type { BackendError, BootstrapReply, StateNotice } from "../../src/contracts/backend";
import type { RouteMount, ScreenLease } from "../../src/app/routeManager";
import type { BootstrapSource, BootstrapSync } from "../../src/operations/bootstrapSync";
import type { CreateScreenOwner } from "../../src/screens/create/screenOwner";
import type { CreateScreenModel, DraftScreenContext } from "../../src/screens/create/screenModel";

interface RouteRecord { readonly lease: ScreenLease; readonly scope: Scope.Scope; closed: boolean }
interface ScreenRecord { readonly lease: ScreenLease; readonly owner: CreateScreenOwner<never>; readonly initial: CreateScreenModel; readonly updates: Array<DraftScreenContext | null> }
interface ListenerRecord { readonly target: EventTarget; readonly type: string; readonly listener: EventListenerOrEventListenerObject }

const native = vi.hoisted(() => ({
  read: undefined as ((call: number) => Effect.Effect<BootstrapReply, BackendError>) | undefined,
  reads: 0, activeReads: 0, cancelledReads: 0, activeNotices: 0, noticeReleases: 0,
  subscribeFailure: false,
  notice: undefined as ((notice: StateNotice) => void) | undefined,
  noticeError: undefined as ((error: BackendError) => void) | undefined,
  sync: null as BootstrapSync | null,
  syncSubscribers: 0, maxSyncSubscribers: 0,
  routes: [] as RouteRecord[], screens: [] as ScreenRecord[], listeners: [] as ListenerRecord[],
  mounted: new Map<number, Deferred.Deferred<void>>(),
  hash: "#/create",
  owner: undefined as { readonly dispose: () => Promise<void>; readonly phase: () => string } | undefined,
}));

// Only the real Wails boundary is replaced. Every app/runtime/route/Bootstrap/
// ScreenOwner implementation still executes, and unused product APIs fail loudly.
vi.mock("../../src/platform/wails", async () => {
  const { Cause, Effect, Exit, Layer } = await import("effect");
  const { Backend, ProtocolError } = await import("../../src/contracts/backend");
  return { WailsBackendLive: Layer.succeed(Backend, {
    product: null,
    bootstrap: () => Effect.scoped(Effect.gen(function*() {
      const call = ++native.reads;
      let completed = false;
      yield* Effect.acquireRelease(Effect.sync(() => { native.activeReads++; }), () => Effect.sync(() => {
        native.activeReads--; if (!completed) native.cancelledReads++;
      }));
      const read = native.read?.(call) ?? Effect.succeed({ protocolVersion: 1 as const, backendSessionId: "session-a", occurredAt: "2026-10-06T00:00:00Z", data: {
        backendNow: "2026-10-06T00:00:00Z", theme: "dark", activeDraft: { backendSessionId: "session-a", draftId: "draft", revision: 1, articleGeneration: 1, state: "ready", snapshot: null, collectionId: null },
        pendingOperations: [], pendingCursor: null, recentResults: [],
      } });
      return yield* read.pipe(Effect.onExit((exit) => Effect.sync(() => { completed = Exit.isSuccess(exit) || !Cause.hasInterrupts(exit.cause); })));
    })),
    listPendingOperations: () => Effect.die("ListPendingOperations is unused by the main lifecycle harness"),
    getOperation: () => Effect.die("GetOperation is unused by the main lifecycle harness"),
    subscribeStateChanges: (listener: (notice: StateNotice) => void, onError: (error: BackendError) => void) => Effect.acquireRelease(Effect.sync(() => {
      native.activeNotices++; native.notice = listener; native.noticeError = onError;
    }), () => Effect.sync(() => { native.activeNotices--; native.noticeReleases++; })).pipe(
      Effect.andThen(Effect.suspend(() => native.subscribeFailure ? Effect.fail(new ProtocolError()) : Effect.void)),
    ),
  }) };
});

// These wrappers observe resource ownership. They never replace a reducer,
// route mount, query or read with a successful placeholder.
vi.mock("../../src/app/routeManager", async () => {
  const actual = await vi.importActual<typeof import("../../src/app/routeManager")>("../../src/app/routeManager");
  const { Deferred, Effect, Scope } = await import("effect");
  return { ...actual, makeRouteManager: (screen: RouteMount<unknown, never>) => actual.makeRouteManager({ ...screen,
    mount: (lease) => Effect.gen(function*() {
      const scope = yield* Scope.Scope;
      const record: RouteRecord = { lease, scope, closed: false }; native.routes.push(record);
      yield* Scope.addFinalizer(scope, Effect.sync(() => { record.closed = true; }));
      yield* screen.mount(lease);
      let mounted = native.mounted.get(lease.generation);
      if (mounted === undefined) { mounted = Deferred.makeUnsafe<void>(); native.mounted.set(lease.generation, mounted); }
      yield* Deferred.succeed(mounted, undefined);
    }),
  }) };
});

vi.mock("../../src/operations/bootstrapSync", async () => {
  const actual = await vi.importActual<typeof import("../../src/operations/bootstrapSync")>("../../src/operations/bootstrapSync");
  const { Effect, Stream } = await import("effect");
  return { ...actual, makeBootstrapSync: (source: BootstrapSource) => actual.makeBootstrapSync(source).pipe(Effect.map((sync) => {
    native.sync = sync;
    return { ...sync, changes: Stream.unwrap(Effect.acquireRelease(Effect.sync(() => {
      native.syncSubscribers++; native.maxSyncSubscribers = Math.max(native.maxSyncSubscribers, native.syncSubscribers);
      return sync.changes;
    }), () => Effect.sync(() => { native.syncSubscribers--; }))) };
  })) };
});

vi.mock("../../src/screens/create/screenOwner", async () => {
  const actual = await vi.importActual<typeof import("../../src/screens/create/screenOwner")>("../../src/screens/create/screenOwner");
  const { Effect } = await import("effect");
  return { ...actual, mountCreateScreenModel: (lease: ScreenLease, context: DraftScreenContext | null, query: null) => actual.mountCreateScreenModel(lease, context, query).pipe(Effect.flatMap((owner) => owner.snapshot.pipe(Effect.map((initial) => {
    const record: ScreenRecord = { lease, owner, initial, updates: [] }; native.screens.push(record);
    return { ...owner, updateContext: (next: DraftScreenContext | null) => owner.updateContext(next).pipe(Effect.tap(() => Effect.sync(() => {
      record.updates.push(next);
    }))) };
  })))) };
});

vi.mock("../../src/app/runtime", async () => {
  const actual = await vi.importActual<typeof import("../../src/app/runtime")>("../../src/app/runtime");
  return { ...actual, createAppRuntimeWithHmr: async (...args: Parameters<typeof actual.createAppRuntimeWithHmr>) => {
    const owner = await actual.createAppRuntimeWithHmr(...args); native.owner = owner;
    return owner;
  } };
});

let dispose: (() => Promise<void>) | undefined;
let host: HTMLElement;

async function waitUntil(predicate: () => boolean): Promise<void> {
  // All dependencies are already completed or controlled by a Deferred. Only
  // runnable fibers need turns; this has a strict bound and no wall-clock sleep.
  for (let turn = 0; turn < 128 && !predicate(); turn++) await Effect.runPromise(Effect.yieldNow);
  expect(predicate(), "runnable fibers must reach the observed state within 128 scheduler turns").toBe(true);
}
function mounted(generation: number): Promise<void> {
  let pending = native.mounted.get(generation);
  if (pending === undefined) { pending = Deferred.makeUnsafe<void>(); native.mounted.set(generation, pending); }
  return Effect.runPromise(Deferred.await(pending));
}
function reply(session = "session-a", revision = 1): BootstrapReply {
  return { protocolVersion: 1, backendSessionId: session, occurredAt: "2026-10-06T00:00:00Z", data: {
    backendNow: "2026-10-06T00:00:00Z", theme: "dark", activeDraft: { backendSessionId: session, draftId: "draft", revision, articleGeneration: revision, state: "ready", snapshot: null, collectionId: null },
    pendingOperations: [], pendingCursor: null, recentResults: [],
  } };
}
function notice(session: string, revision: number): StateNotice {
  return { backendSessionId: session, entityKind: "draft", entityId: "draft", revision, operationId: null };
}
function setHash(hash: string): void { native.hash = hash; }

beforeEach(() => {
  host = document.createElement("main"); host.id = "app"; document.body.append(host); setHash("#/create");
  // Happy DOM also emits hashchange from replaceState. Control only the hash
  // getter and dispatch each real EventTarget event explicitly once.
  vi.spyOn(window.location, "hash", "get").mockImplementation(() => native.hash);
  const track = (target: EventTarget, type: string, listener: EventListenerOrEventListenerObject | null) => {
    if (listener !== null) native.listeners.push({ target, type, listener });
  };
  const untrack = (target: EventTarget, type: string, listener: EventListenerOrEventListenerObject | null) => {
    const index = native.listeners.findIndex((entry) => entry.target === target && entry.type === type && entry.listener === listener);
    if (index >= 0) native.listeners.splice(index, 1);
  };
  const windowAdd = window.addEventListener.bind(window); const windowRemove = window.removeEventListener.bind(window);
  vi.spyOn(window, "addEventListener").mockImplementation((type, listener, options) => { track(window, type, listener); windowAdd(type, listener, options); });
  vi.spyOn(window, "removeEventListener").mockImplementation((type, listener, options) => { untrack(window, type, listener); windowRemove(type, listener, options); });
  const selectAdd = HTMLSelectElement.prototype.addEventListener; const selectRemove = HTMLSelectElement.prototype.removeEventListener;
  vi.spyOn(HTMLSelectElement.prototype, "addEventListener").mockImplementation(function(this: HTMLSelectElement, type, listener, options) { track(this, type, listener); selectAdd.call(this, type, listener, options); });
  vi.spyOn(HTMLSelectElement.prototype, "removeEventListener").mockImplementation(function(this: HTMLSelectElement, type, listener, options) { untrack(this, type, listener); selectRemove.call(this, type, listener, options); });
});

afterEach(async () => {
  try {
  await (dispose ?? native.owner?.dispose)?.(); dispose = undefined;
  expect(native.activeReads).toBe(0); expect(native.activeNotices).toBe(0); expect(native.syncSubscribers).toBe(0);
  expect(native.routes.every((record) => record.closed && record.scope.state._tag === "Closed")).toBe(true);
  expect(native.listeners).toHaveLength(0); expect(host.childElementCount).toBe(0);
  for (const screen of native.screens) expect(await Effect.runPromise(screen.owner.snapshot)).toEqual({ _tag: "closed" });
  } finally {
  dispose = undefined;
  vi.restoreAllMocks(); document.body.replaceChildren(); setHash("#/create");
  native.read = undefined; native.reads = 0; native.activeReads = 0; native.cancelledReads = 0; native.activeNotices = 0; native.noticeReleases = 0;
  native.subscribeFailure = false; native.notice = undefined; native.noticeError = undefined; native.sync = null; native.syncSubscribers = 0; native.maxSyncSubscribers = 0;
  native.routes.length = 0; native.screens.length = 0; native.listeners.length = 0; native.mounted.clear(); native.owner = undefined;
  vi.resetModules();
  }
});

test("actual main keeps one route Scope and a stable listener/subscription baseline through 100 hash transitions", async () => {
  const app = await import("../../src/main"); dispose = app.appOwner.dispose; await mounted(1); await waitUntil(() => native.syncSubscribers === 2);
  const shell = host.firstElementChild; const theme = host.querySelector<HTMLSelectElement>("select")!;
  const appState = await app.appOwner.ready; const confirmed = await app.appOwner.run(appState.sync!.snapshot);
  expect(native.listeners.map((entry) => entry.type).sort()).toEqual(["change", "focus", "hashchange", "pagehide"]);
  for (let index = 0; index < 100; index++) {
    const hash = ["#/unrecognized", "#/results/collection", "#/create", "#/unrecognized"][index % 4]!;
    setHash(hash); window.dispatchEvent(new Event("hashchange")); await mounted(index + 2);
    await waitUntil(() => native.syncSubscribers === (hash === "#/create" ? 2 : 1));
    expect(host.querySelector("h2")?.textContent).toBe(hash === "#/create" ? "일반 추첨" : hash.startsWith("#/results/") ? "추첨 결과" : "화면을 찾을 수 없습니다");
    expect(native.routes.filter((record) => !record.closed)).toHaveLength(1);
    expect(native.routes.slice(0, -1).every((record) => record.scope.state._tag === "Closed" && !record.lease.isCurrent())).toBe(true);
    expect(native.listeners).toHaveLength(4); expect(native.activeNotices).toBe(1); expect(native.reads).toBe(1);
    expect(host.firstElementChild).toBe(shell); expect(host.querySelector(".screen")?.childElementCount).toBe(1);
    for (const screen of native.screens.filter((record) => !record.lease.isCurrent())) expect(await Effect.runPromise(screen.owner.snapshot)).toEqual({ _tag: "closed" });
  }
  expect(native.routes).toHaveLength(101); expect(native.maxSyncSubscribers).toBe(2);
  expect(await app.appOwner.run(appState.sync!.snapshot)).toEqual(confirmed);
  theme.value = "light"; theme.dispatchEvent(new Event("change")); expect(shell?.getAttribute("data-appearance")).toBe("light");
  await app.appOwner.dispose(); await app.appOwner.dispose(); expect(native.noticeReleases).toBe(1);
  theme.value = "dark"; theme.dispatchEvent(new Event("change")); expect(shell?.getAttribute("data-appearance")).toBe("light");
});

test("Nth and continuous refresh failures preserve the confirmed screen until a real authoritative read succeeds", async () => {
  const { ProtocolError } = await import("../../src/contracts/backend");
  native.read = (call) => call === 2 || call === 3 ? Effect.fail(new ProtocolError()) : Effect.succeed(reply("session-a", call));
  const app = await import("../../src/main"); dispose = app.appOwner.dispose; await mounted(1); await waitUntil(() => native.syncSubscribers === 2);
  const state = await app.appOwner.ready; const screen = native.screens[0]!;
  for (let call = 2; call <= 3; call++) {
    await expect(app.appOwner.run(state.sync!.resync)).rejects.toMatchObject({ _tag: "ProtocolError" });
    await waitUntil(() => host.querySelector<HTMLElement>('[role="alert"]')?.hidden === false);
    const snapshot = await app.appOwner.run(state.sync!.snapshot); expect(snapshot.phase).toBe("failed");
    expect(snapshot.coordinator._tag === "active" ? snapshot.coordinator.activeDraft?.revision : null).toBe(1);
    const model = await Effect.runPromise(screen.owner.snapshot); expect(model._tag === "open" ? model.context?.revision : null).toBe(1);
    expect(native.reads).toBe(call); expect(native.routes).toHaveLength(1); expect(native.activeReads).toBe(0); expect(native.activeNotices).toBe(1);
    expect(host.querySelector("h2")?.textContent).toBe("일반 추첨");
  }
  await app.appOwner.run(state.sync!.resync); await waitUntil(() => screen.updates.some((context) => context?.revision === 4));
  const model = await Effect.runPromise(screen.owner.snapshot); expect(model._tag === "open" ? model.context?.revision : null).toBe(4);
  await waitUntil(() => host.querySelector<HTMLElement>('[role="alert"]')?.hidden === true);
  expect(native.reads).toBe(4); expect(native.syncSubscribers).toBe(2);
});

test("actual main rejects late retired-session Bootstrap replies and updates only the current Create owner", async () => {
  const old = Deferred.makeUnsafe<BootstrapReply>(); const firstStarted = Deferred.makeUnsafe<void>();
  native.read = (call) => call === 1 ? Deferred.succeed(firstStarted, undefined).pipe(Effect.andThen(Deferred.await(old))) : Effect.succeed(reply("session-b", 2));
  const importing = import("../../src/main"); await Effect.runPromise(Deferred.await(firstStarted));
  expect(native.activeNotices).toBe(1); native.notice!(notice("session-b", 2));
  await Effect.runPromise(Deferred.succeed(old, reply("session-a", 1)));
  const app = await importing; dispose = app.appOwner.dispose; await mounted(1); await waitUntil(() => native.syncSubscribers === 2);
  expect(native.reads).toBe(2); const initial = native.screens[0]!;
  // A mount already has authoritative context before the first changes event;
  // the subscriber must not be required to repair an empty initial model.
  expect(initial.initial._tag === "open" ? initial.initial.context : null).toEqual({ backendSessionId: "session-b", draftId: "draft", revision: 2, articleGeneration: 2 });
  const initialModel = await Effect.runPromise(initial.owner.snapshot); expect(initialModel._tag === "open" ? initialModel.context?.backendSessionId : undefined).toBe("session-b");
  const state = await app.appOwner.ready;
  // A later authoritative session change retires B. A B response after C was
  // confirmed is discarded and requires another real Bootstrap query.
  native.read = () => Effect.succeed(reply("session-c", 3)); await app.appOwner.run(state.sync!.resync);
  await waitUntil(() => initial.updates.some((context) => context?.backendSessionId === "session-c"));
  const delayed = Deferred.makeUnsafe<BootstrapReply>(); const delayedStarted = Deferred.makeUnsafe<void>(); const next = native.reads + 1;
  native.read = (call) => call === next ? Deferred.succeed(delayedStarted, undefined).pipe(Effect.andThen(Deferred.await(delayed))) : Effect.succeed(reply("session-c", 4));
  window.dispatchEvent(new Event("focus")); await Effect.runPromise(Deferred.await(delayedStarted));
  setHash("#/unrecognized"); window.dispatchEvent(new Event("hashchange")); await mounted(2); await waitUntil(() => native.syncSubscribers === 1);
  const updatesWhenClosed = initial.updates.length;
  await Effect.runPromise(Deferred.succeed(delayed, reply("session-b", 999)));
  await waitUntil(() => native.reads === next + 1 && native.activeReads === 0);
  // Joining resync gives a completion barrier even if the focus-triggered run
  // is still publishing its final snapshot.
  await app.appOwner.run(state.sync!.resync);
  expect(host.querySelector("h2")?.textContent).toBe("화면을 찾을 수 없습니다"); expect(initial.updates).toHaveLength(updatesWhenClosed);
  expect(await Effect.runPromise(initial.owner.snapshot)).toEqual({ _tag: "closed" });
  setHash("#/create"); window.dispatchEvent(new Event("hashchange")); await mounted(3); await waitUntil(() => native.syncSubscribers === 2);
  const current = native.screens.at(-1)!; const model = await Effect.runPromise(current.owner.snapshot);
  expect(model._tag === "open" ? model.context : null).toEqual({ backendSessionId: "session-c", draftId: "draft", revision: 4, articleGeneration: 4 });
  expect(current.updates.every((context) => context?.backendSessionId !== "session-b")).toBe(true);
  const before = native.reads; native.notice!(notice("session-b", 1000)); await app.appOwner.run(Effect.yieldNow); expect(native.reads).toBe(before);
});

for (const failure of ["read", "subscription"] as const) test(`actual main ${failure} initialization failure retains a safe shell and releases partial Bootstrap resources`, async () => {
  const { ProtocolError } = await import("../../src/contracts/backend");
  native.subscribeFailure = failure === "subscription";
  if (failure === "read") native.read = () => Effect.fail(new ProtocolError());
  const app = await import("../../src/main"); dispose = app.appOwner.dispose; await mounted(1);
  expect(app.appOwner.phase()).toBe("running"); expect(native.activeNotices).toBe(0); expect(native.noticeReleases).toBe(1); expect(native.syncSubscribers).toBe(0);
  expect(native.reads).toBe(failure === "read" ? 1 : 0);
  expect(host.querySelector('[role="alert"]')?.textContent).toContain("앱 응답 형식");
  expect(await Effect.runPromise(native.screens[0]!.owner.snapshot)).toMatchObject({ _tag: "open", context: null, cache: [] });
  window.dispatchEvent(new Event("pagehide")); await app.appOwner.dispose(); expect(app.appOwner.phase()).toBe("stopped");
});

test("pagehide during delayed initialization interrupts the real startup read and releases listeners before completion", async () => {
  const started = Deferred.makeUnsafe<void>(); const pending = Deferred.makeUnsafe<BootstrapReply>();
  native.read = () => Deferred.succeed(started, undefined).pipe(Effect.andThen(Deferred.await(pending)));
  const importing = import("../../src/main"); await Effect.runPromise(Deferred.await(started));
  expect(native.activeReads).toBe(1); expect(native.activeNotices).toBe(1); expect(native.listeners).toHaveLength(4);
  window.dispatchEvent(new Event("pagehide"));
  const app = await importing; dispose = app.appOwner.dispose; await app.appOwner.dispose();
  expect(app.appOwner.phase()).toBe("stopped"); expect(native.routes).toHaveLength(0); expect(native.screens).toHaveLength(0);
  expect(native.cancelledReads).toBe(1); expect(native.noticeReleases).toBe(1);
  await Effect.runPromise(Deferred.succeed(pending, reply("session-late", 999)));
  expect(host.childElementCount).toBe(0); expect(native.activeNotices).toBe(0);
});

test("a real shell append failure closes the partially built DOM binding and never starts Bootstrap", async () => {
  const original = host.append.bind(host);
  vi.spyOn(host, "append").mockImplementation((...nodes) => { original(...nodes); throw new Error("injected append-after-side-effect failure"); });
  const app = await import("../../src/main"); dispose = app.appOwner.dispose;
  expect(app.appOwner.phase()).toBe("stopped"); expect(native.reads).toBe(0); expect(native.activeNotices).toBe(0); expect(native.routes).toHaveLength(0);
  expect(host.childElementCount).toBe(0); expect(native.listeners).toHaveLength(0);
});
