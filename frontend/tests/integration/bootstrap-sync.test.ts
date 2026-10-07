import { expect, it } from "@effect/vitest";
import { Cause, Deferred, Effect, Exit, Fiber, Scope } from "effect";
import { TestClock } from "effect/testing";
import { ProtocolError, TransportError, type BackendError, type BootstrapReply, type StateNotice } from "../../src/contracts/backend";
import { makeBootstrapSync, type BootstrapSource } from "../../src/operations/bootstrapSync";
import { projectCoordinator } from "../../src/operations/coordinatorState";
import type { ReadError } from "../../src/operations/readRetry";

function reply(session = "s1", revision = 1, generation = revision): BootstrapReply {
  return { protocolVersion: 1, backendSessionId: session, occurredAt: "2026-10-06T00:00:00Z", data: {
    backendNow: "2026-10-06T00:00:00Z", theme: "system", activeDraft: { backendSessionId: session, draftId: "d1", revision, articleGeneration: generation, state: "ready", snapshot: null, collectionId: null },
    pendingOperations: [], pendingCursor: null, recentResults: [{ collectionId: "c1", roundId: "r1", revision }],
  } };
}
function notice(revision: number, session = "s1", entityId = "d1", entityKind: StateNotice["entityKind"] = "draft"): StateNotice {
  return { backendSessionId: session, entityKind, entityId, revision, operationId: null };
}
function harness(read: (calls: number) => Effect.Effect<BootstrapReply, ReadError> = () => Effect.succeed(reply())) {
  let calls = 0; let active = 0; let released = 0;
  let listener: ((value: StateNotice) => void) | undefined;
  let onError: ((error: BackendError) => void) | undefined;
  const trace: string[] = [];
  const source: BootstrapSource = {
    bootstrap: () => Effect.suspend(() => { calls++; trace.push("bootstrap"); return read(calls); }),
    subscribeStateChanges: (next, error) => Effect.acquireRelease(Effect.sync(() => {
      active++; trace.push("subscribe"); listener = next; onError = error;
    }), () => Effect.sync(() => { active--; released++; trace.push("unsubscribe"); })),
  };
  return { source, emit: (value: StateNotice) => listener?.(value), fail: (error: BackendError) => onError?.(error), get calls() { return calls; }, get active() { return active; }, get released() { return released; }, trace };
}

it.effect("subscribes before Bootstrap and closes only its own scope exactly once", () => Effect.gen(function*() {
  const h = harness(); const owner = yield* Scope.make();
  const sync = yield* makeBootstrapSync(h.source).pipe(Effect.provideService(Scope.Scope, owner));
  expect(h.trace).toEqual(["subscribe", "bootstrap"]);
  expect((yield* sync.snapshot).phase).toBe("ready");
  expect(h.active).toBe(1);
  yield* Scope.close(owner, Exit.void); yield* Scope.close(owner, Exit.void);
  expect([h.active, h.released]).toEqual([0, 1]);
  expect((yield* sync.snapshot).coordinator._tag).toBe("disposed");
  h.emit(notice(9)); h.fail(new ProtocolError()); yield* TestClock.adjust(0);
  expect(h.calls).toBe(1);
  const exit = yield* Effect.exit(sync.resync);
  expect(Exit.isFailure(exit)).toBe(true);
  if (Exit.isFailure(exit)) expect(Cause.hasInterrupts(exit.cause)).toBe(true);
}));

it.effect("a subscription failure releases partial acquisition without waiting for its parent", () => Effect.gen(function*() {
  const h = harness(); const owner = yield* Scope.make(); const failure = new TransportError();
  const source: BootstrapSource = { ...h.source, subscribeStateChanges: (listener, onError) => h.source.subscribeStateChanges(listener, onError).pipe(Effect.andThen(Effect.fail(failure))) };
  const result = yield* Effect.result(makeBootstrapSync(source).pipe(Effect.provideService(Scope.Scope, owner)));
  expect(result._tag).toBe("Failure");
  if (result._tag === "Failure") expect(result.failure).toBe(failure);
  expect([h.calls, h.active, h.released]).toEqual([0, 0, 1]);
  yield* Scope.close(owner, Exit.void);
  expect(h.released).toBe(1);
}));

