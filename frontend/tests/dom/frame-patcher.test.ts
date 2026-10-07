import { expect, it } from "@effect/vitest";
import { vi } from "vitest";
import { Cause, Deferred, Effect, Exit, Queue, Scope } from "effect";
import { DomError, DomPlatform, makeDomPlatform } from "../../src/platform/dom";
import { makeFramePatcher } from "../../src/ui/frame";
import { mountView, patchText, ViewUnavailable } from "../../src/ui/view";

vi.mock("effect", async (importOriginal) => {
  const actual = await importOriginal<typeof import("effect")>();
  return { ...actual, Queue: { ...actual.Queue, take: vi.fn(actual.Queue.take) } };
});

function controlledFrames(fail: (request: number) => boolean = () => false) {
  const pending = new Map<number, (time: number) => void>();
  const signals = new Map<number, Deferred.Deferred<number>>();
  const state = { requested: 0, cancelled: [] as number[] };
  const signal = (id: number) => {
    let result = signals.get(id);
    if (result === undefined) { result = Deferred.makeUnsafe<number>(); signals.set(id, result); }
    return result;
  };
  const dom = makeDomPlatform({
    request: (callback) => {
      const id = state.requested++;
      if (fail(state.requested)) throw new Error("frame source failed");
      pending.set(id, callback);
      Deferred.doneUnsafe(signal(id), Effect.succeed(id));
      return id;
    },
    cancel: (id) => { pending.delete(id); state.cancelled.push(id); },
  }, () => "visible");
  return {
    dom, pending, state,
    wait: (id: number) => Deferred.await(signal(id)).pipe(Effect.tap(() => Effect.sync(() => { signals.delete(id); }))),
    fire: (id: number) => {
      const callback = pending.get(id);
      if (callback === undefined) throw new Error("frame missing");
      pending.delete(id);
      callback(id * 16);
    },
  };
}

const noError = (cause: Cause.Cause<DomError>) => Effect.die(new Error(`unexpected render error: ${Cause.pretty(cause)}`));

it.effect("one frame patches only the latest of one thousand offered projections", () => Effect.gen(function*() {
  const frame = controlledFrames();
  const scope = yield* Scope.make();
  const patched = yield* Deferred.make<void>();
  const values: number[] = [];
  const patcher = yield* makeFramePatcher((value: number) => Effect.sync(() => {
    values.push(value);
    Deferred.doneUnsafe(patched, Effect.void);
  }), noError).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, frame.dom));
  expect(frame.state.requested).toBe(0);
  for (let value = 0; value < 1000; value++) yield* patcher.offer(value);
  yield* frame.wait(0);
  expect(frame.pending.size).toBe(1);
  expect(frame.state.requested).toBe(1);
  expect(values).toEqual([]);
  frame.fire(0);
  yield* Deferred.await(patched);
  expect(values).toEqual([999]);
  yield* Scope.close(scope, Exit.void);
  expect(frame.pending.size).toBe(0);
  expect(frame.state.requested).toBe(1);
}));

for (const value of [undefined, null, 0, ""]) {
  it.effect(`projection value ${String(value)} remains distinct from an empty latest slot`, () => Effect.gen(function*() {
    const frame = controlledFrames();
    const scope = yield* Scope.make();
    const patched = yield* Deferred.make<void>();
    const values: unknown[] = [];
    const patcher = yield* makeFramePatcher((projection: unknown) => Effect.sync(() => {
      values.push(projection);
      Deferred.doneUnsafe(patched, Effect.void);
    }), noError).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, frame.dom));
    yield* patcher.offer(value);
    yield* frame.wait(0);
    frame.fire(0);
    yield* Deferred.await(patched);
    expect(values).toEqual([value]);
    yield* Scope.close(scope, Exit.void);
  }));
}

