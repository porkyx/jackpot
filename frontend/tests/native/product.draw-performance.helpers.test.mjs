import { test } from "node:test";
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { psQuote } from "./webview-recovery.guards.mjs";
import { drawWorkload, ownedEvidencePath, bindingMethod, boundedPSFailure, ownedProcessWaitRecords, initialDrawState, verifyDrawStored, verifyParticipantSnapshots, summarizeDrawSamples, watchCreateDraw } from "./product.draw-performance.helpers.mjs";

test("workloads require20+ new collections and map actual100/200 comments to41/100 participants", () => {
  assert.deepEqual(drawWorkload(100, 20), { comments: 100, participants: 41, winners: 10, samples: 20 });
  assert.equal(drawWorkload(200, 50).participants, 100);
  assert.ok(Object.isFrozen(drawWorkload(200, 20)));
  for (const comments of [0, 99, 101, 201, null, "100"]) assert.throws(() => drawWorkload(comments, 20));
  for (const samples of [0, 1, 19, 51, Infinity, NaN, 20.5, "20"]) assert.throws(() => drawWorkload(100, samples));
});
test("evidence paths reject traversal aliases outside workspace and nonunique names", () => {
  const root = "C:\\test folder\\jackpot", good = root + "\\.task\\draw-performance-" + "a".repeat(32);
  assert.equal(ownedEvidencePath(good, root), good);
  for (const path of [".task/draw-performance-a", root + "\\draw-performance-" + "a".repeat(32), good + "\\child", root + "\\.task\\draw-performance-a", root + "\\.task\\x\\..\\draw-performance-" + "a".repeat(32)]) assert.throws(() => ownedEvidencePath(path, root));
});
test("generated binding dispatch IDs cannot be absent zero out-of-range or another function", () => {
  assert.equal(bindingMethod("export function QueryParticipants(x) { return $Call.ByID(1442270765,x) }", "QueryParticipants"), 1442270765);
  for (const source of ["", "export function Other(){return $Call.ByID(1)}", "export function QueryParticipants(){return $Call.ByID(0)}", "export function QueryParticipants(){return $Call.ByID(4294967296)}", "export function QueryParticipants(){}\nexport function Other(){return $Call.ByID(1)}"]) assert.throws(() => bindingMethod(source, "QueryParticipants"));
  assert.throws(() => bindingMethod("", "x.*"));
});
test("PowerShell failures preserve stdout-only errors exit/message and timeout identity with bounded fields", () => {
  const parse = value => JSON.parse(value.slice("Owned draw command failed: ".length));
  assert.deepEqual(parse(boundedPSFailure({ message: "command failed", code: 1 }, "exclusive profile failure", "")), { message: "command failed", code: 1, signal: null, killed: false, stdout: "exclusive profile failure", stderr: "" });
  const timeout = parse(boundedPSFailure({ message: "m".repeat(2000), code: "ETIMEDOUT", signal: "SIGTERM", killed: true }, "o".repeat(4000), "e".repeat(4000)));
  assert.equal(timeout.message.length, 256); assert.equal(timeout.stdout.length, 480); assert.equal(timeout.stderr.length, 480); assert.equal(timeout.code, "ETIMEDOUT"); assert.equal(timeout.signal, "SIGTERM"); assert.equal(timeout.killed, true);
  assert.deepEqual(parse(boundedPSFailure(undefined, null, undefined)), { message: "unknown", code: null, signal: null, killed: false, stdout: "", stderr: "" });
});
test("wait-only projection preserves PID and exact start without full command profile or creation payload", () => {
  const records = Array.from({ length: 6 }, (_, index) => ({ id: index + 1, started: "2026-10-06T16:05:27.4298104Z", command: "long owned profile argument ".repeat(500), created: "2026-10-06T16:05:27.4298104Z", role: "renderer", name: "msedgewebview2.exe" }));
  const before = JSON.stringify(records), projected = ownedProcessWaitRecords(records);
  assert.equal(JSON.stringify(records), before);
  assert.deepEqual(projected, records.map(({ id, started }) => ({ id, started })));
  assert.ok(JSON.stringify(projected).length < 500);
  assert.ok(before.length > 32767);
  projected[0].id = 99; assert.equal(records[0].id, 1);
});
test("wait projection accepts empty and64 bounded identities including same PID different start", () => {
  assert.deepEqual(ownedProcessWaitRecords([]), []);
  const records = Array.from({ length: 64 }, (_, index) => ({ id: 2147483647, started: new Date(index * 1000).toISOString() }));
  assert.equal(ownedProcessWaitRecords(records).length, 64);
});
test("wait projection rejects invalid duplicate overflow and unbounded process inventory", () => {
  const valid = { id: 1, started: "2026-10-07T00:00:00Z" };
  for (const input of [null, {}, Array.from({ length: 65 }, (_, index) => ({ ...valid, id: index + 1 })), [valid, valid], [null], [{ ...valid, id: 0 }], [{ ...valid, id: -1 }], [{ ...valid, id: 2147483648 }], [{ ...valid, id: 1.5 }], [{ ...valid, id: "1" }], [{ ...valid, started: null }], [{ ...valid, started: "not a timestamp" }], [{ ...valid, started: valid.started + " ".repeat(65) }]]) assert.throws(() => ownedProcessWaitRecords(input));
});
test("actual Windows missing/reused PID is allowed while alive timeout and explicit throws stay failures", { skip: process.platform !== "win32" }, async () => {
  const source = await readFile(new URL("./product.draw-performance.mjs", import.meta.url), "utf8");
  const commandParts = /^\s*const waitCommand = ("(?:[^"\\]|\\.)*") \+ psQuote\(waitProof\) \+ ("(?:[^"\\]|\\.)*");$/m.exec(source);
  assert.ok(commandParts, "Actual driver wait expression required; no duplicate loop implementation");
  const command = records => JSON.parse(commandParts[1]) + psQuote(JSON.stringify(ownedProcessWaitRecords(records))) + JSON.parse(commandParts[2]);
  const ps = text => new Promise(resolve => execFile("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", text], { windowsHide: true, timeout: 10000, maxBuffer: 128 * 1024 }, (error, stdout, stderr) => resolve({ code: error?.code ?? 0, killed: error?.killed === true, stdout, stderr })));
  const absent = await ps("Get-Process -Id 2147483647 -ErrorAction SilentlyContinue");
  assert.equal(absent.code, 1); assert.equal(absent.stdout, ""); assert.equal(absent.stderr, "");
  const restored = await ps(command([{ id: 2147483647, started: "2026-10-07T00:00:00Z" }]));
  assert.equal(restored.code, 0); assert.equal(restored.stdout, ""); assert.equal(restored.stderr, "");
  const empty = await ps(command([])); assert.equal(empty.code, 0);
  const reused = await ps(command([{ id: process.pid, started: "1900-01-01T00:00:00Z" }])); assert.equal(reused.code, 0);
  const ownStart = await ps("$process=Get-Process -Id " + process.pid + " -ErrorAction Stop;try{$process.StartTime.ToUniversalTime().ToString('o')}finally{$process.Dispose()}");
  assert.equal(ownStart.code, 0);
  const alive = await ps(command([{ id: process.pid, started: ownStart.stdout.trim() }]).replace(".AddSeconds(20)", ".AddMilliseconds(20)"));
  assert.equal(alive.code, 1); assert.equal(alive.killed, false); assert.match(alive.stderr, /Owned WebView did not exit/);
  const thrown = await ps("$ErrorActionPreference='Stop';throw 'expected failure';exit 0"); assert.equal(thrown.code, 1); assert.match(thrown.stderr, /expected failure/);
  const summary = { missingWithoutExit: { code: absent.code, stdoutBytes: absent.stdout.length, stderrBytes: absent.stderr.length }, missingWithDriverExit: restored.code, empty: empty.code, reusedInstance: reused.code, ownedAliveTimeout: alive.code, throwBeforeExit: thrown.code, OSsettingsChanged: false, nativeWailsLaunches: 0, processKillCount: 0, longCommandHypothesis: "rejected: failure is PowerShell final unsuccessful missing-PID lookup; coordinator measured fullRecordJSON6579 and projected command815" };
  await writeFile(fileURLToPath(new URL("../../../.task/draw-performance-powershell-reproduction.json", import.meta.url)), JSON.stringify(summary, null, 2) + "\n");
});

