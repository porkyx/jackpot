import { expect, it } from "@effect/vitest";
import { vi } from "vitest";
import { Cause, Deferred, Effect, Exit, Fiber, Schema, Scope, Stream, SubscriptionRef } from "effect";
import { TestClock } from "effect/testing";
import { BackendRejected, ProtocolError, TransportError, type BootstrapReply, type OperationLookupReply, type PendingOperationsReply } from "../../src/contracts/backend";
import { BootstrapData, OperationObservation, PendingOperationsPage, type OperationDescriptor } from "../../src/contracts/schemas";
import type { Received } from "../../src/operations/coordinator";
import { makePendingRecovery, RecoveryUnavailable, type RecoverySource } from "../../src/operations/pendingRecovery";
const schemaDecoders = vi.hoisted(() => new Map<unknown, ReturnType<typeof vi.fn>>());

vi.mock("effect", async (importOriginal) => {
  const actual = await importOriginal<typeof import("effect")>();
  return { ...actual,
    SubscriptionRef: { ...actual.SubscriptionRef, update: vi.fn(actual.SubscriptionRef.update) },
    Schema: { ...actual.Schema, decodeUnknownSync: vi.fn((...args: Parameters<typeof actual.Schema.decodeUnknownSync>) => {
      const decoder = vi.fn(actual.Schema.decodeUnknownSync(...args));
      schemaDecoders.set(args[0], decoder);
      return decoder;
    }) },
  };
});

type Descriptor = typeof OperationDescriptor.Type;
type Observation = typeof OperationObservation.Type;
const descriptor = (id: string, revision = 1): Descriptor => ({ operationId: id, kind: "CreateCollection", collectionId: `c-${id}`, roundId: `r-${id}`, status: "pending", revision });
const receive = <A>(reply: A): Received<A> => ({ reply, receivedAtMillis: 100 });
const header = (backendSessionId = "s1") => ({ protocolVersion: 1 as const, backendSessionId, occurredAt: "2026-10-06T00:00:00Z" });
const boot = (operations: ReadonlyArray<Descriptor> = [], cursor: string | null = null, session = "s1"): Received<BootstrapReply> => receive({ ...header(session), data: {
  backendNow: "2026-10-06T00:00:00Z", theme: "system", activeDraft: null, pendingOperations: operations, pendingCursor: cursor, recentResults: [],
} });
const page = (operations: ReadonlyArray<Descriptor> = [], cursor: string | null = null, session = "s1"): Received<PendingOperationsReply> => receive({ ...header(session), data: { operations, cursor } });
function observation(id: string, state: Observation["state"] = "pending", revision = 1): Observation {
  return { operationId: id, state, kind: state === "unknown" ? null : "CreateCollection", collectionId: state === "unknown" ? null : `c-${id}`, roundId: state === "unknown" ? null : `r-${id}`, revision: state === "unknown" ? null : revision, failureCode: state === "failed" ? "StorageUnavailable" : null };
}
const lookup = (id: string, state: Observation["state"] = "pending", revision = 1, session = "s1"): Received<OperationLookupReply> => receive({ ...header(session), data: observation(id, state, revision) });
function harness(count = 0, statuses: ReadonlyMap<string, Observation["state"]> = new Map()) {
  const ids = Array.from({ length: count }, (_, index) => `${index + 1}`);
  const trace = { bootstrap: 0, queries: [] as string[], pages: [] as { cursor: string | null; limit: number }[], active: 0, cancelled: 0 };
  const source: RecoverySource = {
    bootstrap: () => Effect.sync(() => { trace.bootstrap++; return boot(ids.slice(0, 64).map((id) => descriptor(id)), count > 64 ? "64" : null); }),
    listPendingOperations: (request) => Effect.sync(() => {
      trace.pages.push({ ...request });
      const offset = Number(request.cursor ?? "0");
      const end = Math.min(offset + request.limit, count);
      return page(ids.slice(offset, end).map((id) => descriptor(id)), end < count ? `${end}` : null);
    }),
    getOperation: (id) => Effect.sync(() => { trace.queries.push(id); return lookup(id, statuses.get(id) ?? "pending"); }),
  };
  return { source, ids, trace };
}

it.effect("an unseeded owner stays visibly starting and allows reads while blocking every mutation", () => Effect.gen(function*() {
  const h = harness(); const scope = yield* Scope.make();
  const recovery = yield* makePendingRecovery(h.source).pipe(Scope.provide(scope));
  try {
    const current = yield* recovery.snapshot;
    expect([current.phase, current.readsAllowed, current.writesBlocked, current.enumerationComplete]).toEqual(["starting", true, true, false]);
    expect(yield* recovery.canMutateCollection("new")).toBe(false);
    const result = yield* Effect.result(recovery.resume);
    expect(result._tag).toBe("Failure");
    if (result._tag === "Failure") expect(result.failure).toEqual(new RecoveryUnavailable({ reason: "bootstrap_required" }));
    expect(h.trace.bootstrap).toBe(0);
    const ready = yield* recovery.resync;
    expect([ready.phase, ready.pending.length, ready.writesBlocked]).toEqual(["ready", 0, false]);
    expect(h.trace.bootstrap).toBe(1);
    expect(yield* recovery.canMutateCollection("new")).toBe(true);
    expect(yield* recovery.canMutateCollection("")).toBe(false);
  } finally { yield* Scope.close(scope, Exit.void); }
}));

