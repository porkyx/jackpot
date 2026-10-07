import { expect, it } from "@effect/vitest";
import { vi } from "vitest";
import { Cause, Deferred, Effect, Exit, Fiber, Scope } from "effect";
import { makeRouteManager, nextRouteGeneration, RouteUnavailable, type ScreenLease } from "../../src/app/routeManager";
import { makeDomPlatform } from "../../src/platform/dom";
import { applyEditorInput, createEditor, retainEditor, type EditorValues } from "../../src/screens/create/editorModel";

function signalFor<A>(signals: Map<number, Deferred.Deferred<A>>, key: number): Deferred.Deferred<A> {
  let signal = signals.get(key);
  if (signal === undefined) { signal = Deferred.makeUnsafe<A>(); signals.set(key, signal); }
  return signal;
}
function screenHarness() {
  const host = document.createElement("main");
  const events = new EventTarget();
  const frames = new Map<number, (time: number) => void>();
  const ready = new Map<number, Deferred.Deferred<void>>();
  const leases: ScreenLease[] = [];
  const scopes: Scope.Scope[] = [];
  const caches: Map<string, string>[] = [];
  const frameRequests = new Map<number, Deferred.Deferred<void>>();
  let nextFrameId = 0;
  const counters = { listeners: 0, queries: 0, debounces: 0, painted: 0, received: 0, submitted: 0, mounted: 0, closed: 0 };
  const dom = makeDomPlatform({ request: (callback) => { const id = nextFrameId++; frames.set(id, callback); Deferred.doneUnsafe(signalFor(frameRequests, id), Effect.void); return id; }, cancel: (id) => { frames.delete(id); } }, () => "visible");
  const mount = (lease: ScreenLease) => Effect.gen(function*() {
    const scope = yield* Scope.Scope;
    leases.push(lease); scopes.push(scope);
    const element = document.createElement("section");
    element.textContent = lease.route._tag;
    yield* Effect.acquireRelease(Effect.sync(() => { host.append(element); counters.mounted++; }), () => Effect.sync(() => { element.remove(); counters.closed++; }));
    yield* dom.listen(events, "click", () => { if (lease.isCurrent()) counters.received++; });
    yield* Effect.acquireRelease(Effect.sync(() => { counters.listeners++; }), () => Effect.sync(() => { counters.listeners--; }));
    const cache = new Map([["page-1", "display-only"]]); caches.push(cache);
    yield* Scope.addFinalizer(scope, Effect.sync(() => { cache.clear(); }));
    const queryStarted = yield* Deferred.make<void>();
    const timerStarted = yield* Deferred.make<void>();
    yield* Effect.gen(function*() {
      counters.queries++;
      yield* Deferred.succeed(queryStarted, undefined);
      yield* Effect.never;
    }).pipe(Effect.ensuring(Effect.sync(() => { counters.queries--; })), Effect.forkIn(scope));
    yield* Effect.gen(function*() {
      counters.debounces++;
      yield* Deferred.succeed(timerStarted, undefined);
      yield* Effect.sleep("150 millis");
      if (lease.isCurrent()) counters.submitted++;
    }).pipe(Effect.ensuring(Effect.sync(() => { counters.debounces--; })), Effect.forkIn(scope));
    const frame = dom.nextFrame.pipe(Effect.tap(() => Effect.sync(() => { if (lease.isCurrent()) counters.painted++; })));
    const requestId = nextFrameId;
    yield* frame.pipe(Effect.forkIn(scope));
    yield* Deferred.await(queryStarted);
    yield* Deferred.await(timerStarted);
    yield* Deferred.await(signalFor(frameRequests, requestId));
    yield* Deferred.succeed(signalFor(ready, lease.generation), undefined);
  });
  return { mount, host, events, frames, counters, leases, scopes, caches, wait: (generation: number) => Deferred.await(signalFor(ready, generation)) };
}
const noFailure = (route: ScreenLease["route"], cause: Cause.Cause<unknown>) => Effect.die(new Error(`${route._tag}: ${Cause.pretty(cause)}`));

