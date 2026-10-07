import { Cause, Data, Effect, Exit, Fiber, Scope, Semaphore } from "effect";
import { parseHashRoute } from "./routes";
import type { Route } from "./shellState";

export class RouteUnavailable extends Data.TaggedError("RouteUnavailable")<{
  readonly reason: "closed" | "generation_exhausted";
}> {}
export interface ScreenLease {
  readonly route: Route;
  readonly generation: number;
  readonly isCurrent: () => boolean;
}
export interface RouteManager<R> {
  // Completion means the request was handled. The injected screen owns its
  // real loading/error UI; navigation never fabricates a product result.
  readonly navigate: (hash: string) => Effect.Effect<void, RouteUnavailable, R>;
  readonly close: Effect.Effect<void>;
}
export interface RouteMount<E, R> {
  readonly mount: (lease: ScreenLease) => Effect.Effect<void, E, R | Scope.Scope>;
  readonly onFailure: (route: Route, cause: Cause.Cause<E>) => Effect.Effect<void, never, R>;
}

export function nextRouteGeneration(current: number): number {
  if (!Number.isSafeInteger(current) || current < 0 || current === Number.MAX_SAFE_INTEGER) throw new RouteUnavailable({ reason: "generation_exhausted" });
  return current + 1;
}

export function makeRouteManager<E, R>(screen: RouteMount<E, R>): Effect.Effect<RouteManager<R>, RouteUnavailable, Scope.Scope> {
  return Effect.gen(function*() {
    const parent = yield* Scope.Scope;
    if (parent.state._tag === "Closed") return yield* Effect.fail(new RouteUnavailable({ reason: "closed" }));
    const lock = yield* Semaphore.make(1);
    let closed = false;
    let generation = 0;
    let current: { readonly scope: Scope.Closeable; readonly stop: () => void; readonly initializer: Fiber.Fiber<void, never> } | undefined;

    const closeCurrent = Effect.suspend(() => {
      const previous = current;
      current = undefined;
      if (previous === undefined) return Effect.void;
      previous.stop();
      return Fiber.interrupt(previous.initializer).pipe(Effect.ensuring(Scope.close(previous.scope, Exit.void)), Effect.asVoid);
    });
    const close = Effect.uninterruptible(Effect.gen(function*() {
      closed = true;
      yield* lock.withPermit(closeCurrent);
    }));
    yield* Scope.addFinalizer(parent, close);

    return {
      close,
      navigate: (hash) => Effect.uninterruptible(Effect.gen(function*() {
        if (closed || parent.state._tag === "Closed") return yield* Effect.fail(new RouteUnavailable({ reason: "closed" }));
        const requested = yield* Effect.try({ try: () => nextRouteGeneration(generation), catch: () => new RouteUnavailable({ reason: "generation_exhausted" }) });
        generation = requested;
        const route = parseHashRoute(hash);
        yield* lock.withPermit(Effect.gen(function*() {
          yield* closeCurrent;
          // A newer hash can arrive while an old finalizer is waiting. Skip
          // obsolete mounts, but never open a successor before cleanup ends.
          if (closed || requested !== generation) return;
          const scope = yield* Scope.fork(parent, "sequential");
          let active = true;
          const lease: ScreenLease = Object.freeze({ route, generation: requested, isCurrent: () => !closed && active && requested === generation });
          const initialize = Effect.suspend(() => screen.mount(lease)).pipe(
            Scope.provide(scope),
            Effect.catchCause((cause) => Effect.gen(function*() {
              active = false;
              const cleanup = yield* Effect.exit(Scope.close(scope, Exit.failCause(cause)));
              const failure = Exit.isFailure(cleanup) ? Cause.combine(cause, cleanup.cause) : cause;
              if (!closed && requested === generation && !Cause.hasInterruptsOnly(failure)) yield* screen.onFailure(route, failure);
            })),
          );
          // The initializer is app-owned solely so failure can close its own
          // screen scope without self-interruption. Every replacement explicitly
          // interrupts it before closing that screen's resources.
          const initializer = yield* initialize.pipe(Effect.interruptible, Effect.forkIn(parent));
          current = { scope, stop: () => { active = false; }, initializer };
        }));
      })),
    };
  });
}
