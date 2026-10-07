import { Cause, Effect, Fiber, Queue, Scope, type Exit } from "effect";
import { DomPlatform, type DomError } from "../platform/dom";
import { ViewUnavailable } from "./view";

export interface FramePatcher<A> {
  readonly offer: (model: A) => Effect.Effect<void, ViewUnavailable>;
  readonly stopped: Effect.Effect<Exit.Exit<void>>;
}

// One latest projection slot and one wake signal. The worker does not retain
// the first projection while waiting for a frame, and never retries a render.
export function makeFramePatcher<A, E, R>(
  patch: (model: A) => Effect.Effect<void, E, R>,
  onError: (cause: Cause.Cause<E | DomError>) => Effect.Effect<void, never, R>,
): Effect.Effect<FramePatcher<A>, ViewUnavailable, DomPlatform | R | Scope.Scope> {
  return Effect.gen(function*() {
    const dom = yield* DomPlatform;
    const scope = yield* Scope.Scope;
    if (scope.state._tag === "Closed") return yield* Effect.fail(new ViewUnavailable({ reason: "closed" }));
    const wake = yield* Queue.sliding<void>(1);
    let phase: "open" | "failed" | "closed" = "open";
    let latest: { readonly value: A } | undefined;
    yield* Effect.addFinalizer(() => Effect.sync(() => {
      phase = "closed";
      latest = undefined;
      Queue.shutdownUnsafe(wake);
    }));
    const worker = yield* Effect.gen(function*() {
      while (true) {
        yield* Queue.take(wake);
        yield* dom.nextFrame;
        const projection = yield* Effect.sync(() => {
          Queue.takeUnsafe(wake);
          const value = latest;
          latest = undefined;
          return value;
        });
        if (projection === undefined) return yield* Effect.die(new Error("렌더 요청 없이 frame이 완료되었습니다."));
        yield* patch(projection.value);
      }
    }).pipe(Effect.catchCause((cause) => {
      if (Cause.hasInterruptsOnly(cause)) return Effect.void;
      return Effect.gen(function*() {
        phase = "failed";
        latest = undefined;
        Queue.shutdownUnsafe(wake);
        yield* onError(cause);
      });
    }), Effect.ensuring(Effect.sync(() => {
      if (phase === "open") phase = "closed";
      latest = undefined;
      Queue.shutdownUnsafe(wake);
    })), Effect.forkScoped);
    return {
      stopped: Fiber.await(worker),
      offer: (model) => Effect.suspend(() => {
        if (phase !== "open") return Effect.fail(new ViewUnavailable({ reason: phase === "closed" ? "closed" : "failed" }));
        latest = { value: model };
        Queue.offerUnsafe(wake, undefined);
        return Effect.void;
      }),
    };
  });
}
