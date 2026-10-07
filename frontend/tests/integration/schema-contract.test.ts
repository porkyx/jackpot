import { expect, it } from "@effect/vitest";
import { Effect, Schema } from "effect";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { BootstrapResponse, PendingOperationsResponse, isUtcTimestamp } from "../../src/contracts/schemas";

const fixture = (name: string): unknown => JSON.parse(readFileSync(resolve(import.meta.dirname, "../../../testdata/ipc", name), "utf8"));

for (const name of ["bootstrap-empty.json", "bootstrap-boundaries.json", "bootstrap-error.json"]) {
  it.effect(`decodes the actual Go serializer fixture ${name}`, () => Effect.gen(function*() {
    const decoded = yield* Schema.decodeUnknownEffect(BootstrapResponse)(fixture(name));
    expect(decoded.backendSessionId).toBe("session-fixture");
  }));
}

for (const name of ["pending-empty.json", "pending-boundaries.json"]) {
  it.effect(`decodes recovery page Go serializer fixture ${name}`, () => Effect.gen(function*() {
    const decoded = yield* Schema.decodeUnknownEffect(PendingOperationsResponse)(fixture(name));
    expect(decoded.backendSessionId).toBe("session-fixture");
    expect(decoded.data?.operations?.length).toBe(name === "pending-empty.json" ? 0 : 64);
  }));
}
it.effect("decodes actual DB failure without an empty success page", () => Effect.gen(function*() {
  const decoded = yield* Schema.decodeUnknownEffect(PendingOperationsResponse)(fixture("pending-error.json"));
  expect(decoded.ok).toBe(false);
  expect(decoded.code).toBe("StorageUnavailable");
  expect(decoded.data).toBeUndefined();
}));

for (const [name, data] of [
  ["null array", { operations: null, cursor: null }],
  ["missing array", { cursor: null }],
  ["empty cursor", { operations: [], cursor: "" }],
  ["missing cursor", { operations: [] }],
  ["too many", { operations: Array.from({ length: 65 }, (_, i) => ({ operationId: `op-${i}`, kind: "Rerun", collectionId: "c", roundId: "r", status: "pending", revision: 0 })), cursor: null }],
  ["secret", { operations: [{ operationId: "op", kind: "Rerun", collectionId: "c", roundId: "r", status: "pending", revision: 0, public_fingerprint: "synthetic-only" }], cursor: null }],
] as const) {
  it.effect(`rejects recovery page ${name}`, () => Effect.gen(function*() {
    const result = yield* Effect.exit(Schema.decodeUnknownEffect(PendingOperationsResponse)({ protocolVersion: 1, backendSessionId: "session", occurredAt: "2026-10-06T05:00:00Z", ok: true, data }));
    expect(result._tag).toBe("Failure");
  }));
}

const emptyWire = () => ({
  protocolVersion: 1, backendSessionId: "session", occurredAt: "2026-10-06T05:00:00Z", ok: true,
  data: { backendNow: "2026-10-06T05:00:00Z", theme: "system", activeDraft: null, pendingOperations: [], pendingCursor: null, recentResults: [] },
});
const withoutData = () => {
  const { data, ...envelope } = emptyWire();
  return envelope;
};