it.effect("offers arriving during a slow patch are reduced to the next frame without duplicate workers", () => Effect.gen(function*() {
  const frame = controlledFrames();
  const scope = yield* Scope.make();
  const entered = yield* Deferred.make<void>();
  const release = yield* Deferred.make<void>();
  const completed = yield* Deferred.make<void>();
  let active = 0;
  let maximum = 0;
  const values: number[] = [];
  const patcher = yield* makeFramePatcher((value: number) => Effect.scoped(Effect.gen(function*() {
    yield* Effect.acquireRelease(Effect.sync(() => { active++; maximum = Math.max(maximum, active); }), () => Effect.sync(() => { active--; }));
    values.push(value);
    if (value === 1) { yield* Deferred.succeed(entered, undefined); yield* Deferred.await(release); }
    else yield* Deferred.succeed(completed, undefined);
  })), noError).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, frame.dom));
  yield* patcher.offer(1);
  yield* frame.wait(0);
  frame.fire(0);
  yield* Deferred.await(entered);
  yield* patcher.offer(2);
  yield* patcher.offer(3);
  expect(frame.state.requested).toBe(1);
  yield* Deferred.succeed(release, undefined);
  yield* frame.wait(1);
  expect(frame.pending.size).toBe(1);
  frame.fire(1);
  yield* Deferred.await(completed);
  expect(values).toEqual([1, 3]);
  expect(maximum).toBe(1);
  yield* Scope.close(scope, Exit.void);
  expect(active).toBe(0);
}));

it.effect("unmount cancels a zero frame handle, ignores late callbacks and rejects future offers", () => Effect.gen(function*() {
  const frame = controlledFrames();
  const scope = yield* Scope.make();
  let painted = 0;
  let errors = 0;
  const patcher = yield* makeFramePatcher(() => Effect.sync(() => { painted++; }), () => Effect.sync(() => { errors++; }))
    .pipe(Scope.provide(scope), Effect.provideService(DomPlatform, frame.dom));
  yield* patcher.offer("pending");
  yield* frame.wait(0);
  const stale = frame.pending.get(0);
  yield* Scope.close(scope, Exit.void);
  yield* Scope.close(scope, Exit.void);
  stale?.(999);
  expect([painted, errors]).toEqual([0, 0]);
  expect(frame.state.cancelled).toEqual([0]);
  expect(frame.pending.size).toBe(0);
  const result = yield* Effect.result(patcher.offer("late"));
  expect(result._tag).toBe("Failure");
  if (result._tag === "Failure") expect(result.failure).toEqual(new ViewUnavailable({ reason: "closed" }));
}));

it.effect("unmount interrupts an in-flight patch fiber and releases its own resources", () => Effect.gen(function*() {
  const frame = controlledFrames();
  const scope = yield* Scope.make();
  const entered = yield* Deferred.make<void>();
  let active = 0;
  let errors = 0;
  const patcher = yield* makeFramePatcher(() => Effect.scoped(Effect.gen(function*() {
    yield* Effect.acquireRelease(Effect.sync(() => { active++; }), () => Effect.sync(() => { active--; }));
    yield* Deferred.succeed(entered, undefined);
    return yield* Effect.never;
  })), () => Effect.sync(() => { errors++; })).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, frame.dom));
  yield* patcher.offer("pending");
  yield* frame.wait(0);
  frame.fire(0);
  yield* Deferred.await(entered);
  expect(active).toBe(1);
  yield* Scope.close(scope, Exit.void);
  expect(active).toBe(0);
  expect(errors).toBe(0);
  expect(frame.pending.size).toBe(0);
}));

