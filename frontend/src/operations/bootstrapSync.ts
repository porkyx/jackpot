import { Effect, Exit, Queue, Scope, Semaphore, SubscriptionRef, type Stream } from "effect";
import { ProtocolError, type Backend, type BootstrapReply, type StateNotice } from "../contracts/backend";
import { projectDefect, projectError, type ErrorProjection } from "../app/errors";
import type { ReadError } from "./readRetry";
import { coordinatorFromBootstrap, initialCoordinatorState, markConfirmedEntityStale, type ActiveCoordinatorState, type CoordinatorState } from "./coordinatorState";

export interface BootstrapSource {
  readonly bootstrap: () => Effect.Effect<BootstrapReply, ReadError>;
  readonly subscribeStateChanges: typeof Backend.Service["subscribeStateChanges"];
}
export interface BootstrapSyncSnapshot {
  readonly phase: "starting" | "refreshing" | "ready" | "stale" | "failed" | "disposed";
  readonly coordinator: CoordinatorState;
  readonly failure: ErrorProjection | null;
}
export interface BootstrapSync {
  readonly snapshot: Effect.Effect<BootstrapSyncSnapshot>;
  readonly changes: Stream.Stream<BootstrapSyncSnapshot>;
  readonly resync: Effect.Effect<BootstrapSyncSnapshot, ReadError>;
}
interface Hint { readonly notice: StateNotice; readonly sequence: number }

function knownRevision(state: ActiveCoordinatorState, notice: StateNotice): number | undefined {
  if (notice.entityKind === "draft") return state.activeDraft?.draftId === notice.entityId ? state.activeDraft.revision : undefined;
  const revisions = [...state.results.filter((result) => result.collectionId === notice.entityId).map((result) => result.revision), ...state.pending.filter((pending) => pending.descriptor.collectionId === notice.entityId).map((pending) => pending.descriptor.revision)];
  return revisions.length === 0 ? undefined : Math.max(...revisions);
}

function monotone(previous: CoordinatorState, candidate: ActiveCoordinatorState): ActiveCoordinatorState {
  if (previous._tag !== "active" || previous.backendSessionId !== candidate.backendSessionId) return candidate;
  const oldDraft = previous.activeDraft;
  const newDraft = candidate.activeDraft;
  const activeDraft = oldDraft !== null && newDraft !== null && oldDraft.draftId === newDraft.draftId && (newDraft.revision < oldDraft.revision || newDraft.articleGeneration < oldDraft.articleGeneration) ? oldDraft : newDraft;
  const results = candidate.results.map((result) => {
    const old = previous.results.find((entry) => entry.collectionId === result.collectionId && entry.roundId === result.roundId);
    return old !== undefined && old.revision > result.revision ? old : result;
  });
  const pending = candidate.pending.map((operation) => {
    const old = previous.pending.find((entry) => entry.descriptor.operationId === operation.descriptor.operationId && entry.descriptor.collectionId === operation.descriptor.collectionId && entry.descriptor.roundId === operation.descriptor.roundId);
    return old !== undefined && old.descriptor.revision > operation.descriptor.revision ? old : operation;
  });
  return Object.freeze({ ...candidate, activeDraft, results: Object.freeze(results), pending: Object.freeze(pending) });
}