it.effect("100 screen replacements release their DOM, listeners, debounce, frame, query and page cache while preserving raw editor", () => Effect.scoped(Effect.gen(function*() {
  const harness = screenHarness();
  const manager = yield* makeRouteManager({ mount: harness.mount, onFailure: noFailure });
  const identity = { backendSessionId: "session", draftId: "draft" };
  const values: EditorValues = { url: "", drawMode: "immediate", prizeMode: "single", filters: { excludeAnonymous: false, excludeAuthor: false, excludeDcconOnly: false, timeCutEnabled: false, timeCut: "", includeKeywords: "", excludeKeywords: "" }, single: { id: "single", name: "", count: "1" }, multiple: [{ id: "many", name: "상품", count: "1" }] };
  const editor = applyEditorInput(createEditor(identity, values), { _tag: "SingleCountChanged", raw: "-" });
  for (let visit = 1; visit <= 100; visit++) {
    const oldFrame = [...harness.frames.values()][0];
    yield* manager.navigate(visit % 2 === 1 ? "#/create" : "#/unrecognized");
    yield* harness.wait(visit);
    oldFrame?.(16);
    expect(harness.host.childNodes.length).toBe(1);
    expect(harness.frames.size).toBe(1);
    expect(harness.counters.listeners).toBe(1);
    expect(harness.counters.queries).toBe(1);
    expect(harness.counters.debounces).toBe(1);
    expect(harness.counters.painted).toBe(0);
    expect(harness.counters.submitted).toBe(0);
    expect(retainEditor(editor, identity)).toBe(editor);
    expect(editor.single.count.raw).toBe("-");
    expect(editor.single.count.inputVersion).toBe(1);
    expect(editor.single.count.dirty).toBe(true);
    expect(harness.caches.slice(0, -1).every((cache) => cache.size === 0)).toBe(true);
    if (visit > 1) expect(harness.leases[visit - 2]!.isCurrent()).toBe(false);
    harness.events.dispatchEvent(new Event("click"));
    expect(harness.counters.received).toBe(visit);
  }
  yield* manager.close;
  yield* manager.close;
  harness.events.dispatchEvent(new Event("click"));
  expect(harness.counters).toEqual({ listeners: 0, queries: 0, debounces: 0, painted: 0, received: 100, submitted: 0, mounted: 100, closed: 100 });
  expect(harness.frames.size).toBe(0);
  expect(harness.host.childNodes.length).toBe(0);
  expect(harness.scopes.every((scope) => scope.state._tag === "Closed")).toBe(true);
  expect(harness.leases.every((lease) => !lease.isCurrent())).toBe(true);
  expect(harness.caches.every((cache) => cache.size === 0)).toBe(true);
})));

it.effect("a successor waits for every old resource finalizer before mounting", () => Effect.scoped(Effect.gen(function*() {
  const closing = yield* Deferred.make<void>();
  const permitClose = yield* Deferred.make<void>();
  const mounted = new Map<number, Deferred.Deferred<void>>();
  const order: string[] = [];
  const manager = yield* makeRouteManager({ mount: (lease) => Effect.gen(function*() {
    order.push(`open-${lease.generation}`);
    yield* Effect.acquireRelease(Effect.void, () => Effect.gen(function*() {
      order.push(`close-${lease.generation}`);
      if (lease.generation === 1) { yield* Deferred.succeed(closing, undefined); yield* Deferred.await(permitClose); }
      order.push(`closed-${lease.generation}`);
    }));
    yield* Deferred.succeed(signalFor(mounted, lease.generation), undefined);
  }), onFailure: noFailure });
  yield* manager.navigate("#/create");
  yield* Deferred.await(signalFor(mounted, 1));
  const navigation = yield* manager.navigate("#/unrecognized").pipe(Effect.forkChild);
  yield* Deferred.await(closing);
  expect(order).toEqual(["open-1", "close-1"]);
  yield* Deferred.succeed(permitClose, undefined);
  yield* Fiber.join(navigation);
  yield* Deferred.await(signalFor(mounted, 2));
  expect(order).toEqual(["open-1", "close-1", "closed-1", "open-2"]);
  yield* manager.close;
})));

it.effect("concurrent hashes during cleanup mount only the latest request and invalidate callbacks immediately", () => Effect.scoped(Effect.gen(function*() {
  const closing = yield* Deferred.make<void>();
  const permitClose = yield* Deferred.make<void>();
  const mounted = new Map<number, Deferred.Deferred<void>>();
  const leases: ScreenLease[] = [];
  const manager = yield* makeRouteManager({ mount: (lease) => Effect.gen(function*() {
    leases.push(lease);
    yield* Effect.acquireRelease(Effect.void, () => lease.generation === 1 ? Deferred.succeed(closing, undefined).pipe(Effect.andThen(Deferred.await(permitClose))) : Effect.void);
    yield* Deferred.succeed(signalFor(mounted, lease.generation), undefined);
  }), onFailure: noFailure });
  yield* manager.navigate("#/create");
  yield* Deferred.await(signalFor(mounted, 1));
  const second = yield* manager.navigate("#/unrecognized").pipe(Effect.forkChild);
  yield* Deferred.await(closing);
  expect(leases[0]!.isCurrent()).toBe(false);
  const latestStarted = yield* Deferred.make<void>();
  const latest = yield* Deferred.succeed(latestStarted, undefined).pipe(Effect.andThen(manager.navigate("#/results/new")), Effect.forkChild);
  yield* Deferred.await(latestStarted);
  yield* Effect.yieldNow;
  yield* Deferred.succeed(permitClose, undefined);
  yield* Fiber.join(second);
  yield* Fiber.join(latest);
  yield* Deferred.await(signalFor(mounted, 3));
  expect(leases.map((lease) => lease.route._tag)).toEqual(["create", "result"]);
  expect(leases[1]!.route).toEqual({ _tag: "result", collectionId: "new", roundId: null });
  expect(leases[1]!.isCurrent()).toBe(true);
  yield* manager.close;
})));

