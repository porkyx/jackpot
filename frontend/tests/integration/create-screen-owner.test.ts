import { expect, it } from "@effect/vitest";
import { Cause, Deferred, Effect, Exit, Fiber, Scope, Stream } from "effect";
import { TestClock } from "effect/testing";
import { mountCreateScreenModel, ScreenUnavailable, type CreateScreenOwner } from "../../src/screens/create/screenOwner";
import type { CreateScreenModel, ParticipantPage, ParticipantRequest } from "../../src/screens/create/screenModel";

const context = { backendSessionId: "session", draftId: "draft", revision: 1, articleGeneration: 1 };
const search = { group: "unclassified", search: "", offset: 0 } as const;
function data(request: ParticipantRequest): ParticipantPage {
  return { context: { backendSessionId: request.backendSessionId, draftId: request.draftId, revision: request.revision, articleGeneration: request.articleGeneration }, group: request.group, search: request.search, offset: request.offset,
    rows: [{ participantId: "p-1", displayName: "표시", group: request.group }], groupCount: 1000, matchCount: 1000 };
}
function readPort() {
  let current = true;
  const calls = new Map<number, { request: ParticipantRequest; complete: (effect: Effect.Effect<ParticipantPage, Error>) => void }>();
  const started = new Map<number, Deferred.Deferred<void>>();
  const state = { active: 0, cancelled: 0, total: 0 };
  const signal = (id: number) => { let deferred = started.get(id); if (deferred === undefined) { deferred = Deferred.makeUnsafe<void>(); started.set(id, deferred); } return deferred; };
  return { calls, state, lease: { route: { _tag: "create" } as const, generation: 1, isCurrent: () => current }, invalidate: () => { current = false; },
    query: (request: ParticipantRequest) => Effect.callback<ParticipantPage, Error>((complete) => {
      let released = false;
      const release = () => { if (!released) { released = true; state.active--; state.cancelled++; } };
      state.active++; state.total++; calls.set(request.queryGeneration, { request, complete: (effect) => { release(); complete(effect); } }); Deferred.doneUnsafe(signal(request.queryGeneration), Effect.void);
      return Effect.sync(release);
    }), messageKey: (_cause: Cause.Cause<Error>) => "TransportError" as const,
    wait: (id: number) => Deferred.await(signal(id)).pipe(Effect.tap(() => Effect.sync(() => { started.delete(id); }))),
    succeed: (id: number) => { const call = calls.get(id); if (call === undefined) throw new Error("missing request"); call.complete(Effect.succeed(data(call.request))); },
    fail: (id: number) => { const call = calls.get(id); if (call === undefined) throw new Error("missing request"); call.complete(Effect.fail(new Error("read failed"))); },
  };
}
function waitFor(owner: CreateScreenOwner<never>, predicate: (model: CreateScreenModel) => boolean) { return Stream.runHead(owner.changes.pipe(Stream.filter(predicate))); }