const badPayloads: readonly [string, unknown][] = [
  ["nil", null], ["undefined", undefined], ["empty", {}],
  ["protocol zero", { ...emptyWire(), protocolVersion: 0 }],
  ["protocol unknown", { ...emptyWire(), protocolVersion: 2 }],
  ["missing protocol", { ...emptyWire(), protocolVersion: undefined }],
  ["session empty", { ...emptyWire(), backendSessionId: "" }],
  ["timestamp null", { ...emptyWire(), occurredAt: null }],
  ["timestamp invalid", { ...emptyWire(), occurredAt: "2026-02-30T00:00:00Z" }],
  ["zero timestamp", { ...emptyWire(), occurredAt: "0001-01-01T00:00:00.000000000Z" }],
  ["success missing data", withoutData()],
  ["success null data", { ...emptyWire(), data: null }],
  ["success error code", { ...emptyWire(), code: "InvalidInput" }],
  ["success error message", { ...emptyWire(), messageKey: "InvalidInput" }],
  ["failure with data", { ...emptyWire(), ok: false, code: "InvalidInput", messageKey: "InvalidInput" }],
  ["failure missing code", { ...withoutData(), ok: false, messageKey: "InvalidInput" }],
  ["failure missing code and key", { ...withoutData(), ok: false }],
  ["failure unknown code", { ...withoutData(), ok: false, code: "unknown", messageKey: "unknown" }],
  ["failure zero code", { ...withoutData(), ok: false, code: "", messageKey: "" }],
  ["failure mismatched code", { ...withoutData(), ok: false, code: "InvalidInput", messageKey: "secret" }],
  ["optional operation null", { ...emptyWire(), operationId: null }],
  ["optional operation empty", { ...emptyWire(), operationId: "" }],
  ["optional revision null", { ...emptyWire(), revision: null }],
  ["revision negative", { ...emptyWire(), revision: -1 }],
  ["revision fractional", { ...emptyWire(), revision: 1.5 }],
  ["revision unsafe", { ...emptyWire(), revision: Number.MAX_SAFE_INTEGER + 1 }],
  ["revision NaN", { ...emptyWire(), revision: NaN }],
  ["revision infinity", { ...emptyWire(), revision: Infinity }],
  ["null pending array", { ...emptyWire(), data: { ...emptyWire().data, pendingOperations: null } }],
  ["null recent array", { ...emptyWire(), data: { ...emptyWire().data, recentResults: null } }],
  ["missing pending cursor", { ...emptyWire(), data: { ...emptyWire().data, pendingCursor: undefined } }],
  ["empty cursor", { ...emptyWire(), data: { ...emptyWire().data, pendingCursor: "" } }],
  ["unknown theme", { ...emptyWire(), data: { ...emptyWire().data, theme: "other" } }],
];
for (const [name, input] of badPayloads) {
  it.effect(`rejects ${name} without changing the last good payload`, () => Effect.gen(function*() {
    const good = yield* Schema.decodeUnknownEffect(BootstrapResponse)(emptyWire());
    const before = JSON.stringify(good);
    const result = yield* Effect.exit(Schema.decodeUnknownEffect(BootstrapResponse)(input));
    expect(result._tag).toBe("Failure");
    expect(JSON.stringify(good)).toBe(before);
  }));
}

it.effect("preserves required zero and maximum safe revision", () => Effect.gen(function*() {
  for (const revision of [0, Number.MAX_SAFE_INTEGER]) {
    const result = yield* Schema.decodeUnknownEffect(BootstrapResponse)({ ...emptyWire(), revision, operationId: "op" });
    expect(result.revision).toBe(revision);
  }
}));

for (const [raw, valid] of [
  ["2024-02-29T23:59:59Z", true], ["2000-02-29T00:00:00Z", true], ["1900-02-29T00:00:00Z", false],
  ["2026-02-29T00:00:00Z", false], ["2026-04-31T00:00:00Z", false],
  ["0001-01-01T00:00:00.000000001Z", true], ["9999-12-31T23:59:59.999999999Z", true],
  ["0000-01-01T00:00:00Z", false], ["2026-00-01T00:00:00Z", false], ["2026-13-01T00:00:00Z", false],
  ["2026-01-00T00:00:00Z", false], ["2026-01-01T24:00:00Z", false], ["2026-01-01T00:60:00Z", false],
  ["2026-01-01T00:00:60Z", false], ["2026-01-01T00:00:00+09:00", false],
  ["2026-01-01T00:00:00.1234567890Z", false], ["0001-01-01T00:00:00Z", false], ["0001-01-01T00:00:00.000Z", false],
] as const) {
  it(`UTC calendar boundary ${raw}`, () => expect(isUtcTimestamp(raw)).toBe(valid));
}