it.effect("replacing a screen interrupts its incomplete initializer and cleans partially acquired resources", () => Effect.scoped(Effect.gen(function*() {
  const entered = yield* Deferred.make<void>();
  const next = yield* Deferred.make<void>();
  let active = 0;
  let interrupted = 0;
  let errors = 0;
  let oldLease: ScreenLease | undefined;
  const manager = yield* makeRouteManager({ mount: (lease) => Effect.gen(function*() {
    yield* Effect.acquireRelease(Effect.sync(() => { active++; }), () => Effect.sync(() => { active--; }));
    if (lease.generation === 1) {
      oldLease = lease;
      yield* Deferred.succeed(entered, undefined).pipe(Effect.andThen(Effect.never), Effect.ensuring(Effect.sync(() => { interrupted++; })));
    } else yield* Deferred.succeed(next, undefined);
  }), onFailure: () => Effect.sync(() => { errors++; }) });
  yield* manager.navigate("#/create");
  yield* Deferred.await(entered);
  expect(active).toBe(1);
  yield* manager.navigate("#/unrecognized");
  yield* Deferred.await(next);
  expect([active, interrupted, errors]).toEqual([1, 1, 0]);
  expect(oldLease?.isCurrent()).toBe(false);
  yield* manager.close;
  expect(active).toBe(0);
})));

for (const failureAt of [1, 2, 3, "always"] as const) {
  it.effect(`first/Nth/continuous mount failure ${failureAt} releases partial resources and can navigate again`, () => Effect.scoped(Effect.gen(function*() {
    const settled = new Map<number, Deferred.Deferred<void>>();
    let active = 0;
    const failures: number[] = [];
    const leases: ScreenLease[] = [];
    const manager = yield* makeRouteManager({ mount: (lease) => Effect.gen(function*() {
      leases.push(lease);
      yield* Effect.acquireRelease(Effect.sync(() => { active++; }), () => Effect.sync(() => { active--; }));
      if (failureAt === "always" || lease.generation === failureAt) return yield* Effect.fail(new Error(`mount-${lease.generation}`));
      yield* Deferred.succeed(signalFor(settled, lease.generation), undefined);
    }), onFailure: (_route, cause) => Effect.sync(() => {
      expect(Cause.pretty(cause)).toContain("mount-");
      expect(active).toBe(0);
      const generation = leases.at(-1)!.generation;
      failures.push(generation);
      Deferred.doneUnsafe(signalFor(settled, generation), Effect.void);
    }) });
    for (let request = 1; request <= 3; request++) {
      yield* manager.navigate(request % 2 ? "#/create" : "#/unrecognized");
      yield* Deferred.await(signalFor(settled, request));
      expect(active).toBe(failureAt === "always" || request === failureAt ? 0 : 1);
      expect(leases.at(-1)!.isCurrent()).toBe(!(failureAt === "always" || request === failureAt));
    }
    expect(failures).toEqual(failureAt === "always" ? [1, 2, 3] : [failureAt]);
    yield* manager.close;
    expect(active).toBe(0);
  })));
}

it.effect("cleanup defects after mount failure are reported together and remaining finalizers still run", () => Effect.scoped(Effect.gen(function*() {
  const reported = yield* Deferred.make<void>();
  let released = 0;
  let message = "";
  const manager = yield* makeRouteManager({ mount: () => Effect.gen(function*() {
    yield* Effect.acquireRelease(Effect.void, () => Effect.sync(() => { released++; }));
    yield* Effect.acquireRelease(Effect.void, () => Effect.die(new Error("cleanup-defect")));
    return yield* Effect.fail(new Error("mount-failure"));
  }), onFailure: (_route, cause) => Effect.sync(() => { message = Cause.pretty(cause); Deferred.doneUnsafe(reported, Effect.void); }) });
  yield* manager.navigate("#/create");
  yield* Deferred.await(reported);
  expect(released).toBe(1);
  expect(message).toContain("cleanup-defect");
  expect(message).toContain("mount-failure");
  yield* manager.close;
})));

