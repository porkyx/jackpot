import assert from "node:assert/strict";
import { win32 } from "node:path";
import { normalizeReservationReport } from "./reservationrestart.invariants.mjs";
import { percentile95 } from "./stress.metrics.mjs";

export function drawWorkload(comments, samples) {
  assert.ok(comments === 100 || comments === 200, "100/200 delivered comments required");
  assert.ok(Number.isSafeInteger(samples) && samples >= 20 && samples <= 50, "20..50 fresh collection samples required");
  return Object.freeze({ comments, participants: comments === 100 ? 41 : 100, winners: 10, samples });
}

export function ownedEvidencePath(directory, workspace) {
  const absolute = win32.resolve(directory), task = win32.join(win32.resolve(workspace), ".task");
  assert.ok(win32.isAbsolute(directory) && absolute.toLowerCase() === directory.toLowerCase(), "Canonical absolute evidence path required");
  assert.equal(win32.dirname(absolute).toLowerCase(), task.toLowerCase(), "Evidence must be a direct workspace .task child");
  assert.match(win32.basename(absolute), /^draw-performance-[a-f0-9]{32}$/);
  return absolute;
}

export function bindingMethod(source, name) {
  assert.match(name, /^[A-Za-z]+$/);
  const match = new RegExp("export function " + name + "\\b(?:(?!export function )[\\s\\S])*?return\\s+\\$Call\\.ByID\\((\\d+)(?=[,)])").exec(source);
  const id = Number(match?.[1]);
  assert.ok(Number.isSafeInteger(id) && id > 0 && id <= 4294967295, "Generated method ID required: " + name);
  return id;
}

export function boundedPSFailure(error, stdout, stderr) {
  const detail = {
    message: String(error?.message ?? error ?? "unknown").slice(-256),
    code: typeof error?.code === "number" ? error.code : error?.code === undefined ? null : String(error.code).slice(0, 32),
    signal: error?.signal === undefined ? null : String(error.signal).slice(0, 32),
    killed: error?.killed === true,
    stdout: String(stdout ?? "").slice(-480), stderr: String(stderr ?? "").slice(-480),
  };
  return "Owned draw command failed: " + JSON.stringify(detail);
}

// Full OS/CDP ownership proof is checked and retained by the caller. Waiting
// is read-only and needs only the process instance identity, not its command.
export function ownedProcessWaitRecords(records) {
  assert.ok(Array.isArray(records) && records.length <= 64, "Bounded owned process inventory required");
  const projected = records.map(record => {
    assert.ok(record !== null && typeof record === "object");
    assert.ok(Number.isSafeInteger(record.id) && record.id > 0 && record.id <= 2147483647, "Windows signed PID required");
    assert.ok(typeof record.started === "string" && record.started.length <= 64 && Number.isFinite(Date.parse(record.started)), "Exact process start timestamp required");
    return { id: record.id, started: record.started };
  });
  assert.equal(new Set(projected.map(record => record.id + ":" + record.started)).size, projected.length, "Distinct process instances required");
  return projected;
}

// IDs, timestamps and revisions identify a fresh owner, but do not change the
// configured work. The participant keys and frozen inclusion decisions do.
export function initialDrawState(draft, page, workload) {
  assert.equal(draft.summary.state, "ready");
  assert.equal(draft.load.state, "completed");
  assert.equal(draft.load.pages, 2);
  assert.equal(draft.load.comments, workload.comments);
  assert.equal(draft.participants, workload.participants);
  assert.equal(draft.included, workload.participants);
  assert.equal(draft.excluded, 0);
  assert.equal(draft.prizes.mode, "single");
  assert.equal(draft.prizes.drawMode, "immediate");
  assert.deepEqual({ name: draft.prizes.single.name, count: draft.prizes.single.count }, { name: "즉시 추첨 검증", count: 10 });
  assert.equal(page.context.draftId, draft.summary.draftId);
  assert.equal(page.context.revision, draft.summary.revision);
  assert.equal(page.context.articleGeneration, draft.summary.articleGeneration);
  assert.equal(page.context.backendSessionId, draft.summary.backendSessionId);
  assert.equal(page.total, workload.participants);
  assert.equal(page.matched, workload.participants);
  assert.equal(page.offset, 0);
  assert.equal(page.rows.length, workload.participants);
  assert.equal(new Set(page.rows.map(row => row.id)).size, workload.participants);
  assert.ok(page.rows.every(row => typeof row.id === "string" && row.id.length > 0 && row.included === true));
  assert.equal(page.rows.reduce((sum, row) => sum + row.commentCount, 0), workload.comments);
  return {
    roundCount: 0, article: draft.article, filters: draft.filters,
    prize: { name: draft.prizes.single.name, count: draft.prizes.single.count, mode: "immediate" },
    participants: page.rows.map(({ id, nickname, publicIdentifier, kind, classification, reason, included, commentCount }) => ({ id, nickname, publicIdentifier, kind, classification, reason, included, commentCount })),
  };
}