it.effect("initial Bootstrap failure does not fabricate an empty ready model", () => Effect.gen(function*() {
  const failure = new ProtocolError(); const h = harness(() => Effect.fail(failure));
  const result = yield* Effect.result(Effect.scoped(makeBootstrapSync(h.source)));
  expect(result._tag).toBe("Failure");
  if (result._tag === "Failure") expect(result.failure).toBe(failure);
  expect([h.calls, h.active, h.released]).toEqual([1, 0, 1]);
}));

it.effect("merges startup notices by maximum revision and fetches the authoritative summary", () => Effect.scoped(Effect.gen(function*() {
  const first = yield* Deferred.make<BootstrapReply>(); const started = yield* Deferred.make<void>();
  const h = harness((calls) => calls === 1 ? Deferred.succeed(started, undefined).pipe(Effect.andThen(Deferred.await(first))) : Effect.succeed(reply("s1", 3)));
  const fiber = yield* makeBootstrapSync(h.source).pipe(Effect.forkChild);
  yield* Deferred.await(started); h.emit(notice(3)); h.emit(notice(2));
  yield* Deferred.succeed(first, reply());
  const sync = yield* Fiber.join(fiber); yield* TestClock.adjust(0);
  const snapshot = yield* sync.snapshot;
  expect(snapshot.phase).toBe("ready");
  if (snapshot.coordinator._tag === "active") expect(snapshot.coordinator.activeDraft?.revision).toBe(3);
  expect(h.calls).toBe(2);
})));

it.effect("a startup notice already covered by Bootstrap adds no redundant request", () => Effect.scoped(Effect.gen(function*() {
  const first = yield* Deferred.make<BootstrapReply>(); const started = yield* Deferred.make<void>();
  const h = harness(() => Deferred.succeed(started, undefined).pipe(Effect.andThen(Deferred.await(first))));
  const fiber = yield* makeBootstrapSync(h.source).pipe(Effect.forkChild);
  yield* Deferred.await(started); h.emit(notice(3)); yield* Deferred.succeed(first, reply("s1", 3));
  yield* Fiber.join(fiber); yield* TestClock.adjust(0); expect(h.calls).toBe(1);
})));

it.effect("unknown entity hints remain stale rather than inventing a successful query", () => Effect.scoped(Effect.gen(function*() {
  const h = harness(); const sync = yield* makeBootstrapSync(h.source);
  h.emit(notice(2, "s1", "unknown", "collection")); yield* TestClock.adjust(0);
  const snapshot = yield* sync.snapshot;
  expect(snapshot.phase).toBe("stale"); expect(projectCoordinator(snapshot.coordinator).writesBlocked).toBe(true);
  if (snapshot.coordinator._tag === "active") {
    expect(snapshot.coordinator.stale).toEqual([notice(2, "s1", "unknown", "collection")]);
    expect(snapshot.coordinator.results).toEqual(reply().data.recentResults);
  }
  expect(h.calls).toBe(2);
})));

for (const count of [64, 65, 66]) it.effect(`${count} startup hints are bounded without reporting lost hints as ready`, () => Effect.scoped(Effect.gen(function*() {
  const first = yield* Deferred.make<BootstrapReply>(); const started = yield* Deferred.make<void>();
  const h = harness(() => Deferred.succeed(started, undefined).pipe(Effect.andThen(Deferred.await(first))));
  const fiber = yield* makeBootstrapSync(h.source).pipe(Effect.forkChild);
  yield* Deferred.await(started); for (let index = 0; index < count; index++) h.emit(notice(1, "s1", `unknown-${index}`, "collection"));
  yield* Deferred.succeed(first, reply()); const sync = yield* Fiber.join(fiber); yield* TestClock.adjust(0);
  const snapshot = yield* sync.snapshot;
  expect(snapshot.phase).toBe("stale");
  if (snapshot.coordinator._tag === "active") { expect(snapshot.coordinator.stale.length).toBe(64); expect(snapshot.coordinator.staleOverflow).toBe(count > 64); }
  expect(h.calls).toBe(1);
})));