for (const count of [0, 1, 63, 64, 65]) it.effect(`${count} unresolved descriptors respect 64 slots and keep reads available`, () => Effect.scoped(Effect.gen(function*() {
  const h = harness(count);
  const recovery = yield* makePendingRecovery(h.source);
  const current = yield* recovery.resync;
  expect(current.pending).toHaveLength(Math.min(count, 64));
  expect(current.enumerationComplete).toBe(count <= 64);
  expect(current.writesBlocked).toBe(count >= 64);
  expect(current.readsAllowed).toBe(true);
  expect(current.phase).toBe(count >= 64 ? "saturated" : "ready");
  expect(h.trace.pages).toEqual([]);
  expect(h.trace.queries).toEqual(h.ids.slice(0, 64));
  if (count > 0) expect(yield* recovery.canMutateCollection("c-1")).toBe(false);
  expect(yield* recovery.canMutateCollection("unrelated")).toBe(count < 64);
})));

for (const terminal of ["succeeded", "failed"] as const) it.effect(`${terminal} releases exactly one slot, resumes the saved cursor and never drops unresolved work`, () => Effect.scoped(Effect.gen(function*() {
  const statuses = new Map<string, Observation["state"]>();
  const h = harness(65, statuses);
  const recovery = yield* makePendingRecovery(h.source);
  yield* recovery.resync;
  statuses.set("1", terminal);
  const next = yield* recovery.requery("1");
  expect(h.trace.pages).toEqual([{ cursor: "64", limit: 1 }]);
  expect(h.trace.queries.slice(-2)).toEqual(["1", "65"]);
  expect(next.pending.map((entry) => entry.descriptor.operationId)).toEqual(h.ids.slice(1));
  expect(next.terminal).toEqual([observation("1", terminal)]);
  expect(next.enumerationComplete).toBe(true);
  expect(next.writesBlocked).toBe(true);
  statuses.set("2", "succeeded");
  const released = yield* recovery.requery("2");
  expect(released.pending).toHaveLength(63);
  expect(released.writesBlocked).toBe(false);
  expect(yield* recovery.canMutateCollection("c-1")).toBe(true);
  expect(yield* recovery.canMutateCollection("c-65")).toBe(false);
})));

it.effect("unknown is retained in its slot and descriptor target remains blocked without inventing failed", () => Effect.scoped(Effect.gen(function*() {
  const h = harness(65, new Map([["1", "unknown"]]));
  const recovery = yield* makePendingRecovery(h.source);
  const current = yield* recovery.resync;
  expect(current.pending[0]!.observation).toEqual(observation("1", "unknown"));
  expect(current.pending[0]!.descriptor).toEqual(descriptor("1"));
  expect(current.terminal).toHaveLength(0);
  expect(h.trace.pages).toHaveLength(0);
  const again = yield* recovery.requery("1");
  expect(again.pending).toHaveLength(64);
  expect(again.writesBlocked).toBe(true);
  expect(h.trace.pages).toHaveLength(0);
})));

for (const failures of [[1], [2], [1, 2, 3]]) it.effect(`operation read faults ${failures.join(",")} retain descriptors and do not create terminal outcomes`, () => Effect.scoped(Effect.gen(function*() {
  const h = harness(3); let calls = 0;
  const source: RecoverySource = { ...h.source, getOperation: (id) => Effect.suspend(() => {
    calls++; return failures.includes(calls) ? Effect.fail(new TransportError()) : h.source.getOperation(id);
  }) };
  const recovery = yield* makePendingRecovery(source);
  const current = yield* recovery.resync;
  expect(calls).toBe(3);
  expect(current.pending).toHaveLength(3);
  expect(current.terminal).toHaveLength(0);
  expect(current.pending.filter((entry) => entry.failure?.code === "TransportError")).toHaveLength(failures.length);
  expect(current.readsAllowed).toBe(true);
  expect(yield* recovery.canMutateCollection("c-1")).toBe(false);
  expect(yield* recovery.canMutateCollection("unrelated")).toBe(true);
})));

it.effect("manual requery fails typed, retains the last observation and a later terminal read clears its slot", () => Effect.scoped(Effect.gen(function*() {
  const h = harness(1); let failing = false;
  const failure = new ProtocolError();
  const source: RecoverySource = { ...h.source, getOperation: (id) => failing ? Effect.fail(failure) : Effect.succeed(lookup(id, "pending", 2)) };
  const recovery = yield* makePendingRecovery(source);
  yield* recovery.resync; failing = true;
  const result = yield* Effect.result(recovery.requery("1"));
  expect(result._tag).toBe("Failure");
  if (result._tag === "Failure") expect(result.failure).toBe(failure);
  const current = yield* recovery.snapshot;
  expect(current.pending[0]!.observation).toEqual(observation("1", "pending", 2));
  expect(current.pending[0]!.failure?.code).toBe("ProtocolError");
  failing = false;
  yield* recovery.requery("1");
  expect((yield* recovery.snapshot).pending[0]!.failure).toBeNull();
})));

it.effect("requery of a nontracked id makes no backend request", () => Effect.scoped(Effect.gen(function*() {
  const h = harness(); const recovery = yield* makePendingRecovery(h.source, boot());
  const result = yield* Effect.result(recovery.requery("absent"));
  expect(result._tag).toBe("Failure");
  if (result._tag === "Failure") expect(result.failure).toEqual(new RecoveryUnavailable({ reason: "not_tracked" }));
  expect(h.trace.queries).toEqual([]);
})));

