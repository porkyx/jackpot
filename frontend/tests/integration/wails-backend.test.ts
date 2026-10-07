import { expect, it } from "@effect/vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { Deferred, Effect, Exit, Fiber, Scope } from "effect";
import { BackendRejected, ProtocolError, TransportError } from "../../src/contracts/backend";
import { makeWailsBackend, StateNoticeSchema, type WailsReadPort } from "../../src/platform/backend";
import { Schema } from "effect";

function fixture(name: string): unknown { return JSON.parse(readFileSync(resolve(import.meta.dirname, "../../../testdata/ipc", `${name}.json`), "utf8")); }
function port(bootstrap: unknown = fixture("bootstrap-empty"), pending: unknown = fixture("pending-empty")): WailsReadPort {
  return { bootstrap: () => Promise.resolve(bootstrap), listPendingOperations: () => Promise.resolve(pending), getOperation: () => Promise.resolve(fixture("operation-unknown")), onStateChanged: () => () => {} };
}
const notice = { backendSessionId: "session", entityKind: "draft", entityId: "draft", revision: 1, operationId: null } as const;

it.effect("raw adapter lazily decodes actual Go read fixtures and preserves failure envelope", () => Effect.gen(function*() {
  let calls = 0;
  const backend = makeWailsBackend({ ...port(), bootstrap: () => { calls++; return Promise.resolve(fixture("bootstrap-empty")); } });
  const read = backend.bootstrap(); expect(calls).toBe(0);
  const reply = yield* read; expect(reply.protocolVersion).toBe(1); expect(reply.data.pendingOperations).toEqual([]); expect(calls).toBe(1);
  expect((yield* backend.listPendingOperations({ cursor: null, limit: 64 })).data.operations).toEqual([]);
  for (const kind of ["bootstrap", "pending"] as const) {
    const rejected = makeWailsBackend(port(fixture("bootstrap-error"), fixture("pending-error")));
    const error = kind === "bootstrap" ? yield* Effect.flip(rejected.bootstrap()) : yield* Effect.flip(rejected.listPendingOperations({ cursor: null, limit: 1 }));
    expect(error).toBeInstanceOf(BackendRejected);
    expect(error._tag === "BackendRejected" ? error.code : "").toBe(kind === "bootstrap" ? "InvalidState" : "StorageUnavailable");
  }
}));

it.effect("unknown envelope tags missing metadata dirty dates and null data fail closed", () => Effect.gen(function*() {
  const valid = fixture("bootstrap-empty") as Record<string, unknown>;
  for (const changed of [
    null, {}, { ...valid, protocolVersion: 2 }, { ...valid, backendSessionId: "" }, { ...valid, occurredAt: "bad" },
    { ...valid, data: null }, { ...valid, data: undefined }, { ...valid, code: "InvalidInput" },
    { ...valid, ok: false, data: undefined, code: "unknown", messageKey: "private-password" },
  ]) { expect(yield* Effect.flip(makeWailsBackend(port(changed)).bootstrap())).toBeInstanceOf(ProtocolError); }
}));

it.effect("first Nth and continuous Promise failures stay transport errors without hidden retries or secret causes", () => Effect.gen(function*() {
  for (const failureAt of [1, 3, 0]) {
    let calls = 0;
    const backend = makeWailsBackend({ ...port(), bootstrap: () => { calls++; return failureAt === 0 || calls === failureAt ? Promise.reject(new Error("private-password-and-path")) : Promise.resolve(fixture("bootstrap-empty")); } });
    for (let call = 1; call <= 4; call++) {
      const result = yield* Effect.result(backend.bootstrap());
      const failed = failureAt === 0 || call === failureAt;
      expect(result._tag).toBe(failed ? "Failure" : "Success");
      if (result._tag === "Failure") { expect(result.failure).toBeInstanceOf(TransportError); expect(JSON.stringify(result.failure)).not.toContain("private-password"); }
    }
    expect(calls).toBe(4);
  }
  const backend = makeWailsBackend({ ...port(), bootstrap: () => { throw new Error("private-password"); } });
  expect(yield* Effect.flip(backend.bootstrap())).toBeInstanceOf(TransportError);
}));

it.effect("pending request boundaries are validated before transport and only cursor limit are transmitted", () => Effect.gen(function*() {
  const requests: Array<{ readonly cursor: string | null; readonly limit: number }> = [];
  const backend = makeWailsBackend({ ...port(), listPendingOperations: (request) => { requests.push(request); return Promise.resolve(fixture("pending-empty")); } });
  for (const limit of [0, -1, 65, 1.5, Number.NaN, Number.POSITIVE_INFINITY]) {
    expect(yield* Effect.flip(backend.listPendingOperations({ cursor: null, limit }))).toBeInstanceOf(ProtocolError);
  }
  expect(yield* Effect.flip(backend.listPendingOperations({ cursor: "", limit: 1 }))).toBeInstanceOf(ProtocolError);
  expect(requests).toEqual([]);
  const original = { cursor: "cursor", limit: 1, password: "private" };
  yield* backend.listPendingOperations(original);
  yield* backend.listPendingOperations({ cursor: null, limit: 64 });
  expect(requests).toEqual([{ cursor: "cursor", limit: 1 }, { cursor: null, limit: 64 }]);
  expect(original.password).toBe("private");
}));

