import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
const base = dirname(fileURLToPath(import.meta.url));
const read = (file) => JSON.parse(readFileSync(join(base, file), "utf8"));
const manifest = read("manifest.json");

for (const sample of manifest.samples) test(`${sample.fixture}: checksum, observed fields, identifier relations and anonymity`, () => {
  const raw = readFileSync(join(base, sample.fixture));
  assert.equal(createHash("sha256").update(raw).digest("hex"), sample.fixtureSha256);
  const data = JSON.parse(raw);
  for (const field of ["total_cnt", "comment_cnt", "comments", "pagination", "allow_reply"]) assert.ok(Object.hasOwn(data, field));
  assert.equal(Number.isSafeInteger(data.total_cnt), true);
  const rows = data.comments ?? [];
  if (sample.requestedPage === 2) { assert.deepEqual(rows, []); assert.equal(data.total_cnt, 10); assert.equal(data.pagination, '<a href="javascript:viewComments(1,\'D\',true)">1</a>'); return; }
  assert.equal(rows.length, sample.rawRows);
  assert.equal(rows.filter((row) => row.del_yn === "Y").length, sample.deletedRows);
  assert.equal(rows.filter((row) => row.nicktype === "COMMENT_BOY").length, sample.advertisingRows);
  assert.equal(rows.filter((row) => row.nicktype !== "COMMENT_BOY" && row.del_yn !== "Y").length, data.total_cnt);
  const humanIds = rows.filter((row) => row.nicktype !== "COMMENT_BOY").map((row) => String(row.no));
  assert.equal(new Set(humanIds).size, humanIds.length);
  for (const row of rows) {
    assert.match(row.name, /^Participant_\d+$/);
    assert.ok(row.user_id === "" || /^user_\d+$/.test(row.user_id));
    assert.ok(row.ip === "" || /^198\.\d+$/.test(row.ip));
    assert.ok(row.depth === 0 || row.depth === 1);
    assert.ok(row.memo.startsWith("fixture comment ") || row.memo.includes("https://example.invalid/"));
    assert.ok(!/dcinside\.com|<script|javascript:/i.test(row.memo + row.gallog_icon));
    if (row.nicktype !== "COMMENT_BOY") assert.equal(String(row.parent), String(sample.fixtureArticleNo));
    if (row.reg_date) assert.match(row.reg_date, /^(?:2025\.)?\d\d\.\d\d 12:00:\d\d$/);
  }
  if (sample.author?.identitySufficient) for (const id of sample.author.matchingRowIds) {
    const row = rows.find((entry) => entry.no === id);
    assert.equal(row?.name, sample.author.name); assert.equal(row?.user_id, sample.author.user_id);
  }
});

test("real zero comments preserve null wire fields rather than inventing an empty array", () => {
  const data = read("mini-zero-page1.json");
  assert.equal(data.total_cnt, 0); assert.equal(data.comments, null); assert.equal(data.pagination, null);
  assert.deepEqual(Object.keys(data).sort(), ["allow_reply", "comment_cnt", "comments", "pagination", "total_cnt"].sort());
});
test("observed fixture set includes four gallery kinds, fixed/nonfixed/unregistered, replies and deleted rows", () => {
  assert.equal(manifest.commentsCreated, 0);
  assert.deepEqual([...new Set(manifest.samples.map((sample) => sample.kind))].sort(), ["G", "M", "MI", "PR"]);
  const rows = manifest.samples.flatMap((sample) => read(sample.fixture).comments ?? []);
  assert.ok(rows.some((row) => row.nicktype === "20" && row.gallog_icon.includes("fix_nik.gif")));
  assert.ok(rows.some((row) => row.user_id !== "" && row.gallog_icon.includes("/nik.gif")));
  assert.ok(rows.some((row) => row.user_id === "" && row.ip !== ""));
  assert.ok(rows.some((row) => row.depth === 1)); assert.ok(rows.some((row) => row.del_yn === "Y"));
  assert.ok(rows.some((row) => row.user_id === "" && row.ip === "" && row.nicktype !== "COMMENT_BOY"));
});

const urls = read("urls.json");
test("thirteen live PC/mobile/short URL mappings converge to the same observed article per kind", () => {
  const live = urls.cases.filter((entry) => entry.level === "live-public-read");
  assert.equal(live.length, 13);
  for (const kind of ["G", "M", "MI", "PR"]) {
    const cases = live.filter((entry) => entry.normalized.galleryKind === kind); assert.equal(cases.length, kind === "MI" ? 4 : 3);
    for (const entry of cases) {
      assert.equal(entry.httpStatus, 200); assert.equal(entry.finalUrl, entry.normalized.canonicalUrl);
      assert.deepEqual(entry.normalized, cases[0].normalized);
      assert.ok(urls.allowedHosts.includes(new URL(entry.input).hostname));
    }
  }
});
test("reject fixtures and local normalization decisions are clearly separate from live requests", () => {
  const rejected = urls.cases.filter((entry) => entry.outcome === "reject"); assert.equal(rejected.length, 16);
  for (const entry of rejected) { assert.equal(entry.level, "product-contract"); assert.equal(entry.transportObserved, false); assert.equal(entry.normalized, undefined); }
  assert.equal(urls.observations[0].httpStatus, 200); assert.equal(urls.observations[0].verifiedArticle, false); assert.equal(urls.observations[0].responseKind, "script_redirect_only");
});