it.effect("same operation concurrent requery is one app-owned request and followers get the same snapshot", () => Effect.gen(function*() {
  const scope = yield* Scope.make(); const h = harness(1);
  const started = yield* Deferred.make<void>(); const gate = yield* Deferred.make<Received<OperationLookupReply>>();
  let calls = 0;
  const source: RecoverySource = { ...h.source, getOperation: () => Effect.gen(function*() { calls++; yield* Deferred.succeed(started, undefined); return yield* Deferred.await(gate); }) };
  const recovery = yield* makePendingRecovery(source, boot([descriptor("1")] )).pipe(Scope.provide(scope));
  try {
    const first = yield* recovery.requery("1").pipe(Effect.forkChild);
    yield* Deferred.await(started);
    const second = yield* recovery.requery("1").pipe(Effect.forkChild);
    yield* TestClock.adjust(0);
    expect(calls).toBe(1);
    yield* Deferred.succeed(gate, lookup("1", "succeeded"));
    const left = yield* Fiber.join(first); const right = yield* Fiber.join(second);
    expect(left).toBe(right);
    expect(calls).toBe(1);
    expect(left.pending).toHaveLength(0);
    expect((yield* Effect.result(recovery.requery("1")))._tag).toBe("Failure");
    expect(calls).toBe(1);
  } finally { yield* Scope.close(scope, Exit.void); }
}));

it.effect("operation confirmation has exactly one 15 second deadline and no generic retry", () => Effect.gen(function*() {
  const scope = yield* Scope.make(); const h = harness(1);
  const started = yield* Deferred.make<void>(); let calls = 0; let active = 0;
  const source: RecoverySource = { ...h.source, getOperation: () => Effect.gen(function*() {
    calls++; active++; yield* Deferred.succeed(started, undefined); return yield* Effect.never;
  }).pipe(Effect.ensuring(Effect.sync(() => { active--; }))) };
  const recovery = yield* makePendingRecovery(source, boot([descriptor("1")])).pipe(Scope.provide(scope));
  try {
    const pending = yield* recovery.requery("1").pipe(Effect.result, Effect.forkChild);
    yield* Deferred.await(started);
    yield* TestClock.adjust(14999); expect(active).toBe(1);
    yield* TestClock.adjust(1);
    expect(active).toBe(0);
    const result = yield* Fiber.join(pending);
    expect(result._tag).toBe("Failure");
    expect((yield* recovery.snapshot).pending[0]!.failure?.code).toBe("ReadTimeout");
    expect([calls, active]).toEqual([1, 0]);
    expect((yield* recovery.snapshot).terminal).toHaveLength(0);
  } finally { yield* Scope.close(scope, Exit.void); }
}));

it.effect("scope disposal cancels an active query, empties secret-free metadata and rejects future work", () => Effect.gen(function*() {
  const scope = yield* Scope.make(); const h = harness(1);
  const started = yield* Deferred.make<void>(); let active = 0; let cancelled = 0;
  const source: RecoverySource = { ...h.source, getOperation: () => Effect.gen(function*() {
    active++; yield* Deferred.succeed(started, undefined); return yield* Effect.never;
  }).pipe(Effect.ensuring(Effect.sync(() => { active--; cancelled++; }))) };
  const recovery = yield* makePendingRecovery(source, boot([descriptor("1")])).pipe(Scope.provide(scope));
  const pending = yield* recovery.requery("1").pipe(Effect.forkChild);
  yield* Deferred.await(started); yield* Scope.close(scope, Exit.void); yield* Scope.close(scope, Exit.void);
  const exit = yield* Fiber.await(pending);
  expect(Exit.isFailure(exit)).toBe(true);
  if (Exit.isFailure(exit)) expect(Cause.hasInterrupts(exit.cause)).toBe(true);
  expect([active, cancelled]).toEqual([0, 1]);
  const current = yield* recovery.snapshot;
  expect([current.phase, current.pending.length, current.terminal.length, current.readsAllowed, current.writesBlocked]).toEqual(["disposed", 0, 0, false, true]);
  expect((yield* Effect.result(recovery.resume))._tag).toBe("Failure");
  expect((yield* Effect.result(recovery.requery("1")))._tag).toBe("Failure");
  expect((yield* Effect.result(recovery.acceptBootstrap(boot())))._tag).toBe("Failure");
}));

it.effect("new session Bootstrap cancels the old lookup and never applies its late terminal metadata", () => Effect.gen(function*() {
  const scope = yield* Scope.make(); const h = harness(1);
  const started = yield* Deferred.make<void>(); const gate = yield* Deferred.make<Received<OperationLookupReply>>();
  let cancelled = 0;
  const source: RecoverySource = { ...h.source, getOperation: () => Deferred.succeed(started, undefined).pipe(Effect.andThen(Deferred.await(gate)), Effect.ensuring(Effect.sync(() => { cancelled++; }))) };
  const recovery = yield* makePendingRecovery(source, boot([descriptor("1")])).pipe(Scope.provide(scope));
  try {
    const old = yield* recovery.requery("1").pipe(Effect.forkChild);
    yield* Deferred.await(started);
    yield* recovery.acceptBootstrap(boot([descriptor("new")], null, "s2"));
    expect(cancelled).toBe(1);
    yield* Deferred.succeed(gate, lookup("1", "succeeded"));
    expect(Exit.isFailure(yield* Fiber.await(old))).toBe(true);
    const current = yield* recovery.snapshot;
    expect(current.backendSessionId).toBe("s2");
    expect(current.pending.map((entry) => entry.descriptor.operationId)).toEqual(["1", "new"]);
    expect(current.pending.every((entry) => entry.observation === null && entry.failure === null)).toBe(true);
    expect(current.terminal).toEqual([]);
    expect(cancelled).toBe(1);
    expect((yield* Effect.result(recovery.acceptBootstrap(boot([], null, "s1"))))._tag).toBe("Failure");
    expect((yield* recovery.snapshot).backendSessionId).toBe("s2");
  } finally { yield* Scope.close(scope, Exit.void); }
}));

