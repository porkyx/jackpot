import test from "node:test";
import assert from "node:assert/strict";
const { measureKeywordFilter, keywordFilterExpected, FILTER_PROBE_WORD } = await import(process.env.JACKPOT_FILTER_METRICS_MODULE ?? "./stress.filter.mjs");
function fixture() {
  let applied = false, time = 100, applies = 0, restores = 0, confirms = 0;
  const order = [];
  const port = {
    now: async () => { order.push("clock"); return time; },
    apply: async () => { assert.equal(applied, false); applies++; order.push("apply"); applied = true; time += 10; },
    restore: async () => { assert.equal(applied, true); restores++; order.push("restore"); applied = false; time += 1000; },
    confirm: async expected => { confirms++; order.push("confirm:" + expected); assert.equal(applied, expected); time += expected ? 15 : 1000; },
  };
  return { port, order, snapshot: () => ({ applied, applies, restores, confirms }) };
}
for (const participants of [41, 100]) test("literal keyword changes one participant and unclassified rows for " + participants, () => {
  assert.equal(FILTER_PROBE_WORD, "댓글 00060");
  assert.deepEqual(keywordFilterExpected(participants, false), { participants, included: participants, excluded: 0, rows: participants, matchedPersonRows: 1, matchedPersonIncluded: true });
  assert.deepEqual(keywordFilterExpected(participants, true), { participants, included: participants - 1, excluded: 1, rows: participants - 1, matchedPersonRows: 0, matchedPersonIncluded: null });
});
test("keyword fixture oracle rejects nil/zero/other counts and untyped states without production limits", () => {
  for (const count of [null, undefined, 0, 1, 40, 42, 99, 101, 200, 10000, NaN]) assert.throws(() => keywordFilterExpected(count, false));
  for (const state of [null, undefined, 0, 1, "true"]) assert.throws(() => keywordFilterExpected(41, state));
});
test("twenty keyword samples include input and confirmation but exclude deletion and restoration", async () => {
  const h = fixture(); const samples = await measureKeywordFilter(h.port);
  assert.deepEqual(samples, Array(20).fill(25)); assert.deepEqual(h.snapshot(), { applied: false, applies: 20, restores: 20, confirms: 41 });
  assert.deepEqual(h.order.slice(0, 7), ["confirm:false", "clock", "apply", "confirm:true", "clock", "restore", "confirm:false"]);
});
test("keyword sampling rejects an already applied baseline before adding a duplicate", async () => {
  const h = fixture(); await h.port.apply(); await assert.rejects(measureKeywordFilter(h.port)); assert.equal(h.snapshot().applies, 1); assert.equal(h.snapshot().restores, 0);
});
for (const action of ["apply", "restore"]) for (const at of [1, 7, "continuous"]) test("keyword " + action + " failure " + at + " propagates without retry or success", async () => {
  const h = fixture(); const original = h.port[action]; let calls = 0;
  h.port[action] = async () => { if (++calls === at || at === "continuous") throw Error("controlled action failure"); await original(); };
  await assert.rejects(measureKeywordFilter(h.port), /controlled action failure/); assert.equal(calls, at === "continuous" ? 1 : at); assert.equal(h.snapshot()[action === "apply" ? "applies" : "restores"], calls - 1);
});
for (const at of [1, 7, "continuous"]) test("keyword applied confirmation failure " + at + " cannot be counted as a sample", async () => {
  const h = fixture(); const confirm = h.port.confirm; let calls = 0;
  h.port.confirm = async applied => { if (applied && (++calls === at || at === "continuous")) throw Error("controlled apply confirmation failure"); await confirm(applied); };
  await assert.rejects(measureKeywordFilter(h.port), /controlled apply confirmation failure/); const expected = at === "continuous" ? 1 : at; assert.equal(calls, expected); assert.deepEqual(h.snapshot(), { applied: true, applies: expected, restores: expected - 1, confirms: 2 * expected - 1 });
});
for (const at of [1, 7, "continuous"]) test("keyword restoration confirmation failure " + at + " stops before another sample", async () => {
  const h = fixture(); const confirm = h.port.confirm; let calls = 0;
  h.port.confirm = async applied => { if (!applied && h.snapshot().restores > 0 && (++calls === at || at === "continuous")) throw Error("controlled restore confirmation failure"); await confirm(applied); };
  await assert.rejects(measureKeywordFilter(h.port), /controlled restore confirmation failure/); const expected = at === "continuous" ? 1 : at; assert.equal(calls, expected); assert.equal(h.snapshot().applies, expected); assert.equal(h.snapshot().restores, expected); assert.equal(h.snapshot().applied, false);
});
for (const clock of [-1, NaN, Infinity, undefined, "10"]) test("invalid start clock " + String(clock) + " rejects before mutation", async () => {
  const h = fixture(); h.port.now = async () => clock; await assert.rejects(measureKeywordFilter(h.port), /Valid filter start clock/); assert.equal(h.snapshot().applies, 0);
});
for (const clock of [99, NaN, Infinity, undefined, "100"]) test("invalid end clock " + String(clock) + " cannot produce a statistic", async () => {
  const h = fixture(); let calls = 0; h.port.now = async () => ++calls === 1 ? 100 : clock;
  await assert.rejects(measureKeywordFilter(h.port), /Monotonic filter end clock/); assert.equal(h.snapshot().applies, 1); assert.equal(h.snapshot().restores, 0);
});
for (const elapsed of [0, 200, 200.001, 1000]) test("finite filter elapsed boundary " + elapsed + " retains exact threshold classification", async () => {
  const h = fixture(); let calls = 0; h.port.now = async () => calls++ % 2 === 0 ? 100 : 100 + elapsed;
  const samples = await measureKeywordFilter(h.port); assert.equal(samples.length, 20); assert.ok(samples.every(sample => Math.abs(sample - elapsed) < 1e-10)); assert.equal(samples[0] <= 200, elapsed <= 200);
});
// Removing confirmation never enters a held callback; mutations use immediate oracles.
if (process.env.JACKPOT_FILTER_METRICS_MODULE === undefined) for (const name of ["TimeoutError", "AbortError"]) test("controlled " + name + " propagates unchanged with no retry", async () => {
  const h = fixture(); const original = h.port.confirm; let entered, reject;
  const started = new Promise(resolve => { entered = resolve; }); const held = new Promise((_resolve, fail) => { reject = fail; });
  const failure = Object.assign(Error("controlled dependency ending"), { name });
  h.port.confirm = async applied => { if (applied) { entered(); return held; } return original(applied); };
  const measured = measureKeywordFilter(h.port); await started; assert.equal(h.snapshot().applies, 1); reject(failure);
  await assert.rejects(measured, error => error === failure); assert.deepEqual(h.snapshot(), { applied: true, applies: 1, restores: 0, confirms: 1 });
});
