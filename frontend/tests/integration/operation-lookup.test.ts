import { expect, it } from "@effect/vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { Effect, Schema } from "effect";
import { OperationLookupResponse, OperationObservation } from "../../src/contracts/schemas";
import { ProtocolError, TransportError } from "../../src/contracts/backend";
import { makeWailsBackend, type WailsReadPort } from "../../src/platform/backend";

const fixture = (name: string): unknown => JSON.parse(readFileSync(resolve(import.meta.dirname, "../../../testdata/ipc", `${name}.json`), "utf8"));
const known = { operationId: "operation", state: "pending", kind: "Rerun", collectionId: "collection", roundId: "round", revision: 1, failureCode: null };
const unknown = { operationId: "operation", state: "unknown", kind: null, collectionId: null, roundId: null, revision: null, failureCode: null };
const envelope = { protocolVersion: 1, backendSessionId: "session", occurredAt: "2026-10-06T00:00:00Z", ok: true };
function port(read: WailsReadPort["getOperation"]): WailsReadPort { return { bootstrap: () => Promise.reject(), listPendingOperations: () => Promise.reject(), onStateChanged: () => () => {}, getOperation: read }; }

for (const name of ["operation-unknown", "operation-pending"]) it.effect(`actual SQLite serializer ${name} has the stated observation`, () => Effect.gen(function*() {
  const decoded = yield* Schema.decodeUnknownEffect(OperationLookupResponse)(fixture(name));
  expect(decoded.data?.state).toBe(name === "operation-unknown" ? "unknown" : "pending");
  expect(Object.keys(decoded.data ?? {}).sort()).toEqual(["operationId", "state", "kind", "collectionId", "roundId", "revision", "failureCode"].sort());
}));
it.effect("observation state branches and counter boundaries accept only consistent public metadata", () => Effect.gen(function*() {
  for (const raw of [unknown, known, { ...known, state: "succeeded" }, { ...known, state: "failed", failureCode: "StorageUnavailable" }, ...[0, Number.MAX_SAFE_INTEGER].map((revision) => ({ ...known, revision }))]) expect(yield* Schema.decodeUnknownEffect(OperationObservation)(raw)).toEqual(raw);
  for (const field of ["operationId", "kind", "collectionId", "roundId"]) {
    expect(() => Schema.decodeUnknownSync(OperationObservation)({ ...known, [field]: "" })).toThrow();
  }
  for (const field of ["kind", "collectionId", "roundId", "revision"]) expect(() => Schema.decodeUnknownSync(OperationObservation)({ ...known, [field]: null })).toThrow();
  for (const field of ["kind", "collectionId", "roundId", "revision", "failureCode"]) expect(() => Schema.decodeUnknownSync(OperationObservation)({ ...unknown, [field]: known[field as keyof typeof known] ?? "StorageUnavailable" })).toThrow();
  for (const raw of [null, [], "text", {}, { ...known, state: "" }, { ...known, state: "invalid" }, { ...known, kind: "invalid" }, { ...known, revision: -1 }, { ...known, revision: Number.MAX_SAFE_INTEGER + 1 }, { ...known, state: "failed" }, { ...known, state: "succeeded", failureCode: "StorageUnavailable" }, { ...known, failureCode: "invalid" }, { ...known, fingerprint: "SECRET" }]) expect(() => Schema.decodeUnknownSync(OperationObservation)(raw)).toThrow();
}));
it.effect("raw operation query validates identity and never disguises transport failure as unknown", () => Effect.gen(function*() {
  let calls = 0;
  const backend = makeWailsBackend(port((id, signal) => { calls++; expect(id).toBe("operation"); expect(signal).toBeInstanceOf(AbortSignal); return Promise.resolve({ ...envelope, data: unknown }); }));
  expect((yield* backend.getOperation("operation")).data.state).toBe("unknown"); expect(calls).toBe(1);
  expect(yield* Effect.flip(backend.getOperation(""))).toBeInstanceOf(ProtocolError); expect(calls).toBe(1);
  expect(yield* Effect.flip(makeWailsBackend(port(() => Promise.resolve({ ...envelope, data: { ...known, operationId: "other" } }))).getOperation("operation"))).toBeInstanceOf(ProtocolError);
  for (const synchronous of [false, true]) {
    calls = 0;
    const failing = makeWailsBackend(port(() => { calls++; if (synchronous) throw new Error("SECRET"); return Promise.reject(new Error("SECRET")); }));
    for (let call = 0; call < 3; call++) { const error = yield* Effect.flip(failing.getOperation("operation")); expect(error).toBeInstanceOf(TransportError); expect(JSON.stringify(error)).not.toContain("SECRET"); }
    expect(calls).toBe(3);
  }
}));