it.effect("an unfamiliar lookup session blocks mutation and requires fresh Bootstrap without replacing metadata", () => Effect.scoped(Effect.gen(function*() {
  const h = harness(1);
  const source: RecoverySource = { ...h.source, getOperation: (id) => Effect.succeed(lookup(id, "succeeded", 1, "s2")) };
  const recovery = yield* makePendingRecovery(source, boot([descriptor("1")]));
  const result = yield* Effect.result(recovery.requery("1"));
  expect(result._tag).toBe("Failure");
  if (result._tag === "Failure") expect(result.failure).toEqual(new BackendRejected({ code: "BackendSessionChanged", messageKey: "BackendSessionChanged" }));
  const current = yield* recovery.snapshot;
  expect([current.phase, current.pending.length, current.terminal.length, current.writesBlocked, current.readsAllowed]).toEqual(["session_changed", 1, 0, true, true]);
  expect((yield* Effect.result(recovery.resume))._tag).toBe("Failure");
  yield* recovery.acceptBootstrap(boot([], null, "s2"));
  expect((yield* recovery.resume).writesBlocked).toBe(false);
})));

it.effect("same-session Bootstrap cannot evict unknown descriptors omitted by its new page", () => Effect.scoped(Effect.gen(function*() {
  const h = harness(1, new Map([["1", "unknown"]]));
  const recovery = yield* makePendingRecovery(h.source);
  yield* recovery.resync;
  yield* recovery.acceptBootstrap(boot([]));
  const current = yield* recovery.snapshot;
  expect(current.pending).toHaveLength(1);
  expect(current.pending[0]!.observation?.state).toBe("unknown");
  expect(yield* recovery.canMutateCollection("c-1")).toBe(false);
})));

it.effect("same-session seed overflow keeps old 64 slots and restarts enumeration without discarding new DB records", () => Effect.scoped(Effect.gen(function*() {
  const h = harness(64, new Map(Array.from({ length: 64 }, (_, index) => [`${index + 1}`, "unknown"] as const)));
  const recovery = yield* makePendingRecovery(h.source);
  yield* recovery.resync;
  const next = yield* recovery.acceptBootstrap(boot([descriptor("new")], null));
  expect(next.pending).toHaveLength(64);
  expect(next.pending.some((entry) => entry.descriptor.operationId === "new")).toBe(false);
  expect(next.enumerationComplete).toBe(false);
  expect(next.cursor).toBeNull();
  expect(next.writesBlocked).toBe(true);
  expect(next.readsAllowed).toBe(true);
})));

it.effect("bootstrap schema failure blocks writes and preserves the last good entries", () => Effect.scoped(Effect.gen(function*() {
  const h = harness(1); const recovery = yield* makePendingRecovery(h.source);
  yield* recovery.resync;
  const invalid = boot();
  const bad = { ...invalid, reply: { ...invalid.reply, data: { ...invalid.reply.data, pendingOperations: null } } };
  expect((yield* Effect.result(recovery.acceptBootstrap(bad)))._tag).toBe("Failure");
  const current = yield* recovery.snapshot;
  expect(current.pending[0]!.descriptor).toEqual(descriptor("1"));
  expect(current.phase).toBe("failed");
  expect(current.failure?.code).toBe("ProtocolError");
  expect([current.writesBlocked, current.readsAllowed]).toEqual([true, true]);
})));

it.effect("same-session seed keeps higher descriptor revision and rejects target identity changes", () => Effect.scoped(Effect.gen(function*() {
  const h = harness(); const recovery = yield* makePendingRecovery(h.source, boot([descriptor("1", 2)]));
  yield* recovery.acceptBootstrap(boot([descriptor("1", 1)]));
  expect((yield* recovery.snapshot).pending[0]!.descriptor.revision).toBe(2);
  yield* recovery.acceptBootstrap(boot([descriptor("1", 3)]));
  expect((yield* recovery.snapshot).pending[0]!.descriptor.revision).toBe(3);
  const changed = { ...descriptor("1", 4), collectionId: "different" };
  expect((yield* Effect.result(recovery.acceptBootstrap(boot([changed]))))._tag).toBe("Failure");
  expect((yield* recovery.snapshot).pending[0]!.descriptor).toEqual(descriptor("1", 3));
})));

it.effect("terminal metadata is bounded to 128 and cannot be reintroduced by an old descriptor page", () => Effect.scoped(Effect.gen(function*() {
  const h = harness(193, new Map(Array.from({ length: 193 }, (_, index) => [`${index + 1}`, "succeeded"] as const)));
  const recovery = yield* makePendingRecovery(h.source);
  const current = yield* recovery.resync;
  expect(current.pending).toEqual([]);
  expect(current.terminal).toHaveLength(128);
  expect(current.terminal[0]!.operationId).toBe("66");
  expect(current.terminal[127]!.operationId).toBe("193");
  expect(h.trace.pages).toEqual([{ cursor: "64", limit: 64 }, { cursor: "128", limit: 64 }, { cursor: "192", limit: 64 }]);
  yield* recovery.acceptBootstrap(boot([descriptor("193")]));
  expect((yield* recovery.snapshot).pending).toEqual([]);
})));

it.effect("page failure after terminal cleanup keeps the cursor and a manual resume continues without new Bootstrap", () => Effect.scoped(Effect.gen(function*() {
  const statuses = new Map<string, Observation["state"]>([["1", "succeeded"]]);
  const h = harness(65, statuses); let fail = true;
  const source: RecoverySource = { ...h.source, listPendingOperations: (request) => fail ? Effect.fail(new TransportError()) : h.source.listPendingOperations(request) };
  const recovery = yield* makePendingRecovery(source);
  expect((yield* Effect.result(recovery.resync))._tag).toBe("Failure");
  const failed = yield* recovery.snapshot;
  expect([failed.pending.length, failed.terminal.length, failed.cursor, failed.phase, failed.writesBlocked]).toEqual([63, 1, "64", "failed", true]);
  fail = false; const next = yield* recovery.resume;
  expect(next.enumerationComplete).toBe(true);
  expect(h.trace.pages).toEqual([{ cursor: "64", limit: 1 }]);
  expect(h.trace.bootstrap).toBe(1);
})));