it.effect("a later notice during refresh requires a newer authoritative reply", () => Effect.scoped(Effect.gen(function*() {
  const second = yield* Deferred.make<BootstrapReply>(); const started = yield* Deferred.make<void>();
  const h = harness((calls) => calls === 2 ? Deferred.succeed(started, undefined).pipe(Effect.andThen(Deferred.await(second))) : Effect.succeed(reply("s1", calls === 1 ? 1 : 7)));
  const sync = yield* makeBootstrapSync(h.source); h.emit(notice(5)); yield* Deferred.await(started);
  const refreshing = yield* sync.snapshot; expect(refreshing.phase).toBe("refreshing");
  if (refreshing.coordinator._tag === "active") expect(refreshing.coordinator.activeDraft?.revision).toBe(1);
  h.emit(notice(7)); yield* Deferred.succeed(second, reply("s1", 5)); yield* TestClock.adjust(0);
  const snapshot = yield* sync.snapshot; expect(snapshot.phase).toBe("ready");
  if (snapshot.coordinator._tag === "active") expect(snapshot.coordinator.activeDraft?.revision).toBe(7);
  expect(h.calls).toBe(3);
})));

it.effect("covered and duplicate notices cause no additional request", () => Effect.scoped(Effect.gen(function*() {
  const h = harness(() => Effect.succeed(reply("s1", 3))); const sync = yield* makeBootstrapSync(h.source);
  h.emit(notice(0)); h.emit(notice(3)); h.emit(notice(2, "s1", "c1", "collection")); yield* TestClock.adjust(0);
  expect(h.calls).toBe(1); expect((yield* sync.snapshot).phase).toBe("ready");
})));

it.effect("a new session replaces ephemeral state and rejects retired session notices", () => Effect.scoped(Effect.gen(function*() {
  const h = harness((calls) => Effect.succeed(reply(calls === 1 ? "s1" : "s2", calls === 1 ? 8 : 1)));
  const sync = yield* makeBootstrapSync(h.source); h.emit(notice(1, "s2")); yield* TestClock.adjust(0);
  const snapshot = yield* sync.snapshot; expect(snapshot.phase).toBe("ready");
  if (snapshot.coordinator._tag === "active") { expect(snapshot.coordinator.backendSessionId).toBe("s2"); expect(snapshot.coordinator.activeDraft?.revision).toBe(1); }
  h.emit(notice(99, "s1")); yield* TestClock.adjust(0); expect(h.calls).toBe(2);
})));

it.effect("an old-session Bootstrap arriving after a new-session notice is never adopted", () => Effect.scoped(Effect.gen(function*() {
  const second = yield* Deferred.make<BootstrapReply>(); const started = yield* Deferred.make<void>();
  const h = harness((calls) => calls === 2 ? Deferred.succeed(started, undefined).pipe(Effect.andThen(Deferred.await(second))) : Effect.succeed(reply(calls === 1 ? "s1" : "s2", 1)));
  const sync = yield* makeBootstrapSync(h.source); const fiber = yield* sync.resync.pipe(Effect.forkChild);
  yield* Deferred.await(started); h.emit(notice(1, "s2")); yield* Deferred.succeed(second, reply("s1", 9));
  const snapshot = yield* Fiber.join(fiber); yield* TestClock.adjust(0);
  if (snapshot.coordinator._tag === "active") { expect(snapshot.coordinator.backendSessionId).toBe("s2"); expect(snapshot.coordinator.activeDraft?.revision).toBe(1); }
  expect(h.calls).toBe(3);
})));

