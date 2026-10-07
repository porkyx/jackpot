import { expect, it, vi } from "@effect/vitest";
import { Cause, Deferred, Effect, Exit, Fiber, Scope } from "effect";
import { DomError, DomPlatform, DomPlatformLive, makeDomPlatform, type FrameHost } from "../../src/platform/dom";

function frames() {
  const registered = Deferred.makeUnsafe<void>();
  const pending = new Map<number, (time: number) => void>();
  const cancelled: number[] = [];
  let sequence = 0;
  const host: FrameHost = {
    request: (callback) => {
      const id = sequence++;
      pending.set(id, callback);
      Deferred.doneUnsafe(registered, Effect.void);
      return id;
    },
    cancel: (id) => { cancelled.push(id); pending.delete(id); },
  };
  const fire = (id: number, time: number) => {
    const callback = pending.get(id);
    if (callback === undefined) throw new Error("frame is not pending");
    pending.delete(id);
    callback(time);
  };
  return { registered, pending, cancelled, host, fire };
}

it.effect("scoped listeners receive events only while their owner Scope remains open", () => Effect.gen(function*() {
  const target = new EventTarget();
  const platform = makeDomPlatform(frames().host, () => "visible");
  const firstScope = yield* Scope.make();
  const secondScope = yield* Scope.make();
  let first = 0;
  let second = 0;
  yield* platform.listen(target, "change", () => { first++; }).pipe(Scope.provide(firstScope));
  yield* platform.listen(target, "change", () => { second++; }).pipe(Scope.provide(secondScope));
  target.dispatchEvent(new Event("change"));
  expect([first, second]).toEqual([1, 1]);
  yield* Scope.close(firstScope, Exit.void);
  yield* Scope.close(firstScope, Exit.void);
  target.dispatchEvent(new Event("change"));
  expect([first, second]).toEqual([1, 2]);
  yield* Scope.close(secondScope, Exit.void);
  target.dispatchEvent(new Event("change"));
  expect([first, second]).toEqual([1, 2]);
}));

it.effect("listener capture cleanup stays consistent after caller options mutation", () => Effect.gen(function*() {
  // happy-dom does not model capture-sensitive removal, so independently
  // enforce the native EventTarget matching rule at this injected boundary.
  class CaptureSensitiveTarget extends EventTarget {
    capture = false;
    override addEventListener(...args: Parameters<EventTarget["addEventListener"]>) {
      const options = args[2];
      this.capture = typeof options === "boolean" ? options : options?.capture ?? false;
      super.addEventListener(...args);
    }
    override removeEventListener(...args: Parameters<EventTarget["removeEventListener"]>) {
      const options = args[2];
      const capture = typeof options === "boolean" ? options : options?.capture ?? false;
      if (capture === this.capture) super.removeEventListener(...args);
    }
  }
  const target = new CaptureSensitiveTarget();
  const platform = makeDomPlatform(frames().host, () => "visible");
  const scope = yield* Scope.make();
  let calls = 0;
  const options = { capture: true, passive: true };
  yield* platform.listen(target, "click", () => { calls++; }, options).pipe(Scope.provide(scope));
  options.capture = false;
  target.dispatchEvent(new Event("click"));
  expect(calls).toBe(1);
  yield* Scope.close(scope, Exit.void);
  target.dispatchEvent(new Event("click"));
  expect(calls).toBe(1);
}));

it.effect("empty event names fail before registration and release no unrelated listener", () => Effect.gen(function*() {
  class Target extends EventTarget {
    added = 0;
    removed = 0;
    override addEventListener(...args: Parameters<EventTarget["addEventListener"]>) { this.added++; super.addEventListener(...args); }
    override removeEventListener(...args: Parameters<EventTarget["removeEventListener"]>) { this.removed++; super.removeEventListener(...args); }
  }
  const target = new Target();
  const platform = makeDomPlatform(frames().host, () => "visible");
  const result = yield* Effect.result(Effect.scoped(platform.listen(target, "", () => {})));
  expect(result._tag).toBe("Failure");
  expect([target.added, target.removed]).toEqual([0, 0]);
}));

it.effect("partially registered failing listener is removed and prior listeners are finalized", () => Effect.gen(function*() {
  class Target extends EventTarget {
    added = 0;
    removed = 0;
    override addEventListener(...args: Parameters<EventTarget["addEventListener"]>) {
      this.added++;
      super.addEventListener(...args);
      if (this.added === 2) throw new Error("partial add");
    }
    override removeEventListener(...args: Parameters<EventTarget["removeEventListener"]>) { this.removed++; super.removeEventListener(...args); }
  }
  const target = new Target();
  const platform = makeDomPlatform(frames().host, () => "visible");
  let calls = 0;
  const result = yield* Effect.result(Effect.scoped(Effect.gen(function*() {
    yield* platform.listen(target, "first", () => { calls++; });
    yield* platform.listen(target, "second", () => { calls++; });
  })));
  expect(result._tag).toBe("Failure");
  target.dispatchEvent(new Event("first"));
  target.dispatchEvent(new Event("second"));
  expect([target.added, target.removed, calls]).toEqual([2, 2, 0]);
}));