it.effect("empty advancing pages stop at a bounded sweep and stay enumerating until explicitly resumed", () => Effect.scoped(Effect.gen(function*() {
  const h = harness(); let pages = 0;
  const source: RecoverySource = { ...h.source, listPendingOperations: () => Effect.sync(() => { pages++; return page([], pages > 64 ? null : `${pages}`); }) };
  const recovery = yield* makePendingRecovery(source, boot([], "first"));
  const waiting = yield* recovery.resume;
  expect([pages, waiting.phase, waiting.enumerationComplete, waiting.writesBlocked]).toEqual([64, "enumerating", false, true]);
  expect(waiting.cursor).toBe("64");
  const done = yield* recovery.resume;
  expect([pages, done.phase, done.enumerationComplete, done.writesBlocked]).toEqual([65, "ready", true, false]);
})));

for (const invalid of ["same-cursor", "oversized-page", "duplicate-id", "foreign-session"] as const) it.effect(`page ${invalid} preserves old pending and blocks further enumeration`, () => Effect.scoped(Effect.gen(function*() {
  const h = harness(63);
  const source: RecoverySource = { ...h.source, listPendingOperations: () => Effect.succeed(
    invalid === "same-cursor" ? page([], "cursor") : invalid === "oversized-page" ? page([descriptor("64"), descriptor("65")]) : invalid === "duplicate-id" ? page([descriptor("64"), descriptor("64")]) : page([], null, "s2"),
  ) };
  const recovery = yield* makePendingRecovery(source, boot(h.ids.map((id) => descriptor(id)), "cursor"));
  expect((yield* Effect.result(recovery.resume))._tag).toBe("Failure");
  const current = yield* recovery.snapshot;
  expect(current.pending).toHaveLength(63);
  expect(current.cursor).toBe("cursor");
  expect(current.writesBlocked).toBe(true);
  expect(current.failure?.code).toBe(invalid === "foreign-session" ? "BackendSessionChanged" : "ProtocolError");
})));

for (const invalid of ["id", "kind", "collection", "round", "revision", "secret", "unknown-metadata", "header", "timestamp"] as const) it.effect(`operation ${invalid} corruption cannot release a slot`, () => Effect.scoped(Effect.gen(function*() {
  const h = harness(1);
  let value: Received<OperationLookupReply> = lookup("1", "succeeded");
  if (invalid === "header") value = { ...value, reply: { ...value.reply, protocolVersion: 0 as never } };
  else if (invalid === "timestamp") value = { ...value, receivedAtMillis: NaN };
  else {
    const fields = { ...value.reply.data,
      ...(invalid === "id" ? { operationId: "different" } : invalid === "kind" ? { kind: "Rerun" } : invalid === "collection" ? { collectionId: "different" } : invalid === "round" ? { roundId: "different" } : invalid === "revision" ? { revision: 0 } : invalid === "secret" ? { password: "never-log-me" } : { state: "unknown" as const }),
    };
    value = { ...value, reply: { ...value.reply, data: fields } };
  }
  const source: RecoverySource = { ...h.source, getOperation: () => Effect.succeed(value) };
  const recovery = yield* makePendingRecovery(source, boot([descriptor("1")]));
  expect((yield* Effect.result(recovery.requery("1")))._tag).toBe("Failure");
  const current = yield* recovery.snapshot;
  expect(current.pending).toHaveLength(1);
  expect(current.terminal).toHaveLength(0);
  expect(current.pending[0]!.failure?.code).toBe("ProtocolError");
  expect(JSON.stringify(current)).not.toContain("never-log-me");
})));

it.effect("an observation revision cannot regress after a newer confirmed pending observation", () => Effect.scoped(Effect.gen(function*() {
  const h = harness(1); let revision = 3;
  const source: RecoverySource = { ...h.source, getOperation: (id) => Effect.succeed(lookup(id, revision === 3 ? "pending" : "succeeded", revision)) };
  const recovery = yield* makePendingRecovery(source, boot([descriptor("1")]));
  yield* recovery.requery("1"); revision = 2;
  expect((yield* Effect.result(recovery.requery("1")))._tag).toBe("Failure");
  expect((yield* recovery.snapshot).pending[0]!.observation?.revision).toBe(3);
  expect((yield* recovery.snapshot).terminal).toHaveLength(0);
})));

it.effect("closed scope cannot construct an owner or dispatch Bootstrap", () => Effect.gen(function*() {
  const scope = yield* Scope.make(); yield* Scope.close(scope, Exit.void);
  const h = harness();
  const result = yield* Effect.result(makePendingRecovery(h.source).pipe(Scope.provide(scope)));
  expect(result._tag).toBe("Failure");
  expect(h.trace.bootstrap).toBe(0);
}));

