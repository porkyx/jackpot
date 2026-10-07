import { expect, it } from "@effect/vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { Effect, Layer } from "effect";
import { Backend, ProtocolError, TransportError } from "../../src/contracts/backend";
import { ClientIds, clientIdsFrom } from "../../src/platform/ids";
import { makeWailsBackend, type WailsReadPort } from "../../src/platform/backend";
import { OperationCoordinator, OperationCoordinatorLive } from "../../src/operations/coordinator";
import { makePendingRecovery } from "../../src/operations/pendingRecovery";

const fixture = (name: string): unknown => JSON.parse(readFileSync(resolve(import.meta.dirname, "../../../testdata/ipc", `${name}.json`), "utf8"));
const coordinatorLayer = (port: WailsReadPort) => OperationCoordinatorLive.pipe(Layer.provide(Layer.merge(
  Layer.succeed(Backend, makeWailsBackend(port)),
  Layer.succeed(ClientIds, clientIdsFrom(() => { throw new Error("Recovery must not create a new operation ID"); })),
)));

it.effect("actual SQLite serializer metadata crosses Wails Schema and the live Coordinator into 64 recovery slots", () => Effect.scoped(Effect.gen(function*() {
  let bootstraps = 0; let reads = 0; let pages = 0; let invalid = false;
  const port: WailsReadPort = {
    bootstrap: () => { bootstraps++; return Promise.resolve(fixture("bootstrap-boundaries")); },
    listPendingOperations: () => { pages++; return Promise.resolve(fixture("pending-empty")); },
    getOperation: (id, signal) => {
      reads++; expect(id).toBe("operation-001"); expect(signal).toBeInstanceOf(AbortSignal);
      return Promise.resolve(fixture(invalid ? "operation-unknown" : "operation-pending"));
    },
    onStateChanged: () => () => { throw new Error("Recovery itself must not subscribe to native events"); },
  };
  yield* Effect.gen(function*() {
    const coordinator = yield* OperationCoordinator;
    const seed = yield* coordinator.bootstrap();
    const recovery = yield* makePendingRecovery(coordinator, seed);
    const before = yield* recovery.snapshot;
    expect([before.pending.length, before.enumerationComplete, before.readsAllowed, before.writesBlocked]).toEqual([64, false, true, true]);
    const observed = yield* recovery.requery("operation-001");
    expect(observed.pending[0]!.observation).toEqual({ operationId: "operation-001", state: "pending", kind: "CreateCollection", collectionId: "collection-001", roundId: "round-001", revision: 1, failureCode: null });
    expect(observed.terminal).toEqual([]);
    invalid = true;
    expect(yield* Effect.flip(recovery.requery("operation-001"))).toBeInstanceOf(ProtocolError);
    const rejected = yield* recovery.snapshot;
    expect(rejected.pending).toHaveLength(64);
    expect(rejected.pending[0]!.observation).toEqual(observed.pending[0]!.observation);
    expect(rejected.pending[0]!.failure?.code).toBe("ProtocolError");
    expect(rejected.terminal).toEqual([]);
    expect([bootstraps, reads, pages]).toEqual([1, 2, 0]);
    expect(JSON.stringify(rejected)).not.toMatch(/snapshotId|draftId|acceptedComments|fingerprint|password/);
  }).pipe(Effect.provide(coordinatorLayer(port)));
})));

it.effect("an actual empty Bootstrap is ready while a transport failure stays a visible failure", () => Effect.scoped(Effect.gen(function*() {
  let failing = false; let calls = 0;
  const port: WailsReadPort = {
    bootstrap: () => { calls++; return failing ? Promise.reject(new Error("SECRET")) : Promise.resolve(fixture("bootstrap-empty")); },
    listPendingOperations: () => Promise.reject(new Error("unused")),
    getOperation: () => Promise.reject(new Error("unused")),
    onStateChanged: () => () => {},
  };
  yield* Effect.gen(function*() {
    const coordinator = yield* OperationCoordinator;
    const recovery = yield* makePendingRecovery(coordinator, yield* coordinator.bootstrap());
    expect((yield* recovery.resume).phase).toBe("ready");
    expect(yield* recovery.canMutateCollection("new")).toBe(true);
    failing = true;
    // Read retry is owned by the Coordinator. This test observes its error
    // directly at the recovery boundary, without advancing native wall time.
    const rawBackend = makeWailsBackend(port);
    const unavailable = yield* makePendingRecovery({ ...coordinator, bootstrap: () => rawBackend.bootstrap().pipe(Effect.map((reply) => ({ reply, receivedAtMillis: 0 }))) });
    expect(yield* Effect.flip(unavailable.resync)).toBeInstanceOf(TransportError);
    const failure = yield* unavailable.snapshot;
    expect([failure.phase, failure.failure?.code, failure.readsAllowed, failure.writesBlocked]).toEqual(["failed", "TransportError", true, true]);
    expect(JSON.stringify(failure)).not.toContain("SECRET");
    expect(calls).toBe(2);
  }).pipe(Effect.provide(coordinatorLayer(port)));
})));