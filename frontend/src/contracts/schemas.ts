import { Schema } from "effect";

export const OpaqueId = Schema.String.check(Schema.isMinLength(1));
export const SafeCounter = Schema.Number.check(Schema.makeFilter((value) =>
  Number.isSafeInteger(value) && value >= 0 ? undefined : "safe nonnegative integer required",
));

export function isUtcTimestamp(raw: string): boolean {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?Z$/.exec(raw);
  if (match === null) return false;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const hour = Number(match[4]);
  const minute = Number(match[5]);
  const second = Number(match[6]);
  if (year < 1 || month < 1 || month > 12 || day < 1 || hour > 23 || minute > 59 || second > 59) return false;
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const maxDay = month === 2 ? (leap ? 29 : 28) : ([4, 6, 9, 11].includes(month) ? 30 : 31);
  if (day > maxDay) return false;
  return !/^0001-01-01T00:00:00(?:\.0{1,9})?Z$/.test(raw);
}

export const UtcTimestamp = Schema.String.check(Schema.makeFilter((raw) =>
  isUtcTimestamp(raw) ? undefined : "nonzero UTC RFC3339 timestamp required",
));
const nonzeroTag = Schema.makeFilter<string>((value) => value !== "" ? undefined : "zero enum forbidden");
const DraftState = Schema.Literals(["", "empty", "loading", "ready", "finalized"]).check(nonzeroTag);
const OperationState = Schema.Literals(["", "unknown", "pending", "succeeded", "failed"]).check(nonzeroTag);
const ErrorCode = Schema.Literals(["", "InvalidInput", "InvalidState", "StaleRevision", "StaleArticleContext", "BackendSessionChanged", "ProtocolError", "StorageUnavailable"]).check(nonzeroTag);

const OperationDescriptorFields = Schema.Struct({
  operationId: OpaqueId,
  kind: Schema.String.check(Schema.makeFilter((kind) =>
    ["CreateCollection", "Rerun", "SetSchedule", "CancelSchedule", "RetryRound", "ExecuteDue"].includes(kind) ? undefined : "unknown durable operation kind",
  )),
  collectionId: OpaqueId,
  roundId: OpaqueId,
  status: OperationState.check(Schema.makeFilter((status) => status === "pending" ? undefined : "pending descriptor required")),
  revision: SafeCounter,
});
// Check the raw object before Struct decoding can discard unknown properties.
export const OperationDescriptor = Schema.Unknown.check(Schema.makeFilter((raw) => {
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) return undefined;
  return Object.keys(raw).every((key) => Object.hasOwn(OperationDescriptorFields.fields, key))
    ? undefined : "unexpected operation descriptor field";
})).pipe(Schema.decodeTo(OperationDescriptorFields));

const count = (max: number) => SafeCounter.check(Schema.isLessThanOrEqualTo(max));
export const SnapshotSummary = Schema.Struct({
  snapshotId: OpaqueId, collectedAt: UtcTimestamp, complete: Schema.Boolean,
  pages: count(20), acceptedComments: count(100000),
  deletedComments: count(100000), unsupportedComments: count(100000),
});
export const DraftSummary = Schema.Struct({
  backendSessionId: OpaqueId, draftId: OpaqueId, revision: SafeCounter,
  articleGeneration: SafeCounter, state: DraftState,
  snapshot: Schema.NullOr(SnapshotSummary), collectionId: Schema.NullOr(OpaqueId),
});
export const ResultReference = Schema.Struct({
  collectionId: OpaqueId, roundId: OpaqueId, revision: SafeCounter,
});
const boundedRequiredArray = <S extends Schema.Constraint>(schema: S, maximum: number) =>
  Schema.NullOr(Schema.Array(schema).check(Schema.isMaxLength(maximum))).check(
    Schema.makeFilter((value) => value !== null ? undefined : "array must not be null"),
  );

export const BootstrapData = Schema.Struct({
  backendNow: UtcTimestamp,
  theme: Schema.String.check(Schema.makeFilter((theme) =>
    ["system", "light", "dark"].includes(theme) ? undefined : "unknown theme",
  )),
  activeDraft: Schema.NullOr(DraftSummary),
  pendingOperations: boundedRequiredArray(OperationDescriptor, 64),
  pendingCursor: Schema.NullOr(OpaqueId),
  recentResults: boundedRequiredArray(ResultReference, 16),
});

export const responseEnvelope = <S extends Schema.Constraint>(data: S) => Schema.Struct({
  protocolVersion: Schema.Number.check(Schema.makeFilter((version) => version === 1 ? undefined : "protocol mismatch")),
  backendSessionId: OpaqueId, occurredAt: UtcTimestamp, ok: Schema.Boolean,
  operationId: Schema.optionalKey(Schema.NullOr(OpaqueId)),
  revision: Schema.optionalKey(Schema.NullOr(SafeCounter)),
  data: Schema.optionalKey(Schema.NullOr(data)),
  code: Schema.optionalKey(ErrorCode), messageKey: Schema.optionalKey(Schema.String),
}).check(Schema.makeFilter((response) => {
  if (response.operationId === null || response.revision === null) return "optional envelope fields must be omitted, not null";
  if (response.ok) {
    if (response.data === undefined || response.data === null || response.code !== undefined || response.messageKey !== undefined) return "invalid success discriminant";
  } else {
    if (response.data !== undefined || response.code === undefined || response.messageKey !== response.code) return "invalid failure discriminant";
  }
  return undefined;
}));

export const BootstrapResponse = responseEnvelope(BootstrapData);
export const PendingOperationsPage = Schema.Struct({
  operations: boundedRequiredArray(OperationDescriptor, 64),
  cursor: Schema.NullOr(OpaqueId),
});
export const PendingOperationsResponse = responseEnvelope(PendingOperationsPage);

const OperationObservationFields = Schema.Struct({
  operationId: OpaqueId, state: OperationState,
  kind: Schema.NullOr(OperationDescriptorFields.fields.kind), collectionId: Schema.NullOr(OpaqueId), roundId: Schema.NullOr(OpaqueId),
  revision: Schema.NullOr(SafeCounter), failureCode: Schema.NullOr(ErrorCode),
});
export const OperationObservation = Schema.Unknown.check(Schema.makeFilter((raw) => {
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) return undefined;
  return Object.keys(raw).every((key) => Object.hasOwn(OperationObservationFields.fields, key)) ? undefined : "unexpected operation observation field";
})).pipe(Schema.decodeTo(OperationObservationFields)).check(Schema.makeFilter((value) => {
  if (value.state === "unknown") return value.kind === null && value.collectionId === null && value.roundId === null && value.revision === null && value.failureCode === null ? undefined : "unknown operation has metadata";
  if (value.kind === null || value.collectionId === null || value.roundId === null || value.revision === null) return "known operation target required";
  return value.state === "failed" ? value.failureCode !== null ? undefined : "failure code required" : value.failureCode === null ? undefined : "unexpected failure code";
}));
export const OperationLookupResponse = responseEnvelope(OperationObservation);

export type BootstrapResponse = typeof BootstrapResponse.Type;
export type BootstrapData = typeof BootstrapData.Type;
export type PendingOperationsResponse = typeof PendingOperationsResponse.Type;
