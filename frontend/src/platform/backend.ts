import { Effect, Schema } from "effect";
import {
  BackendRejected, ProtocolError, TransportError, type Backend, type BackendError,
  type BootstrapReply, type PendingOperationsReply, type PendingOperationsRequest, type OperationLookupReply,
  type StateNotice,
} from "../contracts/backend";
import type { ProductBackend } from "../contracts/product";
import { BootstrapResponse, OpaqueId, OperationLookupResponse, PendingOperationsResponse, SafeCounter } from "../contracts/schemas";

export const StateChangedTopic = "jackpot:state-changed";
const StateNoticeFields = Schema.Struct({
  backendSessionId: OpaqueId,
  entityKind: Schema.Literals(["draft", "collection"]),
  entityId: OpaqueId,
  revision: SafeCounter,
  operationId: Schema.NullOr(OpaqueId),
});
const noticeKeys = new Set(["backendSessionId", "entityKind", "entityId", "revision", "operationId"]);
export const StateNoticeSchema = Schema.Unknown.check(Schema.makeFilter((value) => {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return "notice object required";
  return Object.keys(value).every((key) => noticeKeys.has(key)) ? undefined : "unknown notice field";
})).pipe(Schema.decodeTo(StateNoticeFields));
const PendingRequest = Schema.Struct({
  cursor: Schema.NullOr(OpaqueId),
  limit: Schema.Number.check(Schema.makeFilter((limit) =>
    Number.isInteger(limit) && limit >= 1 && limit <= 64 ? undefined : "pending limit must be 1..64")),
});

export interface WailsReadPort {
  readonly bootstrap: (signal: AbortSignal) => PromiseLike<unknown>;
  readonly listPendingOperations: (request: PendingOperationsRequest, signal: AbortSignal) => PromiseLike<unknown>;
  readonly getOperation: (operationId: string, signal: AbortSignal) => PromiseLike<unknown>;
  readonly onStateChanged: (listener: (data: unknown) => void) => () => void;
}
interface Envelope<A> {
  readonly backendSessionId: string;
  readonly occurredAt: string;
  readonly ok: boolean;
  readonly data?: A | null;
  readonly code?: string;
  readonly messageKey?: string;
}
function decodeReply<A>(raw: unknown, decode: (input: unknown) => Envelope<A>) {
  return Effect.try({
    try: () => {
      const response = decode(raw);
      if (!response.ok) {
        if (response.code === undefined || response.messageKey === undefined) throw new ProtocolError();
        throw new BackendRejected({ code: response.code, messageKey: response.messageKey });
      }
      if (response.data === undefined || response.data === null) throw new ProtocolError();
      return { protocolVersion: 1 as const, backendSessionId: response.backendSessionId, occurredAt: response.occurredAt, data: response.data };
    },
    catch: (error): BackendError => error instanceof BackendRejected ? error : new ProtocolError(),
  });
}
export function makeWailsBackend(port: WailsReadPort, product: ProductBackend | null = null): typeof Backend.Service {
  return {
    product,
    bootstrap: (): Effect.Effect<BootstrapReply, BackendError> => Effect.tryPromise({
      try: (signal) => port.bootstrap(signal), catch: () => new TransportError(),
    }).pipe(Effect.flatMap((raw) => decodeReply(raw, Schema.decodeUnknownSync(BootstrapResponse)))),
    listPendingOperations: (request): Effect.Effect<PendingOperationsReply, BackendError> => Effect.try({
      try: () => Schema.decodeUnknownSync(PendingRequest)(request), catch: () => new ProtocolError(),
    }).pipe(Effect.flatMap((validated) => Effect.tryPromise({
      try: (signal) => port.listPendingOperations({ cursor: validated.cursor, limit: validated.limit }, signal),
      catch: () => new TransportError(),
    })), Effect.flatMap((raw) => decodeReply(raw, Schema.decodeUnknownSync(PendingOperationsResponse)))),
    getOperation: (operationId): Effect.Effect<OperationLookupReply, BackendError> => Effect.try({
      try: () => Schema.decodeUnknownSync(OpaqueId)(operationId), catch: () => new ProtocolError(),
    }).pipe(Effect.flatMap((validated) => Effect.tryPromise({
      try: (signal) => port.getOperation(validated, signal), catch: () => new TransportError(),
    })), Effect.flatMap((raw) => decodeReply(raw, Schema.decodeUnknownSync(OperationLookupResponse))),
    Effect.flatMap((reply) => reply.data.operationId === operationId ? Effect.succeed(reply) : Effect.fail(new ProtocolError()))),
    subscribeStateChanges: (listener, onError) => Effect.asVoid(Effect.acquireRelease(
      Effect.try({
        try: () => {
          let active = true;
          const unsubscribe = port.onStateChanged((raw) => {
            if (!active) return;
            let notice: StateNotice;
            try { notice = Schema.decodeUnknownSync(StateNoticeSchema)(raw); }
            catch { onError(new ProtocolError()); return; }
            listener(notice);
          });
          return () => { active = false; unsubscribe(); };
        },
        catch: () => new TransportError(),
      }),
      (unsubscribe) => Effect.sync(unsubscribe),
    )),
  };
}