for (const [revision, generation] of [[4, 5], [5, 4], [4, 4], [5, 5], [6, 6]] as const) it.effect(`same-session draft ${revision}/${generation} never regresses revision or article generation`, () => Effect.scoped(Effect.gen(function*() {
  const h = harness((calls) => Effect.succeed(reply("s1", calls === 1 ? 5 : revision, calls === 1 ? 5 : generation)));
  const sync = yield* makeBootstrapSync(h.source); const snapshot = yield* sync.resync;
  if (snapshot.coordinator._tag === "active") { expect(snapshot.coordinator.activeDraft?.revision).toBe(revision === 6 ? 6 : 5); expect(snapshot.coordinator.activeDraft?.articleGeneration).toBe(generation === 6 ? 6 : 5); expect(snapshot.coordinator.results[0]?.revision).toBe(Math.max(5, revision)); }
})));

it.effect("a refresh failure preserves the last confirmed model and explicit resync can recover", () => Effect.scoped(Effect.gen(function*() {
  const failure = new TransportError(); const h = harness((calls) => calls === 2 ? Effect.fail(failure) : Effect.succeed(reply("s1", calls === 1 ? 1 : 2)));
  const sync = yield* makeBootstrapSync(h.source); h.emit(notice(2)); yield* TestClock.adjust(0);
  const failed = yield* sync.snapshot; expect(failed.phase).toBe("failed"); expect(failed.failure?.code).toBe("TransportError");
  if (failed.coordinator._tag === "active") expect(failed.coordinator.activeDraft?.revision).toBe(1);
  expect((yield* sync.resync).phase).toBe("ready"); expect(h.calls).toBe(3);
})));

it.effect("subscription protocol failure blocks without issuing another Bootstrap", () => Effect.scoped(Effect.gen(function*() {
  const h = harness(); const sync = yield* makeBootstrapSync(h.source);
  h.fail(new ProtocolError()); yield* TestClock.adjust(0);
  const snapshot = yield* sync.snapshot; expect(snapshot.phase).toBe("failed"); expect(snapshot.failure?.code).toBe("ProtocolError");
  expect(h.calls).toBe(1); expect(snapshot.coordinator._tag).toBe("active");
})));

for (const corrupt of [
  (value: BootstrapReply) => ({ ...value, protocolVersion: 2 } as unknown as BootstrapReply),
  (value: BootstrapReply) => ({ ...value, backendSessionId: "" }),
  (value: BootstrapReply) => ({ ...value, backendSessionId: "", data: { ...value.data, activeDraft: null } }),
  (value: BootstrapReply) => ({ ...value, data: { ...value.data, activeDraft: { ...value.data.activeDraft!, backendSessionId: "wrong" } } }),
  (value: BootstrapReply) => ({ ...value, data: { ...value.data, pendingOperations: null } }),
]) it.effect(`malformed authoritative reply ${corrupt(reply()).backendSessionId}/${corrupt(reply()).protocolVersion} keeps normal state and fails protocol`, () => Effect.scoped(Effect.gen(function*() {
  const h = harness((calls) => Effect.succeed(calls === 1 ? reply() : corrupt(reply()))); const sync = yield* makeBootstrapSync(h.source);
  const result = yield* Effect.result(sync.resync); expect(result._tag).toBe("Failure");
  if (result._tag === "Failure") expect(result.failure).toBeInstanceOf(ProtocolError);
  const snapshot = yield* sync.snapshot; expect(snapshot.phase).toBe("failed"); expect(snapshot.failure?.code).toBe("ProtocolError");
  if (snapshot.coordinator._tag === "active") expect(snapshot.coordinator.activeDraft?.revision).toBe(1);
})));

for (const fail of [false, true]) it.effect(`overlapping resyncs share one ${fail ? "failed" : "successful"} request`, () => Effect.scoped(Effect.gen(function*() {
  const response = yield* Deferred.make<BootstrapReply, ReadError>(); const started = yield* Deferred.make<void>(); const failure = new TransportError();
  const h = harness((calls) => calls === 1 ? Effect.succeed(reply()) : Deferred.succeed(started, undefined).pipe(Effect.andThen(Deferred.await(response))));
  const sync = yield* makeBootstrapSync(h.source); const first = yield* Effect.result(sync.resync).pipe(Effect.forkChild);
  yield* Deferred.await(started); const second = yield* Effect.result(sync.resync).pipe(Effect.forkChild); yield* TestClock.adjust(0);
  if (fail) yield* Deferred.fail(response, failure); else yield* Deferred.succeed(response, reply("s1", 2));
  const one = yield* Fiber.join(first); const two = yield* Fiber.join(second); expect(one).toEqual(two); expect(h.calls).toBe(2);
})));

