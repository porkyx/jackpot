import { Effect } from "effect";
import { ReadTimeout } from "../app/errors";
import type { Backend, BackendError, BootstrapReply, PendingOperationsReply, PendingOperationsRequest } from "../contracts/backend";

export type ReadError = BackendError | ReadTimeout;
export interface ReadQueries {
  readonly bootstrap: () => Effect.Effect<BootstrapReply, ReadError>;
  readonly listPendingOperations: (request: PendingOperationsRequest) => Effect.Effect<PendingOperationsReply, ReadError>;
}
type QueryBackend = Pick<typeof Backend.Service, "bootstrap" | "listPendingOperations">;

// Coordinator owns this policy once. Raw Backend/adapter has no retry. Only
// these two real idempotent queries are exposed; mutations, subscriptions and
// GetOperation's separate recovery polling cannot enter through this seam.
export function makeReadQueries(backend: QueryBackend): ReadQueries {
  return {
    bootstrap: () => read(() => backend.bootstrap()),
    listPendingOperations: (request) => {
      const admitted = Object.freeze({ cursor: request.cursor, limit: request.limit });
      return read(() => backend.listPendingOperations(admitted));
    },
  };
}

function read<A>(dispatch: () => Effect.Effect<A, BackendError>): Effect.Effect<A, ReadError> {
  return Effect.gen(function*() {
    let attempt = 0;
    while (true) {
      const result = yield* Effect.result(Effect.suspend(dispatch).pipe(Effect.timeoutOrElse({ duration: 15000, orElse: () => Effect.fail(new ReadTimeout()) })));
      if (result._tag === "Success") return result.success;
      const error = result.failure;
      if ((error._tag !== "TransportError" && error._tag !== "ReadTimeout") || attempt === 2) return yield* Effect.fail(error);
      yield* Effect.sleep(attempt === 0 ? 250 : 1000);
      attempt++;
    }
  });
}