it.effect("one hundred same-session resyncs keep metadata immutable and release replaced work scopes", () => Effect.gen(function*() {
  const scope = yield* Scope.make(); const h = harness(1);
  const recovery = yield* makePendingRecovery(h.source).pipe(Scope.provide(scope));
  try {
    for (let index = 0; index < 100; index++) {
      const current = yield* recovery.resync;
      expect(current.pending).toHaveLength(1);
      expect(Object.isFrozen(current)).toBe(true);
      expect(Object.isFrozen(current.pending)).toBe(true);
      expect(Object.isFrozen(current.pending[0]!.descriptor)).toBe(true);
      expect(Object.isFrozen(current.pending[0]!.observation)).toBe(true);
    }
    expect(h.trace.bootstrap).toBe(100);
    expect(h.trace.queries).toHaveLength(100);
    expect(scope.state._tag).toBe("Open");
    if (scope.state._tag === "Open") expect(scope.state.finalizers).toBeUndefined();
  } finally { yield* Scope.close(scope, Exit.void); }
  expect((yield* recovery.snapshot).phase).toBe("disposed");
}));

it.effect("duplicate bootstrap descriptors reject atomically and construction releases its private scope", () => Effect.gen(function*() {
  const scope = yield* Scope.make(); const h = harness();
  try {
    const result = yield* Effect.result(makePendingRecovery(h.source, boot([descriptor("1"), descriptor("1")])).pipe(Scope.provide(scope)));
    expect(result._tag).toBe("Failure");
    expect(scope.state._tag).toBe("Empty");
  } finally { yield* Scope.close(scope, Exit.void); }
}));

it.effect("active draft session is checked without copying the draft into recovery metadata", () => Effect.scoped(Effect.gen(function*() {
  const h = harness(); const recovery = yield* makePendingRecovery(h.source);
  const seed = boot();
  const draft = { backendSessionId: "s1", draftId: "d1", revision: 0, articleGeneration: 0, state: "empty" as const, snapshot: null, collectionId: null };
  yield* recovery.acceptBootstrap({ ...seed, reply: { ...seed.reply, data: { ...seed.reply.data, activeDraft: draft } } });
  expect(JSON.stringify(yield* recovery.snapshot)).not.toContain("draftId");
  const bad = { ...seed, reply: { ...seed.reply, data: { ...seed.reply.data, activeDraft: { ...draft, backendSessionId: "s2" } } } };
  expect((yield* Effect.result(recovery.acceptBootstrap(bad)))._tag).toBe("Failure");
  expect((yield* recovery.snapshot).backendSessionId).toBe("s1");
})));

for (const nullable of ["pendingOperations", "recentResults"] as const) it.effect(`a faulty decoder returning null ${nullable} violates the production invariant loudly`, () => Effect.scoped(Effect.gen(function*() {
  const h = harness(); const recovery = yield* makePendingRecovery(h.source);
  const decoder = schemaDecoders.get(BootstrapData);
  if (decoder === undefined) throw new Error("Bootstrap decoder absent");
  decoder.mockReturnValueOnce({ ...boot().reply.data, [nullable]: null });
  const result = yield* Effect.result(recovery.acceptBootstrap(boot()));
  expect(result._tag).toBe("Failure");
  expect((yield* recovery.snapshot).failure?.code).toBe("ProtocolError");
})));

it.effect("a faulty page decoder cannot return a null list as empty completion", () => Effect.scoped(Effect.gen(function*() {
  const h = harness(); const recovery = yield* makePendingRecovery(h.source, boot([], "first"));
  const decoder = schemaDecoders.get(PendingOperationsPage);
  if (decoder === undefined) throw new Error("Page decoder absent");
  decoder.mockReturnValueOnce({ operations: null, cursor: null });
  expect((yield* Effect.result(recovery.resume))._tag).toBe("Failure");
  expect((yield* recovery.snapshot).enumerationComplete).toBe(false);
})));

it.effect("a faulty observation decoder cannot release revision-zero work with a missing revision", () => Effect.scoped(Effect.gen(function*() {
  const h = harness(); const source: RecoverySource = { ...h.source, getOperation: () => Effect.succeed(lookup("1", "succeeded", 0)) };
  const recovery = yield* makePendingRecovery(source, boot([descriptor("1", 0)]));
  const decoder = schemaDecoders.get(OperationObservation);
  if (decoder === undefined) throw new Error("Operation decoder absent");
  decoder.mockReturnValueOnce({ ...observation("1", "succeeded", 0), revision: null });
  expect((yield* Effect.result(recovery.requery("1")))._tag).toBe("Failure");
  expect((yield* recovery.snapshot).pending).toHaveLength(1);
  expect((yield* recovery.snapshot).terminal).toEqual([]);
})));

for (const field of ["kind", "roundId"] as const) it.effect(`the same operation cannot change its descriptor ${field}`, () => Effect.scoped(Effect.gen(function*() {
  const h = harness(); const recovery = yield* makePendingRecovery(h.source, boot([descriptor("1")]));
  const changed = { ...descriptor("1"), [field]: field === "kind" ? "Rerun" : "different" };
  expect((yield* Effect.result(recovery.acceptBootstrap(boot([changed]))))._tag).toBe("Failure");
  expect((yield* recovery.snapshot).pending[0]!.descriptor).toEqual(descriptor("1"));
})));

for (const invalid of ["protocol", "session", "negative-time", "infinite-time"] as const) it.effect(`bootstrap ${invalid} header is rejected before installing recovery metadata`, () => Effect.scoped(Effect.gen(function*() {
  const h = harness(); const recovery = yield* makePendingRecovery(h.source);
  const seed = boot([descriptor("1")]);
  const value: Received<BootstrapReply> = invalid === "protocol" ? { ...seed, reply: { ...seed.reply, protocolVersion: 0 as never } } : invalid === "session" ? { ...seed, reply: { ...seed.reply, backendSessionId: "" } } : { ...seed, receivedAtMillis: invalid === "negative-time" ? -1 : Infinity };
  expect((yield* Effect.result(recovery.acceptBootstrap(value)))._tag).toBe("Failure");
  expect((yield* recovery.snapshot).pending).toHaveLength(0);
  expect((yield* recovery.snapshot).writesBlocked).toBe(true);
})));

