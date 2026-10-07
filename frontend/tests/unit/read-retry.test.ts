import { expect, it } from "@effect/vitest";
import { Cause, Clock, Deferred, Effect, Exit, Fiber } from "effect";
import { TestClock } from "effect/testing";
import { BackendRejected, ProtocolError, TransportError, type BootstrapReply, type PendingOperationsReply, type PendingOperationsRequest } from "../../src/contracts/backend";
import { ReadTimeout } from "../../src/app/errors";
import { makeReadQueries } from "../../src/operations/readRetry";

const reply: BootstrapReply = {
  protocolVersion: 1, backendSessionId: "session", occurredAt: "2026-10-06T00:00:00Z",
  data: { backendNow: "2026-10-06T00:00:00Z", theme: "system", activeDraft: null, pendingOperations: [], pendingCursor: null, recentResults: [] },
};
const pending: PendingOperationsReply = { ...reply, data: { operations: [], cursor: null } };
function backend(bootstrap: () => Effect.Effect<BootstrapReply, TransportError | ProtocolError | BackendRejected>) {
  return { bootstrap, listPendingOperations: (_request: PendingOperationsRequest) => Effect.succeed(pending) };
}

it.effect("read policy is lazy and successful Bootstrap performs one request", () => Effect.gen(function*() {
  let calls = 0;
  const queries = makeReadQueries(backend(() => Effect.sync(() => { calls++; return reply; })));
  const read = queries.bootstrap();
  expect(calls).toBe(0);
  expect(yield* read).toBe(reply);
  expect(calls).toBe(1);
}));

it.effect("first and second transport failures retry exactly at 250ms and 1s", () => Effect.gen(function*() {
  const calls: number[] = [];
  const queries = makeReadQueries(backend(() => Effect.gen(function*() {
    calls.push(yield* Clock.currentTimeMillis);
    return calls.length < 3 ? yield* Effect.fail(new TransportError()) : reply;
  })));
  const fiber = yield* queries.bootstrap().pipe(Effect.forkChild);
  yield* TestClock.adjust(249);
  expect(calls).toEqual([0]);
  yield* TestClock.adjust(1);
  expect(calls).toEqual([0, 250]);
  yield* TestClock.adjust(999);
  expect(calls).toEqual([0, 250]);
  yield* TestClock.adjust(1);
  expect(yield* Fiber.join(fiber)).toBe(reply);
  expect(calls).toEqual([0, 250, 1250]);
}));

it.effect("continuous transport failure stops after three requests and keeps the final error", () => Effect.gen(function*() {
  let calls = 0;
  const final = new TransportError();
  const queries = makeReadQueries(backend(() => Effect.suspend(() => { calls++; return Effect.fail(final); })));
  const fiber = yield* Effect.result(queries.bootstrap()).pipe(Effect.forkChild);
  yield* TestClock.adjust(250);
  yield* TestClock.adjust(1000);
  expect(fiber.pollUnsafe()).toBeDefined();
  const result = yield* Fiber.join(fiber);
  expect(result._tag).toBe("Failure");
  if (result._tag === "Failure") expect(result.failure).toBe(final);
  yield* TestClock.adjust(60000);
  expect(calls).toBe(3);
}));

for (const error of [new ProtocolError(), new BackendRejected({ code: "StorageUnavailable", messageKey: "SECRET" }), new BackendRejected({ code: "TransportError", messageKey: "SECRET" })]) {
  it.effect(`${error._tag}/${error._tag === "BackendRejected" ? error.code : "decode"} never retries`, () => Effect.gen(function*() {
    let calls = 0;
    const queries = makeReadQueries(backend(() => Effect.suspend(() => { calls++; return Effect.fail(error); })));
    const fiber = yield* Effect.result(queries.bootstrap()).pipe(Effect.forkChild);
    yield* TestClock.adjust(0);
    expect(fiber.pollUnsafe()).toBeDefined();
    const result = yield* Fiber.join(fiber);
    expect(result._tag).toBe("Failure");
    if (result._tag === "Failure") expect(result.failure).toBe(error);
    yield* TestClock.adjust(10000);
    expect(calls).toBe(1);
  }));
}