it.effect("continuous newer known revisions stop catch-up after three authoritative requests", () => Effect.scoped(Effect.gen(function*() {
  const h = harness((calls) => Effect.sync(() => { h.emit(notice(calls + 1)); return reply("s1", calls); }));
  const sync = yield* makeBootstrapSync(h.source); yield* TestClock.adjust(0);
  expect(h.calls).toBe(3); const snapshot = yield* sync.snapshot; expect(snapshot.phase).toBe("stale");
  if (snapshot.coordinator._tag === "active") { expect(snapshot.coordinator.activeDraft?.revision).toBe(3); expect(snapshot.coordinator.stale[0]?.revision).toBe(4); }
})));

it.effect("continuous session churn is bounded and cannot yield an empty ready model", () => Effect.scoped(Effect.gen(function*() {
  const h = harness((calls) => Effect.sync(() => { h.emit(notice(1, `session-${calls + 1}`)); return reply(`session-${calls}`); }));
  const sync = yield* makeBootstrapSync(h.source); yield* TestClock.adjust(0);
  expect(h.calls).toBe(3); const snapshot = yield* sync.snapshot; expect(snapshot.phase).toBe("stale"); expect(snapshot.coordinator._tag).toBe("awaiting_bootstrap");
})));

it.effect("scope closure interrupts an in-flight refresh and releases its subscription", () => Effect.gen(function*() {
  const started = yield* Deferred.make<void>(); let interrupted = 0;
  const h = harness((calls) => calls === 1 ? Effect.succeed(reply()) : Deferred.succeed(started, undefined).pipe(Effect.andThen(Effect.never), Effect.onInterrupt(() => Effect.sync(() => { interrupted++; }))));
  const owner = yield* Scope.make(); const sync = yield* makeBootstrapSync(h.source).pipe(Effect.provideService(Scope.Scope, owner));
  h.emit(notice(2)); yield* Deferred.await(started); yield* Scope.close(owner, Exit.void);
  expect([h.active, h.released, interrupted]).toEqual([0, 1, 1]); expect((yield* sync.snapshot).phase).toBe("disposed");
}));

it.effect("worker defects produce a fixed safe failure without inspecting secret causes", () => Effect.scoped(Effect.gen(function*() {
  const secret = { get password() { throw new Error("SECRET_PASSWORD"); } };
  const h = harness((calls) => calls === 1 ? Effect.succeed(reply()) : Effect.die(secret)); const sync = yield* makeBootstrapSync(h.source);
  h.emit(notice(2)); yield* TestClock.adjust(0); const snapshot = yield* sync.snapshot;
  expect(snapshot.phase).toBe("failed"); expect(snapshot.failure?.diagnosticId).toBe("FE-UNEXPECTED"); expect(JSON.stringify(snapshot.failure)).not.toContain("SECRET");
})));

it.effect("one hundred scope lifetimes leave no subscriptions or late callback work", () => Effect.gen(function*() {
  const h = harness();
  for (let index = 0; index < 100; index++) yield* Effect.scoped(makeBootstrapSync(h.source));
  h.emit(notice(8)); h.fail(new ProtocolError()); yield* TestClock.adjust(0);
  expect([h.calls, h.active, h.released]).toEqual([100, 0, 100]);
}));

