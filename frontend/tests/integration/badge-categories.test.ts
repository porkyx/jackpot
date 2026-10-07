import { expect, it } from "@effect/vitest";
import { Deferred, Effect, Exit, Fiber, Schema, Scope, Stream } from "effect";
import { BadgeRules, FilterConfiguration, ParticipantData, type Filters } from "../../src/contracts/product";
import { ProtocolError } from "../../src/contracts/backend";
import { badgeCategoryLabels, badgePolicyText, participantCategory } from "../../src/app/participantCategories";
import { badgeEditorFields } from "../../src/screens/create/editorModel";
import { makeCreateWorkspace } from "../../src/screens/create/productOwner";
import { ClientIds, clientIdsFrom } from "../../src/platform/ids";
import { commandHarness, draft } from "../helpers/productCommands";

const defaults = (): typeof BadgeRules.Type => ({ weightingEnabled: false, fixed: { excluded: false, weight: 100 }, semiFixed: { excluded: false, weight: 100 }, mainManager: { excluded: false, weight: 100 }, subManager: { excluded: false, weight: 100 }, newAccount: { excluded: false, weight: 100 }, anonymous: { excluded: false, weight: 100 } });
function setup(filters: Filters = draft().filters) { return Effect.gen(function*() {
  const port = yield* commandHarness(draft({ filters })); Object.assign(port.commands, { changes: Stream.empty });
  const scope = yield* Scope.make(); const workspace = yield* makeCreateWorkspace(port.commands, () => {}).pipe(Scope.provide(scope), Effect.provideService(ClientIds, clientIdsFrom(() => "category")));
  return { port, scope, workspace, close: Scope.close(scope, Exit.void).pipe(Effect.andThen(port.close)) };
}); }

