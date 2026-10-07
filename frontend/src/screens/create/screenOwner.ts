import { Cause, Data, Effect, Exit, Fiber, PubSub, Scope, Semaphore, SubscriptionRef, type Stream } from "effect";
import type { ScreenLease } from "../../app/routeManager";
import { closeScreenModel, createScreenModel, failParticipantQuery, prepareParticipantQuery, receiveParticipantPage, updateScreenContext,
  type CreateScreenModel, type DraftScreenContext, type ParticipantPage, type ParticipantRequest, type ParticipantSearch } from "./screenModel";

export class ScreenUnavailable extends Data.TaggedError("ScreenUnavailable")<{ readonly reason: "closed" | "no_draft" | "query_unavailable" }> {}
export interface ParticipantQueries<E, R> {
  readonly query: (request: ParticipantRequest) => Effect.Effect<ParticipantPage, E, R>;
  readonly messageKey: (cause: Cause.Cause<E>) => "ProtocolError" | "TransportError";
}
export interface CreateScreenOwner<R> {
  readonly snapshot: Effect.Effect<CreateScreenModel>;
  readonly changes: Stream.Stream<CreateScreenModel>;
  readonly updateContext: (context: DraftScreenContext | null) => Effect.Effect<void, ScreenUnavailable>;
  readonly query: (search: ParticipantSearch) => Effect.Effect<void, ScreenUnavailable, R>;
  readonly close: Effect.Effect<void>;
}
export function mountCreateScreenModel<E = never, R = never>(lease: ScreenLease, context: DraftScreenContext | null, queries: ParticipantQueries<E, R> | null): Effect.Effect<CreateScreenOwner<R>, ScreenUnavailable, Scope.Scope> {
  return Effect.gen(function*() {
    const parent = yield* Scope.Scope;
    if (parent.state._tag === "Closed" || !lease.isCurrent()) return yield* Effect.fail(new ScreenUnavailable({ reason: "closed" }));
    const scope = yield* Scope.fork(parent, "sequential");
    return yield* Effect.gen(function*() {
    const ref = yield* SubscriptionRef.make<CreateScreenModel>(createScreenModel(lease.generation, context));
    const lock = yield* Semaphore.make(1);
    let closed = false;
    let query: Fiber.Fiber<void, never> | undefined;
    const interruptQuery = Effect.suspend(() => {
      const previous = query; query = undefined;
      return previous === undefined ? Effect.void : Fiber.interrupt(previous).pipe(Effect.asVoid);
    });
    yield* Scope.addFinalizer(scope, Effect.gen(function*() {
      closed = true; query = undefined;
      yield* SubscriptionRef.set(ref, closeScreenModel());
      yield* PubSub.shutdown(ref.pubsub);
    }));
    const available = () => !closed && scope.state._tag !== "Closed" && lease.isCurrent();
    return {
      snapshot: SubscriptionRef.get(ref), changes: SubscriptionRef.changes(ref), close: Scope.close(scope, Exit.void),
      updateContext: (next: DraftScreenContext | null) => lock.withPermit(Effect.gen(function*() {
        if (!available()) return yield* Effect.fail(new ScreenUnavailable({ reason: "closed" }));
        const before = yield* SubscriptionRef.get(ref);
        yield* SubscriptionRef.update(ref, (model) => updateScreenContext(model, next));
        if ((yield* SubscriptionRef.get(ref)) !== before) yield* interruptQuery;
      })),
      query: (search: ParticipantSearch) => lock.withPermit(Effect.gen(function*() {
        if (!available()) return yield* Effect.fail(new ScreenUnavailable({ reason: "closed" }));
        if (queries === null) return yield* Effect.fail(new ScreenUnavailable({ reason: "query_unavailable" }));
        const current = yield* SubscriptionRef.get(ref);
        if (current._tag === "closed" || current.context === null) return yield* Effect.fail(new ScreenUnavailable({ reason: "no_draft" }));
        const request = yield* SubscriptionRef.modify(ref, (model) => {
          const plan = prepareParticipantQuery(model, search); return [plan.request, plan.model] as const;
        });
        yield* interruptQuery;
        if (request === null) return;
        query = yield* Effect.suspend(() => queries.query(request)).pipe(
          Effect.flatMap((page) => SubscriptionRef.update(ref, (model) => available() ? receiveParticipantPage(model, request, page) : model)),
          Effect.catchCause((cause) => Cause.hasInterruptsOnly(cause) ? Effect.void : SubscriptionRef.update(ref, (model) => available() ? failParticipantQuery(model, request, queries.messageKey(cause)) : model)),
          Effect.forkIn(scope),
        );
      })),
    };
    }).pipe(Effect.onExit((exit) => Exit.isFailure(exit) ? Scope.close(scope, exit) : Effect.void));
  });
}