function record(value: unknown): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) throw new Error("fixture object required");
  return value as Record<string, unknown>;
}
function corruptedBoundary(path: readonly string[], value: unknown): unknown {
  const cloned: unknown = structuredClone(fixture("bootstrap-boundaries.json"));
  let target = record(cloned);
  for (const key of path.slice(0, -1)) target = record(target[key]);
  const key = path.at(-1);
  if (key === undefined) throw new Error("corruption path required");
  target[key] = value;
  return cloned;
}

const descriptor = (index: number) => ({ operationId: `op-${index}`, kind: "CreateCollection", collectionId: "collection", roundId: "round", status: "pending", revision: 0 });
const nestedCorruptions: readonly [string, readonly string[], unknown][] = [
  ["pending limit +1", ["data", "pendingOperations"], Array.from({ length: 65 }, (_, index) => descriptor(index))],
  ["recent limit +1", ["data", "recentResults"], Array.from({ length: 17 }, () => ({ collectionId: "c", roundId: "r", revision: 1 }))],
  ["descriptor secret", ["data", "pendingOperations"], [{ ...descriptor(1), password: "synthetic-only" }]],
  ["descriptor nil", ["data", "pendingOperations"], [null]],
  ["descriptor primitive", ["data", "pendingOperations"], ["pending"]],
  ["descriptor array", ["data", "pendingOperations"], [[]]],
  ["descriptor missing kind", ["data", "pendingOperations"], [{ ...descriptor(1), kind: undefined }]],
  ["descriptor unknown kind", ["data", "pendingOperations"], [{ ...descriptor(1), kind: "unknown" }]],
  ["descriptor zero status", ["data", "pendingOperations"], [{ ...descriptor(1), status: "" }]],
  ["descriptor unknown status", ["data", "pendingOperations"], [{ ...descriptor(1), status: "unknown" }]],
  ["descriptor terminal status", ["data", "pendingOperations"], [{ ...descriptor(1), status: "succeeded" }]],
  ["descriptor empty ID", ["data", "pendingOperations"], [{ ...descriptor(1), operationId: "" }]],
  ["draft zero state", ["data", "activeDraft", "state"], ""],
  ["draft unknown state", ["data", "activeDraft", "state"], "unknown"],
  ["draft unsafe generation", ["data", "activeDraft", "articleGeneration"], Number.MAX_SAFE_INTEGER + 1],
  ["draft generation null", ["data", "activeDraft", "articleGeneration"], null],
  ["snapshot missing complete", ["data", "activeDraft", "snapshot", "complete"], undefined],
  ["snapshot invalid date", ["data", "activeDraft", "snapshot", "collectedAt"], "invalid"],
  ["snapshot pages max+1", ["data", "activeDraft", "snapshot", "pages"], 21],
  ["snapshot comments max+1", ["data", "activeDraft", "snapshot", "acceptedComments"], 100001],
  ["snapshot negative count", ["data", "activeDraft", "snapshot", "deletedComments"], -1],
  ["snapshot unsupported max+1", ["data", "activeDraft", "snapshot", "unsupportedComments"], 100001],
];
for (const [name, path, value] of nestedCorruptions) {
  it.effect(`rejects ${name}`, () => Effect.gen(function*() {
    const result = yield* Effect.exit(Schema.decodeUnknownEffect(BootstrapResponse)(corruptedBoundary(path, value)));
    expect(result._tag).toBe("Failure");
  }));
}

it.effect("ignores harmless additional read fields and preserves the source object", () => Effect.gen(function*() {
  const raw = { ...emptyWire(), compatibleFutureField: "extra" };
  const before = JSON.stringify(raw);
  const decoded = yield* Schema.decodeUnknownEffect(BootstrapResponse)(raw);
  expect("compatibleFutureField" in decoded).toBe(false);
  expect(JSON.stringify(raw)).toBe(before);
}));
