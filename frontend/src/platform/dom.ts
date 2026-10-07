import { Context, Data, Effect, Layer, type Scope } from "effect";

export class DomError extends Data.TaggedError("DomError")<{}> {}
export interface ListenerOptions {
  readonly capture?: boolean;
  readonly passive?: boolean;
  readonly once?: boolean;
}
export interface FrameHost {
  readonly request: (callback: (time: number) => void) => number;
  readonly cancel: (id: number) => void;
}

export class DomPlatform extends Context.Service<DomPlatform, {
  readonly listen: (target: EventTarget, type: string, listener: EventListener, options?: ListenerOptions) => Effect.Effect<void, DomError, Scope.Scope>;
  readonly nextFrame: Effect.Effect<number, DomError>;
  readonly focus: (target: HTMLElement) => Effect.Effect<void, DomError>;
  readonly visibility: Effect.Effect<DocumentVisibilityState, DomError>;
}>()("jackpot/DomPlatform") {}

export function makeDomPlatform(frames: FrameHost, readVisibility: () => DocumentVisibilityState): typeof DomPlatform.Service {
  return {
    listen: (target, type, listener, options = {}) => {
      const capture = options.capture ?? false;
      return Effect.asVoid(Effect.acquireRelease(
        Effect.try({
          try: () => {
            if (type.length === 0) throw new DomError();
            try {
              target.addEventListener(type, listener, options);
            } catch (error) {
              target.removeEventListener(type, listener, capture);
              throw error;
            }
          },
          catch: () => new DomError(),
        }),
        () => Effect.sync(() => target.removeEventListener(type, listener, capture)),
      ));
    },
    nextFrame: Effect.callback<number, DomError>((resume) => {
      let id: number;
      try {
        id = frames.request((time) => resume(Number.isFinite(time) && time >= 0
          ? Effect.succeed(time) : Effect.fail(new DomError())));
      } catch {
        resume(Effect.fail(new DomError()));
        return;
      }
      return Effect.sync(() => frames.cancel(id));
    }),
    focus: (target) => Effect.try({ try: () => target.focus(), catch: () => new DomError() }),
    visibility: Effect.try({ try: readVisibility, catch: () => new DomError() }),
  };
}

export const DomPlatformLive = Layer.sync(DomPlatform, () => makeDomPlatform({
  request: (callback) => window.requestAnimationFrame(callback),
  cancel: (id) => window.cancelAnimationFrame(id),
}, () => document.visibilityState));