for (const failing of [[1], [3], [1, 2, 3]]) {
  it.effect(`renderer failures ${failing.join(",")} stop once with explicit failure and no automatic retry`, () => Effect.gen(function*() {
    const frame = controlledFrames();
    const scope = yield* Scope.make();
    const failed = yield* Deferred.make<void>();
    const painted = new Map<number, Deferred.Deferred<void>>();
    let calls = 0;
    let errors = 0;
    const patcher = yield* makeFramePatcher((value: number) => Effect.suspend(() => {
      calls++;
      if (failing.includes(calls)) return Effect.fail(new DomError());
      const signal = painted.get(value);
      if (signal === undefined) throw new Error("paint signal missing");
      return Deferred.succeed(signal, undefined).pipe(Effect.asVoid);
    }), (cause) => Effect.sync(() => {
      expect(Cause.hasFails(cause)).toBe(true);
      errors++;
      Deferred.doneUnsafe(failed, Effect.void);
    })).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, frame.dom));
    const firstFailure = Math.min(...failing);
    for (let value = 1; value <= firstFailure; value++) {
      const signal = yield* Deferred.make<void>();
      painted.set(value, signal);
      yield* patcher.offer(value);
      yield* frame.wait(value - 1);
      frame.fire(value - 1);
      yield* (value === firstFailure ? Deferred.await(failed) : Deferred.await(signal));
    }
    expect(calls).toBe(firstFailure);
    expect(errors).toBe(1);
    const result = yield* Effect.result(patcher.offer(99));
    expect(result._tag).toBe("Failure");
    if (result._tag === "Failure") expect(result.failure).toEqual(new ViewUnavailable({ reason: "failed" }));
    expect(frame.state.requested).toBe(firstFailure);
    yield* Scope.close(scope, Exit.void);
    expect(frame.pending.size).toBe(0);
  }));
}

it.effect("frame dependency failure is reported once without rendering or rescheduling", () => Effect.gen(function*() {
  const frame = controlledFrames(() => true);
  const scope = yield* Scope.make();
  const failed = yield* Deferred.make<void>();
  let renders = 0;
  let errors = 0;
  const patcher = yield* makeFramePatcher(() => Effect.sync(() => { renders++; }), () => Effect.sync(() => {
    errors++;
    Deferred.doneUnsafe(failed, Effect.void);
  })).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, frame.dom));
  yield* patcher.offer("one");
  yield* Deferred.await(failed);
  expect([frame.state.requested, renders, errors]).toEqual([1, 0, 1]);
  expect((yield* Effect.result(patcher.offer("two")))._tag).toBe("Failure");
  yield* Scope.close(scope, Exit.void);
}));

it.effect("a renderer's own interruption ends the worker without an error and closes admission", () => Effect.gen(function*() {
  const frame = controlledFrames();
  const scope = yield* Scope.make();
  const ended = yield* Deferred.make<void>();
  let errors = 0;
  const patcher = yield* makeFramePatcher(() => Effect.failCause(Cause.interrupt()).pipe(Effect.ensuring(Deferred.succeed(ended, undefined))),
    () => Effect.sync(() => { errors++; })).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, frame.dom));
  yield* patcher.offer("one");
  yield* frame.wait(0);
  frame.fire(0);
  yield* Deferred.await(ended);
  yield* patcher.stopped;
  const result = yield* Effect.result(patcher.offer("two"));
  expect(result._tag).toBe("Failure");
  if (result._tag === "Failure") expect(result.failure.reason).toBe("closed");
  expect(errors).toBe(0);
  expect(frame.pending.size).toBe(0);
  yield* Scope.close(scope, Exit.void);
}));

it.effect("unexpected wake corruption trips a production invariant rather than inventing a projection", () => Effect.gen(function*() {
  const frame = controlledFrames();
  const scope = yield* Scope.make();
  const failed = yield* Deferred.make<void>();
  const errors: string[] = [];
  vi.mocked(Queue.take).mockImplementationOnce(() => Effect.void as never);
  try {
    yield* makeFramePatcher(() => Effect.die("must not paint"), (cause) => Effect.sync(() => {
      errors.push(Cause.pretty(cause));
      Deferred.doneUnsafe(failed, Effect.void);
    })).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, frame.dom));
    yield* frame.wait(0);
    frame.fire(0);
    yield* Deferred.await(failed);
    expect(errors).toHaveLength(1);
    expect(errors[0]).toContain("렌더 요청 없이 frame이 완료되었습니다.");
  } finally { yield* Scope.close(scope, Exit.void); }
}));

