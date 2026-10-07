import { describe, expect, it } from "vitest";
import { acknowledgeEdit, applyEditorInput, beginEditorLock, confirmEditorReset, createEditor, editorFieldNeedsAdmission,
  followSupersededReceipt, getEditorField, isEditorFieldLocked, projectEditor, recollectEditor, recordEditReceipt, rejectEditorLock, releaseEditor,
  retainEditor, syncCleanEditorValues, validateEditorField,
  type CreateEditorModel, type EditorIdentity, type EditorInput, type EditorLock, type EditorValues, type FieldTarget, type InputFence } from "../../src/screens/create/editorModel";

const identity = { backendSessionId: "session-a", draftId: "draft-a" } as const;
function values(): EditorValues {
  return { url: "https://gall.dcinside.com/board/view/?id=test&no=1", drawMode: "immediate", prizeMode: "single",
    filters: { excludeAnonymous: false, excludeAuthor: false, excludeDcconOnly: false, timeCutEnabled: false, timeCut: "", includeKeywords: "", excludeKeywords: "" },
    single: { id: "single", name: "", count: "1" }, multiple: [{ id: "p-a", name: "동일", count: "1" }, { id: "p-b", name: "동일", count: "1" }],
  };
}
const singleCount: FieldTarget = { _tag: "singleCount" };
function fence(model: CreateEditorModel, target: FieldTarget = singleCount): InputFence {
  return { identity: { ...model.identity }, editorEpoch: model.editorEpoch, inputVersion: getEditorField(model, target).inputVersion };
}
function validated(model: CreateEditorModel, target: FieldTarget = singleCount) { return validateEditorField(model, target, fence(model, target), { _tag: "valid" }); }
function input(raw = "2") { return validated(applyEditorInput(createEditor(identity, values()), { _tag: "SingleCountChanged", raw })); }
function admitted(raw = "2") {
  const model = input(raw);
  return recordEditReceipt(model, singleCount, fence(model), { intentId: "intent-1", draftId: identity.draftId, articleGeneration: 1, sequence: 1 });
}