it.effect("decoded delayed read updates only the current mount and cached page uses no second transport", () => Effect.scoped(Effect.gen(function*() {
  const port = readPort(); const owner = yield* mountCreateScreenModel(port.lease, context, port);
  yield* owner.query(search); yield* port.wait(1);
  expect(port.state.active).toBe(1); expect((yield* owner.snapshot)._tag).toBe("open");
  port.succeed(1); yield* waitFor(owner, (model) => model._tag === "open" && model.query._tag === "ready");
  yield* owner.query(search);
  expect(port.state.total).toBe(1);
  const model = yield* owner.snapshot;
  expect(model._tag === "open" && model.cache.length).toBe(1);
  yield* owner.close; yield* owner.close;
  expect(yield* owner.snapshot).toEqual({ _tag: "closed" }); expect(port.state.active).toBe(0);
})));
it.effect("new search cancels the old callback resource and ignores its late completion", () => Effect.scoped(Effect.gen(function*() {
  const port = readPort(); const owner = yield* mountCreateScreenModel(port.lease, context, port);
  yield* owner.query(search); yield* port.wait(1);
  yield* owner.query({ ...search, search: "latest" }); yield* port.wait(2);
  expect(port.state.active).toBe(1); expect(port.state.cancelled).toBe(1);
  port.succeed(1); port.succeed(2);
  yield* waitFor(owner, (model) => model._tag === "open" && model.query._tag === "ready");
  const model = yield* owner.snapshot;
  expect(model._tag === "open" && model.visiblePage?.search).toBe("latest");
  yield* owner.close; expect(port.state.active).toBe(0);
})));
it.effect("same context preserves pending query; authoritative revision/session/context removal interrupts and drops cache", () => Effect.scoped(Effect.gen(function*() {
  const port = readPort(); const owner = yield* mountCreateScreenModel(port.lease, context, port);
  yield* owner.query(search); yield* port.wait(1);
  yield* owner.updateContext(context); expect(port.state.active).toBe(1);
  yield* owner.updateContext({ ...context, revision: 2 }); expect(port.state.active).toBe(0);
  port.succeed(1);
  yield* owner.query(search); yield* port.wait(3); port.succeed(3);
  yield* waitFor(owner, (model) => model._tag === "open" && model.query._tag === "ready");
  yield* owner.updateContext({ ...context, backendSessionId: "new-session" });
  let model = yield* owner.snapshot; expect(model._tag === "open" && model.cache).toEqual([]);
  yield* owner.updateContext(null); model = yield* owner.snapshot; expect(model._tag === "open" && model.context).toBeNull();
  const result = yield* Effect.result(owner.query(search)); expect(result._tag).toBe("Failure");
  if (result._tag === "Failure") expect(result.failure.reason).toBe("no_draft");
  yield* owner.close;
})));
for (const failing of [1, 2, "always"] as const) it.effect(`first/Nth/continuous delayed read failure ${failing} preserves a visible error and has no retries`, () => Effect.scoped(Effect.gen(function*() {
  const port = readPort(); const owner = yield* mountCreateScreenModel(port.lease, context, port);
  for (let id = 1; id <= 3; id++) {
    yield* owner.query({ ...search, offset: id * 100 }); yield* port.wait(id);
    const failed = failing === "always" || id === failing;
    if (failed) port.fail(id); else port.succeed(id);
    yield* waitFor(owner, (model) => model._tag === "open" && model.query._tag === (failed ? "failed" : "ready"));
    const model = yield* owner.snapshot;
    if (model._tag !== "open") throw new Error("screen closed before assertion");
    expect(model.query._tag).toBe(failed ? "failed" : "ready");
    expect(port.state.total).toBe(id);
  }
  yield* owner.close; expect(port.state.active).toBe(0);
})));
it.effect("port timeout is an error and later screen shutdown cancels without replacing it with ready", () => Effect.scoped(Effect.gen(function*() {
  const port = readPort();
  const owner = yield* mountCreateScreenModel(port.lease, context, { ...port, query: (request) => port.query(request).pipe(Effect.timeout("15 seconds"), Effect.catch(() => Effect.fail(new Error("timeout")))) });
  yield* owner.query(search); yield* port.wait(1); yield* TestClock.adjust("15 seconds");
  yield* waitFor(owner, (model) => model._tag === "open" && model.query._tag === "failed");
  expect(port.state.active).toBe(0); expect(port.state.total).toBe(1);
  yield* owner.close; expect(yield* owner.snapshot).toEqual({ _tag: "closed" });
})));
it.effect("stale route denies further work and a completion cannot update the last model", () => Effect.scoped(Effect.gen(function*() {
  const port = readPort(); const owner = yield* mountCreateScreenModel(port.lease, context, port);
  yield* owner.query(search); yield* port.wait(1); const before = yield* owner.snapshot;
  port.invalidate(); port.succeed(1); yield* Effect.yieldNow;
  expect(yield* owner.snapshot).toBe(before);
  const query = yield* Effect.result(owner.query(search)); const update = yield* Effect.result(owner.updateContext(context));
  expect(query._tag).toBe("Failure"); expect(update._tag).toBe("Failure");
  yield* owner.close; expect(port.state.active).toBe(0);
})));
it.effect("a stale failure is discarded without overwriting the old screen or logging a raw error", () => Effect.scoped(Effect.gen(function*() {
  const port = readPort(); const owner = yield* mountCreateScreenModel(port.lease, context, port);
  yield* owner.query(search); yield* port.wait(1); const before = yield* owner.snapshot;
  port.invalidate(); port.fail(1); yield* Effect.yieldNow;
  expect(yield* owner.snapshot).toBe(before); expect(JSON.stringify(yield* owner.snapshot)).not.toContain("read failed");
  yield* owner.close;
})));
it.effect("malformed page is caught as a protocol/read failure and does not enter cache", () => Effect.scoped(Effect.gen(function*() {
  const port = readPort(); const owner = yield* mountCreateScreenModel(port.lease, context, { ...port, messageKey: () => "ProtocolError" as const });
  yield* owner.query(search); yield* port.wait(1);
  const call = port.calls.get(1)!; call.complete(Effect.succeed({ ...data(call.request), groupCount: -1 }));
  yield* waitFor(owner, (model) => model._tag === "open" && model.query._tag === "failed");
  const model = yield* owner.snapshot; expect(model._tag === "open" && model.cache).toEqual([]);
  expect(model._tag === "open" && model.query).toEqual({ _tag: "failed", messageKey: "ProtocolError" });
  yield* owner.close;
})));
it.effect("100 mount/query/unmount cycles cancel every query and keep no cache or prior mount state", () => Effect.scoped(Effect.gen(function*() {
  const port = readPort();
  for (let index = 0; index < 100; index++) {
    const owner = yield* mountCreateScreenModel({ ...port.lease, generation: index + 1 }, context, port);
    yield* owner.query(search); yield* port.wait(1); yield* Effect.yieldNow;
    expect(port.state.active).toBe(1);
    yield* owner.close; yield* owner.close;
    expect(port.state.active).toBe(0); expect(yield* owner.snapshot).toEqual({ _tag: "closed" });
    const denied = yield* Effect.result(owner.query(search)); expect(denied._tag).toBe("Failure");
  }
  expect(port.state.total).toBe(100); expect(port.state.cancelled).toBe(100);
})));
it.effect("missing live query is explicit unavailable and cannot manufacture a successful page", () => Effect.scoped(Effect.gen(function*() {
  const port = readPort(); const owner = yield* mountCreateScreenModel(port.lease, context, null);
  const result = yield* Effect.result(owner.query(search)); expect(result._tag).toBe("Failure");
  if (result._tag === "Failure") expect(result.failure).toEqual(new ScreenUnavailable({ reason: "query_unavailable" }));
  expect((yield* owner.snapshot)._tag).toBe("open"); yield* owner.close;
})));
it.effect("closed parent and stale route deny allocation before acquiring an observable model", () => Effect.gen(function*() {
  const port = readPort(); const scope = yield* Scope.make(); yield* Scope.close(scope, Exit.void);
  const closed = yield* Effect.result(mountCreateScreenModel(port.lease, context, port).pipe(Scope.provide(scope))); expect(closed._tag).toBe("Failure");
  port.invalidate(); const stale = yield* Effect.result(mountCreateScreenModel(port.lease, context, port).pipe(Effect.scoped)); expect(stale._tag).toBe("Failure");
}));