function fixture(comments = 200, completed = 1) {
  const workload = drawWorkload(comments, 20), n = workload.participants;
  const rows = Array.from({ length: n }, (_, index) => ({ id: "p-" + index, nickname: "참가자" + index, publicIdentifier: "public" + index, kind: "fixed", classification: "included", reason: "", included: true, commentCount: index === 0 ? 60 : comments === 100 || index < 59 ? 1 : 2, previews: [index === 0 ? "[디시콘]" : "댓글 본문"] }));
  const summary = { backendSessionId: "session", draftId: "draft", revision: 5, articleGeneration: 1, state: "ready" };
  const context = { backendSessionId: "session", draftId: "draft", revision: 5, articleGeneration: 1 };
  const article = { url: "https://example.test/fixture", title: "fixture" }, filters = { excludeAuthor: true }, single = { id: "prize", name: "즉시 추첨 검증", count: 10 };
  const draft = { summary, load: { state: "completed", pages: 2, comments }, participants: n, included: n, excluded: 0, prizes: { mode: "single", drawMode: "immediate", single }, article, filters };
  const page = { context, offset: 0, total: n, matched: n, rows };
  const round = { number: 1, attempt: 1, state: "completed", mode: "immediate", executedAt: "2026-10-07T00:00:00Z", winners: rows.slice(0, 10).map((participant, index) => ({ participant: { ...participant, previews: [] }, prizeId: "prize", slot: index })), prizes: [single] };
  const collection = { collectionId: "collection", revision: 3, participantCount: n, selectedCount: n, remainingCount: n - 10, snapshot: { acceptedComments: comments, pages: 2, complete: true }, article, filters, roundTotal: 1, roundOffset: 0, rounds: [round], latestRound: round };
  const counts = factor => ({ collections: factor, operations: factor, rounds: factor, attempts: factor, results: factor, winners: factor * 10, participants: factor * n });
  const old = Array.from({ length: completed - 1 }, (_, index) => ({ ...collection, collectionId: "old" + index }));
  const before = { session: "session", counts: counts(completed - 1), fixtureCalls: completed * 3, fixtureBodiesClosed: completed * 3, latest: old };
  const after = { session: "session", counts: counts(completed), fixtureCalls: completed * 3, fixtureBodiesClosed: completed * 3, latest: [...old, collection] };
  const frozen = { collectionId: "collection", revision: 3, total: n, matched: n, offset: 0, rows };
  const prior = new Set(old.map(value => value.collectionId));
  return { workload, draft, page, before, after, frozen, prior, completed };
}
test("equivalent initial state ignores fresh owner IDs/revision and preserves exact inclusion/prize/comment input", () => {
  for (const comments of [100, 200]) {
    const value = fixture(comments), before = JSON.stringify(value);
    const first = initialDrawState(value.draft, value.page, value.workload);
    value.draft.summary.draftId = "another"; value.page.context.draftId = "another";
    value.draft.summary.revision = 10; value.page.context.revision = 10;
    assert.deepEqual(initialDrawState(value.draft, value.page, value.workload), first);
    assert.equal(first.roundCount, 0); assert.notEqual(JSON.stringify(value), before);
    assert.equal(first.participants.length, value.workload.participants);
  }
});
test("initial state rejects unfinished collection stale context missing duplicate excluded or undercounted participants", () => {
  const cases = [v => { v.draft.load.state = "loading"; }, v => { v.draft.summary.state = "finalized"; }, v => { v.draft.prizes.single.count = 9; }, v => { v.page.context.revision--; }, v => { v.page.context.backendSessionId = "old"; }, v => { v.page.rows[1].id = v.page.rows[0].id; }, v => { v.page.rows[0].included = false; }, v => { v.page.rows[0].commentCount--; }, v => { v.draft.included--; }];
  for (const change of cases) { const v = fixture(); change(v); assert.throws(() => initialDrawState(v.draft, v.page, v.workload)); }
});
test("durable oracle accepts real-projection shape and exact growing counts without consuming prior collections", () => {
  for (const comments of [100, 200]) for (const completed of [1, 20]) {
    const v = fixture(comments, completed), before = JSON.stringify(v);
    const result = verifyDrawStored(v.after, v.before, v.draft, v.frozen, v.workload, completed, v.prior);
    assert.equal(result.collection.collectionId, "collection"); assert.equal(JSON.stringify(v), before);
  }
});
test("stored participant preview text and empty winner previews have distinct explicit contracts", () => {
  const v = fixture(100), before = JSON.stringify(v);
  verifyParticipantSnapshots(v.page.rows, v.frozen.rows);
  verifyDrawStored(v.after, v.before, v.draft, v.frozen, v.workload, v.completed, v.prior);
  assert.equal(JSON.stringify(v), before);
  assert.equal(v.frozen.rows[0].previews[0], "[디시콘]");
  assert.deepEqual(v.after.latest[0].rounds[0].winners[0].participant.previews, []);
  v.after.latest[0].rounds[0].winners[0].participant.previews = ["leaked preview"];
  assert.throws(() => verifyDrawStored(v.after, v.before, v.draft, v.frozen, v.workload, v.completed, v.prior));
});
test("each stored public participant field and row ordering remains independently enforced", () => {
  for (const field of ["id", "nickname", "publicIdentifier", "kind", "classification", "reason", "included", "commentCount"]) {
    const v = fixture(), frozen = structuredClone(v.frozen.rows);
    frozen[0][field] = field === "included" ? false : field === "commentCount" ? 59 : "changed";
    assert.throws(() => verifyParticipantSnapshots(v.page.rows, frozen), field);
  }
  const v = fixture(), reordered = structuredClone(v.frozen.rows); reordered.reverse(); assert.throws(() => verifyParticipantSnapshots(v.page.rows, reordered));
});
test("participant previews preserve exact text and reject omissions null or excessive count/UTF16 length", () => {
  for (const previews of [[], null, undefined, ["changed"], [1], ["x".repeat(257)], ["a", "b", "c", "d"]]) {
    const v = fixture(), frozen = structuredClone(v.frozen.rows); frozen[0].previews = previews; assert.throws(() => verifyParticipantSnapshots(v.page.rows, frozen));
  }
  for (const previews of [[], ["x".repeat(256)], ["a", "b", "c"]]) {
    const v = fixture(); v.page.rows[0].previews = previews; const frozen = structuredClone(v.page.rows); verifyParticipantSnapshots(v.page.rows, frozen);
  }
});
for (const [name, change] of [
  ["duplicate durable result", v => { v.after.counts.results++; }],
  ["wrong previous DB count", v => { v.before.counts.rounds++; }],
  ["changed session", v => { v.after.session = "new"; }],
  ["unexpected recollection", v => { v.after.fixtureCalls++; v.after.fixtureBodiesClosed++; }],
  ["open network body", v => { v.after.fixtureBodiesClosed--; }],
  ["not a fresh collection", v => { v.prior.add("collection"); }],
  ["missing accepted comments", v => { v.after.latest.at(-1).snapshot.acceptedComments--; }],
  ["wrong selected count", v => { v.after.latest.at(-1).selectedCount--; }],
  ["wrong remaining count", v => { v.after.latest.at(-1).remainingCount++; }],
  ["extra round", v => { v.after.latest.at(-1).roundTotal = 2; }],
  ["not completed", v => { v.after.latest.at(-1).rounds[0].state = "executing"; }],
  ["duplicate winner", v => { v.after.latest.at(-1).rounds[0].winners[1] = v.after.latest.at(-1).rounds[0].winners[0]; }],
  ["missing winner", v => { v.after.latest.at(-1).rounds[0].winners.pop(); }],
  ["foreign winner", v => { v.after.latest.at(-1).rounds[0].winners[0].participant = { ...v.frozen.rows[0], id: "foreign" }; }],
  ["excluded stored participant", v => { v.frozen.rows[30].included = false; }],
  ["stale frozen revision", v => { v.frozen.revision--; }],
  ["wrong prize", v => { v.after.latest.at(-1).rounds[0].winners[0].prizeId = "foreign"; }],
]) test("durable oracle rejects " + name, () => { const v = fixture(); change(v); assert.throws(() => verifyDrawStored(v.after, v.before, v.draft, v.frozen, v.workload, v.completed, v.prior)); });