it.effect("pure mount interruption releases resources without being labeled a screen failure", () => Effect.scoped(Effect.gen(function*() {
  const released = yield* Deferred.make<void>();
  let reported = 0;
  const manager = yield* makeRouteManager({ mount: () => Effect.gen(function*() {
    yield* Effect.acquireRelease(Effect.void, () => Deferred.succeed(released, undefined));
    yield* Effect.interrupt;
  }), onFailure: () => Effect.sync(() => { reported++; }) });
  yield* manager.navigate("#/create");
  yield* Deferred.await(released);
  yield* Effect.yieldNow;
  expect(reported).toBe(0);
  yield* manager.close;
  expect(reported).toBe(0);
})));

it.effect("replacement cleanup failure remains observable, releases other resources, and allows a later explicit route request", () => Effect.scoped(Effect.gen(function*() {
  const mounted = new Map<number, Deferred.Deferred<void>>();
  const order: string[] = [];
  const manager = yield* makeRouteManager({ mount: (lease) => Effect.gen(function*() {
    order.push(`open-${lease.generation}`);
    yield* Effect.acquireRelease(Effect.void, () => Effect.sync(() => { order.push(`release-${lease.generation}`); }));
    if (lease.generation === 1) yield* Effect.acquireRelease(Effect.void, () => Effect.die(new Error("replacement-cleanup")));
    yield* Deferred.succeed(signalFor(mounted, lease.generation), undefined);
  }), onFailure: noFailure });
  yield* manager.navigate("#/create");
  yield* Deferred.await(signalFor(mounted, 1));
  const failed = yield* Effect.exit(manager.navigate("#/unrecognized"));
  expect(failed._tag).toBe("Failure");
  if (failed._tag === "Failure") expect(Cause.pretty(failed.cause)).toContain("replacement-cleanup");
  expect(order).toEqual(["open-1", "release-1"]);
  yield* manager.navigate("#/unrecognized");
  yield* Deferred.await(signalFor(mounted, 3));
  expect(order).toEqual(["open-1", "release-1", "open-3"]);
  yield* manager.close;
  expect(order).toEqual(["open-1", "release-1", "open-3", "release-3"]);
})));

it.effect("closing during pending screen cleanup stops admission and prevents successor mount", () => Effect.scoped(Effect.gen(function*() {
  const closing = yield* Deferred.make<void>();
  const permitClose = yield* Deferred.make<void>();
  const mounted = yield* Deferred.make<void>();
  let count = 0;
  const manager = yield* makeRouteManager({ mount: () => Effect.gen(function*() {
    count++;
    yield* Effect.acquireRelease(Effect.void, () => Deferred.succeed(closing, undefined).pipe(Effect.andThen(Deferred.await(permitClose))));
    yield* Deferred.succeed(mounted, undefined);
  }), onFailure: noFailure });
  yield* manager.navigate("#/create");
  yield* Deferred.await(mounted);
  const navigation = yield* manager.navigate("#/unrecognized").pipe(Effect.forkChild);
  yield* Deferred.await(closing);
  const disposal = yield* manager.close.pipe(Effect.forkChild);
  yield* Effect.yieldNow;
  const rejected = yield* Effect.result(manager.navigate("#/results/late"));
  expect(rejected._tag).toBe("Failure");
  if (rejected._tag === "Failure") expect(rejected.failure).toEqual(new RouteUnavailable({ reason: "closed" }));
  yield* Deferred.succeed(permitClose, undefined);
  yield* Fiber.join(navigation);
  yield* Fiber.join(disposal);
  expect(count).toBe(1);
})));

it.effect("parent scope shutdown closes a live screen and rejects subsequent navigation idempotently", () => Effect.gen(function*() {
  const parent = yield* Scope.make();
  const harness = screenHarness();
  const manager = yield* makeRouteManager({ mount: harness.mount, onFailure: noFailure }).pipe(Scope.provide(parent));
  yield* manager.navigate("#/create");
  yield* harness.wait(1);
  yield* Scope.close(parent, Exit.void);
  yield* Scope.close(parent, Exit.void);
  yield* manager.close;
  expect(harness.host.childNodes.length).toBe(0);
  expect(harness.frames.size).toBe(0);
  expect(harness.counters.closed).toBe(1);
  const rejected = yield* Effect.result(manager.navigate("#/unrecognized"));
  expect(rejected._tag).toBe("Failure");
  if (rejected._tag === "Failure") expect(rejected.failure.reason).toBe("closed");
}).pipe(Effect.scoped));

