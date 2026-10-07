import { expect, it } from "@effect/vitest";
import { Cause, Deferred, Effect, Exit, Fiber, Layer } from "effect";
import { TestClock } from "effect/testing";
import { makeAppLayer, makeAppLive } from "../../src/app/layers";
import { Backend, BackendRejected, ProtocolError, TransportError, type BootstrapReply, type PendingOperationsRequest } from "../../src/contracts/backend";
import { OperationCoordinator } from "../../src/operations/coordinator";
import { ResultImage, ResultImageError } from "../../src/platform/canvas";
import { DomPlatform, makeDomPlatform } from "../../src/platform/dom";
import { ClientIds, clientIdsSequence } from "../../src/platform/ids";

const reply: BootstrapReply = {
  protocolVersion: 1, backendSessionId: "session", occurredAt: "2026-10-06T00:00:00Z",
  data: { backendNow: "2026-10-06T00:00:00Z", theme: "system", activeDraft: null, pendingOperations: [], pendingCursor: null, recentResults: [] },
};

function dependencies(backend: typeof Backend.Service, ids = clientIdsSequence(["op-1", "intent-2"])) {
  return {
    backend: Layer.succeed(Backend, backend), ids,
    dom: Layer.succeed(DomPlatform, makeDomPlatform({ request: () => 1, cancel: () => {} }, () => "hidden")),
    resultImage: Layer.succeed(ResultImage, { renderPng: () => Effect.fail(new ResultImageError({ reason: "encoding" })) }),
  };
}

function readBackend(): typeof Backend.Service {
  return {
    product: null,
    bootstrap: () => Effect.succeed(reply),
    listPendingOperations: () => Effect.succeed({ ...reply, data: { operations: [], cursor: null } }),
    subscribeStateChanges: () => Effect.void,
    getOperation: () => Effect.die("GetOperation is unused by the read Layer harness"),
  };
}

it.effect("Layer composition is lazy and one Coordinator is shared by all consumers", () => Effect.gen(function*() {
  let reads = 0;
  const layer = makeAppLayer(dependencies({ ...readBackend(), bootstrap: () => Effect.sync(() => { reads++; return reply; }) }));
  expect(reads).toBe(0);
  yield* Effect.gen(function*() {
    const first = yield* OperationCoordinator;
    const second = yield* OperationCoordinator;
    expect(first).toBe(second);
    expect(reads).toBe(0);
    expect((yield* first.bootstrap()).reply).toBe(reply);
    expect(reads).toBe(1);
  }).pipe(Effect.provide(layer));
}));

it.effect("production composition requires concrete Backend and ResultImage providers", () => Effect.gen(function*() {
  const renderer = { renderPng: () => Effect.fail(new ResultImageError({ reason: "encoding" })) };
  const layer = makeAppLive(Layer.succeed(Backend, readBackend()), Layer.succeed(ResultImage, renderer));
  yield* Effect.gen(function*() {
    const coordinator = yield* OperationCoordinator;
    expect((yield* coordinator.bootstrap()).reply).toBe(reply);
    expect(yield* ResultImage).toBe(renderer);
    expect(yield* DomPlatform).toBeDefined();
    expect(yield* ClientIds).toBeDefined();
  }).pipe(Effect.provide(layer));
}));

it.effect("Coordinator uses the injected Clock when a delayed read is received", () => Effect.gen(function*() {
  const response = yield* Deferred.make<BootstrapReply>();
  const dispatched = yield* Deferred.make<void>();
  const layer = makeAppLayer(dependencies({ ...readBackend(), bootstrap: () => Effect.gen(function*() {
    yield* Deferred.succeed(dispatched, undefined);
    return yield* Deferred.await(response);
  }) }));
  yield* Effect.gen(function*() {
    const coordinator = yield* OperationCoordinator;
    const fiber = yield* coordinator.bootstrap().pipe(Effect.forkChild);
    yield* Deferred.await(dispatched);
    yield* TestClock.setTime(3210);
    yield* Deferred.succeed(response, reply);
    const received = yield* Fiber.join(fiber);
    expect(received).toEqual({ reply, receivedAtMillis: 3210 });
  }).pipe(Effect.provide(layer));
}));

it.effect("Coordinator receives Backend and ClientIds from the same application dependency graph", () => Effect.gen(function*() {
  const requests: PendingOperationsRequest[] = [];
  const request = { cursor: "opaque-cursor", limit: 64 } as const;
  const layer = makeAppLayer(dependencies({ ...readBackend(), listPendingOperations: (input) => Effect.sync(() => {
    requests.push(input);
    return { ...reply, data: { operations: [], cursor: null } };
  }) }));
  yield* Effect.gen(function*() {
    const coordinator = yield* OperationCoordinator;
    expect(yield* coordinator.newOperationId).toBe("op-1");
    const ids = yield* ClientIds;
    expect(yield* ids.intentId).toBe("intent-2");
    const result = yield* coordinator.listPendingOperations(request);
    expect(requests).toEqual([request]);
    expect(requests[0]).not.toBe(request);
    expect(Object.isFrozen(requests[0])).toBe(true);
    expect(result.reply.data.operations).toEqual([]);
    expect(result.receivedAtMillis).toBe(0);
  }).pipe(Effect.provide(layer));
}));