it.effect("a non-retryable Nth response ends transport retries immediately", () => Effect.gen(function*() {
  let calls = 0;
  const final = new ProtocolError();
  const queries = makeReadQueries(backend(() => Effect.suspend(() => {
    calls++;
    return Effect.fail(calls === 1 ? new TransportError() : final);
  })));
  const fiber = yield* Effect.result(queries.bootstrap()).pipe(Effect.forkChild);
  yield* TestClock.adjust(250);
  expect(fiber.pollUnsafe()).toBeDefined();
  const result = yield* Fiber.join(fiber);
  expect(result._tag).toBe("Failure");
  if (result._tag === "Failure") expect(result.failure).toBe(final);
  yield* TestClock.adjust(1000);
  expect(calls).toBe(2);
}));

it.effect("each independent successful query resets its retry count", () => Effect.gen(function*() {
  let calls = 0;
  const queries = makeReadQueries(backend(() => Effect.suspend(() => {
    calls++;
    return calls === 1 || calls === 3 ? Effect.fail(new TransportError()) : Effect.succeed(reply);
  })));
  for (let index = 0; index < 2; index++) {
    const fiber = yield* queries.bootstrap().pipe(Effect.forkChild);
    yield* TestClock.adjust(250);
    expect(yield* Fiber.join(fiber)).toBe(reply);
  }
  expect(calls).toBe(4);
}));

it.effect("ListPendingOperations retries the admitted cursor/limit snapshot only", () => Effect.gen(function*() {
  const requests: PendingOperationsRequest[] = [];
  const request = { cursor: "original-cursor", limit: 64 };
  const queries = makeReadQueries({ ...backend(() => Effect.succeed(reply)), listPendingOperations: (input) => Effect.suspend(() => {
    requests.push(input);
    return requests.length === 1 ? Effect.fail(new TransportError()) : Effect.succeed(pending);
  }) });
  const read = queries.listPendingOperations(request);
  request.cursor = "changed-cursor";
  request.limit = 1;
  const fiber = yield* read.pipe(Effect.forkChild);
  yield* TestClock.adjust(250);
  expect(yield* Fiber.join(fiber)).toBe(pending);
  expect(requests).toEqual([{ cursor: "original-cursor", limit: 64 }, { cursor: "original-cursor", limit: 64 }]);
  expect(requests[0]).toBe(requests[1]);
  expect(Object.isFrozen(requests[0])).toBe(true);
}));

it.effect("no subscription, mutation or operation polling can be called through the read seam", () => Effect.gen(function*() {
  let mutations = 0;
  let subscriptions = 0;
  let polling = 0;
  const raw = {
    ...backend(() => Effect.succeed(reply)),
    mutation: () => Effect.sync(() => { mutations++; }),
    subscribeStateChanges: () => Effect.sync(() => { subscriptions++; }),
    getOperation: () => Effect.sync(() => { polling++; }),
  };
  const queries = makeReadQueries(raw);
  expect(Object.keys(queries).sort()).toEqual(["bootstrap", "listPendingOperations"]);
  yield* queries.bootstrap();
  yield* queries.listPendingOperations({ cursor: null, limit: 1 });
  expect([mutations, subscriptions, polling]).toEqual([0, 0, 0]);
  yield* raw.mutation();
  yield* raw.subscribeStateChanges();
  yield* raw.getOperation();
  expect([mutations, subscriptions, polling]).toEqual([1, 1, 1]);
}));

it.effect("read timeout occurs at 15s, interrupts each attempt and stops after the third", () => Effect.gen(function*() {
  let calls = 0;
  let active = 0;
  let released = 0;
  const queries = makeReadQueries(backend(() => Effect.scoped(Effect.gen(function*() {
    yield* Effect.acquireRelease(Effect.sync(() => { calls++; active++; }), () => Effect.sync(() => { active--; released++; }));
    return yield* Effect.never;
  }))));
  const fiber = yield* Effect.result(queries.bootstrap()).pipe(Effect.forkChild);
  yield* TestClock.adjust(14999);
  expect([calls, active, released]).toEqual([1, 1, 0]);
  yield* TestClock.adjust(1);
  expect([calls, active, released]).toEqual([1, 0, 1]);
  yield* TestClock.adjust(250);
  expect([calls, active, released]).toEqual([2, 1, 1]);
  yield* TestClock.adjust(15000);
  yield* TestClock.adjust(1000);
  expect([calls, active, released]).toEqual([3, 1, 2]);
  yield* TestClock.adjust(15000);
  const result = yield* Fiber.join(fiber);
  expect(result._tag).toBe("Failure");
  if (result._tag === "Failure") expect(result.failure).toBeInstanceOf(ReadTimeout);
  expect([calls, active, released]).toEqual([3, 0, 3]);
}));