it.effect("a pure interrupted query is discarded without inventing a protocol error", () => Effect.scoped(Effect.gen(function*() {
  const port = readPort(); const interrupted = yield* Deferred.make<void>();
  const owner = yield* mountCreateScreenModel(port.lease, context, { ...port, query: () => Effect.interrupt.pipe(Effect.ensuring(Deferred.succeed(interrupted, undefined))) });
  yield* owner.query(search); yield* Deferred.await(interrupted); yield* Effect.yieldNow;
  const model = yield* owner.snapshot; expect(model._tag === "open" && model.query._tag).toBe("loading");
  yield* owner.close;
})));

it.effect("allocation failure does not leave an orphan child scope on an open parent", () => Effect.scoped(Effect.gen(function*() {
  const parent = yield* Scope.Scope; const before = parent.state._tag;
  const port = readPort();
  const result = yield* Effect.exit(mountCreateScreenModel(port.lease, { ...context, draftId: "" }, port));
  expect(result._tag).toBe("Failure"); expect(parent.state._tag).toBe(before); expect(port.state.active).toBe(0);
})));

it.effect("scope close blocks new reads immediately while a query resource is still finalizing", () => Effect.scoped(Effect.gen(function*() {
  const port = readPort(); const closing = yield* Deferred.make<void>(); const release = yield* Deferred.make<void>(); const entered = yield* Deferred.make<void>();
  const owner = yield* mountCreateScreenModel(port.lease, context, { ...port, query: (_request) => Effect.acquireRelease(Deferred.succeed(entered, undefined), () => Deferred.succeed(closing, undefined).pipe(Effect.andThen(Deferred.await(release)))).pipe(Effect.andThen(Effect.never), Effect.scoped) });
  yield* owner.query(search); yield* Deferred.await(entered);
  const close = yield* owner.close.pipe(Effect.forkChild); yield* Deferred.await(closing);
  const readStarted = yield* Deferred.make<void>();
  const denied = yield* Deferred.succeed(readStarted, undefined).pipe(Effect.andThen(Effect.result(owner.query({ ...search, offset: 100 }))), Effect.forkChild);
  yield* Deferred.await(readStarted); yield* Effect.yieldNow;
  yield* Deferred.succeed(release, undefined); yield* Fiber.join(close);
  const result = yield* Fiber.join(denied); expect(result._tag).toBe("Failure");
  if (result._tag === "Failure") expect(result.failure.reason).toBe("closed");
})));