it.effect("collection hints read actual result and pending descriptor summaries", () => Effect.scoped(Effect.gen(function*() {
  const pendingReply = (revision: number): BootstrapReply => ({ ...reply(), data: { ...reply().data, pendingOperations: [{ operationId: "o1", kind: "Rerun", collectionId: "pending-collection", roundId: "round", status: "pending", revision }] } });
  const h = harness((calls) => Effect.succeed(pendingReply(calls === 1 ? 3 : calls === 2 ? 2 : 4)));
  const sync = yield* makeBootstrapSync(h.source);
  const lower = yield* sync.resync;
  if (lower.coordinator._tag === "active") expect(lower.coordinator.pending[0]?.descriptor.revision).toBe(3);
  h.emit(notice(3, "s1", "pending-collection", "collection")); yield* TestClock.adjust(0); expect(h.calls).toBe(2);
  h.emit(notice(4, "s1", "pending-collection", "collection")); yield* TestClock.adjust(0);
  const latest = yield* sync.snapshot; expect(latest.phase).toBe("ready");
  if (latest.coordinator._tag === "active") expect(latest.coordinator.pending[0]?.descriptor.revision).toBe(4);
  expect(h.calls).toBe(3);
})));

it.effect("new and retargeted pending identities do not inherit another target revision", () => Effect.scoped(Effect.gen(function*() {
  const pendingReply = (calls: number): BootstrapReply => ({ ...reply(), data: { ...reply().data, pendingOperations: [{ operationId: calls === 3 ? "other-operation" : "o1", kind: "Rerun", collectionId: calls === 2 ? "other-collection" : "c1", roundId: calls === 4 ? "other-round" : "r1", status: "pending", revision: calls === 1 ? 9 : 1 }] } });
  const h = harness((calls) => Effect.succeed(pendingReply(calls))); const sync = yield* makeBootstrapSync(h.source);
  for (let index = 0; index < 3; index++) {
    const snapshot = yield* sync.resync;
    if (snapshot.coordinator._tag === "active") expect(snapshot.coordinator.pending[0]?.descriptor.revision).toBe(1);
  }
})));

it.effect("same target pending summary may advance its revision", () => Effect.scoped(Effect.gen(function*() {
  const h = harness((calls) => Effect.succeed({ ...reply(), data: { ...reply().data, pendingOperations: [{ operationId: "o1", kind: "Rerun", collectionId: "c1", roundId: "r1", status: "pending", revision: calls }] } }));
  const sync = yield* makeBootstrapSync(h.source); const snapshot = yield* sync.resync;
  if (snapshot.coordinator._tag === "active") expect(snapshot.coordinator.pending[0]?.descriptor.revision).toBe(2);
})));

it.effect("a subscription protocol error during Bootstrap rejects the in-flight reply", () => Effect.scoped(Effect.gen(function*() {
  const response = yield* Deferred.make<BootstrapReply>(); const started = yield* Deferred.make<void>();
  const h = harness((calls) => calls === 1 ? Effect.succeed(reply()) : Deferred.succeed(started, undefined).pipe(Effect.andThen(Deferred.await(response))));
  const sync = yield* makeBootstrapSync(h.source); const fiber = yield* Effect.result(sync.resync).pipe(Effect.forkChild);
  yield* Deferred.await(started); h.fail(new ProtocolError()); yield* Deferred.succeed(response, reply("s1", 9));
  expect((yield* Fiber.join(fiber))._tag).toBe("Failure"); yield* TestClock.adjust(0);
  const snapshot = yield* sync.snapshot; expect(snapshot.phase).toBe("failed"); expect(snapshot.failure?.code).toBe("ProtocolError");
  if (snapshot.coordinator._tag === "active") expect(snapshot.coordinator.activeDraft?.revision).toBe(1);
  expect(h.calls).toBe(2);
})));

it.effect("a retired-session reply never reverts the accepted newer session", () => Effect.scoped(Effect.gen(function*() {
  const h = harness((calls) => Effect.succeed(reply(calls === 1 || calls >= 3 ? "s1" : "s2")));
  const sync = yield* makeBootstrapSync(h.source); h.emit(notice(1, "s2")); yield* TestClock.adjust(0);
  const snapshot = yield* sync.resync; expect(snapshot.phase).toBe("stale");
  if (snapshot.coordinator._tag === "active") expect(snapshot.coordinator.backendSessionId).toBe("s2");
  expect(h.calls).toBe(5);
})));