const pages = read("page-signals.json");
test("observed page20 has a later page and actual nonempty page21, so the20-page cap is incomplete", () => {
  const [first, twentieth, twentyFirst] = pages.cases;
  for (const page of [first, twentieth, twentyFirst]) {
    assert.equal(page.level, "live-public-read-metadata"); assert.equal(page.httpStatus, 200);
    assert.match(page.sourceSha256, /^[a-f0-9]{64}$/); assert.equal(page.rawWireNotCopied, true);
    assert.equal(page.totalCount, 6454); assert.equal(page.commentCountField, 0);
    assert.equal(page.currentPageMarker, page.requestedPage); assert.equal(page.referencedLastPage, 66);
    assert.ok(page.rawRows > page.advertisingRows); assert.equal(page.wireCommentType, "array");
  }
  assert.equal(twentieth.requestedPage, 20); assert.ok(twentieth.referencedPages.includes(21));
  assert.equal(twentyFirst.requestedPage, 21); assert.equal(twentyFirst.rawRows, 105);
});
test("an out-of-range empty requested page cannot turn a positive global count into a zero-comment article", () => {
  const page = pages.cases.find((entry) => entry.id === "best-near20-20");
  assert.equal(page.requestedPage, 20); assert.equal(page.referencedLastPage, 5);
  assert.equal(page.totalCount, 998); assert.equal(page.rawRows, 0); assert.equal(page.currentPageMarker, null);
  assert.equal(page.wireCommentType, "array");
  const zero = read("mini-zero-page1.json");
  assert.equal(zero.total_cnt, 0); assert.equal(zero.comments, null); assert.equal(zero.pagination, null);
});
test("the repeated public page preserves every observed human identity without publishing those identities", () => {
  assert.equal(pages.duplicatePage.originalHumanRows, 10);
  assert.equal(pages.duplicatePage.repeatedHumanRows, 10);
  assert.equal(pages.duplicatePage.identicalHumanIds, 10);
  assert.equal(pages.duplicatePage.rawIdentityValuesNotCopied, true);
});
test("missing articles and failed original results remain explicit failures instead of empty successes", () => {
  assert.equal(pages.missingArticle.httpStatus, 404); assert.equal(pages.missingArticle.emptySuccess, false);
  assert.equal(pages.originalResults.httpStatus, 530); assert.equal(pages.originalResults.resultsVerified, false);
});

const parity = JSON.parse(readFileSync(join(base, "../parity/classification.json"), "utf8"));
const caseById = (id) => { const result = parity.cases.find((entry) => entry.id === id); assert.ok(result, id); return result; };
test("client parity evidence is synthetic input and immutable indexed source rather than server proof", () => {
  assert.equal(parity.level, "public-client-model-on-synthetic-input"); assert.equal(parity.cases.length, 33); assert.match(parity.sourceSha256, /^[A-F0-9]{64}$/);
  for (const entry of parity.cases) { assert.equal(entry.observed.entries.length, entry.observed.participantCount); assert.ok(entry.trace.length > 0); assert.deepEqual(entry.trace.at(-1), entry.observed); }
});
test("source-observed independent filter priorities and cutoff boundaries stay recorded", () => {
  const expected = { "priority-unregistered-before-all": "유동", "priority-author-before-dccon": "작성자", "priority-dccon-before-cutoff": "디시콘", "priority-cutoff-before-keyword": "시간컷", "priority-exclude-before-include": "단어제외", "included-keyword": "단어포함" };
  for (const [id, reason] of Object.entries(expected)) assert.equal(caseById(id).observed.entries[0].reason, reason);
  assert.equal(caseById("cutoff-before-end").observed.entries[0].automatic, "unclassified");
  assert.equal(caseById("cutoff-exact-end").observed.entries[0].reason, "시간컷");
  assert.equal(caseById("included-keyword-miss-is-manual").observed.entries[0].finalIncluded, true);
  assert.equal(caseById("keywords-see-comments-after-cutoff").observed.entries[0].reason, "단어제외");
});
test("source-observed manual controls and author reset difference cannot disappear from parity evidence", () => {
  assert.equal(caseById("auto-include-manual-excluded").observed.entries[0].finalIncluded, false);
  assert.equal(caseById("auto-exclude-override-included").observed.entries[0].finalIncluded, true);
  assert.equal(caseById("manual-state-survives-filter-change").observed.entries[0].finalIncluded, false);
  assert.equal(caseById("reset-manual-keeps-auto-filter").observed.entries[0].finalIncluded, false);
  assert.deepEqual(caseById("bulk-affects-only-unclassified").observed.entries.map((row) => row.finalIncluded), [true, false]);
  assert.equal(caseById("author-hash-reset-by-load-sequence").observed.hasAuthorHash, false);
  assert.equal(caseById("author-hash-reset-by-load-sequence").decision, "D03");
});