it.effect("a manager cannot be allocated below an already closed parent", () => Effect.gen(function*() {
  const parent = yield* Scope.make();
  yield* Scope.close(parent, Exit.void);
  const result = yield* Effect.result(makeRouteManager({ mount: () => Effect.void, onFailure: noFailure }).pipe(Scope.provide(parent)));
  expect(result._tag).toBe("Failure");
  if (result._tag === "Failure") expect(result.failure).toEqual(new RouteUnavailable({ reason: "closed" }));
}));

it.effect("accepted app-scope work survives route change; late completion neither cancels work nor forces navigation", () => Effect.scoped(Effect.gen(function*() {
  const app = yield* Scope.Scope;
  const completion = yield* Deferred.make<void>();
  const started = yield* Deferred.make<void>();
  const finished = yield* Deferred.make<void>();
  const harness = screenHarness();
  const manager = yield* makeRouteManager({ mount: harness.mount, onFailure: noFailure });
  let executions = 0;
  let cancelled = 0;
  const accepted = yield* Effect.gen(function*() {
    executions++;
    yield* Deferred.succeed(started, undefined);
    yield* Deferred.await(completion);
    yield* Deferred.succeed(finished, undefined);
  }).pipe(Effect.onExit((exit) => Exit.isFailure(exit) ? Effect.sync(() => { cancelled++; }) : Effect.void), Effect.forkIn(app));
  yield* Deferred.await(started);
  yield* manager.navigate("#/create");
  yield* harness.wait(1);
  yield* manager.navigate("#/unrecognized");
  yield* harness.wait(2);
  yield* Deferred.succeed(completion, undefined);
  yield* Deferred.await(finished);
  yield* Fiber.join(accepted);
  expect([executions, cancelled]).toEqual([1, 0]);
  expect(harness.leases.at(-1)!.route).toEqual({ _tag: "not_found", hash: "#/unrecognized" });
  expect(harness.host.textContent).toBe("not_found");
  expect(harness.counters.submitted).toBe(0);
  yield* manager.close;
})));

it.effect("unknown route is a screen lease rather than an inferred command or external redirect", () => Effect.scoped(Effect.gen(function*() {
  const mounted = yield* Deferred.make<ScreenLease>();
  const manager = yield* makeRouteManager({ mount: (lease) => Deferred.succeed(mounted, lease).pipe(Effect.asVoid), onFailure: noFailure });
  yield* manager.navigate("https://outside.test/#/create");
  const lease = yield* Deferred.await(mounted);
  expect(lease.route).toEqual({ _tag: "not_found", hash: "https://outside.test/#/create" });
  expect(Object.isFrozen(lease)).toBe(true);
  expect(lease.isCurrent()).toBe(true);
  yield* manager.close;
  expect(lease.isCurrent()).toBe(false);
})));

it.effect("generation boundary failure preserves the last screen and remains a typed failure", () => Effect.scoped(Effect.gen(function*() {
  const harness = screenHarness();
  const manager = yield* makeRouteManager({ mount: harness.mount, onFailure: noFailure });
  yield* manager.navigate("#/create");
  yield* harness.wait(1);
  const check = vi.spyOn(Number, "isSafeInteger").mockReturnValue(false);
  const result = yield* Effect.result(manager.navigate("#/unrecognized")).pipe(Effect.ensuring(Effect.sync(() => { check.mockRestore(); })));
  expect(result._tag).toBe("Failure");
  if (result._tag === "Failure") expect(result.failure.reason).toBe("generation_exhausted");
  expect(harness.leases.length).toBe(1);
  expect(harness.leases[0]!.isCurrent()).toBe(true);
  yield* manager.close;
})));

for (const value of [-1, NaN, Infinity, 0.5, Number.MAX_SAFE_INTEGER]) {
  it(`route generation ${value} fails before a nonmonotonic or unsafe lease can be created`, () => {
    expect(() => nextRouteGeneration(value)).toThrow(RouteUnavailable);
  });
}
it("route generation begins at one and reaches the last safe integer exactly", () => {
  expect(nextRouteGeneration(0)).toBe(1);
  expect(nextRouteGeneration(Number.MAX_SAFE_INTEGER - 1)).toBe(Number.MAX_SAFE_INTEGER);
});
