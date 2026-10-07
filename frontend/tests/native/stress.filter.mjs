import assert from "node:assert/strict";
export const FILTER_PROBE_WORD = "댓글 00060";
// Fixture index60 belongs only to person1. Its exclusion also removes it from
// the default unclassified query page; the article author is not a participant.
export function keywordFilterExpected(participants, applied) {
  assert.ok(participants === 41 || participants === 100, "Local fixture participant count required");
  assert.equal(typeof applied, "boolean", "Keyword applied state required");
  const excluded = applied ? 1 : 0;
  return { participants, included: participants - excluded, excluded, rows: participants - excluded, matchedPersonRows: applied ? 0 : 1, matchedPersonIncluded: applied ? null : true };
}
// Every sample starts and ends with the original empty exclude-keyword list.
// Native callbacks retain real input/click/IPC/DOM checks and failure behavior.
export async function measureKeywordFilter({ now, apply, restore, confirm }) {
  const samples = [];
  await confirm(false);
  for (let index = 0; index < 20; index++) {
    const started = await now();
    assert.ok(Number.isFinite(started) && started >= 0, "Valid filter start clock required");
    await apply();
    await confirm(true);
    const ended = await now();
    assert.ok(Number.isFinite(ended) && ended >= started, "Monotonic filter end clock required");
    samples.push(ended - started);
    await restore();
    await confirm(false);
  }
  return samples;
}