describe("draft editor ownership", () => {
  it("owns copies of nonsecret raw values and stable prize identities", () => {
    const seed = { ...values(), password: "do not retain", pageCache: [1], node: {} };
    const model = createEditor(identity, seed);
    expect(Object.keys(model)).toEqual(["identity", "editorEpoch", "url", "drawMode", "prizeMode", "filters", "single", "multiple", "lock"]);
    expect(JSON.stringify(model)).not.toContain("do not retain");
    expect(model.multiple.map((prize) => prize.id)).toEqual(["p-a", "p-b"]);
    expect(model.multiple.map((prize) => prize.name.raw)).toEqual(["동일", "동일"]);
    expect(Object.isFrozen(model)).toBe(true);
    expect(Object.isFrozen(model.single.count)).toBe(true);
    expect(Object.isFrozen(model.multiple)).toBe(true);
  });

  it.each(["", "-", "0", "11", "1.5", "abc", "999999999999999999999999999999999"])("preserves invalid numeric raw %j rather than coercing it", (raw) => {
    const before = createEditor(identity, values());
    const after = applyEditorInput(before, { _tag: "SingleCountChanged", raw });
    expect(after.single.count).toMatchObject({ raw, dirty: true, inputVersion: 1, validation: { _tag: "unchecked" } });
    expect(before.single.count.raw).toBe("1");
  });

  it("mode switching keeps both inactive and active raw versions", () => {
    let model = applyEditorInput(createEditor(identity, values()), { _tag: "SingleCountChanged", raw: "-" });
    model = applyEditorInput(model, { _tag: "MultipleCountChanged", prizeId: "p-a", raw: "7" });
    model = applyEditorInput(model, { _tag: "PrizeModeChanged", raw: "multiple" });
    expect(projectEditor(model).activePrizes.map((prize) => prize.count)).toEqual(["7", "1"]);
    model = applyEditorInput(model, { _tag: "PrizeModeChanged", raw: "single" });
    expect(projectEditor(model).activePrizes.map((prize) => prize.count)).toEqual(["-"]);
    expect(model.multiple[0]?.count).toMatchObject({ raw: "7", inputVersion: 1, dirty: true });
  });

  it.each<readonly [EditorInput, FieldTarget, string | boolean]>([
    [{ _tag: "UrlChanged", raw: "raw url" }, { _tag: "url" }, "raw url"],
    [{ _tag: "DrawModeChanged", raw: "reservation" }, { _tag: "drawMode" }, "reservation"],
    [{ _tag: "PrizeModeChanged", raw: "multiple" }, { _tag: "prizeMode" }, "multiple"],
    [{ _tag: "SingleNameChanged", raw: "가" }, { _tag: "singleName" }, "가"],
    [{ _tag: "SingleCountChanged", raw: "3" }, singleCount, "3"],
    [{ _tag: "FilterToggleChanged", field: "excludeAnonymous", raw: true }, { _tag: "filterToggle", field: "excludeAnonymous" }, true],
    [{ _tag: "FilterTextChanged", field: "includeKeywords", raw: "a,b" }, { _tag: "filterText", field: "includeKeywords" }, "a,b"],
    [{ _tag: "MultipleNameChanged", prizeId: "p-b", raw: "나" }, { _tag: "multipleName", prizeId: "p-b" }, "나"],
    [{ _tag: "MultipleCountChanged", prizeId: "p-b", raw: "4" }, { _tag: "multipleCount", prizeId: "p-b" }, "4"],
  ])("concrete %j input changes only its owned field", (action, target, raw) => {
    const before = createEditor(identity, values());
    const after = applyEditorInput(before, action);
    expect(getEditorField(after, target)).toMatchObject({ raw, inputVersion: 1, dirty: true });
    expect(getEditorField(before, target).inputVersion).toBe(0);
  });

  it("uses the same user value as a new explicit version without automatic deduplication", () => {
    const model = applyEditorInput(input("2"), { _tag: "SingleCountChanged", raw: "2" });
    expect(model.single.count.inputVersion).toBe(2);
    expect(model.single.count.validation._tag).toBe("unchecked");
  });

  it.each(["session", "draft", "epoch", "version"])("late validation with changed %s preserves current input", (changed) => {
    const model = input();
    const old = fence(model);
    const wrong = changed === "session" ? { ...old, identity: { ...identity, backendSessionId: "old" } }
      : changed === "draft" ? { ...old, identity: { ...identity, draftId: "old" } }
      : changed === "epoch" ? { ...old, editorEpoch: 1 } : { ...old, inputVersion: 0 };
    expect(validateEditorField(model, singleCount, wrong, { _tag: "invalid", messageKey: "InvalidInput" })).toBe(model);
  });

  it("admits each valid dirty input version once and retains only its receipt reference", () => {
    const model = admitted();
    expect(editorFieldNeedsAdmission(model, singleCount)).toBe(false);
    expect(model.single.count.submitted).toEqual({ editorEpoch: 0, inputVersion: 1, receipt: { intentId: "intent-1", draftId: "draft-a", articleGeneration: 1, sequence: 1 } });
    expect(() => recordEditReceipt(model, singleCount, fence(model), model.single.count.submitted!.receipt)).toThrow("already admitted");
    expect(editorFieldNeedsAdmission(input(), singleCount)).toBe(true);
    expect(editorFieldNeedsAdmission(createEditor(identity, values()), singleCount)).toBe(false);
    expect(editorFieldNeedsAdmission(applyEditorInput(createEditor(identity, values()), { _tag: "SingleCountChanged", raw: "-" }), singleCount)).toBe(false);
  });

  it("confirmed ack clears only the matching dirty input without replacing raw", () => {
    const model = admitted("02");
    const after = acknowledgeEdit(model, singleCount, fence(model), 1, { _tag: "confirmed" });
    expect(after.single.count.raw).toBe("02");
    expect(after.single.count.dirty).toBe(false);
    expect(model.single.count.dirty).toBe(true);
    expect(after.single.count.submitted).toEqual(model.single.count.submitted);
  });

  it.each(["confirmed", "rejected", "expired"] as const)("old %s ack cannot overwrite newer raw", (_tag) => {
    const before = admitted();
    const newer = applyEditorInput(before, { _tag: "SingleCountChanged", raw: "-" });
    expect(acknowledgeEdit(newer, singleCount, fence(before), 1, { _tag })).toBe(newer);
    expect(newer.single.count).toMatchObject({ raw: "-", dirty: true, validation: { _tag: "unchecked" } });
  });

  it.each(["rejected", "expired"] as const)("%s retains raw/dirty and prevents automatic re-admission", (_tag) => {
    const model = admitted();
    const after = acknowledgeEdit(model, singleCount, fence(model), 1, { _tag });
    expect(after.single.count).toMatchObject({ raw: "2", dirty: true, validation: { _tag: "invalid", messageKey: _tag === "expired" ? "ReceiptExpired" : "SubmissionRejected" } });
    expect(editorFieldNeedsAdmission(validated(after), singleCount)).toBe(false);
  });

  it("follows superseded receipt instead of treating the original as confirmed", () => {
    const model = admitted();
    const after = followSupersededReceipt(model, singleCount, fence(model), 1, { intentId: "intent-2", draftId: "draft-a", articleGeneration: 1, sequence: 2 });
    expect(after.single.count.submitted?.receipt.sequence).toBe(2);
    expect(acknowledgeEdit(after, singleCount, fence(after), 1, { _tag: "confirmed" })).toBe(after);
    expect(acknowledgeEdit(after, singleCount, fence(after), 2, { _tag: "confirmed" }).single.count.dirty).toBe(false);
    expect(editorFieldNeedsAdmission(after, singleCount)).toBe(false);
  });

  it("unsubmitted or different receipt ack is ignored", () => {
    const model = input();
    expect(acknowledgeEdit(model, singleCount, fence(model), 1, { _tag: "confirmed" })).toBe(model);
    const pending = admitted();
    expect(acknowledgeEdit(pending, singleCount, fence(pending), 2, { _tag: "confirmed" })).toBe(pending);
  });

  it("ack after recollect cannot clear previous epoch dirty state", () => {
    const before = admitted();
    const after = recollectEditor(before);
    expect(after.editorEpoch).toBe(1);
    expect(after.single.count).toMatchObject({ raw: "2", dirty: true, inputVersion: 1, validation: { _tag: "unchecked" } });
    expect(acknowledgeEdit(after, singleCount, fence(before), 1, { _tag: "confirmed" })).toBe(after);
    expect(editorFieldNeedsAdmission(validated(after), singleCount)).toBe(false);
  });

  it("reset admission locks affected fields, retains raw on failure and unknown", () => {
    const before = applyEditorInput(input(), { _tag: "FilterTextChanged", field: "includeKeywords", raw: "보존" });
    const locked = beginEditorLock(before, { kind: "article", referenceId: "reset-1" });
    expect(applyEditorInput(locked, { _tag: "UrlChanged", raw: "replace" })).toBe(locked);
    expect(applyEditorInput(locked, { _tag: "FilterToggleChanged", field: "excludeAuthor", raw: true })).toBe(locked);
    expect(applyEditorInput(locked, { _tag: "SingleCountChanged", raw: "3" }).single.count.raw).toBe("3");
    expect(confirmEditorReset(locked, "wrong")).toBe(locked);
    expect(rejectEditorLock(locked, "wrong")).toBe(locked);
    const rejected = rejectEditorLock(locked, "reset-1");
    expect(rejected.lock).toBeNull();
    expect(rejected.editorEpoch).toBe(0);
    expect(rejected.filters.includeKeywords.raw).toBe("보존");
    expect(locked.lock).toEqual({ kind: "article", referenceId: "reset-1" });
  });

  it.each(["article", "filters", "manual"] as const)("confirmed %s reset follows the preservation table", (kind) => {
    const seed = values();
    const before = createEditor(identity, { ...seed, drawMode: "reservation", prizeMode: "multiple", filters: { ...seed.filters, excludeAuthor: true, timeCut: "12:-", includeKeywords: "keep", excludeKeywords: "exclude" } });
    const locked = beginEditorLock(before, { kind, referenceId: "reset" });
    const after = confirmEditorReset(locked, "reset");
    expect(after.editorEpoch).toBe(1);
    expect(after.lock).toBeNull();
    expect(after.url.raw).toBe(kind === "article" ? "" : seed.url);
    expect(after.filters.excludeAuthor.raw).toBe(kind === "manual");
    expect(after.filters.includeKeywords.raw).toBe(kind === "manual" ? "keep" : "");
    expect(after.drawMode).toBe(before.drawMode);
    expect(after.prizeMode).toBe(before.prizeMode);
    expect(after.single).toEqual(before.single);
    expect(after.multiple).toEqual(before.multiple);
  });

  it.each<readonly [EditorLock["kind"], FieldTarget, boolean]>([
    ["article", { _tag: "url" }, true], ["article", singleCount, false],
    ["filters", { _tag: "url" }, false], ["filters", { _tag: "filterText", field: "timeCut" }, true],
    ["filters", { _tag: "filterToggle", field: "excludeAuthor" }, true], ["manual", singleCount, false], ["finalization", singleCount, true],
  ])("%s lock owns %j only where specified", (kind, target, expected) => {
    expect(isEditorFieldLocked(beginEditorLock(input(), { kind, referenceId: "ref" }), target)).toBe(expected);
  });

  it("finalization unknown retains lock and confirmed admission releases editor", () => {
    const locked = beginEditorLock(admitted(), { kind: "finalization", referenceId: "op-create" });
    expect(editorFieldNeedsAdmission(locked, singleCount)).toBe(false);
    expect(() => confirmEditorReset(locked, "op-create")).toThrow("released");
    expect(releaseEditor()).toBeNull();
  });

  it("confirmed re-query updates clean fields and preserves dirty raw", () => {
    const model = applyEditorInput(createEditor(identity, values()), { _tag: "SingleCountChanged", raw: "-" });
    const seed = values();
    const after = syncCleanEditorValues(model, { ...seed, url: "new confirmed URL", filters: { ...seed.filters, excludeAuthor: true }, single: { ...seed.single, count: "1", name: "approved" }, multiple: seed.multiple.map((prize) => ({ ...prize, count: "3" })) });
    expect(after.url.raw).toBe("new confirmed URL");
    expect(after.single.count.raw).toBe("-");
    expect(after.single.name.raw).toBe("approved");
    expect(after.filters.excludeAuthor.raw).toBe(true);
    expect(after.multiple.map((prize) => prize.count.raw)).toEqual(["3", "3"]);
    const locked = beginEditorLock(model, { kind: "article", referenceId: "reset" });
    expect(syncCleanEditorValues(locked, { ...seed, url: "changed" }).url.raw).toBe(model.url.raw);
  });

  it("same session draft retains valid and invalid raw for 100 route visits", () => {
    const original = applyEditorInput(input(), { _tag: "MultipleCountChanged", prizeId: "p-b", raw: "-" });
    let retained: CreateEditorModel | null = original;
    for (let n = 0; n < 100; n++) retained = retainEditor(retained, { ...identity });
    expect(retained).toBe(original);
    expect(retained?.multiple[1]?.count.raw).toBe("-");
    expect(retained?.single.count.raw).toBe("2");
  });

  it.each<EditorIdentity | null>([null, { backendSessionId: "new", draftId: "draft-a" }, { backendSessionId: "session-a", draftId: "new" }])("identity change %j discards old editor without creating replay", (nextIdentity) => {
    expect(retainEditor(admitted(), nextIdentity)).toBeNull();
    expect(retainEditor(null, nextIdentity)).toBeNull();
    expect(releaseEditor()).toBeNull();
  });

  it("projection is small and immutable and cannot change original model", () => {
    const model = input();
    const before = JSON.stringify(model);
    const projected = projectEditor(model);
    expect(() => Object.assign(projected.activePrizes[0]!, { count: "99" })).toThrow();
    expect(JSON.stringify(model)).toBe(before);
    expect(Object.keys(projected)).toEqual(["identity", "editorEpoch", "url", "drawMode", "prizeMode", "locked", "activePrizes"]);
  });
});

