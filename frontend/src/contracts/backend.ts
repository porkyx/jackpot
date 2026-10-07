import { Context, Data, type Effect, type Scope } from "effect";
import type { ProductBackend } from "./product";
import type { BootstrapData, PendingOperationsPage, OperationObservation } from "./schemas";

export class TransportError extends Data.TaggedError("TransportError")<{}> {}
export class ProtocolError extends Data.TaggedError("ProtocolError")<{}> {}
export class BackendRejected extends Data.TaggedError("BackendRejected")<{
  readonly code: string;
  readonly messageKey: string;
}> {}
export type BackendError = TransportError | ProtocolError | BackendRejected;

export interface ReplyHeader {
  readonly protocolVersion: 1;
  readonly backendSessionId: string;
  readonly occurredAt: string;
}
export interface BootstrapReply extends ReplyHeader {
  readonly data: BootstrapData;
}
export interface PendingOperationsReply extends ReplyHeader {
  readonly data: typeof PendingOperationsPage.Type;
}
export interface OperationLookupReply extends ReplyHeader { readonly data: typeof OperationObservation.Type }
export interface PendingOperationsRequest {
  readonly cursor: string | null;
  readonly limit: number;
}
export interface StateNotice {
  readonly backendSessionId: string;
  readonly entityKind: "draft" | "collection";
  readonly entityId: string;
  readonly revision: number;
  readonly operationId: string | null;
}

// Grow this contract alongside the real Go methods. Unimplemented mutations
// have no successful placeholder and cannot be dispatched through this service.
export class Backend extends Context.Service<Backend, {
  readonly product: ProductBackend | null;
  readonly bootstrap: () => Effect.Effect<BootstrapReply, BackendError>;
  readonly listPendingOperations: (request: PendingOperationsRequest) => Effect.Effect<PendingOperationsReply, BackendError>;
  readonly getOperation: (operationId: string) => Effect.Effect<OperationLookupReply, BackendError>;
  readonly subscribeStateChanges: (listener: (notice: StateNotice) => void, onError: (error: BackendError) => void) => Effect.Effect<void, BackendError, Scope.Scope>;
}>()("jackpot/Backend") {}
