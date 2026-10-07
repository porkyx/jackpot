import type { Route } from "./shellState";

function safeCollectionId(id: string): boolean { return id.length > 0 && !/[\u0000-\u001f\u007f]/.test(id); }

// Opaque collection identities are one URI component, never external URLs.
// An optional round identity selects a read-only anchor, never a command.
export function parseHashRoute(hash: string): Route {
  if (hash === "" || hash === "#/create") return Object.freeze({ _tag: "create" });
  const match = /^#\/results\/([^/?#]+)(?:\/rounds\/([^/?#]+))?$/.exec(hash);
  if (match !== null) {
    try {
      const collectionId = decodeURIComponent(match[1]!);
      const roundId = match[2] === undefined ? null : decodeURIComponent(match[2]);
      if (safeCollectionId(collectionId) && (roundId === null || safeCollectionId(roundId))) {
        return Object.freeze({ _tag: "result", collectionId, roundId });
      }
    } catch { /* Malformed percent encoding is an explanatory unknown route. */ }
  }
  return Object.freeze({ _tag: "not_found", hash });
}

export function hashForRoute(route: Exclude<Route, { readonly _tag: "not_found" }>): string {
  switch (route._tag) {
    case "create": return "#/create";
    case "result": {
      if (!safeCollectionId(route.collectionId)) throw new Error("Collection route identity is invalid");
      if (route.roundId !== null && !safeCollectionId(route.roundId)) throw new Error("Round route identity is invalid");
      return `#/results/${encodeURIComponent(route.collectionId)}` + (route.roundId === null ? "" : `/rounds/${encodeURIComponent(route.roundId)}`);
    }
    default: throw new Error(`Unknown route: ${String(route)}`);
  }
}