it.effect("retired session metadata stays bounded across 66 authoritative sessions", () => Effect.scoped(Effect.gen(function*() {
  const h = harness(); const recovery = yield* makePendingRecovery(h.source);
  for (let index = 0; index < 67; index++) yield* recovery.acceptBootstrap(boot([], null, `session-${index}`));
  expect((yield* Effect.result(recovery.acceptBootstrap(boot([], null, "session-65"))))._tag).toBe("Failure");
  expect((yield* recovery.snapshot).backendSessionId).toBe("session-66");
  // An evicted old ID still needs an authoritative Bootstrap; notices or reads
  // can never install it. IDs are opaque and not ordered by the frontend.
  yield* recovery.acceptBootstrap(boot([], null, "session-0"));
  expect((yield* recovery.snapshot).backendSessionId).toBe("session-0");
})));

it.effect("a new page's individual read failure keeps its accepted descriptor instead of rolling it back", () => Effect.scoped(Effect.gen(function*() {
  const h = harness(65, new Map([["1", "succeeded"]]));
  const source: RecoverySource = { ...h.source, getOperation: (id) => id === "65" ? Effect.fail(new TransportError()) : h.source.getOperation(id) };
  const recovery = yield* makePendingRecovery(source);
  const current = yield* recovery.resync;
  expect(current.pending.find((entry) => entry.descriptor.operationId === "65")?.failure?.code).toBe("TransportError");
  expect(current.pending).toHaveLength(64);
  expect(current.enumerationComplete).toBe(true);
  expect(current.terminal).toHaveLength(1);
})));

it.effect("lookup session change during resync retains the bootstrap-required fence on the following resume", () => Effect.scoped(Effect.gen(function*() {
  const h = harness(1);
  const source: RecoverySource = { ...h.source, getOperation: (id) => Effect.succeed(lookup(id, "pending", 1, "s2")) };
  const recovery = yield* makePendingRecovery(source);
  expect((yield* Effect.result(recovery.resync))._tag).toBe("Failure");
  expect((yield* recovery.snapshot).phase).toBe("session_changed");
  const again = yield* Effect.result(recovery.resume);
  expect(again._tag).toBe("Failure");
  if (again._tag === "Failure") expect(again.failure).toEqual(new RecoveryUnavailable({ reason: "bootstrap_required" }));
})));

for (const retained of ["pending", "unknown"] as const) it.effect(`a new Go session retains ${retained} IDs, invalidates old observations and confirms them before releasing slots`, () => Effect.scoped(Effect.gen(function*() {
  const h = harness(2); let session = "s1"; let failedRead = false; let nextState: Observation["state"] = retained;
  const source: RecoverySource = { ...h.source, getOperation: (id) => Effect.suspend(() => {
    h.trace.queries.push(id);
    return failedRead ? Effect.fail(new TransportError()) : Effect.succeed(lookup(id, id === "2" ? "succeeded" : nextState, 1, session));
  }) };
  const recovery = yield* makePendingRecovery(source);
  yield* recovery.resync;
  failedRead = true;
  expect((yield* Effect.result(recovery.requery("1")))._tag).toBe("Failure");
  session = "s2"; failedRead = false;
  const changed = yield* recovery.acceptBootstrap(boot([], null, session));
  expect(changed.pending.map((entry) => entry.descriptor.operationId)).toEqual(["1"]);
  expect(changed.pending[0]!.observation).toBeNull();
  expect(changed.pending[0]!.failure).toBeNull();
  expect(changed.terminal).toEqual([]);
  expect(yield* recovery.canMutateCollection("c-1")).toBe(false);
  expect(yield* recovery.canMutateCollection("unrelated")).toBe(true);
  const confirmed = yield* recovery.resume;
  expect(confirmed.pending[0]!.observation?.state).toBe(retained);
  expect(h.trace.queries).toEqual(["1", "2", "1", "1"]);
  nextState = "failed";
  const terminal = yield* recovery.requery("1");
  expect(terminal.pending).toEqual([]);
  expect(terminal.terminal.map((item) => item.operationId)).toEqual(["1"]);
  expect(yield* recovery.canMutateCollection("c-1")).toBe(true);
})));

it.effect("a new Go session cannot evict 64 older unknown IDs to admit new page descriptors", () => Effect.scoped(Effect.gen(function*() {
  const h = harness(64); let terminal = false;
  const source: RecoverySource = { ...h.source,
    getOperation: (id) => Effect.succeed(lookup(id, terminal && id === "1" ? "succeeded" : "unknown", 1, "s2")),
    listPendingOperations: (request) => Effect.sync(() => { h.trace.pages.push({ ...request }); return page([descriptor("new")], null, "s2"); }),
  };
  const recovery = yield* makePendingRecovery(source, boot(h.ids.map((id) => descriptor(id))));
  const changed = yield* recovery.acceptBootstrap(boot([descriptor("new")], null, "s2"));
  expect(changed.pending.map((entry) => entry.descriptor.operationId)).toEqual(h.ids);
  expect([changed.enumerationComplete, changed.writesBlocked, changed.readsAllowed]).toEqual([false, true, true]);
  const unresolved = yield* recovery.resume;
  expect(unresolved.pending.every((entry) => entry.observation?.state === "unknown")).toBe(true);
  expect(h.trace.pages).toEqual([]);
  terminal = true;
  const released = yield* recovery.requery("1");
  expect(h.trace.pages).toEqual([{ cursor: null, limit: 1 }]);
  expect(released.pending.map((entry) => entry.descriptor.operationId)).toEqual([...h.ids.slice(1), "new"]);
  expect(released.enumerationComplete).toBe(true);
  expect(released.writesBlocked).toBe(true);
  expect(released.terminal.map((item) => item.operationId)).toEqual(["1"]);
})));

