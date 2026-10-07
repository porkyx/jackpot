import { Clock, Context, Effect, Layer } from "effect";
import { ProtocolError } from "../contracts/backend";
import { makeProductCoordinator, type ProductCoordinator } from "./productCoordinator";
import { Backend, type BackendError, type BootstrapReply, type PendingOperationsReply, type PendingOperationsRequest, type OperationLookupReply } from "../contracts/backend";
import { ClientIds, type ClientIdError } from "../platform/ids";
import { makeReadQueries, type ReadError } from "./readRetry";

export interface Received<A> {
  readonly reply: A;
  readonly receivedAtMillis: number;
}

// T01 establishes the single owner and its concrete dependencies. Command
// lanes, confirmed state, and reconciliation are added by their owning tickets.
// A successful transport read is not an admitted or confirmed mutation.
export class OperationCoordinator extends Context.Service<OperationCoordinator, {
  readonly product: ProductCoordinator | null;
  readonly bootstrap: () => Effect.Effect<Received<BootstrapReply>, ReadError>;
  readonly listPendingOperations: (request: PendingOperationsRequest) => Effect.Effect<Received<PendingOperationsReply>, ReadError>;
  readonly getOperation: (operationId: string) => Effect.Effect<Received<OperationLookupReply>, BackendError>;
  readonly newOperationId: Effect.Effect<string, ClientIdError>;
}>()("jackpot/OperationCoordinator") {}

export const OperationCoordinatorLive = Layer.effect(OperationCoordinator, Effect.gen(function*() {
  const backend = yield* Backend;
  const queries = makeReadQueries(backend);
  const ids = yield* ClientIds;
  const clock = yield* Clock.Clock;
  const receive = <A, E>(read: Effect.Effect<A, E>): Effect.Effect<Received<A>, E> => Effect.gen(function*() {
    const reply = yield* read;
    const receivedAtMillis = yield* clock.currentTimeMillis;
    return { reply, receivedAtMillis };
  });
  const product = backend.product === null ? null : yield* makeProductCoordinator({ backend: backend.product, newId: ids.operationId, lookup: backend.getOperation });
  return {
    product,
    bootstrap: () => receive(queries.bootstrap()).pipe(Effect.tap((received) => product === null ? Effect.void : product.acceptBootstrap(received.reply).pipe(Effect.mapError(() => new ProtocolError())))),
    listPendingOperations: (request) => receive(queries.listPendingOperations(request)),
    getOperation: (operationId) => receive(backend.getOperation(operationId)),
    newOperationId: ids.operationId,
  } satisfies typeof OperationCoordinator.Service;
}));