it.effect("a spurious wake after a completed frame cannot replay the previous projection", () => Effect.gen(function*() {
  const frame = controlledFrames();
  const scope = yield* Scope.make();
  const failed = yield* Deferred.make<void>();
  const replayed = yield* Deferred.make<void>();
  let paints = 0;
  const take = vi.mocked(Queue.take);
  const original = take.getMockImplementation();
  if (original === undefined) throw new Error("queue implementation absent");
  take.mockImplementationOnce(original).mockImplementationOnce(() => Effect.void as never);
  const patcher = yield* makeFramePatcher(() => Effect.sync(() => {
    paints++;
    if (paints > 1) Deferred.doneUnsafe(replayed, Effect.void);
  }), () => Effect.sync(() => {
    Deferred.doneUnsafe(failed, Effect.void);
  })).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, frame.dom));
  try {
    yield* patcher.offer("original");
    yield* frame.wait(0);
    frame.fire(0);
    yield* frame.wait(1);
    frame.fire(1);
    const outcome = yield* Effect.raceFirst(
      Deferred.await(failed).pipe(Effect.as("failed")),
      Deferred.await(replayed).pipe(Effect.as("replayed")),
    );
    expect(outcome).toBe("failed");
    yield* patcher.stopped;
    expect(paints).toBe(1);
  } finally { yield* Scope.close(scope, Exit.void); }
}));

it.effect("closed Scope cannot create a frame worker or retain a projection", () => Effect.gen(function*() {
  const scope = yield* Scope.make();
  yield* Scope.close(scope, Exit.void);
  const frame = controlledFrames();
  const result = yield* Effect.result(makeFramePatcher(() => Effect.void, noError).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, frame.dom)));
  expect(result._tag).toBe("Failure");
  expect(frame.state.requested).toBe(0);
}));

it.effect("one hundred screen swaps clean their own DOM, listeners, frames and query fibers", () => Effect.gen(function*() {
  const container = document.createElement("main");
  document.body.append(container);
  const frame = controlledFrames();
  let activeQueries = 0;
  let callbacks = 0;
  let errors = 0;
  try {
    for (let index = 0; index < 100; index++) {
      const scope = yield* Scope.make();
      const entered = yield* Deferred.make<void>();
      const mounted = yield* mountView(container, Effect.gen(function*() {
        const root = document.createElement("button");
        patchText(root, `화면 ${index}`);
        yield* frame.dom.listen(root, "click", () => { callbacks++; });
        yield* Effect.gen(function*() {
          activeQueries++;
          yield* Deferred.succeed(entered, undefined);
          return yield* Effect.never;
        }).pipe(Effect.ensuring(Effect.sync(() => { activeQueries--; })), Effect.forkScoped);
        const patcher = yield* makeFramePatcher((model: string) => Effect.sync(() => patchText(root, model)), () => Effect.sync(() => { errors++; }));
        return { element: root, value: patcher };
      })).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, frame.dom));
      yield* Deferred.await(entered);
      mounted.element.click();
      yield* mounted.value.offer("늦은 projection");
      yield* frame.wait(index);
      expect(container.childElementCount).toBe(1);
      expect(activeQueries).toBe(1);
      expect(frame.pending.size).toBe(1);
      yield* Scope.close(scope, Exit.void);
      mounted.element.click();
      expect(callbacks).toBe(index + 1);
      expect(container.childNodes).toHaveLength(0);
      expect(activeQueries).toBe(0);
      expect(frame.pending.size).toBe(0);
    }
    expect(errors).toBe(0);
    expect(frame.state.cancelled).toHaveLength(100);
  } finally { container.remove(); }
}));
