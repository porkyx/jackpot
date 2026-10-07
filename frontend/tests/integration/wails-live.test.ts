import { expect, it } from "@effect/vitest";
import { Effect, Exit, Layer, Scope } from "effect";
import { vi } from "vitest";
import { Backend } from "../../src/contracts/backend";
import { StateChangedTopic } from "../../src/platform/backend";
import { WailsBackendLive } from "../../src/platform/wails";

const native = vi.hoisted(() => ({ bootstrap: vi.fn(), pending: vi.fn(), operation: vi.fn(), on: vi.fn() }));
vi.mock("../../bindings/github.com/porkyx/jackpot/internal/desktop/service", () => ({ Bootstrap: native.bootstrap, ListPendingOperations: native.pending }));
vi.mock("../../bindings/github.com/porkyx/jackpot/internal/desktop/recoveryservice", () => ({ GetOperation: native.operation }));
vi.mock("@wailsio/runtime", () => ({ Events: { On: native.on } }));
const envelope = { protocolVersion: 1, backendSessionId: "session", occurredAt: "2026-10-06T00:00:00Z", ok: true };

it.effect("live binding adapter forwards each caller AbortSignal and the exact sanitized pending request", () => Effect.gen(function*() {
  native.bootstrap.mockReset(); native.pending.mockReset(); native.operation.mockReset();
  const bootstrapSignals: AbortSignal[] = []; const pendingSignals: AbortSignal[] = []; const operationSignals: AbortSignal[] = [];
  native.bootstrap.mockImplementation(() => ({ cancelOn: (signal: AbortSignal) => {
    bootstrapSignals.push(signal);
    return Promise.resolve({ ...envelope, data: { backendNow: envelope.occurredAt, theme: "system", activeDraft: null, pendingOperations: [], pendingCursor: null, recentResults: [] } });
  } }));
  native.pending.mockImplementation(() => ({ cancelOn: (signal: AbortSignal) => {
    pendingSignals.push(signal); return Promise.resolve({ ...envelope, data: { operations: [], cursor: null } });
  } }));
  native.operation.mockImplementation(() => ({ cancelOn: (signal: AbortSignal) => {
    operationSignals.push(signal); return Promise.resolve({ ...envelope, data: { operationId: "operation", state: "unknown", kind: null, collectionId: null, roundId: null, revision: null, failureCode: null } });
  } }));
  yield* Effect.gen(function*() {
    const backend = yield* Backend;
    yield* backend.bootstrap(); yield* backend.bootstrap();
    yield* backend.listPendingOperations({ cursor: "opaque", limit: 64 });
    yield* backend.getOperation("operation");
  }).pipe(Effect.provide(WailsBackendLive));
  expect(native.bootstrap).toHaveBeenCalledTimes(2); expect(native.pending).toHaveBeenCalledExactlyOnceWith({ cursor: "opaque", limit: 64 });
  expect(bootstrapSignals).toHaveLength(2); expect(pendingSignals).toHaveLength(1);
  expect(bootstrapSignals[0]).toBeInstanceOf(AbortSignal); expect(bootstrapSignals[0]).not.toBe(bootstrapSignals[1]);
  expect(pendingSignals[0]).not.toBe(bootstrapSignals[0]);
  expect(native.operation).toHaveBeenCalledExactlyOnceWith({ operationId: "operation" });
  expect(operationSignals).toHaveLength(1); expect(operationSignals[0]).toBeInstanceOf(AbortSignal);
  expect(operationSignals[0]).not.toBe(pendingSignals[0]);
}));

it.effect("Events.On adapter unwraps event.data and releases only its own subscription", () => Effect.gen(function*() {
  native.on.mockReset();
  const callbacks: Array<(event: { readonly data: unknown }) => void> = [];
  const unsubscribes = [vi.fn(), vi.fn()];
  native.on.mockImplementation((name: string, callback: (event: { readonly data: unknown }) => void) => {
    expect(name).toBe(StateChangedTopic); callbacks.push(callback); return unsubscribes[callbacks.length - 1];
  });
  const app = yield* Scope.make(); const context = yield* Layer.buildWithScope(WailsBackendLive, app);
  const backend = context.mapUnsafe.get(Backend.key) as typeof Backend.Service;
  const first = yield* Scope.make(); const second = yield* Scope.make(); const seen: string[] = []; const faults: string[] = [];
  yield* backend.subscribeStateChanges((notice) => seen.push(notice.entityId), (error) => faults.push(error._tag)).pipe(Scope.provide(first));
  yield* backend.subscribeStateChanges((notice) => seen.push(notice.entityId), (error) => faults.push(error._tag)).pipe(Scope.provide(second));
  const data = { backendSessionId: "session", entityKind: "draft", entityId: "draft", revision: 1, operationId: null };
  callbacks.forEach((callback) => callback({ data })); expect(seen).toEqual(["draft", "draft"]);
  callbacks.forEach((callback) => callback({ data: { ...data, password: "private" } })); expect(faults).toEqual(["ProtocolError", "ProtocolError"]);
  yield* Scope.close(first, Exit.void); yield* Scope.close(first, Exit.void);
  expect(unsubscribes[0]).toHaveBeenCalledOnce(); expect(unsubscribes[1]).not.toHaveBeenCalled();
  callbacks.forEach((callback) => callback({ data })); expect(seen).toEqual(["draft", "draft", "draft"]);
  yield* Scope.close(second, Exit.void); yield* Scope.close(app, Exit.void); expect(unsubscribes[1]).toHaveBeenCalledOnce();
}));
