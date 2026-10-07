import type { EditReceipt } from "../../operations/coordinatorState";

export interface EditorIdentity { readonly backendSessionId: string; readonly draftId: string }
export type EditorValidation = { readonly _tag: "unchecked" } | { readonly _tag: "valid" } | {
  readonly _tag: "invalid"; readonly messageKey: "InvalidInput" | "TooManyWinners" | "SubmissionRejected" | "ReceiptExpired";
};
export interface SubmittedField {
  readonly editorEpoch: number;
  readonly inputVersion: number;
  readonly receipt: EditReceipt;
}
export interface InputField<A> {
  readonly raw: A;
  readonly inputVersion: number;
  readonly dirty: boolean;
  readonly validation: EditorValidation;
  readonly submitted: SubmittedField | null;
}
export type DrawMode = "immediate" | "reservation";
export type PrizeMode = "single" | "multiple";
export interface PrizeInput {
  readonly id: string;
  readonly name: InputField<string>;
  readonly count: InputField<string>;
}
export interface FilterInputs {
  readonly excludeAnonymous: InputField<boolean>;
  readonly excludeAuthor: InputField<boolean>;
  readonly excludeDcconOnly: InputField<boolean>;
  readonly timeCutEnabled: InputField<boolean>;
  readonly timeCut: InputField<string>;
  readonly includeKeywords: InputField<string>;
  readonly excludeKeywords: InputField<string>;
  readonly weightingEnabled: InputField<boolean>;
  readonly excludeFixed: InputField<boolean>; readonly excludeSemiFixed: InputField<boolean>;
  readonly excludeMainManager: InputField<boolean>; readonly excludeSubManager: InputField<boolean>; readonly excludeNewAccount: InputField<boolean>;
  readonly fixedWeight: InputField<string>; readonly semiFixedWeight: InputField<string>; readonly mainManagerWeight: InputField<string>;
  readonly subManagerWeight: InputField<string>; readonly newAccountWeight: InputField<string>; readonly anonymousWeight: InputField<string>;
}
export type EditorLock = { readonly kind: "article" | "filters" | "manual" | "finalization"; readonly referenceId: string };
export interface CreateEditorModel {
  readonly identity: EditorIdentity;
  readonly editorEpoch: number;
  readonly url: InputField<string>;
  readonly drawMode: InputField<DrawMode>;
  readonly prizeMode: InputField<PrizeMode>;
  readonly filters: FilterInputs;
  readonly single: PrizeInput;
  readonly multiple: ReadonlyArray<PrizeInput>;
  readonly lock: EditorLock | null;
}
export interface EditorValues {
  readonly url: string;
  readonly drawMode: DrawMode;
  readonly prizeMode: PrizeMode;
  readonly filters: {
    readonly excludeAnonymous: boolean; readonly excludeAuthor: boolean; readonly excludeDcconOnly: boolean;
    readonly timeCutEnabled: boolean; readonly timeCut: string; readonly includeKeywords: string; readonly excludeKeywords: string;
    readonly weightingEnabled?: boolean; readonly excludeFixed?: boolean; readonly excludeSemiFixed?: boolean;
    readonly excludeMainManager?: boolean; readonly excludeSubManager?: boolean; readonly excludeNewAccount?: boolean;
    readonly fixedWeight?: string; readonly semiFixedWeight?: string; readonly mainManagerWeight?: string;
    readonly subManagerWeight?: string; readonly newAccountWeight?: string; readonly anonymousWeight?: string;
  };
  readonly single: { readonly id: string; readonly name: string; readonly count: string };
  readonly multiple: ReadonlyArray<{ readonly id: string; readonly name: string; readonly count: string }>;
}
export const filterToggleFields = ["excludeAnonymous", "excludeAuthor", "excludeDcconOnly", "timeCutEnabled", "weightingEnabled", "excludeFixed", "excludeSemiFixed", "excludeMainManager", "excludeSubManager", "excludeNewAccount"] as const;
export const filterTextFields = ["timeCut", "includeKeywords", "excludeKeywords", "fixedWeight", "semiFixedWeight", "mainManagerWeight", "subManagerWeight", "newAccountWeight", "anonymousWeight"] as const;
export type FilterToggle = typeof filterToggleFields[number];
export type FilterText = typeof filterTextFields[number];
export const badgeEditorFields = [
  { category: "fixed", rule: "fixed", excluded: "excludeFixed", weight: "fixedWeight" },
  { category: "semi_fixed", rule: "semiFixed", excluded: "excludeSemiFixed", weight: "semiFixedWeight" },
  { category: "main_manager", rule: "mainManager", excluded: "excludeMainManager", weight: "mainManagerWeight" },
  { category: "sub_manager", rule: "subManager", excluded: "excludeSubManager", weight: "subManagerWeight" },
  { category: "new_account", rule: "newAccount", excluded: "excludeNewAccount", weight: "newAccountWeight" },
  { category: "anonymous", rule: "anonymous", excluded: "excludeAnonymous", weight: "anonymousWeight" },
] as const;
export type FieldTarget =
  | { readonly _tag: "url" | "drawMode" | "prizeMode" | "singleName" | "singleCount" }
  | { readonly _tag: "filterToggle"; readonly field: FilterToggle }
  | { readonly _tag: "filterText"; readonly field: FilterText }
  | { readonly _tag: "multipleName" | "multipleCount"; readonly prizeId: string };