it.effect("one hundred mount scopes return listener resources to their baseline", () => Effect.gen(function*() {
  const target = new EventTarget();
  const platform = makeDomPlatform(frames().host, () => "visible");
  let calls = 0;
  for (let index = 0; index < 100; index++) {
    yield* Effect.scoped(Effect.gen(function*() {
      yield* platform.listen(target, "change", () => { calls++; });
      target.dispatchEvent(new Event("change"));
    }));
    target.dispatchEvent(new Event("change"));
    expect(calls).toBe(index + 1);
  }
}));

for (const timestamp of [0, 1, 123.5, Number.MAX_SAFE_INTEGER]) {
  it.effect(`a controlled frame preserves timestamp ${timestamp} and consumes only one callback`, () => Effect.gen(function*() {
    const frame = frames();
    const platform = makeDomPlatform(frame.host, () => "visible");
    const fiber = yield* platform.nextFrame.pipe(Effect.forkChild);
    yield* Deferred.await(frame.registered);
    expect(frame.pending.size).toBe(1);
    frame.fire(0, timestamp);
    expect(yield* Fiber.join(fiber)).toBe(timestamp);
    expect(frame.pending.size).toBe(0);
    expect(frame.cancelled).toEqual([]);
  }));
}

for (const timestamp of [NaN, Infinity, -Infinity, -1]) {
  it.effect(`invalid frame timestamp ${timestamp} is an explicit failure`, () => Effect.gen(function*() {
    const frame = frames();
    const platform = makeDomPlatform(frame.host, () => "visible");
    const fiber = yield* platform.nextFrame.pipe(Effect.forkChild);
    yield* Deferred.await(frame.registered);
    frame.fire(0, timestamp);
    const result = yield* Effect.result(Fiber.join(fiber));
    expect(result._tag).toBe("Failure");
    if (result._tag === "Failure") expect(result.failure).toBeInstanceOf(DomError);
    expect(frame.pending.size).toBe(0);
  }));
}

it.effect("interrupting a pending frame cancels its zero handle and ignores a late callback", () => Effect.gen(function*() {
  const frame = frames();
  const platform = makeDomPlatform(frame.host, () => "visible");
  const fiber = yield* platform.nextFrame.pipe(Effect.forkChild);
  yield* Deferred.await(frame.registered);
  const staleCallback = frame.pending.get(0);
  yield* Fiber.interrupt(fiber);
  yield* Fiber.interrupt(fiber);
  expect(frame.cancelled).toEqual([0]);
  expect(frame.pending.size).toBe(0);
  staleCallback?.(42);
  const exit = yield* Fiber.await(fiber);
  expect(Exit.isFailure(exit)).toBe(true);
  if (Exit.isFailure(exit)) expect(Cause.hasInterrupts(exit.cause)).toBe(true);
}));

it.effect("frame request failures retain no handle or hidden retry", () => Effect.gen(function*() {
  let requested = 0;
  let cancelled = 0;
  const platform = makeDomPlatform({ request: () => { requested++; throw new Error("request failed"); }, cancel: () => { cancelled++; } }, () => "hidden");
  expect((yield* Effect.result(platform.nextFrame))._tag).toBe("Failure");
  expect([requested, cancelled]).toEqual([1, 0]);
}));

it.effect("production DOM Layer owns exactly its browser frame and reads current visibility", () => Effect.gen(function*() {
  const registered = yield* Deferred.make<void>();
  const request = vi.spyOn(window, "requestAnimationFrame").mockImplementation(() => {
    Deferred.doneUnsafe(registered, Effect.void);
    return 0;
  });
  const cancel = vi.spyOn(window, "cancelAnimationFrame").mockImplementation(() => {});
  try {
    yield* Effect.gen(function*() {
      const platform = yield* DomPlatform;
      expect(yield* platform.visibility).toBe(document.visibilityState);
      const fiber = yield* platform.nextFrame.pipe(Effect.forkChild);
      yield* Deferred.await(registered);
      expect(request).toHaveBeenCalledTimes(1);
      expect(cancel).not.toHaveBeenCalled();
      yield* Fiber.interrupt(fiber);
      expect(cancel).toHaveBeenCalledExactlyOnceWith(0);
    }).pipe(Effect.provide(DomPlatformLive));
  } finally {
    request.mockRestore();
    cancel.mockRestore();
  }
}));

it.effect("focus and visibility are deferred, replaceable effects with safe failures", () => Effect.gen(function*() {
  const target = document.createElement("button");
  document.body.append(target);
  try {
    let visibility: DocumentVisibilityState = "hidden";
    const platform = makeDomPlatform(frames().host, () => visibility);
    expect(document.activeElement).not.toBe(target);
    yield* platform.focus(target);
    expect(document.activeElement).toBe(target);
    expect(yield* platform.visibility).toBe("hidden");
    visibility = "visible";
    expect(yield* platform.visibility).toBe("visible");
    target.focus = () => { throw new Error("secret"); };
    const focused = yield* Effect.result(platform.focus(target));
    expect(focused._tag).toBe("Failure");
    if (focused._tag === "Failure") expect(JSON.stringify(focused.failure)).not.toContain("secret");
    const failed = makeDomPlatform(frames().host, () => { throw new Error("secret"); });
    expect((yield* Effect.result(failed.visibility))._tag).toBe("Failure");
  } finally {
    target.remove();
  }
}));