it.effect("a response at 14999ms wins the timeout and performs no retry", () => Effect.gen(function*() {
  const response = yield* Deferred.make<BootstrapReply>();
  const started = yield* Deferred.make<void>();
  let calls = 0;
  const queries = makeReadQueries(backend(() => Effect.gen(function*() {
    calls++;
    yield* Deferred.succeed(started, undefined);
    return yield* Deferred.await(response);
  })));
  const fiber = yield* queries.bootstrap().pipe(Effect.forkChild);
  yield* Deferred.await(started);
  yield* TestClock.adjust(14999);
  yield* Deferred.succeed(response, reply);
  expect(yield* Fiber.join(fiber)).toBe(reply);
  yield* TestClock.adjust(1001);
  expect(calls).toBe(1);
}));

it.effect("timeout then successful transport query preserves the actual successful reply", () => Effect.gen(function*() {
  let calls = 0;
  const queries = makeReadQueries(backend(() => Effect.suspend(() => { calls++; return calls === 1 ? Effect.never : Effect.succeed(reply); })));
  const fiber = yield* queries.bootstrap().pipe(Effect.forkChild);
  yield* TestClock.adjust(15000);
  yield* TestClock.adjust(250);
  expect(yield* Fiber.join(fiber)).toBe(reply);
  expect(calls).toBe(2);
}));

it.effect("interruption during an in-flight request cleans its resource without reporting cancellation success", () => Effect.gen(function*() {
  const started = yield* Deferred.make<void>();
  let calls = 0;
  let active = 0;
  let released = 0;
  const queries = makeReadQueries(backend(() => Effect.scoped(Effect.gen(function*() {
    yield* Effect.acquireRelease(Effect.sync(() => { calls++; active++; }), () => Effect.sync(() => { active--; released++; }));
    yield* Deferred.succeed(started, undefined);
    return yield* Effect.never;
  }))));
  const fiber = yield* queries.bootstrap().pipe(Effect.forkChild);
  yield* Deferred.await(started);
  yield* Fiber.interrupt(fiber);
  const exit = yield* Fiber.await(fiber);
  expect(Exit.isFailure(exit)).toBe(true);
  if (Exit.isFailure(exit)) expect(Cause.hasInterrupts(exit.cause)).toBe(true);
  yield* TestClock.adjust(60000);
  expect([calls, active, released]).toEqual([1, 0, 1]);
}));

for (const delay of [249, 250, 1249]) {
  it.effect(`interruption during retry delay at ${delay}ms sends no further request`, () => Effect.gen(function*() {
    let calls = 0;
    const queries = makeReadQueries(backend(() => Effect.suspend(() => { calls++; return Effect.fail(new TransportError()); })));
    const fiber = yield* queries.bootstrap().pipe(Effect.forkChild);
    yield* TestClock.adjust(delay);
    const before = calls;
    yield* Fiber.interrupt(fiber);
    const exit = yield* Fiber.await(fiber);
    expect(Exit.isFailure(exit)).toBe(true);
    if (Exit.isFailure(exit)) expect(Cause.hasInterrupts(exit.cause)).toBe(true);
    yield* TestClock.adjust(60000);
    expect(calls).toBe(before);
  }));
}

it.effect("a dispatch defect is never retried or converted to a normal error/success", () => Effect.gen(function*() {
  let calls = 0;
  const defect = { password: "SECRET" };
  const queries = makeReadQueries(backend(() => Effect.suspend(() => { calls++; return Effect.die(defect); })));
  const exit = yield* Effect.exit(queries.bootstrap());
  expect(Exit.isFailure(exit)).toBe(true);
  if (Exit.isFailure(exit)) expect(Cause.hasDies(exit.cause)).toBe(true);
  yield* TestClock.adjust(60000);
  expect(calls).toBe(1);
}));
