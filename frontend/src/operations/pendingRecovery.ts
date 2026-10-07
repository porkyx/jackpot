import { Data, Deferred, Effect, Exit, Fiber, PubSub, Schema, Scope, Semaphore, SubscriptionRef, type Stream } from "effect";
import { projectError, ReadTimeout, type ErrorProjection } from "../app/errors";
import { BackendRejected, ProtocolError, type BootstrapReply, type OperationLookupReply, type PendingOperationsReply } from "../contracts/backend";
import { BootstrapData, OperationDescriptor, OperationObservation, PendingOperationsPage } from "../contracts/schemas";
import type { OperationCoordinator, Received } from "./coordinator";
import type { ReadError } from "./readRetry";

export class RecoveryUnavailable extends Data.TaggedError("RecoveryUnavailable")<{ readonly reason: "closed" | "not_tracked" | "bootstrap_required" }> {}
export type RecoveryError = ReadError | RecoveryUnavailable;
export type RecoverySource = Pick<typeof OperationCoordinator.Service, "bootstrap" | "listPendingOperations" | "getOperation">;
type Descriptor = typeof OperationDescriptor.Type;
type Observation = typeof OperationObservation.Type;
export interface RecoveryEntry {
  readonly descriptor: Descriptor;
  readonly observation: Observation | null;
  readonly failure: ErrorProjection | null;
}
export interface RecoverySnapshot {
  readonly phase: "starting" | "enumerating" | "saturated" | "ready" | "failed" | "session_changed" | "disposed";
  readonly backendSessionId: string | null;
  readonly pending: ReadonlyArray<RecoveryEntry>;
  readonly terminal: ReadonlyArray<Observation>;
  readonly cursor: string | null;
  readonly enumerationComplete: boolean;
  readonly failure: ErrorProjection | null;
  readonly readsAllowed: boolean;
  readonly writesBlocked: boolean;
}
export interface PendingRecovery {
  readonly snapshot: Effect.Effect<RecoverySnapshot>;
  readonly changes: Stream.Stream<RecoverySnapshot>;
  readonly acceptBootstrap: (reply: Received<BootstrapReply>) => Effect.Effect<RecoverySnapshot, RecoveryError>;
  readonly resync: Effect.Effect<RecoverySnapshot, RecoveryError>;
  readonly resume: Effect.Effect<RecoverySnapshot, RecoveryError>;
  readonly requery: (id: string) => Effect.Effect<RecoverySnapshot, RecoveryError>;
  readonly canMutateCollection: (id: string) => Effect.Effect<boolean>;
}
const decodeBootstrap = Schema.decodeUnknownSync(BootstrapData);
const decodePage = Schema.decodeUnknownSync(PendingOperationsPage);
const decodeOperation = Schema.decodeUnknownSync(OperationObservation);
function ownEntry(descriptor: Descriptor): RecoveryEntry { return Object.freeze({ descriptor: Object.freeze({ ...descriptor }), observation: null, failure: null }); }
function phase(snapshot: RecoverySnapshot): RecoverySnapshot {
  const nextPhase = snapshot.failure !== null ? snapshot.phase : snapshot.pending.length >= 64 ? "saturated" : snapshot.enumerationComplete ? "ready" : "enumerating";
  return Object.freeze({ ...snapshot, phase: nextPhase,
    writesBlocked: snapshot.failure !== null || !snapshot.enumerationComplete || snapshot.pending.length >= 64,
    pending: Object.freeze([...snapshot.pending]), terminal: Object.freeze([...snapshot.terminal]),
  });
}
function header(reply: Received<BootstrapReply | PendingOperationsReply | OperationLookupReply>): void {
  if (reply.reply.protocolVersion !== 1 || reply.reply.backendSessionId.length === 0 || !Number.isFinite(reply.receivedAtMillis) || reply.receivedAtMillis < 0) throw new ProtocolError();
}
function merge(entries: ReadonlyArray<RecoveryEntry>, descriptors: ReadonlyArray<Descriptor>, terminal: ReadonlyArray<Observation>): ReadonlyArray<RecoveryEntry> {
  const next = new Map(entries.map((entry) => [entry.descriptor.operationId, entry]));
  const seen = new Set<string>();
  for (const descriptor of descriptors) {
    if (seen.has(descriptor.operationId)) throw new ProtocolError();
    seen.add(descriptor.operationId);
    if (terminal.some((item) => item.operationId === descriptor.operationId)) continue;
    const previous = next.get(descriptor.operationId);
    if (previous !== undefined) {
      if (previous.descriptor.kind !== descriptor.kind || previous.descriptor.collectionId !== descriptor.collectionId || previous.descriptor.roundId !== descriptor.roundId) throw new ProtocolError();
      if (descriptor.revision > previous.descriptor.revision) next.set(descriptor.operationId, Object.freeze({ ...previous, descriptor: Object.freeze({ ...descriptor }) }));
    } else next.set(descriptor.operationId, ownEntry(descriptor));
  }
  return [...next.values()];
}

