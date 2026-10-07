import { Context, Data, Effect, Layer } from "effect";

export class ClientIdError extends Data.TaggedError("ClientIdError")<{}> {}

export class ClientIds extends Context.Service<ClientIds, {
  readonly operationId: Effect.Effect<string, ClientIdError>;
  readonly intentId: Effect.Effect<string, ClientIdError>;
}>()("jackpot/ClientIds") {}

export function clientIdsFrom(source: () => string): typeof ClientIds.Service {
  const next = Effect.try({
    try: () => {
      const value = source();
      if (typeof value !== "string" || value.length === 0) throw new ClientIdError();
      return value;
    },
    catch: () => new ClientIdError(),
  });
  return { operationId: next, intentId: next };
}

// UUIDs identify frontend requests. They are never draw entropy or seeds.
export const ClientIdsLive = Layer.sync(ClientIds, () => clientIdsFrom(() => crypto.randomUUID()));

// Each Layer acquisition owns its cursor; caller changes to the source list do
// not change an already-created test sequence. Exhaustion is an explicit error.
export function clientIdsSequence(values: ReadonlyArray<string>): Layer.Layer<ClientIds> {
  const copy = [...values];
  return Layer.sync(ClientIds, () => {
    let cursor = 0;
    return clientIdsFrom(() => {
      const value = copy[cursor++];
      if (value === undefined) throw new ClientIdError();
      return value;
    });
  });
}
