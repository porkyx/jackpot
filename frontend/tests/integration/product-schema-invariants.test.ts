import { expect, test } from "vitest";
import { Schema } from "effect";
import { CollectionData, DraftData, DraftOperation } from "../../src/contracts/product";
import { draft } from "../helpers/productCommands";
import { collection } from "../helpers/productBackend";

test("draft participant accounting rejects either independent count mismatch without altering input", () => {
  const valid = draft();
  expect(Schema.decodeUnknownSync(DraftData)(valid)).toEqual(valid);
  for (const change of [{ included: 2 }, { excluded: 1 }, { participants: 4 }]) {
    const input = { ...valid, ...change };
    const before = JSON.stringify(input);
    expect(() => Schema.decodeUnknownSync(DraftData)(input)).toThrow("participant count invariant");
    expect(JSON.stringify(input)).toBe(before);
  }
});

const summary = draft().summary;
for (const state of ["unknown", "pending", "succeeded", "failed"] as const) {
  for (const hasSummary of [false, true]) {
    for (const hasFailure of [false, true]) {
      test(`draft operation ${state} summary=${hasSummary} failure=${hasFailure} obeys metadata contract`, () => {
        const input = { operationId: "operation", state, summary: hasSummary ? summary : null, failureCode: hasFailure ? "InvalidInput" : null };
        const before = JSON.stringify(input);
        const allowed = state === "unknown" ? !hasSummary && !hasFailure : hasSummary && (state === "failed" ? hasFailure : !hasFailure);
        if (allowed) expect(Schema.decodeUnknownSync(DraftOperation)(input)).toEqual(input);
        else expect(() => Schema.decodeUnknownSync(DraftOperation)(input)).toThrow();
        expect(JSON.stringify(input)).toBe(before);
      });
    }
  }
}

test("collection page cannot extend beyond the round total or an offset past the end", () => {
  const valid = collection();
  expect(Schema.decodeUnknownSync(CollectionData)(valid)).toEqual(valid);
  for (const roundOffset of [1, 2]) {
    const input = { ...valid, roundOffset };
    const before = JSON.stringify(input);
    expect(() => Schema.decodeUnknownSync(CollectionData)(input)).toThrow("round page bounds");
    expect(JSON.stringify(input)).toBe(before);
  }
  const empty = { ...valid, roundOffset: 2, rounds: [] };
  expect(Schema.decodeUnknownSync(CollectionData)(empty)).toEqual(empty);
});
