import { expect, it, vi } from "@effect/vitest";
import { Effect } from "effect";
import { ClientIdError, ClientIds, ClientIdsLive, clientIdsFrom, clientIdsSequence } from "../../src/platform/ids";

it.effect("ID source is lazy and each operation or intent consumes a fresh value", () => Effect.gen(function*() {
  let calls = 0;
  const ids = clientIdsFrom(() => `id-${++calls}`);
  expect(calls).toBe(0);
  expect(yield* ids.operationId).toBe("id-1");
  expect(yield* ids.intentId).toBe("id-2");
  expect(yield* ids.operationId).toBe("id-3");
  expect(calls).toBe(3);
}));

it.effect("ID source failure is safe and does not poison subsequent reads", () => Effect.gen(function*() {
  let calls = 0;
  const ids = clientIdsFrom(() => {
    calls++;
    if (calls === 2) throw new Error("password=secret filepath=private");
    return `id-${calls}`;
  });
  expect(yield* ids.operationId).toBe("id-1");
  const failed = yield* Effect.result(ids.intentId);
  expect(failed._tag).toBe("Failure");
  if (failed._tag === "Failure") {
    expect(failed.failure).toBeInstanceOf(ClientIdError);
    expect(JSON.stringify(failed.failure)).not.toContain("secret");
    expect(JSON.stringify(failed.failure)).not.toContain("private");
  }
  expect(yield* ids.operationId).toBe("id-3");
}));

it.effect("empty IDs and continuously failing sources produce explicit errors", () => Effect.gen(function*() {
  for (const source of [() => "", () => { throw new Error("failure"); }]) {
    const ids = clientIdsFrom(source);
    for (let call = 0; call < 3; call++) {
      const result = yield* Effect.result(ids.operationId);
      expect(result._tag).toBe("Failure");
      if (result._tag === "Failure") expect(result.failure._tag).toBe("ClientIdError");
    }
  }
}));

it.effect("corrupted JavaScript ID sources cannot return non-string values", () => Effect.gen(function*() {
  for (const value of [null, undefined, 0, false, []]) {
    // Deliberately corrupt the declared source type at this adapter boundary.
    const ids = clientIdsFrom(() => value as never);
    expect((yield* Effect.result(ids.operationId))._tag).toBe("Failure");
  }
}));

it.effect("nonempty opaque IDs preserve Unicode and whitespace without reinterpretation", () => Effect.gen(function*() {
  for (const value of ["a", " ", "작업/☃", "x".repeat(65536)]) {
    expect(yield* clientIdsFrom(() => value).operationId).toBe(value);
  }
}));

it.effect("a controlled sequence snapshots its input and fails on exact exhaustion", () => Effect.gen(function*() {
  const values = ["first", "second"];
  const layer = clientIdsSequence(values);
  values[0] = "changed";
  values.push("third");
  yield* Effect.gen(function*() {
    const ids = yield* ClientIds;
    expect(yield* ids.operationId).toBe("first");
    expect(yield* ids.intentId).toBe("second");
    expect((yield* Effect.result(ids.operationId))._tag).toBe("Failure");
    expect((yield* Effect.result(ids.intentId))._tag).toBe("Failure");
  }).pipe(Effect.provide(layer));
}));

it.effect("zero-length controlled sequence cannot fabricate an ID", () => Effect.gen(function*() {
  yield* Effect.gen(function*() {
    const ids = yield* ClientIds;
    expect((yield* Effect.result(ids.operationId))._tag).toBe("Failure");
  }).pipe(Effect.provide(clientIdsSequence([])));
}));

it.effect("independent acquisition of one sequence starts at its own first ID", () => Effect.gen(function*() {
  const layer = clientIdsSequence(["first", "second"]);
  for (let index = 0; index < 4; index++) {
    yield* Effect.scoped(Effect.gen(function*() {
      const ids = yield* ClientIds;
      expect(yield* ids.operationId).toBe("first");
    }).pipe(Effect.provide(layer)));
  }
}));

it.effect("production IDs invoke Web Crypto for every value and remain independent of test sequences", () => Effect.gen(function*() {
  const first = "00000000-0000-4000-8000-000000000001";
  const second = "00000000-0000-4000-8000-000000000002";
  const randomUUID = vi.spyOn(crypto, "randomUUID").mockReturnValueOnce(first).mockReturnValueOnce(second);
  try {
    yield* Effect.gen(function*() {
      const ids = yield* ClientIds;
      expect(randomUUID).not.toHaveBeenCalled();
      expect(yield* ids.operationId).toBe(first);
      expect(yield* ids.intentId).toBe(second);
      expect(randomUUID).toHaveBeenCalledTimes(2);
    }).pipe(Effect.provide(ClientIdsLive));
    yield* Effect.gen(function*() {
      const ids = yield* ClientIds;
      expect(yield* ids.operationId).toBe("test");
      expect(randomUUID).toHaveBeenCalledTimes(2);
    }).pipe(Effect.provide(clientIdsSequence(["test"])));
  } finally {
    randomUUID.mockRestore();
  }
}));
