import { Effect, Schema } from "effect";
import { BackendRejected, ProtocolError, TransportError, type BackendError } from "../contracts/backend";
import {
  CollectionData, CommentsPage, DraftData, DraftOperation, ParticipantsPage, FrozenParticipantsPage, FrozenCommentsPage,
  type ProductBackend, type ProductReply, type MutationHeader, type DraftQuery, type LoadRequest, type DraftEditRequest,
  type ParticipantQuery, type CommentsQuery, type CreateCollectionRequest, type CollectionCommandRequest, type FrozenParticipantsQuery, type FrozenCommentsQuery, type CollectionReadOptions, type Collection,
} from "../contracts/product";
import { OpaqueId, SafeCounter, responseEnvelope } from "../contracts/schemas";
import { assertQuerySize } from "./querySize";

export interface WailsProductPort {
 readonly queryFrozenParticipants: (request: FrozenParticipantsQuery, signal: AbortSignal) => PromiseLike<unknown>;
 readonly queryFrozenComments: (request: FrozenCommentsQuery, signal: AbortSignal) => PromiseLike<unknown>;
 readonly getDraftOperation: (id: string, signal: AbortSignal) => PromiseLike<unknown>;
 readonly createDraft: (request: MutationHeader, signal: AbortSignal) => PromiseLike<unknown>;
 readonly getDraft: (request: DraftQuery, signal: AbortSignal) => PromiseLike<unknown>;
 readonly loadArticle: (request: LoadRequest, signal: AbortSignal) => PromiseLike<unknown>;
 readonly editDraft: (request: DraftEditRequest, signal: AbortSignal) => PromiseLike<unknown>;
 readonly queryParticipants: (request: ParticipantQuery, signal: AbortSignal) => PromiseLike<unknown>;
 readonly queryParticipantComments: (request: CommentsQuery, signal: AbortSignal) => PromiseLike<unknown>;
 readonly createCollection: (request: CreateCollectionRequest, signal: AbortSignal) => PromiseLike<unknown>;
 readonly getCollection: (id: string, options: CollectionReadOptions, signal: AbortSignal) => PromiseLike<unknown>;
 readonly rerun: (request: CollectionCommandRequest, signal: AbortSignal) => PromiseLike<unknown>;
 readonly setSchedule: (request: CollectionCommandRequest, signal: AbortSignal) => PromiseLike<unknown>;
 readonly cancelSchedule: (request: CollectionCommandRequest, signal: AbortSignal) => PromiseLike<unknown>;
 readonly retryRound: (request: CollectionCommandRequest, signal: AbortSignal) => PromiseLike<unknown>;
}
function header(request: MutationHeader): void {
 if (request.protocolVersion !== 1) throw new ProtocolError();
 Schema.decodeUnknownSync(OpaqueId)(request.backendSessionId); Schema.decodeUnknownSync(OpaqueId)(request.operationId);
 Schema.decodeUnknownSync(SafeCounter)(request.expectedRevision);
}
function draftHeader(request: LoadRequest | DraftEditRequest | CreateCollectionRequest): void {
 header(request); Schema.decodeUnknownSync(OpaqueId)(request.draftId); Schema.decodeUnknownSync(SafeCounter)(request.articleGeneration);
}
function invoke<S extends Schema.Constraint & { readonly DecodingServices: never }>(call: (signal: AbortSignal) => PromiseLike<unknown>, schema: S, validate: () => void = () => {}, mutation?: MutationHeader, query = mutation === undefined): Effect.Effect<ProductReply<S["Type"]>, BackendError> {
 return Effect.try({ try: validate, catch: () => new ProtocolError() }).pipe(Effect.andThen(Effect.tryPromise({ try: call, catch: () => new TransportError() })), Effect.flatMap((raw) => Effect.try({
  try: () => {
   if (query) assertQuerySize(raw);
   const decoded = Schema.decodeUnknownSync(responseEnvelope(schema))(raw);
   if (!decoded.ok) throw new BackendRejected({ code: decoded.code ?? "ProtocolError", messageKey: decoded.messageKey ?? "ProtocolError" });
   if (decoded.data === undefined || decoded.data === null) throw new ProtocolError();
   if (mutation !== undefined && (decoded.operationId !== mutation.operationId || decoded.backendSessionId !== mutation.backendSessionId)) throw new ProtocolError();
   return { protocolVersion: 1 as const, backendSessionId: decoded.backendSessionId, occurredAt: decoded.occurredAt, data: decoded.data,
    operationId: decoded.operationId ?? null, revision: decoded.revision ?? null };
  }, catch: (error): BackendError => error instanceof BackendRejected ? error : new ProtocolError(),
 })));
}
export function makeWailsProductBackend(port: WailsProductPort): ProductBackend {
 return {
  queryFrozenParticipants: (request) => invoke((signal) => port.queryFrozenParticipants(request, signal), FrozenParticipantsPage, () => { validateFrozenQuery(request, 100); validateSearch(request.query,request.group); }).pipe(Effect.flatMap((reply) => reply.data.collectionId === request.collectionId && validReadPage(reply.data,request.limit,request.offset,reply.data.matched) && (request.expectedRevision == null || reply.data.revision === request.expectedRevision) ? Effect.succeed(reply) : Effect.fail(new ProtocolError()))),
  queryFrozenComments: (request) => invoke((signal) => port.queryFrozenComments(request, signal), FrozenCommentsPage, () => { validateFrozenQuery(request, 50); Schema.decodeUnknownSync(OpaqueId)(request.participantId); }).pipe(Effect.flatMap((reply) => reply.data.collectionId === request.collectionId && reply.data.participantId === request.participantId && validReadPage(reply.data,request.limit,request.offset) && (request.expectedRevision == null || reply.data.revision === request.expectedRevision) ? Effect.succeed(reply) : Effect.fail(new ProtocolError()))),
  getDraftOperation: (id) => invoke((signal) => port.getDraftOperation(id, signal), DraftOperation, () => { Schema.decodeUnknownSync(OpaqueId)(id); }).pipe(Effect.flatMap((reply) => reply.data.operationId === id ? Effect.succeed(reply) : Effect.fail(new ProtocolError()))),
  createDraft: (request) => invoke((signal) => port.createDraft(request, signal), DraftData, () => header(request), request),
  getDraft: (request) => invoke((signal) => port.getDraft(request, signal), DraftData, () => { Schema.decodeUnknownSync(OpaqueId)(request.backendSessionId); Schema.decodeUnknownSync(OpaqueId)(request.draftId); }).pipe(Effect.flatMap((reply) => reply.backendSessionId === request.backendSessionId && reply.data.summary.draftId === request.draftId ? Effect.succeed(reply) : Effect.fail(new ProtocolError()))),
  loadArticle: (request) => invoke((signal) => port.loadArticle(request, signal), DraftData, () => draftHeader(request), request),
  editDraft: (request) => invoke((signal) => port.editDraft(request, signal), DraftData, () => draftHeader(request), request),
  queryParticipants: (request) => invoke((signal) => port.queryParticipants(request, signal), ParticipantsPage, () => { validateDraftRead(request,100); validateSearch(request.query,request.group); }).pipe(Effect.flatMap((reply) => sameDraftContext(reply.backendSessionId,reply.data.context,request) && validReadPage(reply.data,request.limit,request.offset,reply.data.matched) ? Effect.succeed(reply) : Effect.fail(new ProtocolError()))),
  queryParticipantComments: (request) => invoke((signal) => port.queryParticipantComments(request, signal), CommentsPage, () => { validateDraftRead(request,50); Schema.decodeUnknownSync(OpaqueId)(request.participantId); }).pipe(Effect.flatMap((reply) => sameDraftContext(reply.backendSessionId,reply.data.context,request) && reply.data.participantId === request.participantId && validReadPage(reply.data,request.limit,request.offset) ? Effect.succeed(reply) : Effect.fail(new ProtocolError()))),
  createCollection: (request) => invoke((signal) => port.createCollection(request, signal), CollectionData, () => { draftHeader(request); Schema.decodeUnknownSync(SafeCounter)(request.afterSequence); }, request),
  getCollection: (id, options = {}) => invoke((signal) => port.getCollection(id, options, signal), CollectionData, () => { Schema.decodeUnknownSync(OpaqueId)(id); validateRoundQuery(options); }).pipe(Effect.flatMap((reply) => validRoundReply(reply.data, id, options) ? Effect.succeed(reply) : Effect.fail(new ProtocolError()))),
  rerun: (request) => invoke((signal) => port.rerun(request, signal), CollectionData, () => header(request), request),
  setSchedule: (request) => invoke((signal) => port.setSchedule(request, signal), CollectionData, () => header(request), request),
  cancelSchedule: (request) => invoke((signal) => port.cancelSchedule(request, signal), CollectionData, () => header(request), request),
  retryRound: (request) => invoke((signal) => port.retryRound(request, signal), CollectionData, () => header(request), request),
 };
}
function validateFrozenQuery(request: FrozenParticipantsQuery | FrozenCommentsQuery, maximum: number): void {
 Schema.decodeUnknownSync(OpaqueId)(request.collectionId); Schema.decodeUnknownSync(SafeCounter)(request.offset); if (request.offset > 0xffffffff) throw new ProtocolError();
 if (!Number.isInteger(request.limit) || request.limit < 1 || request.limit > maximum) throw new ProtocolError();
 if (request.expectedRevision != null) Schema.decodeUnknownSync(SafeCounter)(request.expectedRevision);
}
function validateRoundQuery(options: CollectionReadOptions): void {
 const offset = options.roundOffset ?? 0; const limit = options.roundLimit ?? 0;
 Schema.decodeUnknownSync(SafeCounter)(offset);
 if (offset > 0xffffffff || !Number.isInteger(limit) || limit < 0 || limit > 50) throw new ProtocolError();
 if (options.roundId != null && options.roundId !== "") {
  Schema.decodeUnknownSync(OpaqueId)(options.roundId);
  if (offset !== 0) throw new ProtocolError();
 }
}
function validRoundReply(value: Collection, id: string, options: CollectionReadOptions): boolean {
 const limit = options.roundLimit || 50; const anchor = options.roundId;
 if (value.collectionId !== id || value.rounds.length !== Math.min(limit, Math.max(0, value.roundTotal - value.roundOffset))) return false;
 if (anchor != null && anchor !== "") return value.roundOffset % limit === 0 && value.rounds.some(round => round.roundId === anchor);
 return value.roundOffset === (options.roundOffset ?? 0);
}
function validateDraftRead(request: ParticipantQuery | CommentsQuery, maximum: number): void {
 Schema.decodeUnknownSync(OpaqueId)(request.backendSessionId); Schema.decodeUnknownSync(OpaqueId)(request.draftId);
 Schema.decodeUnknownSync(SafeCounter)(request.revision); Schema.decodeUnknownSync(SafeCounter)(request.articleGeneration);
 Schema.decodeUnknownSync(SafeCounter)(request.offset); if (request.offset > 0xffffffff) throw new ProtocolError();
 if (!Number.isInteger(request.limit) || request.limit < 1 || request.limit > maximum) throw new ProtocolError();
}
function validateSearch(query: string, group: string): void {
 if (new TextEncoder().encode(query).byteLength > 1000 || !["", "unclassified", "included", "excluded"].includes(group)) throw new ProtocolError();
}
function sameDraftContext(session: string, actual: Pick<ParticipantQuery,"backendSessionId"|"draftId"|"revision"|"articleGeneration">, expected: ParticipantQuery | CommentsQuery): boolean {
 return session === expected.backendSessionId && actual.backendSessionId === expected.backendSessionId && actual.draftId === expected.draftId && actual.revision === expected.revision && actual.articleGeneration === expected.articleGeneration;
}
function validReadPage(page: {readonly total:number; readonly offset:number;readonly rows:ReadonlyArray<{readonly id:string}>}, limit:number, offset:number, matched=page.total): boolean {
 return page.offset === offset && matched <= page.total && page.rows.length <= limit && page.rows.length <= Math.max(0,matched-offset) && new Set(page.rows.map(row=>row.id)).size === page.rows.length;
}