it.effect("repeated unknown hints retain the highest revision without unbounded reads", () => Effect.scoped(Effect.gen(function*() {
  const response = yield* Deferred.make<BootstrapReply>(); const started = yield* Deferred.make<void>();
  const h = harness((calls) => calls === 1 ? Effect.succeed(reply()) : Deferred.succeed(started, undefined).pipe(Effect.andThen(Deferred.await(response))));
  const sync = yield* makeBootstrapSync(h.source); h.emit(notice(2, "s1", "unknown", "collection")); yield* Deferred.await(started);
  h.emit(notice(1, "s1", "unknown", "collection")); h.emit(notice(3, "s1", "unknown", "collection")); yield* Deferred.succeed(response, reply()); yield* TestClock.adjust(0);
  const snapshot = yield* sync.snapshot;
  if (snapshot.coordinator._tag === "active") expect(snapshot.coordinator.stale[0]?.revision).toBe(3);
  expect(h.calls).toBe(2);
})));

it.effect("more than 64 accepted sessions keep retired-session tracking bounded", () => Effect.scoped(Effect.gen(function*() {
  const h = harness((calls) => Effect.succeed(reply(`session-${calls}`))); const sync = yield* makeBootstrapSync(h.source);
  for (let index = 0; index < 66; index++) yield* sync.resync;
  const snapshot = yield* sync.snapshot; expect(snapshot.phase).toBe("ready");
  if (snapshot.coordinator._tag === "active") expect(snapshot.coordinator.backendSessionId).toBe("session-67");
})));

it.effect("Bootstrap accepts a changed or absent draft and a new result identity", () => Effect.scoped(Effect.gen(function*() {
  const h = harness((calls) => Effect.succeed({ ...reply(), data: { ...reply().data,
    activeDraft: calls === 1 ? reply().data.activeDraft : calls === 2 ? { ...reply().data.activeDraft!, draftId: "d2" } : null,
    recentResults: [{ collectionId: `collection-${calls}`, roundId: `round-${calls}`, revision: 1 }],
  } }));
  const sync = yield* makeBootstrapSync(h.source); const changed = yield* sync.resync;
  if (changed.coordinator._tag === "active") expect(changed.coordinator.activeDraft?.draftId).toBe("d2");
  const absent = yield* sync.resync;
  if (absent.coordinator._tag === "active") expect(absent.coordinator.activeDraft).toBe(null);
})));

it.effect("an unknown draft stays stale without borrowing the active draft revision", () => Effect.scoped(Effect.gen(function*() {
  const h = harness(() => Effect.succeed(reply("s1", 99))); const sync = yield* makeBootstrapSync(h.source);
  h.emit(notice(1, "s1", "other-draft")); yield* TestClock.adjust(0);
  const snapshot = yield* sync.snapshot; expect(snapshot.phase).toBe("stale");
  if (snapshot.coordinator._tag === "active") expect(snapshot.coordinator.stale).toEqual([notice(1, "s1", "other-draft")]);
  expect(h.calls).toBe(2);
})));

it.effect("a pre-request unrelated session notice cannot choose the authoritative session", () => Effect.scoped(Effect.gen(function*() {
  const h = harness();
  const source: BootstrapSource = { ...h.source, subscribeStateChanges: (listener, onError) => h.source.subscribeStateChanges(listener, onError).pipe(Effect.andThen(Effect.sync(() => h.emit(notice(99, "unconfirmed-session"))))) };
  const sync = yield* makeBootstrapSync(source); yield* TestClock.adjust(0);
  const snapshot = yield* sync.snapshot; expect(snapshot.phase).toBe("ready");
  if (snapshot.coordinator._tag === "active") expect(snapshot.coordinator.backendSessionId).toBe("s1");
  expect(h.calls).toBe(1);
})));