describe("editor production invariants", () => {
  it.each(["session", "draft"])("empty %s identity fails loudly", (empty) => {
    expect(() => createEditor({ ...identity, [empty === "session" ? "backendSessionId" : "draftId"]: "" }, values())).toThrow("identity");
  });
  it.each([0, 11])("%i multiple prizes fails", (count) => {
    expect(() => createEditor(identity, { ...values(), multiple: Array.from({ length: count }, (_, n) => ({ id: `p-${n}`, name: "", count: "1" })) })).toThrow("one to ten");
  });
  it("one and ten prize rows and duplicate names are valid", () => {
    for (const count of [1, 10]) expect(createEditor(identity, { ...values(), multiple: Array.from({ length: count }, (_, n) => ({ id: `p-${n}`, name: "same", count: "1" })) }).multiple).toHaveLength(count);
  });
  it("duplicate and empty prize IDs fail", () => {
    expect(() => createEditor(identity, { ...values(), multiple: [values().multiple[0]!, values().multiple[0]!] })).toThrow("unique");
    expect(() => createEditor(identity, { ...values(), single: { id: "", name: "", count: "1" } })).toThrow("identity");
    expect(() => getEditorField(input(), { _tag: "multipleCount", prizeId: "missing" })).toThrow("Unknown prize");
  });
  it.each([-1, Number.MAX_SAFE_INTEGER, Number.NaN, Infinity])("counter %j cannot advance", (inputVersion) => {
    const model = createEditor(identity, values());
    expect(() => applyEditorInput({ ...model, single: { ...model.single, count: { ...model.single.count, inputVersion } } }, { _tag: "SingleCountChanged", raw: "2" })).toThrow("counter");
  });
  it("last safe input version advances exactly to max", () => {
    const model = createEditor(identity, values());
    expect(applyEditorInput({ ...model, single: { ...model.single, count: { ...model.single.count, inputVersion: Number.MAX_SAFE_INTEGER - 1 } } }, { _tag: "SingleCountChanged", raw: "2" }).single.count.inputVersion).toBe(Number.MAX_SAFE_INTEGER);
  });
  it("epoch max cannot reset or recollect", () => {
    const model = { ...input(), editorEpoch: Number.MAX_SAFE_INTEGER };
    expect(() => recollectEditor(model)).toThrow("counter");
    expect(() => confirmEditorReset(beginEditorLock(model, { kind: "filters", referenceId: "reset" }), "reset")).toThrow("counter");
  });
  it.each([0, -1, 1.5, Number.MAX_SAFE_INTEGER + 1])("receipt sequence %j fails without state mutation", (sequence) => {
    const model = input();
    expect(() => recordEditReceipt(model, singleCount, fence(model), { intentId: "intent", draftId: "draft-a", articleGeneration: 1, sequence })).toThrow("valid dirty");
    expect(model.single.count.submitted).toBeNull();
  });
  it("clean/invalid/different draft/empty intent receipts cannot be admitted", () => {
    const receipt = { intentId: "intent", draftId: "draft-a", articleGeneration: 1, sequence: 1 };
    const clean = createEditor(identity, values());
    expect(() => recordEditReceipt(clean, singleCount, fence(clean), receipt)).toThrow();
    const invalid = applyEditorInput(clean, { _tag: "SingleCountChanged", raw: "-" });
    expect(() => recordEditReceipt(invalid, singleCount, fence(invalid), receipt)).toThrow();
    const validModel = input();
    expect(() => recordEditReceipt(validModel, singleCount, fence(validModel), { ...receipt, draftId: "other" })).toThrow();
    expect(() => recordEditReceipt(validModel, singleCount, fence(validModel), { ...receipt, intentId: "" })).toThrow();
    expect(recordEditReceipt(validModel, singleCount, { ...fence(validModel), editorEpoch: 1 }, receipt)).toBe(validModel);
  });
  it("empty/double lock and changed confirmed prize identities fail", () => {
    const model = input();
    expect(() => beginEditorLock(model, { kind: "filters", referenceId: "" })).toThrow();
    expect(() => beginEditorLock(beginEditorLock(model, { kind: "filters", referenceId: "one" }), { kind: "manual", referenceId: "two" })).toThrow();
    expect(() => syncCleanEditorValues(model, { ...values(), single: { ...values().single, id: "other" } })).toThrow("stable");
    expect(() => syncCleanEditorValues(model, { ...values(), multiple: [] })).toThrow("stable");
    expect(() => syncCleanEditorValues(model, { ...values(), multiple: [...values().multiple].reverse() })).toThrow("stable");
  });
  it.each(["drawMode", "prizeMode"])("unknown %s is a loud production invariant", (mode) => {
    expect(() => createEditor(identity, { ...values(), [mode]: "unknown" } as unknown as EditorValues)).toThrow("mode");
  });
  it("unknown input, field and lock discriminants fail loudly", () => {
    const model = input();
    expect(() => applyEditorInput(model, { _tag: "unknown" } as never)).toThrow("Unknown editor input");
    expect(() => getEditorField(model, { _tag: "unknown" } as never)).toThrow("Unknown editor field");
    expect(() => isEditorFieldLocked({ ...model, lock: { kind: "unknown", referenceId: "lock" } as never }, singleCount)).toThrow("Unknown editor lock");
  });
  it("all concrete field metadata updates preserve each raw type", () => {
    const model = input();
    const targets: ReadonlyArray<FieldTarget> = [{ _tag: "url" }, { _tag: "drawMode" }, { _tag: "prizeMode" }, { _tag: "singleName" }, singleCount,
      { _tag: "filterToggle", field: "excludeAuthor" }, { _tag: "filterText", field: "timeCut" }, { _tag: "multipleName", prizeId: "p-a" }, { _tag: "multipleCount", prizeId: "p-a" }];
    for (const target of targets) {
      const updated = validateEditorField(model, target, fence(model, target), { _tag: "invalid", messageKey: "InvalidInput" });
      expect(getEditorField(updated, target).raw).toBe(getEditorField(model, target).raw);
      expect(getEditorField(updated, target).validation).toEqual({ _tag: "invalid", messageKey: "InvalidInput" });
    }
  });
  it("mutable discriminant getters cannot bypass second production invariant checks", () => {
    const model = input();
    let inputReads = 0;
    const unstableInput = { get _tag() { return inputReads++ === 0 ? "SingleCountChanged" : "unknown"; }, raw: "3" } as unknown as EditorInput;
    expect(() => applyEditorInput(model, unstableInput)).toThrow("Unknown editor input");
    let targetReads = 0;
    const unstableTarget = { get _tag() { return targetReads++ === 0 ? "singleCount" : "unknown"; } } as FieldTarget;
    expect(() => validateEditorField(model, unstableTarget, fence(model), { _tag: "valid" })).toThrow("Unknown editor field");
  });
  it("submitted metadata epoch and version mismatches do not clear dirty", () => {
    const model = admitted();
    const original = model.single.count.submitted!;
    for (const altered of [{ ...original, editorEpoch: 1 }, { ...original, inputVersion: 0 }]) {
      const wrong = { ...model, single: { ...model.single, count: { ...model.single.count, submitted: altered } } };
      expect(acknowledgeEdit(wrong, singleCount, fence(model), 1, { _tag: "confirmed" })).toBe(wrong);
    }
  });
  it("superseded handling respects fence, missing receipt and source sequence", () => {
    const model = admitted();
    const replacement = { intentId: "next", draftId: "draft-a", articleGeneration: 1, sequence: 2 };
    expect(followSupersededReceipt(model, singleCount, { ...fence(model), editorEpoch: 1 }, 1, replacement)).toBe(model);
    const unsent = input();
    expect(followSupersededReceipt(unsent, singleCount, fence(unsent), 1, replacement)).toBe(unsent);
    expect(followSupersededReceipt(model, singleCount, fence(model), 2, replacement)).toBe(model);
  });
  it.each([{ draftId: "other" }, { articleGeneration: 2 }, { sequence: 1 }, { sequence: 1.5 }, { intentId: "" }])("replacement receipt %j is rejected independently", (changed) => {
    const model = admitted();
    expect(() => followSupersededReceipt(model, singleCount, fence(model), 1, { intentId: "next", draftId: "draft-a", articleGeneration: 1, sequence: 2, ...changed })).toThrow("later replacement");
  });
  it.each([-1, 1.5, Number.MAX_SAFE_INTEGER + 1])("receipt article generation %j is rejected", (articleGeneration) => {
    const model = input();
    expect(() => recordEditReceipt(model, singleCount, fence(model), { intentId: "next", draftId: "draft-a", articleGeneration, sequence: 1 })).toThrow("valid dirty");
  });
  it("generation zero and maximum receipt sequence are valid references", () => {
    const model = input();
    expect(recordEditReceipt(model, singleCount, fence(model), { intentId: "next", draftId: "draft-a", articleGeneration: 0, sequence: Number.MAX_SAFE_INTEGER }).single.count.submitted?.receipt.sequence).toBe(Number.MAX_SAFE_INTEGER);
  });
  it("mutating an input array between validation and copy is a loud invariant", () => {
    const seed = values();
    let reads = 0;
    const unstable = new Proxy([...seed.multiple], { get(target, key, receiver) {
      if (key === "0" && ++reads > 1) return undefined;
      return Reflect.get(target, key, receiver);
    } });
    expect(() => syncCleanEditorValues(input(), { ...seed, multiple: unstable })).toThrow("missing after validation");
  });
});
