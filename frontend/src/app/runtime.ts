import { Data, Effect, Fiber, ManagedRuntime, Scope, Exit, type Layer } from "effect";

export type RuntimePhase = "starting" | "running" | "stopping" | "stopped";
export class RuntimeUnavailable extends Data.TaggedError("RuntimeUnavailable")<{
  readonly phase: RuntimePhase;
}> {}

export interface AppRuntime<R, A> {
  readonly ready: Promise<A>;
  readonly phase: () => RuntimePhase;
  readonly run: <B, F>(effect: Effect.Effect<B, F, R>) => Promise<B>;
  readonly mount: <B, F>(effect: Effect.Effect<B, F, R | Scope.Scope>) => Promise<B>;
  readonly dispose: () => Promise<void>;
}

// One owner is created by main. Its Layer, initialization, screen resources,
// and application resources all live below one ManagedRuntime.
export function createAppRuntime<R, ER, A, E>(
  layer: Layer.Layer<R, ER>,
  initialize: Effect.Effect<A, E, R | Scope.Scope>,
): AppRuntime<R, A> {
  const runtime = ManagedRuntime.make(layer);
  const appScope = Scope.forkUnsafe(runtime.scope, "sequential");
  const screens = Scope.forkUnsafe(appScope, "sequential");
  let phase: RuntimePhase = "starting";
  let disposal: Promise<void> | undefined;
  const startup = runtime.runFork(initialize.pipe(Scope.provide(appScope)));

  const dispose = () => {
    if (disposal !== undefined) return disposal;
    phase = "stopping";
    disposal = Effect.runPromise(
      Fiber.interrupt(startup).pipe(Effect.ensuring(
        Scope.close(screens, Exit.void).pipe(Effect.ensuring(runtime.disposeEffect)),
      )),
    ).finally(() => { phase = "stopped"; });
    return disposal;
  };

  const ready = Effect.runPromise(Fiber.join(startup)).then(
    (value) => {
      if (phase !== "starting") throw new RuntimeUnavailable({ phase });
      phase = "running";
      return value;
    },
    async (error: unknown) => {
      try {
        await dispose();
      } catch (cleanupError) {
        throw new AggregateError([error, cleanupError], "앱 초기화와 자원 정리가 실패했습니다.");
      }
      throw error;
    },
  );

  return {
    ready, phase: () => phase, dispose,
    run: (effect) => phase === "running" ? runtime.runPromise(effect.pipe(Effect.forkIn(appScope), Effect.flatMap(Fiber.join))) : Promise.reject(new RuntimeUnavailable({ phase })),
    mount: (effect) => phase === "running" ? runtime.runPromise(effect.pipe(Scope.provide(screens), Effect.forkIn(screens), Effect.flatMap(Fiber.join))) : Promise.reject(new RuntimeUnavailable({ phase })),
  };
}

export interface RuntimeHotData {
  jackpotPreviousDispose?: Promise<void>;
}
export interface RuntimeHotContext {
  readonly data: RuntimeHotData;
  readonly dispose: (callback: (data: RuntimeHotData) => void) => void;
  readonly on?: (event: "vite:beforeFullReload", callback: () => Promise<void>) => void;
}

// Vite does not await dispose callbacks. Pass the previous disposal Promise
// through hot.data, and await it before allocating the next ManagedRuntime.
export async function createAppRuntimeWithHmr<R, ER, A, E>(
  layer: Layer.Layer<R, ER>,
  initialize: Effect.Effect<A, E, R | Scope.Scope>,
  hot: RuntimeHotContext | undefined,
  onDisposeFailure: (error: unknown) => void,
): Promise<AppRuntime<R, A>> {
  await hot?.data.jackpotPreviousDispose;
  const owner = createAppRuntime(layer, initialize);
  // Module replacement uses hot.data; a full reload has no module disposer, so
  // Vite's awaited beforeFullReload event must finish the same cleanup promise.
  let disposal: Promise<void> | undefined;
  const dispose = (data: RuntimeHotData) => {
    if (disposal === undefined) {
      disposal = owner.dispose();
      void disposal.catch(onDisposeFailure);
    }
    data.jackpotPreviousDispose = disposal;
    return disposal;
  };
  hot?.dispose((data) => { void dispose(data); });
  hot?.on?.("vite:beforeFullReload", () => dispose(hot.data));
  return owner;
}
