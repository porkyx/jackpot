import type { Round } from "../../src/contracts/product";
export function round(change: Partial<Round> = {}): Round {
 return { collectionId: "collection", roundId: "round", number: 1, attempt: 1, state: "completed", revision: 1, roundVersion: 1,
  mode: "immediate", message: "", prizes: [], scheduledAt: null, executedAt: "2026-10-06T00:00:00Z", failureCode: null,
  winners: [], algorithmVersion: "v1", appVersion: "0.1.0", ...change };
}