it.effect("pending transport catches synchronous and Promise failures without retry", () => Effect.gen(function*() {
  for (const synchronous of [false, true]) {
    let calls = 0;
    const backend = makeWailsBackend({ ...port(), listPendingOperations: () => {
      calls++;
      if (synchronous) throw new Error("private-password");
      return Promise.reject(new Error("private-password"));
    } });
    for (let attempt = 0; attempt < 3; attempt++) {
      const error = yield* Effect.flip(backend.listPendingOperations({ cursor: null, limit: 1 }));
      expect(error).toBeInstanceOf(TransportError); expect(JSON.stringify(error)).not.toContain("private-password");
    }
    expect(calls).toBe(3);
  }
}));

it.effect("interruption aborts the read signal and leaves no pending native worker", () => Effect.gen(function*() {
  const entered = yield* Deferred.make<void>(); let aborted = 0;
  const backend = makeWailsBackend({ ...port(), bootstrap: (signal) => new Promise<unknown>((_resolve, reject) => {
    signal.addEventListener("abort", () => { aborted++; reject(new Error("cancelled private path")); }, { once: true });
    Effect.runSync(Deferred.succeed(entered, undefined));
  }) });
  const fiber = yield* Effect.forkScoped(backend.bootstrap()); yield* Deferred.await(entered);
  yield* Fiber.interrupt(fiber); expect(aborted).toBe(1);
}));

it.effect("actual Go state notice fixture and each corrupted metadata field are decoded independently", () => Effect.gen(function*() {
  const actual = Schema.decodeUnknownSync(StateNoticeSchema)(fixture("state-notice")); expect(actual.revision).toBe(Number.MAX_SAFE_INTEGER); expect(actual.operationId).toBeNull();
  for (const raw of [null, [], "string", {}, { ...notice, backendSessionId: "" }, { ...notice, entityKind: "unknown" }, { ...notice, entityId: "" }, { ...notice, revision: -1 }, { ...notice, revision: Number.MAX_SAFE_INTEGER + 1 }, { ...notice, operationId: "" }, { ...notice, password: "private" }]) {
    expect(() => Schema.decodeUnknownSync(StateNoticeSchema)(raw)).toThrow();
  }
  expect(Schema.decodeUnknownSync(StateNoticeSchema)({ ...notice, entityKind: "collection", revision: 0, operationId: "operation" })).toEqual({ ...notice, entityKind: "collection", revision: 0, operationId: "operation" });
}));

it.effect("each subscription owns its unsubscribe once and ignores callbacks after its scope closes", () => Effect.gen(function*() {
  const callbacks: Array<(data: unknown) => void> = []; let closes = 0;
  const backend = makeWailsBackend({ ...port(), onStateChanged: (callback) => { callbacks.push(callback); return () => { closes++; }; } });
  const first = yield* Scope.make(); const second = yield* Scope.make(); const seen: number[] = []; const errors: string[] = [];
  yield* backend.subscribeStateChanges(() => seen.push(1), (error) => errors.push(error._tag)).pipe(Scope.provide(first));
  yield* backend.subscribeStateChanges(() => seen.push(2), (error) => errors.push(error._tag)).pipe(Scope.provide(second));
  callbacks.forEach((callback) => callback(notice)); expect(seen).toEqual([1, 2]);
  callbacks.forEach((callback) => callback({ ...notice, secret: "private" })); expect(errors).toEqual(["ProtocolError", "ProtocolError"]); expect(seen).toEqual([1, 2]);
  yield* Scope.close(first, Exit.void); yield* Scope.close(first, Exit.void); expect(closes).toBe(1);
  callbacks.forEach((callback) => callback(notice)); expect(seen).toEqual([1, 2, 2]);
  yield* Scope.close(second, Exit.void); expect(closes).toBe(2);
  callbacks.forEach((callback) => callback(notice)); expect(seen).toEqual([1, 2, 2]);
}));

it.effect("subscription acquisition failure is typed and a finalizer failure still disables its callback", () => Effect.gen(function*() {
  const backend = makeWailsBackend({ ...port(), onStateChanged: () => { throw new Error("private event path"); } });
  expect(yield* Effect.flip(Effect.scoped(backend.subscribeStateChanges(() => {}, () => {})))).toBeInstanceOf(TransportError);
  let callback: ((raw: unknown) => void) | undefined; let seen = 0; let closes = 0;
  const failing = makeWailsBackend({ ...port(), onStateChanged: (listener) => { callback = listener; return () => { closes++; throw new Error("close failed"); }; } });
  const scope = yield* Scope.make(); yield* failing.subscribeStateChanges(() => { seen++; }, () => {}).pipe(Scope.provide(scope));
  const exit = yield* Effect.exit(Scope.close(scope, Exit.void)); expect(Exit.isFailure(exit)).toBe(true); expect(closes).toBe(1);
  callback?.(notice); expect(seen).toBe(0);
}));