// This observes durable metadata only. No admission, payload queue, secret or
// server deletion is exposed. The caller runs resume in the app scope after
// construction; starting/enumerating are never reported as boot completion.
export const makePendingRecovery = (source: RecoverySource, initial?: Received<BootstrapReply>): Effect.Effect<PendingRecovery, RecoveryError, Scope.Scope> => Effect.gen(function*() {
  const parent = yield* Scope.Scope;
  if (parent.state._tag === "Closed") return yield* Effect.fail(new RecoveryUnavailable({ reason: "closed" }));
  const owner = yield* Scope.fork(parent, "sequential");
  let work = yield* Scope.fork(owner, "sequential");
  const lock = yield* Semaphore.make(1);
  const state = yield* SubscriptionRef.make<RecoverySnapshot>(Object.freeze({ phase: "starting", backendSessionId: null, pending: [], terminal: [], cursor: null, enumerationComplete: false, failure: null, readsAllowed: true, writesBlocked: true }));
  let generation = Symbol();
  const retired = new Set<string>();
  const flights = new Map<string, Deferred.Deferred<RecoverySnapshot, RecoveryError>>();
  yield* Scope.addFinalizer(owner, Effect.suspend(() => {
    generation = Symbol(); retired.clear(); flights.clear();
    return SubscriptionRef.set(state, Object.freeze({ phase: "disposed", backendSessionId: null, pending: [], terminal: [], cursor: null, enumerationComplete: false, failure: null, readsAllowed: false, writesBlocked: true })).pipe(Effect.andThen(PubSub.shutdown(state.pubsub)));
  }));
  const guard = Effect.suspend(() => owner.state._tag === "Closed" ? Effect.fail(new RecoveryUnavailable({ reason: "closed" })) : Effect.void);
  const query = <A, E>(effect: Effect.Effect<A, E>): Effect.Effect<A, E> => effect.pipe(Effect.forkIn(work), Effect.flatMap(Fiber.join));
  const owned = <A, E>(effect: Effect.Effect<A, E>): Effect.Effect<A, E | RecoveryUnavailable> => guard.pipe(Effect.andThen(effect.pipe(Effect.forkIn(owner), Effect.flatMap(Fiber.join))));
  const fresh = (token: symbol) => Effect.suspend(() => token !== generation || owner.state._tag === "Closed" ? Effect.interrupt : Effect.void);
  const updateFresh = (token: symbol, change: (current: RecoverySnapshot) => RecoverySnapshot) => SubscriptionRef.update(state, (current) => token !== generation || owner.state._tag === "Closed" ? current : change(current));
  const recordFailure = (error: RecoveryError) => error._tag === "RecoveryUnavailable" ? Effect.void : SubscriptionRef.update(state, (current) => owner.state._tag === "Closed" ? current : Object.freeze({ ...current,
    phase: error._tag === "BackendRejected" && error.code === "BackendSessionChanged" ? "session_changed" : "failed",
    failure: projectError(error), writesBlocked: true,
  }));
  const acceptBootstrap = (received: Received<BootstrapReply>): Effect.Effect<RecoverySnapshot, RecoveryError> => Effect.gen(function*() {
    yield* guard;
    const previous = yield* SubscriptionRef.get(state);
    const next = yield* Effect.try({ try: () => {
      header(received);
      if (retired.has(received.reply.backendSessionId)) throw new ProtocolError();
      const data = decodeBootstrap(received.reply.data);
      if (data.pendingOperations === null || data.recentResults === null || (data.activeDraft !== null && data.activeDraft.backendSessionId !== received.reply.backendSessionId)) throw new ProtocolError();
      const same = previous.backendSessionId === received.reply.backendSessionId;
      const terminal = same ? previous.terminal : [];
      // Durable IDs outlive a Go session. Only observations and read failures
      // expire; a descriptor leaves its slot after terminal confirmation.
      const previousEntries = same ? previous.pending : previous.pending.map((entry) => Object.freeze({ ...entry, observation: null, failure: null }));
      const combined = merge(previousEntries, data.pendingOperations, terminal);
      const overflow = combined.length > 64;
      return phase({ ...previous, backendSessionId: received.reply.backendSessionId, pending: overflow ? previousEntries : combined, terminal,
        cursor: overflow ? null : data.pendingCursor, enumerationComplete: !overflow && data.pendingCursor === null,
        failure: null, readsAllowed: true,
      });
    }, catch: () => new ProtocolError() });
    if (previous.backendSessionId !== null && previous.backendSessionId !== next.backendSessionId) {
      retired.add(previous.backendSessionId);
      if (retired.size > 64) retired.delete(retired.values().next().value!);
    }
    generation = Symbol();
    const old = work; work = yield* Scope.fork(owner, "sequential");
    yield* SubscriptionRef.set(state, next);
    yield* Scope.close(old, Exit.void);
    return next;
  }).pipe(Effect.catch((error) => recordFailure(error).pipe(Effect.andThen(Effect.fail(error)))));
  const observe = (id: string, token: symbol): Effect.Effect<void, RecoveryError> => Effect.gen(function*() {
    yield* fresh(token);
    const original = yield* SubscriptionRef.get(state);
    const entry = original.pending.find((pending) => pending.descriptor.operationId === id);
    if (entry === undefined) return yield* Effect.fail(new RecoveryUnavailable({ reason: "not_tracked" }));
    const checked = Effect.gen(function*() {
      const reply = yield* query(source.getOperation(id).pipe(Effect.timeoutOrElse({ duration: 15000, orElse: () => Effect.fail(new ReadTimeout()) })));
      yield* fresh(token);
      yield* Effect.try({ try: () => header(reply), catch: () => new ProtocolError() });
      if (reply.reply.backendSessionId !== original.backendSessionId) {
        const error = new BackendRejected({ code: "BackendSessionChanged", messageKey: "BackendSessionChanged" });
        yield* updateFresh(token, (current) => Object.freeze({ ...current, phase: "session_changed", failure: projectError(error), writesBlocked: true }));
        return yield* Effect.fail(error);
      }
      const observation = yield* Effect.try({ try: () => {
        const data = decodeOperation(reply.reply.data);
        const descriptor = entry.descriptor;
        const minimum = Math.max(descriptor.revision, entry.observation?.revision ?? 0);
        if (data.operationId !== id || (data.state !== "unknown" && (data.kind !== descriptor.kind || data.collectionId !== descriptor.collectionId || data.roundId !== descriptor.roundId || data.revision === null || data.revision < minimum))) throw new ProtocolError();
        return Object.freeze({ ...data });
      }, catch: () => new ProtocolError() });
      yield* fresh(token);
      yield* updateFresh(token, (current) => {
        if (observation.state === "succeeded" || observation.state === "failed") return phase({ ...current, pending: current.pending.filter((item) => item.descriptor.operationId !== id), terminal: [...current.terminal.filter((item) => item.operationId !== id), observation].slice(-128) });
        return phase({ ...current, pending: current.pending.map((item) => item.descriptor.operationId === id ? Object.freeze({ ...item, observation, failure: null }) : item) });
      });
    });
    yield* checked.pipe(Effect.catch((error) => fresh(token).pipe(Effect.andThen(updateFresh(token, (current) => Object.freeze({ ...current, pending: Object.freeze(current.pending.map((item) => item.descriptor.operationId === id ? Object.freeze({ ...item, failure: projectError(error) }) : item)) }))), Effect.andThen(Effect.fail(error)))));
  });
  const ensureBootstrap = Effect.suspend(() => SubscriptionRef.get(state).pipe(Effect.flatMap((current) => current.backendSessionId === null || current.phase === "session_changed" ? Effect.fail(new RecoveryUnavailable({ reason: "bootstrap_required" })) : Effect.void)));
  const drain = (token: symbol): Effect.Effect<void, RecoveryError> => Effect.gen(function*() {
    // A bounded sweep also terminates for a malformed cursor cycle. Keeping the
    // cursor permits an explicit next sweep; it never fabricates completion.
    for (let pages = 0; pages < 64; pages++) {
      yield* fresh(token); yield* ensureBootstrap;
      const current = yield* SubscriptionRef.get(state);
      if (current.enumerationComplete || current.pending.length >= 64) return;
      const limit = 64 - current.pending.length;
      const reply = yield* query(source.listPendingOperations({ cursor: current.cursor, limit }));
      yield* fresh(token);
      const next = yield* Effect.try({ try: () => {
        header(reply);
        if (reply.reply.backendSessionId !== current.backendSessionId) throw new BackendRejected({ code: "BackendSessionChanged", messageKey: "BackendSessionChanged" });
        const page = decodePage(reply.reply.data);
        if (page.operations === null || page.operations.length > limit || (page.cursor !== null && page.cursor === current.cursor)) throw new ProtocolError();
        return phase({ ...current, pending: merge(current.pending, page.operations, current.terminal), cursor: page.cursor, enumerationComplete: page.cursor === null, failure: null });
      }, catch: (error) => error instanceof BackendRejected ? error : new ProtocolError() });
      yield* updateFresh(token, () => next);
      for (const entry of next.pending) if (!current.pending.some((item) => item.descriptor.operationId === entry.descriptor.operationId)) {
        yield* observe(entry.descriptor.operationId, token).pipe(Effect.catch(() => Effect.void));
        yield* ensureBootstrap;
      }
    }
  }).pipe(Effect.catch((error) => fresh(token).pipe(Effect.andThen(recordFailure(error)), Effect.andThen(Effect.fail(error)))));
  const sweep = Effect.gen(function*() {
    yield* ensureBootstrap;
    const token = generation;
    const ids = (yield* SubscriptionRef.get(state)).pending.map((entry) => entry.descriptor.operationId);
    for (const id of ids) {
      yield* observe(id, token).pipe(Effect.catch(() => Effect.void));
      yield* ensureBootstrap;
    }
    yield* drain(token);
    return yield* SubscriptionRef.get(state);
  });
  const resume = owned(lock.withPermit(sweep));
  const resync = owned(lock.withPermit(Effect.gen(function*() {
    const token = generation;
    const reply = yield* query(source.bootstrap());
    yield* fresh(token);
    yield* acceptBootstrap(reply);
    return yield* sweep;
  }).pipe(Effect.catch((error) => recordFailure(error).pipe(Effect.andThen(Effect.fail(error)))))));
  const ownerApi: PendingRecovery = {
    snapshot: SubscriptionRef.get(state), changes: SubscriptionRef.changes(state), acceptBootstrap,
    resync, resume,
    requery: (id) => guard.pipe(Effect.andThen(Effect.suspend(() => {
      const existing = flights.get(id);
      if (existing !== undefined) return Deferred.await(existing);
      return Effect.gen(function*() {
        const completion = yield* Deferred.make<RecoverySnapshot, RecoveryError>();
        flights.set(id, completion);
        yield* lock.withPermit(Effect.gen(function*() {
          yield* ensureBootstrap;
          const token = generation;
          yield* observe(id, token); yield* drain(token);
          return yield* SubscriptionRef.get(state);
        })).pipe(Effect.onExit((exit) => Effect.sync(() => {
          flights.delete(id);
          Deferred.doneUnsafe(completion, Exit.isSuccess(exit) ? Effect.succeed(exit.value) : Effect.failCause(exit.cause));
        })), Effect.forkIn(owner));
        return yield* Deferred.await(completion);
      });
    }))),
    canMutateCollection: (id) => SubscriptionRef.get(state).pipe(Effect.map((current) => id.length > 0 && !current.writesBlocked && !current.pending.some((entry) => entry.descriptor.collectionId === id))),
  };
  if (initial !== undefined) yield* acceptBootstrap(initial).pipe(Effect.onError(() => Scope.close(owner, Exit.void)));
  return ownerApi;
});
