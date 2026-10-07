import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import vm from "node:vm";
process.env.TZ = "Asia/Seoul";
assert.equal(process.argv.length, 3, "Provide the local, previously inspected S04 bundle path. This harness never fetches network resources.");
const raw = readFileSync(process.argv[2]);
const hash = createHash("sha256").update(raw).digest("hex").toUpperCase();
assert.equal(hash, "2DC3149B19098BA37DE016D93E0F7E7888B32BC968F19A5E4457C25FB2B5D500");
const source = raw.toString("utf8");
const start = source.indexOf("let $=function(e){return e[e.Included=0]");
const end = source.indexOf(",fo=class", start);
assert.ok(start >= 0 && end > start);
class ControlledDate extends Date { constructor(value) { super(value ?? "2026-10-06T03:00:00.000Z"); } static now() { return Date.parse("2026-10-06T03:00:00.000Z"); } }
const context = vm.createContext({ Date: ControlledDate });
// Only the inspected pure model fragment runs, with no DOM/network/imports.
// MobX binding is irrelevant to direct calls. String identity replaces MD5;
// this observes nickname+identifier equality, not MD5 implementation.
vm.runInContext(`const sa=()=>{};const io={default:(value)=>value};${source.slice(start, end)};globalThis.observer={Store:lo,Form:uo,hash:so,Included:$.Included,Excluded:$.Excluded};`, context, { timeout: 100 });
const model = context.observer;
const user = (memberIcon = 1, name = "Participant_A", userId = "user_a", ip = "") => ({ name, userId, ip, memberIcon });
const comment = (body = "fixture", type = "text", actor = user(), time = "2026-10-06T03:30:00Z", index = 1) => ({ user: actor, body, type, time, index });
const cutoff = { useTimeCut: true, cutTime: { day: 0, hour: 12, minute: 30 } };
const cases = [
  { id: "default-manual-included", comments: [comment()] },
  { id: "unregistered-only", comments: [comment("fixture", "text", user(3, "Participant_A", "", "198.1"))], filter: { excludeUnregistered: true } },
  { id: "author-only", comments: [comment()], authorAfterParse: user() },
  { id: "dccon-only", comments: [comment("fixture image", "dccon")], filter: { excludeDccon: true } },
  { id: "mixed-dccon-and-text", comments: [comment("fixture image", "dccon"), comment("fixture text")], filter: { excludeDccon: true } },
  { id: "cutoff-before-end", comments: [comment("fixture", "text", user(), "2026-10-06T03:30:59Z")], filter: cutoff },
  { id: "cutoff-exact-end", comments: [comment("fixture", "text", user(), "2026-10-06T03:31:00Z")], filter: cutoff },
  { id: "one-before-cutoff", comments: [comment("early", "text", user(), "2026-10-06T03:30:59Z"), comment("late", "text", user(), "2026-10-06T03:31:01Z")], filter: cutoff },
  { id: "excluded-keyword", comments: [comment("prefix BAN suffix")], filter: { excludedKeyword: ["BAN"] } },
  { id: "included-keyword", comments: [comment("prefix JOIN suffix")], filter: { includedKeyword: ["JOIN"] } },
  { id: "included-keyword-miss-is-manual", comments: [comment("unrelated")], filter: { includedKeyword: ["JOIN"] } },
  { id: "case-sensitive-keyword", comments: [comment("Join")], filter: { includedKeyword: ["JOIN"] } },
  { id: "keyword-or", comments: [comment("JOIN")], filter: { includedKeyword: ["MISS", "JOIN"] } },
  { id: "priority-unregistered-before-all", comments: [comment("fixture", "dccon", user(3, "Participant_A", "", "198.1"), "2026-10-06T03:31:00Z")], authorAfterParse: user(3, "Participant_A", "", "198.1"), filter: { excludeUnregistered: true, excludeDccon: true, ...cutoff, excludedKeyword: ["디시콘"], includedKeyword: ["디시콘"] } },
  { id: "priority-author-before-dccon", comments: [comment("fixture", "dccon")], authorAfterParse: user(), filter: { excludeDccon: true, ...cutoff } },
  { id: "priority-dccon-before-cutoff", comments: [comment("fixture", "dccon", user(), "2026-10-06T03:31:00Z")], filter: { excludeDccon: true, ...cutoff, excludedKeyword: ["디시콘"] } },
  { id: "priority-cutoff-before-keyword", comments: [comment("BAN JOIN", "text", user(), "2026-10-06T03:31:00Z")], filter: { ...cutoff, excludedKeyword: ["BAN"], includedKeyword: ["JOIN"] } },
  { id: "priority-exclude-before-include", comments: [comment("BAN JOIN")], filter: { excludedKeyword: ["BAN"], includedKeyword: ["JOIN"] } },
  { id: "keywords-see-comments-after-cutoff", comments: [comment("early", "text", user(), "2026-10-06T03:30:00Z"), comment("BAN", "text", user(), "2026-10-06T03:31:00Z")], filter: { ...cutoff, excludedKeyword: ["BAN"] } },
  { id: "same-user-and-name-one-participant", comments: [comment("first"), comment("second")] },
  { id: "renamed-user-separate-participants", comments: [comment("first"), comment("second", "text", user(1, "Participant_B"))] },
  { id: "same-name-different-user-separate", comments: [comment("first"), comment("second", "text", user(1, "Participant_A", "user_b"))] },
  { id: "same-name-different-ip-separate", comments: [comment("first", "text", user(3, "Participant_A", "", "198.1")), comment("second", "text", user(3, "Participant_A", "", "198.2"))] },
  { id: "auto-include-manual-excluded", comments: [comment("JOIN")], filter: { includedKeyword: ["JOIN"] }, steps: [{ action: "status", value: "excluded" }] },
  { id: "auto-exclude-override-included", comments: [comment("BAN")], filter: { excludedKeyword: ["BAN"] }, steps: [{ action: "status", value: "included" }] },
  { id: "manual-state-survives-filter-change", comments: [comment("JOIN")], steps: [{ action: "status", value: "excluded" }, { action: "filter", value: { includedKeyword: ["JOIN"] } }] },
  { id: "override-survives-filter-off-on", comments: [comment("BAN")], filter: { excludedKeyword: ["BAN"] }, steps: [{ action: "status", value: "included" }, { action: "filter", value: { excludedKeyword: [] } }, { action: "filter", value: { excludedKeyword: ["BAN"] } }] },
  { id: "reset-manual-keeps-auto-filter", comments: [comment("BAN")], filter: { excludedKeyword: ["BAN"] }, steps: [{ action: "status", value: "included" }, { action: "resetManual" }] },
  { id: "bulk-affects-only-unclassified", comments: [comment("JOIN"), comment("other", "text", user(1, "Participant_B", "user_b"))], filter: { includedKeyword: ["JOIN"] }, steps: [{ action: "bulk", value: "excluded" }] },
  { id: "author-hash-reset-by-load-sequence", comments: [comment()], authorBeforeParse: user(), question: "Q02", decision: "D03" },
  { id: "unsupported-comment-not-participant", comments: [comment("unsupported", "unsupported")] },
  { id: "client-voice-placeholder", comments: [comment('<iframe src="https://example.invalid/voice/sample">')], liveWireObserved: false },
  { id: "empty-comment-list", comments: [] },
];
for (const item of cases) {
  const store = new model.Store();
  if (item.authorBeforeParse) store.setAuthorHash(model.hash(item.authorBeforeParse));
  store.parse(item.comments);
  if (item.filter) store.updateFilter(item.filter);
  if (item.authorAfterParse) store.setAuthorHash(model.hash(item.authorAfterParse));
  const trace = [];
  const snapshot = () => ({ participantCount: store.count, hasAuthorHash: store.hasAuthorHash, entries: store.getEntryList().map((entry) => ({ name: entry.user.name, userId: entry.user.userId, memberType: entry.user.memberType, automatic: entry.isAuto === null ? "unclassified" : entry.isAuto === model.Included ? "included" : "excluded", reason: entry.autoReason, finalIncluded: entry.status === model.Included, comments: entry.comments.map((value) => ({ body: value.body, type: value.type, index: value.index })) })) });
  trace.push(snapshot());
  for (const step of item.steps ?? []) {
    if (step.action === "status") store.getEntryList()[0].status = step.value === "included" ? model.Included : model.Excluded;
    else if (step.action === "filter") store.updateFilter(step.value);
    else if (step.action === "resetManual") store.resetAllStatus();
    else if (step.action === "bulk") store.setAllManualStatus(step.value === "included" ? model.Included : model.Excluded);
    trace.push(snapshot());
  }
  item.observed = snapshot(); item.trace = trace;
}
assert.equal(cases.find((item) => item.id === "author-hash-reset-by-load-sequence").observed.hasAuthorHash, false);
assert.equal(cases.find((item) => item.id === "auto-include-manual-excluded").observed.entries[0].finalIncluded, false);
assert.equal(cases.find((item) => item.id === "cutoff-exact-end").observed.entries[0].reason, "시간컷");
writeFileSync(new URL("./classification.json", import.meta.url), `${JSON.stringify({ observedAt: "2026-10-06", level: "public-client-model-on-synthetic-input", source: "https://dclottery.net/assets/es-Cr2bzqFj.js", sourceSha256: hash, questions: ["Q02"], decisions: ["D03", "D07"], limitations: "Isolated inspected pure model, MobX no-op, identity string instead of MD5. This is not real UI/server/raw DCInside mapping proof. No original model/bundle code is copied into fixture or product.", cases }, null, 2)}\n`);
console.log(`${cases.length} public-client model parity observations recorded`);