it.effect("closing the owner ends external change consumers and future subscriptions without leaving fibers", () => Effect.gen(function*() {
  const scope = yield* Scope.make(); const h = harness(); const observed = yield* Deferred.make<void>();
  const recovery = yield* makePendingRecovery(h.source, boot()).pipe(Scope.provide(scope));
  let active = 0;
  const consumer = yield* Effect.suspend(() => {
    active++;
    return recovery.changes.pipe(Stream.runForEach(() => Deferred.succeed(observed, undefined)), Effect.ensuring(Effect.sync(() => { active--; })));
  }).pipe(Effect.forkChild);
  try {
    yield* Deferred.await(observed); expect(active).toBe(1);
    yield* Scope.close(scope, Exit.void); yield* Scope.close(scope, Exit.void); yield* TestClock.adjust(0);
    expect(active).toBe(0);
    expect(consumer.pollUnsafe()).toBeDefined();
    const future = yield* recovery.changes.pipe(Stream.runDrain, Effect.forkChild);
    try {
      yield* TestClock.adjust(0);
      expect(future.pollUnsafe()).toBeDefined();
      expect((yield* recovery.snapshot).phase).toBe("disposed");
    } finally { yield* Fiber.interrupt(future); }
  } finally { yield* Fiber.interrupt(consumer); yield* Scope.close(scope, Exit.void); }
}));

function pauseNextPublication(entered: Deferred.Deferred<void>, release: Deferred.Deferred<void>) {
  const update = vi.mocked(SubscriptionRef.update);
  const original = update.getMockImplementation();
  if (original === undefined) throw new Error("Reference update implementation absent");
  update.mockImplementationOnce((...args) => Deferred.succeed(entered, undefined).pipe(
    Effect.andThen(Deferred.await(release)), Effect.andThen(original(...args)), Effect.uninterruptible,
  ) as never);
}

for (const state of ["succeeded", "foreign-session", "read-failure", "page"] as const) it.effect(`a newer Bootstrap arriving immediately before ${state} publication fences all old changes`, () => Effect.gen(function*() {
  const scope = yield* Scope.make(); const h = harness();
  const entered = yield* Deferred.make<void>(); const release = yield* Deferred.make<void>();
  const source: RecoverySource = { ...h.source,
    getOperation: () => state === "read-failure" ? Effect.fail(new TransportError()) : Effect.succeed(lookup("1", "succeeded", 1, state === "foreign-session" ? "s2" : "s1")),
    listPendingOperations: () => Effect.succeed(page([descriptor("2")], null)),
  };
  const recovery = yield* makePendingRecovery(source, state === "page" ? boot([], "first") : boot([descriptor("1")])).pipe(Scope.provide(scope));
  pauseNextPublication(entered, release);
  try {
    const pending = yield* (state === "page" ? recovery.resume : recovery.requery("1")).pipe(Effect.forkChild);
    const boundary = yield* Effect.raceFirst(Deferred.await(entered).pipe(Effect.as("entered")), Fiber.await(pending).pipe(Effect.as("ended")));
    expect(boundary).toBe("entered");
    yield* recovery.acceptBootstrap(boot([descriptor("new")], null, "s3"));
    yield* Deferred.succeed(release, undefined);
    const exit = yield* Fiber.await(pending);
    expect(Exit.isFailure(exit)).toBe(true);
    if (Exit.isFailure(exit) && state !== "read-failure") expect(Cause.hasInterrupts(exit.cause)).toBe(true);
    const current = yield* recovery.snapshot;
    expect(current.backendSessionId).toBe("s3");
    expect(current.pending.map((entry) => entry.descriptor.operationId)).toEqual(state === "page" ? ["new"] : ["1", "new"]);
    expect(current.terminal).toEqual([]);
    expect(current.failure).toBeNull();
    expect(current.phase).toBe("ready");
  } finally { yield* Deferred.succeed(release, undefined); yield* Scope.close(scope, Exit.void); }
}));

for (const kind of ["operation", "bootstrap-error"] as const) it.effect(`owner close before ${kind} publication cannot resurrect metadata`, () => Effect.gen(function*() {
  const scope = yield* Scope.make(); const h = harness();
  const entered = yield* Deferred.make<void>(); const release = yield* Deferred.make<void>();
  const source: RecoverySource = { ...h.source, bootstrap: () => Effect.fail(new TransportError()), getOperation: () => Effect.succeed(lookup("1", "succeeded")) };
  const recovery = yield* makePendingRecovery(source, boot([descriptor("1")])).pipe(Scope.provide(scope));
  pauseNextPublication(entered, release);
  try {
    const pending = yield* (kind === "operation" ? recovery.requery("1") : recovery.resync).pipe(Effect.forkChild);
    const boundary = yield* Effect.raceFirst(Deferred.await(entered).pipe(Effect.as("entered")), Fiber.await(pending).pipe(Effect.as("ended")));
    expect(boundary).toBe("entered");
    const closing = yield* Scope.close(scope, Exit.void).pipe(Effect.forkChild);
    yield* TestClock.adjust(0);
    yield* Deferred.succeed(release, undefined);
    yield* Fiber.join(closing); yield* Fiber.await(pending);
    const current = yield* recovery.snapshot;
    expect([current.phase, current.pending.length, current.terminal.length]).toEqual(["disposed", 0, 0]);
  } finally { yield* Deferred.succeed(release, undefined); yield* Scope.close(scope, Exit.void); }
}));