test("p95 summary requires all20 verified same-input samples and keeps result vs post-query timing separate", () => {
  const samples = Array.from({ length: 20 }, (_, index) => ({ verified: true, initialStateEqual: true, clickToResultMs: index, clickToDurableRequeryMs: index + 100 }));
  const before = JSON.stringify(samples);
  assert.deepEqual(summarizeDrawSamples(samples, 20), { sampleCount: 20, clickToResultP95Ms: 18, clickToDurableRequeryP95Ms: 118 });
  assert.equal(JSON.stringify(samples), before);
  assert.throws(() => summarizeDrawSamples(samples.slice(1), 20));
  for (const field of ["verified", "initialStateEqual"]) { const changed = structuredClone(samples); changed[0][field] = false; assert.throws(() => summarizeDrawSamples(changed, 20)); }
  for (const bad of [NaN, Infinity, -1, "10"]) { const changed = structuredClone(samples); changed[0].clickToResultMs = bad; assert.throws(() => summarizeDrawSamples(changed, 20)); }
  const reversed = structuredClone(samples); reversed[1].clickToDurableRequeryMs = 0; assert.throws(() => summarizeDrawSamples(reversed, 20));
});

function timerEnvironment() {
  const model = { now: 0, result: false, options: [], rows: 10, selected: "round", disabled: false, contains: true, listeners: new Map(), disconnects: 0, clears: 0, timer: null, check: null };
  const create = { contains: () => model.contains };
  const screen = { querySelector: () => model.noSelect ? null : ({ value: model.selected, disabled: model.disabled }), querySelectorAll: query => query.includes(" option") ? model.options : Array.from({ length: model.rows }) };
  const env = { window: {}, performance: { now: () => model.now }, document: { body: {}, querySelector: query => query === ".create-product" ? create : model.result ? screen : null, addEventListener: (name, listener) => model.listeners.set(name, listener), removeEventListener: name => model.listeners.delete(name) }, MutationObserver: class { constructor(callback) { model.check = callback; } observe() {} disconnect() { model.disconnects++; } }, setTimeout: function (callback) { assert.equal(this, env.window); model.timer = callback; return 1; }, clearTimeout: function () { assert.equal(this, env.window); model.clears++; } };
  const click = (extra = {}) => model.listeners.get("click")({ isTrusted: true, target: { closest: () => ({ textContent: "추첨 생성", disabled: false }) }, ...extra });
  const complete = () => { model.result = true; model.options = [{ value: "round", textContent: "1회 완료" }]; model.check(); };
  return { model, env, click, complete };
}
test("first trusted Create click to exact DOM timer excludes setup and post-verification wait", async () => {
  const { model, env, click, complete } = timerEnvironment(); watchCreateDraw({}, env); const owner = env.window.__jackpotCreateTimer;
  model.now = 100; click(); model.now = 200; complete(); model.now = 500;
  assert.deepEqual(await owner.promise, { ok: true, elapsedMs: 100 }); assert.equal(owner.verifiedElapsed(), 400);
  assert.equal(model.listeners.size, 0); assert.equal(model.disconnects, 1); assert.equal(model.clears, 1); owner.dispose(); model.check(); assert.equal(model.disconnects, 1);
});
test("synthetic foreign disabled wrong button and duplicate clicks cannot produce or reset trusted timing", async () => {
  const { model, env, click, complete } = timerEnvironment(); watchCreateDraw({}, env);
  model.now = 1; click({ isTrusted: false }); complete(); assert.equal(model.disconnects, 0);
  model.contains = false; click(); model.check(); assert.equal(model.disconnects, 0); model.contains = true;
  for (const button of [{ textContent: "推", disabled: false }, { textContent: "추첨 생성", disabled: true }]) { click({ target: { closest: () => button } }); model.check(); assert.equal(model.disconnects, 0); }
  model.now = 5; click(); model.now = 50; click(); model.now = 100; model.check(); assert.deepEqual(await env.window.__jackpotCreateTimer.promise, { ok: true, elapsedMs: 95 });
});
for (const [name, change] of [["missing select", m => { m.noSelect = true; }], ["pending query", m => { m.disabled = true; }], ["wrong selection", m => { m.selected = "old"; }], ["nine winners", m => { m.rows = 9; }], ["extra round", m => { m.options.push({ value: "other", textContent: "2회 완료" }); }], ["executing state", m => { m.options[0].textContent = "1회 추첨 중"; }]]) test("timing does not accept " + name, async () => {
  const { model, env, click } = timerEnvironment(); watchCreateDraw({}, env); click(); model.result = true; model.options = [{ value: "round", textContent: "1회 완료" }]; change(model); model.check(); assert.equal(model.disconnects, 0); model.timer(); assert.equal((await env.window.__jackpotCreateTimer.promise).ok, false); assert.throws(() => env.window.__jackpotCreateTimer.verifiedElapsed()); assert.equal(model.listeners.size, 0);
});
test("missing click timeout and cancellation resolve failure with deterministic complete cleanup", async () => {
  for (const reason of ["timeout", "dispose"]) { const { model, env } = timerEnvironment(); watchCreateDraw({}, env); const owner = env.window.__jackpotCreateTimer; model.now = 20; if (reason === "timeout") model.timer(); else owner.dispose(); assert.deepEqual(await owner.promise, { ok: false, elapsedMs: null }); assert.equal(model.listeners.size, 0); assert.equal(model.disconnects, 1); assert.equal(model.clears, 1); }
});
test("invalid timer owner bounds and setup dependency failures cannot retain listeners", () => {
  for (const timeoutMs of [0, -1, Infinity, NaN, 60001]) { const { model, env } = timerEnvironment(); assert.throws(() => watchCreateDraw({ timeoutMs }, env)); assert.equal(model.listeners.size, 0); }
  for (const reason of ["owner", "create", "body", "listener", "observe", "timer"]) {
    const { model, env } = timerEnvironment();
    if (reason === "owner") env.window.__jackpotCreateTimer = {};
    if (reason === "create") env.document.querySelector = () => null;
    if (reason === "body") env.document.body = null;
    if (reason === "listener") env.document.addEventListener = (name, listener) => { model.listeners.set(name, listener); throw new Error("listener fault"); };
    if (reason === "observe") env.MutationObserver.prototype.observe = () => { throw new Error("observer fault"); };
    if (reason === "timer") env.setTimeout = () => { throw new Error("timer fault"); };
    assert.throws(() => watchCreateDraw({}, env)); assert.equal(model.listeners.size, 0);
  }
});
test("synchronous timeout clears its later assigned timer and exact60000 bound is allowed", async () => {
  const { model, env } = timerEnvironment(); env.setTimeout = callback => { callback(); return 1; };
  watchCreateDraw({ timeoutMs: 60000 }, env); assert.deepEqual(await env.window.__jackpotCreateTimer.promise, { ok: false, elapsedMs: null }); assert.equal(model.clears, 1); assert.equal(model.disconnects, 1);
});
