import type { BootstrapResponse as GoResponse } from "../../bindings/github.com/porkyx/jackpot/internal/contracts/models";
import type { BootstrapResponse } from "../../src/contracts/schemas";
import type { PendingOperationsResponse as GoPendingResponse } from "../../bindings/github.com/porkyx/jackpot/internal/contracts/models";
import type { PendingOperationsResponse } from "../../src/contracts/schemas";
import type { StateNotice as GoStateNotice } from "../../bindings/github.com/porkyx/jackpot/internal/contracts/models";
import type { StateNoticeSchema } from "../../src/platform/backend";
import type { OperationLookupResponse as GoOperationLookupResponse } from "../../bindings/github.com/porkyx/jackpot/internal/contracts/models";
import type { OperationLookupResponse } from "../../src/contracts/schemas";

// Generated enums and mutable arrays describe wire JSON. Schema exposes readonly
// observations of the same shape and validates their semantic refinements.
type Wire<T> = T extends string ? `${T}` : T extends readonly (infer U)[] ? readonly Wire<U>[] : T extends object ? { readonly [K in keyof T]: Wire<T[K]> } : T;
type IsAny<T> = 0 extends (1 & T) ? true : false;
type Assert<T extends true> = T;
export type GeneratedResponseMustNotBeAny = Assert<IsAny<GoResponse> extends false ? true : false>;
export type SchemaToGenerated = Assert<BootstrapResponse extends Wire<GoResponse> ? true : false>;
export type GeneratedToSchema = Assert<Wire<GoResponse> extends BootstrapResponse ? true : false>;
export type PendingMustNotBeAny = Assert<IsAny<GoPendingResponse> extends false ? true : false>;
export type PendingSchemaToGenerated = Assert<PendingOperationsResponse extends Wire<GoPendingResponse> ? true : false>;
export type PendingGeneratedToSchema = Assert<Wire<GoPendingResponse> extends PendingOperationsResponse ? true : false>;
export type NoticeMustNotBeAny = Assert<IsAny<GoStateNotice> extends false ? true : false>;
export type NoticeSchemaToGenerated = Assert<typeof StateNoticeSchema.Type extends Wire<GoStateNotice> ? true : false>;
// The generator includes the Go enum zero value. Validate rejects that value;
// the runtime Schema must preserve this stricter production invariant.
type ValidGoStateNotice = Omit<Wire<GoStateNotice>, "entityKind"> & { readonly entityKind: Exclude<Wire<GoStateNotice>["entityKind"], ""> };
export type NoticeGeneratedToSchema = Assert<ValidGoStateNotice extends typeof StateNoticeSchema.Type ? true : false>;
export type LookupMustNotBeAny = Assert<IsAny<GoOperationLookupResponse> extends false ? true : false>;
export type LookupSchemaToGenerated = Assert<typeof OperationLookupResponse.Type extends Wire<GoOperationLookupResponse> ? true : false>;
export type LookupGeneratedToSchema = Assert<Wire<GoOperationLookupResponse> extends typeof OperationLookupResponse.Type ? true : false>;