for (const error of [new ProtocolError(), new BackendRejected({ code: "StorageUnavailable", messageKey: "StorageUnavailable" })]) {
  it.effect(`Coordinator preserves ${error._tag} without empty success or hidden retry`, () => Effect.gen(function*() {
    let calls = 0;
    const layer = makeAppLayer(dependencies({ ...readBackend(), bootstrap: () => Effect.suspend(() => { calls++; return Effect.fail(error); }) }));
    yield* Effect.gen(function*() {
      const coordinator = yield* OperationCoordinator;
      const result = yield* Effect.result(coordinator.bootstrap());
      expect(result._tag).toBe("Failure");
      if (result._tag === "Failure") expect(result.failure).toBe(error);
      expect(calls).toBe(1);
    }).pipe(Effect.provide(layer));
  }));
}

it.effect("Coordinator applies the read retry policy once for first Nth and continuous failures", () => Effect.gen(function*() {
  for (const failures of [[1], [3], [1, 2, 3, 4, 5, 6]]) {
    let calls = 0;
    const layer = makeAppLayer(dependencies({ ...readBackend(), bootstrap: () => Effect.suspend(() => {
      calls++;
      return failures.includes(calls) ? Effect.fail(new TransportError()) : Effect.succeed(reply);
    }) }));
    yield* Effect.gen(function*() {
      const coordinator = yield* OperationCoordinator;
      for (let call = 1; call <= 4; call++) {
        const fiber = yield* Effect.result(coordinator.bootstrap()).pipe(Effect.forkChild);
        yield* TestClock.adjust(250);
        yield* TestClock.adjust(1000);
        const result = yield* Fiber.join(fiber);
        expect(result._tag).toBe(failures.length === 6 && call <= 2 ? "Failure" : "Success");
      }
      expect(calls).toBe(failures.length === 6 ? 8 : 5);
    }).pipe(Effect.scoped, Effect.provide(layer));
  }
}));

it.effect("interrupting an in-flight read runs its finalizer once and produces no reply", () => Effect.gen(function*() {
  const dispatched = yield* Deferred.make<void>();
  let active = 0;
  let released = 0;
  const layer = makeAppLayer(dependencies({ ...readBackend(), bootstrap: () => Effect.scoped(Effect.gen(function*() {
    yield* Effect.acquireRelease(Effect.sync(() => { active++; }), () => Effect.sync(() => { active--; released++; }));
    yield* Deferred.succeed(dispatched, undefined);
    return yield* Effect.never;
  })) }));
  yield* Effect.gen(function*() {
    const coordinator = yield* OperationCoordinator;
    const fiber = yield* coordinator.bootstrap().pipe(Effect.forkChild);
    yield* Deferred.await(dispatched);
    expect(active).toBe(1);
    yield* Fiber.interrupt(fiber);
    expect(active).toBe(0);
    expect(released).toBe(1);
    const exit = yield* Fiber.await(fiber);
    expect(Exit.isFailure(exit)).toBe(true);
    if (Exit.isFailure(exit)) expect(Cause.hasInterrupts(exit.cause)).toBe(true);
  }).pipe(Effect.provide(layer));
}));

it.effect("each application acquisition owns independent ClientIds and Backend resources", () => Effect.gen(function*() {
  let active = 0;
  let acquired = 0;
  let released = 0;
  const backend = Layer.effect(Backend, Effect.acquireRelease(
    Effect.sync(() => { active++; acquired++; return readBackend(); }),
    () => Effect.sync(() => { active--; released++; }),
  ));
  const layer = makeAppLayer({ ...dependencies(readBackend()), backend });
  for (let index = 0; index < 3; index++) {
    yield* Effect.scoped(Effect.gen(function*() {
      const coordinator = yield* OperationCoordinator;
      expect(active).toBe(1);
      expect(yield* coordinator.newOperationId).toBe("op-1");
    }).pipe(Effect.provide(layer)));
    expect(active).toBe(0);
  }
  expect(acquired).toBe(3);
  expect(released).toBe(3);
}));

it.effect("partial Layer acquisition failure cleans an already acquired Backend", () => Effect.gen(function*() {
  const acquired = yield* Deferred.make<void>();
  let active = 0;
  let released = 0;
  const backend = Layer.effect(Backend, Effect.acquireRelease(Effect.gen(function*() {
    active++;
    yield* Deferred.succeed(acquired, undefined);
    return readBackend();
  }), () => Effect.sync(() => { active--; released++; })));
  const failure = new ResultImageError({ reason: "font" });
  const resultImage = Layer.effect(ResultImage, Effect.gen(function*() {
    yield* Deferred.await(acquired);
    return yield* Effect.fail(failure);
  }));
  const layer = makeAppLayer({ ...dependencies(readBackend()), backend, resultImage });
  const result = yield* Effect.result(Effect.scoped(OperationCoordinator.pipe(Effect.provide(layer))));
  expect(result._tag).toBe("Failure");
  if (result._tag === "Failure") expect(result.failure).toBe(failure);
  expect(active).toBe(0);
  expect(released).toBe(1);
}));