export function publicParticipantSnapshot({ id, nickname, publicIdentifier, kind, classification, reason, included, commentCount }) {
  return { id, nickname, publicIdentifier, kind, classification, reason, included, commentCount };
}

export function verifyParticipantSnapshots(draftRows, frozenRows) {
  assert.ok(Array.isArray(draftRows) && Array.isArray(frozenRows));
  assert.deepEqual(frozenRows.map(publicParticipantSnapshot), draftRows.map(publicParticipantSnapshot), "Stored public participant fields and order must equal the confirmed draft");
  // Both participant page producers use contracts.PreviewTexts. The winner
  // projection instead deliberately sends previews=[] (round.go); it is not
  // the same DTO projection even though its stored participant is identical.
  for (let index = 0; index < draftRows.length; index++) {
    for (const row of [draftRows[index], frozenRows[index]]) assert.ok(Array.isArray(row.previews) && row.previews.length <= 3 && row.previews.every(value => typeof value === "string" && value.length <= 256), "Bounded participant preview contract required");
    assert.deepEqual(frozenRows[index].previews, draftRows[index].previews, "Stored comment previews must equal the draft preview projection");
  }
}

export function verifyDrawStored(raw, beforeRaw, draft, frozen, workload, completed, previousIDs) {
  const state = normalizeReservationReport(raw), before = normalizeReservationReport(beforeRaw);
  assert.equal(state.session, before.session);
  const expected = { collections: completed, operations: completed, rounds: completed, attempts: completed, results: completed, winners: completed * 10, participants: completed * workload.participants };
  assert.deepEqual(state.counts, expected);
  assert.deepEqual(before.counts, Object.fromEntries(Object.entries(expected).map(([key, value]) => [key, value - (key === "winners" ? 10 : key === "participants" ? workload.participants : 1)])));
  assert.equal(state.fixtureCalls - before.fixtureCalls, 0, "Draw must not recollect");
  assert.equal(state.fixtureCalls, state.fixtureBodiesClosed);
  assert.equal(state.fixtureCalls, completed * 3);
  assert.equal(state.latest.length, completed);
  const additions = state.latest.filter(collection => !previousIDs.has(collection.collectionId));
  assert.equal(additions.length, 1, "Exactly one new collection required");
  const collection = additions[0];
  assert.equal(collection.participantCount, workload.participants);
  assert.equal(collection.selectedCount, workload.participants);
  assert.equal(collection.remainingCount, workload.participants - 10);
  assert.equal(collection.snapshot.acceptedComments, workload.comments);
  assert.equal(collection.snapshot.complete, true);
  assert.equal(collection.snapshot.pages, 2);
  assert.deepEqual(collection.article, draft.article);
  assert.deepEqual(collection.filters, draft.filters);
  assert.equal(collection.roundTotal, 1);
  assert.equal(collection.roundOffset, 0);
  assert.equal(collection.rounds.length, 1);
  const round = collection.rounds[0];
  assert.deepEqual(round, collection.latestRound);
  assert.equal(round.number, 1);
  assert.equal(round.attempt, 1);
  assert.equal(round.state, "completed");
  assert.equal(round.mode, "immediate");
  assert.ok(round.executedAt !== null && Number.isFinite(Date.parse(round.executedAt)));
  assert.equal(round.winners.length, 10);
  assert.equal(new Set(round.winners.map(winner => winner.participant.id)).size, 10);
  assert.equal(round.prizes.length, 1);
  assert.deepEqual(round.prizes[0], draft.prizes.single);
  assert.equal(frozen.collectionId, collection.collectionId);
  assert.equal(frozen.revision, collection.revision);
  assert.equal(frozen.total, workload.participants);
  assert.equal(frozen.matched, workload.participants);
  assert.equal(frozen.offset, 0);
  assert.equal(frozen.rows.length, workload.participants);
  assert.ok(frozen.rows.every(row => row.included === true));
  assert.equal(frozen.rows.reduce((sum, row) => sum + row.commentCount, 0), workload.comments);
  const stored = new Map(frozen.rows.map(row => [row.id, row]));
  assert.equal(stored.size, workload.participants);
  for (const winner of round.winners) {
    assert.ok(stored.has(winner.participant.id), "Winner must belong to included stored snapshot");
    assert.deepEqual(publicParticipantSnapshot(winner.participant), publicParticipantSnapshot(stored.get(winner.participant.id)), "Winner public participant fields must equal the included stored snapshot");
    assert.deepEqual(winner.participant.previews, [], "Winner projection intentionally omits comment previews");
    assert.equal(winner.prizeId, draft.prizes.single.id);
  }
  return { state, collection };
}