export type EditorInput =
  | { readonly _tag: "UrlChanged"; readonly raw: string }
  | { readonly _tag: "DrawModeChanged"; readonly raw: DrawMode }
  | { readonly _tag: "PrizeModeChanged"; readonly raw: PrizeMode }
  | { readonly _tag: "FilterToggleChanged"; readonly field: FilterToggle; readonly raw: boolean }
  | { readonly _tag: "FilterTextChanged"; readonly field: FilterText; readonly raw: string }
  | { readonly _tag: "SingleNameChanged" | "SingleCountChanged"; readonly raw: string }
  | { readonly _tag: "MultipleNameChanged" | "MultipleCountChanged"; readonly prizeId: string; readonly raw: string };
export interface InputFence {
  readonly identity: EditorIdentity;
  readonly editorEpoch: number;
  readonly inputVersion: number;
}
export type EditAcknowledgement = { readonly _tag: "confirmed" } | { readonly _tag: "rejected" } | { readonly _tag: "expired" };

const unchecked: EditorValidation = Object.freeze({ _tag: "unchecked" });
const valid: EditorValidation = Object.freeze({ _tag: "valid" });
function next(value: number): number {
  if (!Number.isSafeInteger(value) || value < 0 || value >= Number.MAX_SAFE_INTEGER) throw new Error("Editor counter cannot advance");
  return value + 1;
}
function field<A>(raw: A, validation: EditorValidation = unchecked): InputField<A> {
  return Object.freeze({ raw, inputVersion: 0, dirty: false, validation, submitted: null });
}
function edit<A>(previous: InputField<A>, raw: A): InputField<A> {
  return Object.freeze({ raw, inputVersion: next(previous.inputVersion), dirty: true, validation: unchecked, submitted: previous.submitted });
}
function ownPrize(value: EditorValues["single"]): PrizeInput {
  if (value.id.length === 0) throw new Error("Prize identity is required");
  return Object.freeze({ id: value.id, name: field(value.name), count: field(value.count) });
}
function ownEditor(model: CreateEditorModel): CreateEditorModel {
  return Object.freeze({ ...model, identity: Object.freeze({ ...model.identity }), filters: Object.freeze({ ...model.filters }),
    single: Object.freeze({ ...model.single }), multiple: Object.freeze(model.multiple.map((prize) => Object.freeze({ ...prize }))),
    lock: model.lock === null ? null : Object.freeze({ ...model.lock }),
  });
}
function initialFilters(values: EditorValues["filters"]): FilterInputs {
  return Object.freeze({ excludeAnonymous: field(values.excludeAnonymous, valid), excludeAuthor: field(values.excludeAuthor, valid),
    excludeDcconOnly: field(values.excludeDcconOnly, valid), timeCutEnabled: field(values.timeCutEnabled, valid),
    timeCut: field(values.timeCut), includeKeywords: field(values.includeKeywords), excludeKeywords: field(values.excludeKeywords),
    weightingEnabled: field(values.weightingEnabled ?? false, valid), excludeFixed: field(values.excludeFixed ?? false, valid),
    excludeSemiFixed: field(values.excludeSemiFixed ?? false, valid), excludeMainManager: field(values.excludeMainManager ?? false, valid),
    excludeSubManager: field(values.excludeSubManager ?? false, valid), excludeNewAccount: field(values.excludeNewAccount ?? false, valid),
    fixedWeight: field(values.fixedWeight ?? "100"), semiFixedWeight: field(values.semiFixedWeight ?? "100"), mainManagerWeight: field(values.mainManagerWeight ?? "100"),
    subManagerWeight: field(values.subManagerWeight ?? "100"), newAccountWeight: field(values.newAccountWeight ?? "100"), anonymousWeight: field(values.anonymousWeight ?? "100"),
  });
}
export function createEditor(identity: EditorIdentity, values: EditorValues): CreateEditorModel {
  if (identity.backendSessionId.length === 0 || identity.draftId.length === 0) throw new Error("Editor identity is required");
  if (!["immediate", "reservation"].includes(values.drawMode) || !["single", "multiple"].includes(values.prizeMode)) throw new Error("Unknown editor mode");
  if (values.multiple.length < 1 || values.multiple.length > 10 || new Set(values.multiple.map((prize) => prize.id)).size !== values.multiple.length) throw new Error("Multiple prizes require one to ten stable unique identities");
  return ownEditor({ identity, editorEpoch: 0, url: field(values.url), drawMode: field(values.drawMode, valid), prizeMode: field(values.prizeMode, valid),
    filters: initialFilters(values.filters),
    single: ownPrize(values.single), multiple: values.multiple.map(ownPrize), lock: null,
  });
}
function isFilter(target: FieldTarget): boolean { return target._tag === "filterToggle" || target._tag === "filterText"; }
export function isEditorFieldLocked(model: CreateEditorModel, target: FieldTarget): boolean {
  if (model.lock === null) return false;
  switch (model.lock.kind) {
    case "finalization": return true;
    case "article": return target._tag === "url" || isFilter(target);
    case "filters": return isFilter(target);
    case "manual": return false;
    default: throw new Error("Unknown editor lock");
  }
}
export function getEditorField(model: CreateEditorModel, target: FieldTarget): InputField<string | boolean> {
  switch (target._tag) {
    case "url": return model.url;
    case "drawMode": return model.drawMode;
    case "prizeMode": return model.prizeMode;
    case "singleName": return model.single.name;
    case "singleCount": return model.single.count;
    case "filterToggle": return model.filters[target.field];
    case "filterText": return model.filters[target.field];
    case "multipleName": case "multipleCount": {
      const prize = model.multiple.find((value) => value.id === target.prizeId);
      if (prize === undefined) throw new Error("Unknown prize identity");
      return target._tag === "multipleName" ? prize.name : prize.count;
    }
    default: throw new Error("Unknown editor field");
  }
}
function changeField(model: CreateEditorModel, target: FieldTarget, change: <A>(value: InputField<A>) => InputField<A>): CreateEditorModel {
  switch (target._tag) {
    case "url": return ownEditor({ ...model, url: change(model.url) });
    case "drawMode": return ownEditor({ ...model, drawMode: change(model.drawMode) });
    case "prizeMode": return ownEditor({ ...model, prizeMode: change(model.prizeMode) });
    case "singleName": return ownEditor({ ...model, single: { ...model.single, name: change(model.single.name) } });
    case "singleCount": return ownEditor({ ...model, single: { ...model.single, count: change(model.single.count) } });
    case "filterToggle": return ownEditor({ ...model, filters: { ...model.filters, [target.field]: change(model.filters[target.field]) } });
    case "filterText": return ownEditor({ ...model, filters: { ...model.filters, [target.field]: change(model.filters[target.field]) } });
    case "multipleName": case "multipleCount": {
      getEditorField(model, target);
      return ownEditor({ ...model, multiple: model.multiple.map((prize) => prize.id !== target.prizeId ? prize : target._tag === "multipleName" ? { ...prize, name: change(prize.name) } : { ...prize, count: change(prize.count) }) });
    }
    default: throw new Error("Unknown editor field");
  }
}
function inputTarget(input: EditorInput): FieldTarget {
  switch (input._tag) {
    case "UrlChanged": return { _tag: "url" };
    case "DrawModeChanged": return { _tag: "drawMode" };
    case "PrizeModeChanged": return { _tag: "prizeMode" };
    case "FilterToggleChanged": return { _tag: "filterToggle", field: input.field };
    case "FilterTextChanged": return { _tag: "filterText", field: input.field };
    case "SingleNameChanged": return { _tag: "singleName" };
    case "SingleCountChanged": return { _tag: "singleCount" };
    case "MultipleNameChanged": return { _tag: "multipleName", prizeId: input.prizeId };
    case "MultipleCountChanged": return { _tag: "multipleCount", prizeId: input.prizeId };
    default: throw new Error("Unknown editor input");
  }
}
export function applyEditorInput(model: CreateEditorModel, input: EditorInput): CreateEditorModel {
  const target = inputTarget(input);
  if (isEditorFieldLocked(model, target)) return model;
  // Target and raw are paired by the concrete EditorInput union. The updater
  // changes only metadata generically; input assignment stays in this switch.
  switch (input._tag) {
    case "UrlChanged": return ownEditor({ ...model, url: edit(model.url, input.raw) });
    case "DrawModeChanged": return ownEditor({ ...model, drawMode: edit(model.drawMode, input.raw) });
    case "PrizeModeChanged": return ownEditor({ ...model, prizeMode: edit(model.prizeMode, input.raw) });
    case "FilterToggleChanged": return ownEditor({ ...model, filters: { ...model.filters, [input.field]: edit(model.filters[input.field], input.raw) } });
    case "FilterTextChanged": return ownEditor({ ...model, filters: { ...model.filters, [input.field]: edit(model.filters[input.field], input.raw) } });
    case "SingleNameChanged": return ownEditor({ ...model, single: { ...model.single, name: edit(model.single.name, input.raw) } });
    case "SingleCountChanged": return ownEditor({ ...model, single: { ...model.single, count: edit(model.single.count, input.raw) } });
    case "MultipleNameChanged": case "MultipleCountChanged": {
      getEditorField(model, target);
      return ownEditor({ ...model, multiple: model.multiple.map((prize) => prize.id !== input.prizeId ? prize : input._tag === "MultipleNameChanged" ? { ...prize, name: edit(prize.name, input.raw) } : { ...prize, count: edit(prize.count, input.raw) }) });
    }
    default: throw new Error("Unknown editor input");
  }
}
function matches(model: CreateEditorModel, target: FieldTarget, fence: InputFence): boolean {
  return model.identity.backendSessionId === fence.identity.backendSessionId && model.identity.draftId === fence.identity.draftId
    && model.editorEpoch === fence.editorEpoch && getEditorField(model, target).inputVersion === fence.inputVersion;
}
export function validateEditorField(model: CreateEditorModel, target: FieldTarget, fence: InputFence, validation: EditorValidation): CreateEditorModel {
  if (!matches(model, target, fence)) return model;
  return changeField(model, target, (value) => Object.freeze({ ...value, validation: Object.freeze({ ...validation }) }));
}
export function recordEditReceipt(model: CreateEditorModel, target: FieldTarget, fence: InputFence, receipt: EditReceipt): CreateEditorModel {
  if (!matches(model, target, fence)) return model;
  const value = getEditorField(model, target);
  if (!value.dirty || value.validation._tag !== "valid" || receipt.draftId !== model.identity.draftId || receipt.intentId.length === 0 || !Number.isSafeInteger(receipt.sequence) || receipt.sequence < 1 || !Number.isSafeInteger(receipt.articleGeneration) || receipt.articleGeneration < 0) throw new Error("Receipt does not bind a valid dirty input");
  if (value.submitted?.inputVersion === value.inputVersion) throw new Error("Input version is already admitted");
  return changeField(model, target, (current) => Object.freeze({ ...current, submitted: Object.freeze({ editorEpoch: model.editorEpoch, inputVersion: current.inputVersion, receipt: Object.freeze({ ...receipt }) }) }));
}
export function acknowledgeEdit(model: CreateEditorModel, target: FieldTarget, fence: InputFence, sequence: number, result: EditAcknowledgement): CreateEditorModel {
  if (!matches(model, target, fence)) return model;
  const submitted = getEditorField(model, target).submitted;
  if (submitted === null || submitted.editorEpoch !== fence.editorEpoch || submitted.inputVersion !== fence.inputVersion || submitted.receipt.sequence !== sequence) return model;
  return changeField(model, target, (value) => result._tag === "confirmed"
    ? Object.freeze({ ...value, dirty: false, validation: valid })
    : Object.freeze({ ...value, validation: Object.freeze({ _tag: "invalid", messageKey: result._tag === "expired" ? "ReceiptExpired" : "SubmissionRejected" }) }));
}
export function followSupersededReceipt(model: CreateEditorModel, target: FieldTarget, fence: InputFence, fromSequence: number, replacement: EditReceipt): CreateEditorModel {
  if (!matches(model, target, fence)) return model;
  const submitted = getEditorField(model, target).submitted;
  if (submitted === null || submitted.receipt.sequence !== fromSequence) return model;
  if (replacement.draftId !== model.identity.draftId || replacement.articleGeneration !== submitted.receipt.articleGeneration || !Number.isSafeInteger(replacement.sequence) || replacement.sequence <= fromSequence || replacement.intentId.length === 0) throw new Error("Superseded receipt requires a later replacement in the same draft context");
  return changeField(model, target, (value) => Object.freeze({ ...value, submitted: Object.freeze({ ...submitted, receipt: Object.freeze({ ...replacement }) }) }));
}
export function editorFieldNeedsAdmission(model: CreateEditorModel, target: FieldTarget): boolean {
  const value = getEditorField(model, target);
  return !isEditorFieldLocked(model, target) && value.dirty && value.validation._tag === "valid" && value.submitted?.inputVersion !== value.inputVersion;
}
export function beginEditorLock(model: CreateEditorModel, lock: EditorLock): CreateEditorModel {
  if (lock.referenceId.length === 0 || model.lock !== null) throw new Error("Editor lock requires a new admitted reference");
  return ownEditor({ ...model, lock });
}
export function rejectEditorLock(model: CreateEditorModel, referenceId: string): CreateEditorModel {
  return model.lock?.referenceId === referenceId ? ownEditor({ ...model, lock: null }) : model;
}
export function confirmEditorReset(model: CreateEditorModel, referenceId: string): CreateEditorModel {
  if (model.lock?.referenceId !== referenceId) return model;
  if (model.lock.kind === "finalization") throw new Error("Finalized editor must be released, not reset");
  const clearFilters = model.lock.kind === "article" || model.lock.kind === "filters";
  return ownEditor({ ...model, editorEpoch: next(model.editorEpoch), lock: null,
    url: model.lock.kind === "article" ? field("") : model.url,
    filters: clearFilters ? initialFilters({ excludeAnonymous: false, excludeAuthor: false, excludeDcconOnly: false,
      timeCutEnabled: false, timeCut: "", includeKeywords: "", excludeKeywords: "" }) : model.filters,
  });
}
export function recollectEditor(model: CreateEditorModel): CreateEditorModel {
  return ownEditor({ ...model, editorEpoch: next(model.editorEpoch),
    single: { ...model.single, count: Object.freeze({ ...model.single.count, validation: unchecked }) },
    multiple: model.multiple.map((prize) => ({ ...prize, count: Object.freeze({ ...prize.count, validation: unchecked }) })),
  });
}
export function syncCleanEditorValues(model: CreateEditorModel, values: EditorValues): CreateEditorModel {
  if (model.single.id !== values.single.id || model.multiple.length !== values.multiple.length || model.multiple.some((prize, index) => prize.id !== values.multiple[index]?.id)) throw new Error("Confirmed input reconciliation requires stable prize identities");
  const clean = <A>(current: InputField<A>, raw: A, target: FieldTarget): InputField<A> => current.dirty || isEditorFieldLocked(model, target)
    ? current : Object.freeze({ ...current, raw, validation: unchecked });
  const confirmedFilters = initialFilters(values.filters), filters = { ...model.filters };
  for (const field of filterToggleFields) filters[field] = clean(model.filters[field], confirmedFilters[field].raw, { _tag: "filterToggle", field });
  for (const field of filterTextFields) filters[field] = clean(model.filters[field], confirmedFilters[field].raw, { _tag: "filterText", field });
  return ownEditor({ ...model, url: clean(model.url, values.url, { _tag: "url" }),
    drawMode: clean(model.drawMode, values.drawMode, { _tag: "drawMode" }), prizeMode: clean(model.prizeMode, values.prizeMode, { _tag: "prizeMode" }),
    filters,
    single: { id: model.single.id, name: clean(model.single.name, values.single.name, { _tag: "singleName" }), count: clean(model.single.count, values.single.count, { _tag: "singleCount" }) },
    multiple: model.multiple.map((prize, index) => {
      const value = values.multiple[index];
      if (value === undefined) throw new Error("Confirmed prize identity missing after validation");
      return { id: prize.id, name: clean(prize.name, value.name, { _tag: "multipleName", prizeId: prize.id }), count: clean(prize.count, value.count, { _tag: "multipleCount", prizeId: prize.id }) };
    }),
  });
}
export function retainEditor(model: CreateEditorModel | null, identity: EditorIdentity | null): CreateEditorModel | null {
  return model !== null && identity !== null && model.identity.backendSessionId === identity.backendSessionId && model.identity.draftId === identity.draftId ? model : null;
}
// The caller drops the sole editor reference on admission, explicit draft
// replacement, backend session change, WebView recreation, or application exit.
export function releaseEditor(): null { return null; }
export function projectEditor(model: CreateEditorModel) {
  const active = model.prizeMode.raw === "single" ? [model.single] : model.multiple;
  return Object.freeze({ identity: Object.freeze({ ...model.identity }), editorEpoch: model.editorEpoch, url: model.url.raw,
    drawMode: model.drawMode.raw, prizeMode: model.prizeMode.raw, locked: model.lock !== null,
    activePrizes: Object.freeze(active.map((prize) => Object.freeze({ id: prize.id, name: prize.name.raw, count: prize.count.raw,
      validation: Object.freeze({ ...prize.count.validation }), dirty: prize.name.dirty || prize.count.dirty }))),
  });
}