it("legacy missing or null rules decode and retain individual uniform policy", () => {
  for (const filters of [draft().filters, { ...draft().filters, badgeRules: null }]) {
    expect(Schema.decodeUnknownSync(FilterConfiguration)(filters)).toEqual(filters);
    expect(badgePolicyText(filters)).toBe("개인별 균등 추첨");
  }
});
for (const spec of badgeEditorFields) it("category " + spec.category + " decodes separately from fixed identity with literal badge label", () => {
  const person = { id: "p", nickname: "이름", publicIdentifier: "id", kind: "fixed" as const, badgeCategory: spec.category, classification: "excluded" as const, reason: "badge_category" as const, included: false, commentCount: 0, previews: [] };
  const decoded = Schema.decodeUnknownSync(ParticipantData)(person);
  expect(participantCategory(decoded)).toBe(spec.category); expect(badgeCategoryLabels[spec.category]).toBeDefined(); expect(decoded.kind).toBe("fixed");
  const { badgeCategory: _category, ...legacy } = person; expect(participantCategory(Schema.decodeUnknownSync(ParticipantData)(legacy))).toBe("fixed");
  expect(() => Schema.decodeUnknownSync(ParticipantData)({ ...person, badgeCategory: "unknown" })).toThrow();
});
for (const weight of [0, 100, -1, 101, 1.5, NaN]) it("wire weight " + weight + " accepts only integer 0 through100 without a sum100 condition", () => {
  const value = { ...defaults(), weightingEnabled: true, fixed: { excluded: false, weight } };
  if (Number.isInteger(weight) && weight >= 0 && weight <= 100) expect(Schema.decodeUnknownSync(BadgeRules)(value)).toEqual(value);
  else expect(() => Schema.decodeUnknownSync(BadgeRules)(value)).toThrow();
});
it.effect("legacy default remains uniform and an unrelated filter does not manufacture badge rules", () => Effect.gen(function*() {
  const h = yield* setup(); try {
    expect(h.workspace.read().editor?.filters.weightingEnabled.raw).toBe(false);
    for (const spec of badgeEditorFields) { expect(h.workspace.read().editor?.filters[spec.weight].raw).toBe("100"); expect(h.workspace.read().editor?.filters[spec.excluded].raw).toBe(false); }
    h.workspace.input({ _tag: "FilterToggleChanged", field: "excludeAuthor", raw: false }); yield* h.workspace.commitField({ _tag: "filterToggle", field: "excludeAuthor" });
    expect(h.port.state.edits[0]?.filters?.badgeRules).toBeUndefined(); expect(h.port.state.edits[0]?.filters?.excludeAuthor).toBe(false);
  } finally { yield* h.close; }
}));
for (const spec of badgeEditorFields) it.effect("exclusion " + spec.category + " commits with weighting disabled and synchronizes anonymous legacy flag", () => Effect.gen(function*() {
  const h = yield* setup({ ...draft().filters, badgeRules: defaults() }); try {
    h.workspace.input({ _tag: "FilterToggleChanged", field: spec.excluded, raw: true }); yield* h.workspace.commitField({ _tag: "filterToggle", field: spec.excluded });
    const submitted = h.port.state.edits[0]?.filters;
    expect(submitted?.badgeRules?.weightingEnabled).toBe(false); expect(submitted?.badgeRules?.[spec.rule]).toEqual({ excluded: true, weight: 100 }); expect(submitted?.excludeAnonymous).toBe(spec.category === "anonymous");
    expect(h.workspace.read().editor?.filters[spec.excluded].dirty).toBe(false);
    yield* h.workspace.commitField({ _tag: "filterToggle", field: spec.excluded }); expect(h.port.state.edits).toHaveLength(1);
  } finally { yield* h.close; }
}));
for (const raw of ["0", "100", "", "-1", "101", "1.5", "text"]) it.effect("enabled raw ratio " + JSON.stringify(raw) + " either commits exact value or preserves invalid raw without admission", () => Effect.gen(function*() {
  const h = yield* setup(); try {
    h.workspace.input({ _tag: "FilterToggleChanged", field: "weightingEnabled", raw: true }); h.workspace.input({ _tag: "FilterTextChanged", field: "fixedWeight", raw });
    const result = yield* Effect.result(h.workspace.commitField({ _tag: "filterText", field: "fixedWeight" })); const valid = raw === "0" || raw === "100";
    expect(result._tag).toBe(valid ? "Success" : "Failure"); expect(h.workspace.read().editor?.filters.fixedWeight.raw).toBe(raw); expect(h.port.state.edits).toHaveLength(valid ? 1 : 0);
    if (valid) expect(h.port.state.edits[0]?.filters?.badgeRules?.fixed.weight).toBe(Number(raw)); else { expect(h.workspace.read().editor?.filters.fixedWeight.dirty).toBe(true); expect(h.workspace.read().editor?.filters.fixedWeight.validation).toEqual({ _tag: "invalid", messageKey: "InvalidInput" }); }
  } finally { yield* h.close; }
}));
for (const inactive of ["disabled", "excluded"] as const) it.effect("invalid inactive weight " + inactive + " preserves raw while transmitting the last safe number", () => Effect.gen(function*() {
  const rules = { ...defaults(), weightingEnabled: true, fixed: { excluded: false, weight: 70 } }; const h = yield* setup({ ...draft().filters, badgeRules: rules }); try {
    h.workspace.input({ _tag: "FilterTextChanged", field: "fixedWeight", raw: "bad" }); h.workspace.input({ _tag: "FilterToggleChanged", field: inactive === "disabled" ? "weightingEnabled" : "excludeFixed", raw: inactive === "excluded" });
    expect((yield* Effect.result(h.workspace.commitField({ _tag: "filterText", field: "fixedWeight" })))._tag).toBe("Success");
    expect(h.port.state.edits[0]?.filters?.badgeRules?.fixed.weight).toBe(70); expect(h.workspace.read().editor?.filters.fixedWeight).toMatchObject({ raw: "bad", dirty: true, submitted: null });
    h.workspace.input({ _tag: "FilterToggleChanged", field: inactive === "disabled" ? "weightingEnabled" : "excludeFixed", raw: inactive === "disabled" });
    expect((yield* Effect.result(h.workspace.commitField({ _tag: "filterText", field: "fixedWeight" })))._tag).toBe("Failure"); expect(h.port.state.edits).toHaveLength(1);
  } finally { yield* h.close; }
}));
it.effect("reset clears all category raw and receipts back to legacy uniform defaults", () => Effect.gen(function*() {
  const h = yield* setup(); try {
    h.workspace.input({ _tag: "FilterToggleChanged", field: "weightingEnabled", raw: true }); for (const spec of badgeEditorFields) { h.workspace.input({ _tag: "FilterToggleChanged", field: spec.excluded, raw: true }); h.workspace.input({ _tag: "FilterTextChanged", field: spec.weight, raw: "0" }); }
    yield* h.workspace.commitField({ _tag: "filterToggle", field: "weightingEnabled" }); yield* h.workspace.resetFilters;
    expect(h.workspace.read().editor?.filters.weightingEnabled.raw).toBe(false); for (const spec of badgeEditorFields) { expect(h.workspace.read().editor?.filters[spec.excluded].raw).toBe(false); expect(h.workspace.read().editor?.filters[spec.weight]).toMatchObject({ raw: "100", dirty: false, submitted: null }); }
  } finally { yield* h.close; }
}));
it.effect("all six ratio inputs serialize as independent relative weights with no sum100 rule", () => Effect.gen(function*() {
  const h = yield* setup(); try {
    h.workspace.input({ _tag: "FilterToggleChanged", field: "weightingEnabled", raw: true });
    for (const [index, spec] of badgeEditorFields.entries()) h.workspace.input({ _tag: "FilterTextChanged", field: spec.weight, raw: String(index + 1) });
    yield* h.workspace.commitField({ _tag: "filterToggle", field: "weightingEnabled" });
    const rules = h.port.state.edits[0]?.filters?.badgeRules;
    expect(rules?.weightingEnabled).toBe(true); for (const [index, spec] of badgeEditorFields.entries()) { expect(rules?.[spec.rule].weight).toBe(index + 1); expect(h.workspace.read().editor?.filters[spec.weight].dirty).toBe(false); }
    expect(h.port.state.edits).toHaveLength(1);
  } finally { yield* h.close; }
}));
it.effect("a failed admitted category version retries its receipt without creating another mutation", () => Effect.gen(function*() {
  const h = yield* setup(); try {
    h.port.state.failCommit = 1; h.workspace.input({ _tag: "FilterToggleChanged", field: "excludeFixed", raw: true });
    expect((yield* Effect.result(h.workspace.commitField({ _tag: "filterToggle", field: "excludeFixed" })))._tag).toBe("Failure"); expect(h.workspace.read().editor?.filters.excludeFixed.submitted?.receipt.sequence).toBe(1);
    h.port.state.failCommit = 0; yield* h.workspace.commitField({ _tag: "filterToggle", field: "excludeFixed" }); expect(h.port.state.edits).toHaveLength(1); expect(h.workspace.read().draft?.filters.badgeRules?.fixed.excluded).toBe(true);
  } finally { yield* h.close; }
}));
it.effect("late weight acknowledgement cannot overwrite newer raw; next commit submits the newer version", () => Effect.gen(function*() {
  const h = yield* setup({ ...draft().filters, badgeRules: { ...defaults(), weightingEnabled: true } }); const started = yield* Deferred.make<void>(), release = yield* Deferred.make<void>(); const original = h.port.commands.commitThrough;
  Object.assign(h.port.commands, { commitThrough: (receipt: Parameters<typeof original>[0]) => receipt.sequence === 1 ? Deferred.succeed(started, undefined).pipe(Effect.andThen(Deferred.await(release)), Effect.andThen(original(receipt))) : original(receipt) });
  try { h.workspace.input({ _tag: "FilterTextChanged", field: "fixedWeight", raw: "70" }); const first = yield* h.workspace.commitField({ _tag: "filterText", field: "fixedWeight" }).pipe(Effect.forkScoped); yield* Deferred.await(started);
    h.workspace.input({ _tag: "FilterTextChanged", field: "fixedWeight", raw: "30" }); yield* Deferred.succeed(release, undefined); yield* Fiber.join(first); expect(h.workspace.read().editor?.filters.fixedWeight).toMatchObject({ raw: "30", dirty: true });
    yield* h.workspace.commitField({ _tag: "filterText", field: "fixedWeight" }); expect(h.port.state.edits.map(value => value.filters?.badgeRules?.fixed.weight)).toEqual([70, 30]); expect(h.workspace.read().editor?.filters.fixedWeight.dirty).toBe(false);
  } finally { yield* h.close; }
}));
it("readonly policy explains relative group redistribution and disabled legacy uniform", () => {
  const rules = { ...defaults(), weightingEnabled: true, fixed: { excluded: true, weight: 70 }, semiFixed: { excluded: false, weight: 30 } };
  expect(badgePolicyText({ ...draft().filters, badgeRules: rules })).toContain("분류 제외: 고닉"); expect(badgePolicyText({ ...draft().filters, badgeRules: rules })).toContain("고닉 70 / 비고닉 30"); expect(badgePolicyText({ ...draft().filters, badgeRules: rules })).toContain("후보가 남은 분류끼리 다시 배분");
  expect(badgePolicyText({ ...draft().filters, badgeRules: defaults() })).toBe("개인별 균등 추첨");
});
for (const failing of [1, 2, -1]) it.effect("first Nth continuous category admission failure " + failing + " retains raw and cannot finalize", () => Effect.gen(function*() {
  const h = yield* setup(); const original = h.port.commands.edit; let calls = 0;
  Object.assign(h.port.commands, { edit: (...args: Parameters<typeof original>) => ++calls === failing || failing === -1 ? Effect.fail(new ProtocolError()) : original(...args) });
  try {
    for (let index = 1; index <= (failing === 2 ? 2 : 1); index++) {
      h.workspace.input({ _tag: "FilterToggleChanged", field: "excludeFixed", raw: index === 1 });
      const result = yield* Effect.result(h.workspace.commitField({ _tag: "filterToggle", field: "excludeFixed" })); const failed = index === failing || failing === -1;
      expect(result._tag).toBe(failed ? "Failure" : "Success"); expect(h.workspace.read().editor?.filters.excludeFixed.dirty).toBe(failed);
      if (failed) expect(h.workspace.read().editor?.filters.excludeFixed.validation).toEqual({ _tag: "invalid", messageKey: "SubmissionRejected" });
    }
    expect(h.port.state.creates).toHaveLength(0); expect(h.workspace.read().editor?.lock).toBeNull();
  } finally { yield* h.close; }
}));
it.effect("Scope close interrupts an admitted weight confirmation and cannot acknowledge a late completion", () => Effect.gen(function*() {
  const h = yield* setup({ ...draft().filters, badgeRules: { ...defaults(), weightingEnabled: true } }); const started = yield* Deferred.make<void>(), release = yield* Deferred.make<void>(); const original = h.port.commands.commitThrough;
  Object.assign(h.port.commands, { commitThrough: (receipt: Parameters<typeof original>[0]) => Deferred.succeed(started, undefined).pipe(Effect.andThen(Deferred.await(release)), Effect.andThen(original(receipt))) });
  try { h.workspace.input({ _tag: "FilterTextChanged", field: "fixedWeight", raw: "70" }); const running = yield* h.workspace.commitField({ _tag: "filterText", field: "fixedWeight" }).pipe(Effect.forkScoped); yield* Deferred.await(started); yield* Scope.close(h.scope, Exit.void);
    expect((yield* Fiber.await(running))._tag).toBe("Failure"); expect(h.workspace.read().closed).toBe(true); expect(h.workspace.read().editor).toBeNull(); const closed = h.workspace.read(); yield* Deferred.succeed(release, undefined); expect(h.workspace.read()).toBe(closed); expect(h.port.state.edits).toHaveLength(1);
  } finally { yield* h.close; }
}));