for (const different of ["operationId", "collectionId", "roundId"] as const) it.effect(`pending ${different} difference keeps identities distinct`, () => Effect.scoped(Effect.gen(function*() {
  const descriptor = { operationId: "o1", collectionId: "c1", roundId: "r1", kind: "Rerun", status: "pending" as const, revision: 9 };
  const h = harness((calls) => Effect.succeed({ ...reply(), data: { ...reply().data, pendingOperations: [{ ...descriptor, ...(calls === 1 ? {} : { [different]: "other", revision: 1 }) }] } }));
  const sync = yield* makeBootstrapSync(h.source); const snapshot = yield* sync.resync;
  if (snapshot.coordinator._tag === "active") expect(snapshot.coordinator.pending[0]?.descriptor.revision).toBe(1);
})));

it.effect("an already-stale identical unknown hint performs no redundant read", () => Effect.scoped(Effect.gen(function*() {
  const h = harness(); const sync = yield* makeBootstrapSync(h.source); const hint = notice(2, "s1", "unknown", "collection");
  h.emit(hint); yield* TestClock.adjust(0); h.emit(hint); yield* TestClock.adjust(0);
  expect(h.calls).toBe(2); expect((yield* sync.snapshot).phase).toBe("stale");
})));

for (const [resultRevision, pendingRevision] of [[8, 3], [3, 8]] as const) it.effect(`known collection revision uses maximum of results ${resultRevision} and pending ${pendingRevision}`, () => Effect.scoped(Effect.gen(function*() {
  const h = harness(() => Effect.succeed({ ...reply(), data: { ...reply().data, recentResults: [{ collectionId: "c1", roundId: "r1", revision: resultRevision }], pendingOperations: [{ operationId: "o1", kind: "Rerun", collectionId: "c1", roundId: "r2", status: "pending", revision: pendingRevision }] } }));
  const sync = yield* makeBootstrapSync(h.source); h.emit(notice(7, "s1", "c1", "collection")); yield* TestClock.adjust(0);
  expect(h.calls).toBe(1); expect((yield* sync.snapshot).phase).toBe("ready");
})));

it.effect("a typed worker failure leaves the worker available for the next notice", () => Effect.scoped(Effect.gen(function*() {
  const h = harness((calls) => calls === 2 ? Effect.fail(new TransportError()) : Effect.succeed(reply("s1", calls === 1 ? 1 : 3)));
  const sync = yield* makeBootstrapSync(h.source); h.emit(notice(2)); yield* TestClock.adjust(0);
  expect((yield* sync.snapshot).phase).toBe("failed"); h.emit(notice(3)); yield* TestClock.adjust(0);
  expect((yield* sync.snapshot).phase).toBe("ready"); expect(h.calls).toBe(3);
})));

for (const overflow of [false, true]) it.effect(`mixed session hints retain the 65th hint and expose ${overflow ? "overflow" : "no overflow"}`, () => Effect.scoped(Effect.gen(function*() {
  const response = yield* Deferred.make<BootstrapReply>(); const started = yield* Deferred.make<void>();
  const h = harness((calls) => calls === 1 ? Deferred.succeed(started, undefined).pipe(Effect.andThen(Deferred.await(response))) : Effect.succeed(reply("s1", 5)));
  const fiber = yield* makeBootstrapSync(h.source).pipe(Effect.forkChild); yield* Deferred.await(started);
  for (let index = 0; index < 64; index++) h.emit(notice(1, "unconfirmed-session", `unknown-${index}`, "collection"));
  h.emit(notice(3)); if (overflow) h.emit(notice(5, "s1", "other-draft"));
  yield* Deferred.succeed(response, reply()); const sync = yield* Fiber.join(fiber); yield* TestClock.adjust(0);
  const snapshot = yield* sync.snapshot; expect(snapshot.phase).toBe(overflow ? "stale" : "ready");
  if (snapshot.coordinator._tag === "active") expect(snapshot.coordinator.staleOverflow).toBe(overflow);
  expect(h.calls).toBe(2);
})));