export function summarizeDrawSamples(samples, requested) {
  assert.ok(Number.isSafeInteger(requested) && requested >= 20 && requested <= 50);
  assert.equal(samples.length, requested);
  assert.ok(samples.every(sample => sample.verified === true && sample.initialStateEqual === true && Number.isFinite(sample.clickToResultMs) && sample.clickToResultMs >= 0 && Number.isFinite(sample.clickToDurableRequeryMs) && sample.clickToDurableRequeryMs >= sample.clickToResultMs));
  return { sampleCount: samples.length, clickToResultP95Ms: percentile95(samples.map(sample => sample.clickToResultMs)), clickToDurableRequeryP95Ms: percentile95(samples.map(sample => sample.clickToDurableRequeryMs)) };
}

// Browser-serializable. Capture the actual trusted submit click; startup,
// collection delivery, Playwright actionability and post-result assertions are
// excluded. Observe the stable document because Create replaces its screen.
export function watchCreateDraw({ timeoutMs = 20000 } = {}, environment = { document, window, MutationObserver, performance, setTimeout, clearTimeout }) {
  if (!Number.isFinite(timeoutMs) || timeoutMs <= 0 || timeoutMs > 60000) throw new Error("Invalid create timing bound");
  const { document: doc, window: host, performance: clock } = environment;
  const create = doc.querySelector(".create-product");
  if (create === null || doc.body === null || host.__jackpotCreateTimer !== undefined) throw new Error("Create timing owner unavailable");
  let started, ready, finished = false, succeeded = false, listener = false, timeout;
  let resolve;
  const promise = new Promise(done => { resolve = done; });
  const cleanup = () => { observer.disconnect(); if (listener) { doc.removeEventListener("click", clicked, true); listener = false; } if (timeout !== undefined) { environment.clearTimeout.call(host, timeout); timeout = undefined; } };
  const finish = ok => { if (finished) return; finished = true; succeeded = ok; ready = clock.now(); cleanup(); resolve({ ok, elapsedMs: started === undefined ? null : ready - started }); };
  const clicked = event => {
    const button = event.target?.closest?.("button");
    if (started === undefined && event.isTrusted === true && button?.textContent.trim() === "추첨 생성" && button.disabled === false && create.contains(button)) started = clock.now();
  };
  const check = () => {
    const screen = doc.querySelector('[data-product-screen="result"]');
    if (started === undefined || screen === null) return;
    const select = screen.querySelector('select[name="resultRound"]');
    const options = screen.querySelectorAll('select[name="resultRound"] option');
    const winners = screen.querySelectorAll('ol[aria-label="품목별 당첨자"] > li > ul > li');
    if (select !== null && select.disabled === false && options.length === 1 && options[0].textContent.includes("완료") && select.value === options[0].value && winners.length === 10) finish(true);
  };
  const observer = new environment.MutationObserver(check);
  host.__jackpotCreateTimer = {
    promise, dispose: () => finish(false),
    verifiedElapsed: () => { if (!succeeded || started === undefined || ready === undefined) throw new Error("Create result not measured"); return clock.now() - started; },
  };
  try {
    listener = true; doc.addEventListener("click", clicked, true);
    observer.observe(doc.body, { childList: true, subtree: true, characterData: true, attributes: true });
    timeout = environment.setTimeout.call(host, () => finish(false), timeoutMs);
    if (finished && timeout !== undefined) { environment.clearTimeout.call(host, timeout); timeout = undefined; }
  } catch (error) { finish(false); delete host.__jackpotCreateTimer; throw error; }
}