// The owner passes the already-policy-managed Bootstrap query. This module
// never wraps it in another retry or invents entity queries that Go lacks.
export const makeBootstrapSync = (source: BootstrapSource): Effect.Effect<BootstrapSync, ReadError, Scope.Scope> => Effect.gen(function*() {
  const owned = yield* Scope.make();
  const wake = yield* Queue.make<void>({ capacity: 1, strategy: "sliding" });
  const lock = yield* Semaphore.make(1);
  const state = yield* SubscriptionRef.make<BootstrapSyncSnapshot>(Object.freeze({ phase: "starting", coordinator: initialCoordinatorState(), failure: null }));
  const hints = new Map<string, Hint>();
  const retired = new Set<string>();
  let sequence = 0;
  let completed = 0;
  let appliedSequence = 0;
  let lastError: ReadError | null = null;
  let noticeError: ReadError | null = null;
  let overflow = false;
  let closed = false;
  const publish = (coordinator: CoordinatorState, phase: BootstrapSyncSnapshot["phase"], failure: ErrorProjection | null = null) => SubscriptionRef.set(state, Object.freeze({ coordinator, phase, failure }));
  const close = Effect.suspend(() => {
    if (closed) return Effect.void;
    closed = true;
    return Scope.close(owned, Exit.void).pipe(Effect.andThen(Queue.shutdown(wake)), Effect.andThen(publish(Object.freeze({ _tag: "disposed" }), "disposed")));
  });
  yield* Effect.addFinalizer(() => close);

  const refresh = Effect.gen(function*() {
    const original = yield* SubscriptionRef.get(state);
    yield* publish(original.coordinator, "refreshing");
    for (let round = 0; round < 3; round++) {
      if (noticeError !== null) { const error = noticeError; noticeError = null; return yield* Effect.fail(error); }
      const requestedAt = sequence;
      const reply = yield* source.bootstrap();
      if (noticeError !== null) { const error = noticeError; noticeError = null; return yield* Effect.fail(error); }
      const candidate = yield* Effect.try({ try: () => {
        if (reply.protocolVersion !== 1 || reply.backendSessionId === "") throw new Error("Invalid Bootstrap header");
        return coordinatorFromBootstrap(reply);
      }, catch: () => new ProtocolError() });
      if (retired.has(candidate.backendSessionId)) continue;
      // An unfamiliar session hint during the request can make this reply old.
      // A fresh authoritative request, rather than the hint, decides the session.
      if ([...hints.values()].some((hint) => hint.sequence > requestedAt && hint.notice.backendSessionId !== reply.backendSessionId)) continue;
      const previous = (yield* SubscriptionRef.get(state)).coordinator;
      if (previous._tag === "active" && previous.backendSessionId !== candidate.backendSessionId) {
        retired.add(previous.backendSessionId);
        if (retired.size > 64) retired.delete(retired.values().next().value!);
      }
      let confirmed = monotone(previous, candidate);
      let needsKnownRead = false;
      for (const [key, hint] of hints) {
        if (hint.notice.backendSessionId !== reply.backendSessionId) { hints.delete(key); continue; }
        const revision = knownRevision(confirmed, hint.notice);
        if (revision !== undefined && revision >= hint.notice.revision) { hints.delete(key); continue; }
        confirmed = markConfirmedEntityStale(confirmed, hint.notice) as ActiveCoordinatorState;
        if (revision !== undefined) needsKnownRead = true;
      }
      if (overflow) confirmed = Object.freeze({ ...confirmed, staleOverflow: true });
      const stale = confirmed.stale.length > 0 || confirmed.staleOverflow;
      appliedSequence = sequence;
      yield* publish(confirmed, stale ? "stale" : "ready");
      if (!needsKnownRead) return yield* SubscriptionRef.get(state);
    }
    const current = yield* SubscriptionRef.get(state);
    appliedSequence = sequence;
    yield* publish(current.coordinator, "stale");
    return yield* SubscriptionRef.get(state);
  });
  const resync: Effect.Effect<BootstrapSyncSnapshot, ReadError> = Effect.gen(function*() {
    const epoch = completed;
    return yield* lock.withPermit(Effect.gen(function*() {
      if (closed) return yield* Effect.interrupt;
      if (completed !== epoch) {
        if (lastError !== null) return yield* Effect.fail(lastError);
        return yield* SubscriptionRef.get(state);
      }
      const result = yield* Effect.result(refresh);
      completed++;
      if (result._tag === "Failure") {
        lastError = result.failure;
        appliedSequence = sequence;
        const current = yield* SubscriptionRef.get(state);
        yield* publish(current.coordinator, "failed", projectError(result.failure));
        return yield* Effect.fail(result.failure);
      }
      lastError = null;
      return result.success;
    }));
  });
  const acquired = Effect.gen(function*() {
    yield* source.subscribeStateChanges((notice) => {
      if (closed || retired.has(notice.backendSessionId)) return;
      const current = SubscriptionRef.getUnsafe(state).coordinator;
      // Notices from a different session never replace confirmed state.
      const key = JSON.stringify([notice.backendSessionId, notice.entityKind, notice.entityId]);
      const previous = hints.get(key);
      if (previous !== undefined && previous.notice.revision >= notice.revision) return;
      if (current._tag === "active" && current.backendSessionId === notice.backendSessionId) {
        const revision = knownRevision(current, notice);
        if (revision !== undefined && revision >= notice.revision) return;
      }
      sequence++;
      if (hints.size < 65 || previous !== undefined) hints.set(key, { notice: Object.freeze({ ...notice }), sequence });
      else overflow = true;
      Queue.offerUnsafe(wake, undefined);
    }, (error) => {
      if (closed) return;
      noticeError = error;
      Queue.offerUnsafe(wake, undefined);
    });
    yield* resync;
    yield* Effect.gen(function*() {
      while (true) {
        yield* Queue.take(wake);
        if (sequence === appliedSequence && noticeError === null) continue;
        yield* resync.pipe(Effect.catch(() => Effect.void), Effect.catchDefect(() => {
          return SubscriptionRef.get(state).pipe(Effect.flatMap((current) => publish(current.coordinator, "failed", projectDefect(undefined))));
        }));
      }
    }).pipe(Effect.forkIn(owned));
    return { snapshot: SubscriptionRef.get(state), changes: SubscriptionRef.changes(state), resync };
  }).pipe(Effect.provideService(Scope.Scope, owned), Effect.onError(() => close));
  return yield* acquired;
});
