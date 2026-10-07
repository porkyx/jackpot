import { Cause, Clock, Data, Effect, Fiber, Option, PubSub, Scope, Semaphore, Stream } from "effect";
import { BackendRejected, ProtocolError, TransportError } from "../../contracts/backend";
import type { Collection, Draft, Filters, Prizes } from "../../contracts/product";
import { projectError } from "../../app/errors";
import type { EditReceipt } from "../../operations/coordinatorState";
import { ProductUnavailable, type DraftIntent, type ProductCommands, type ProductError } from "../../operations/productCoordinator";
import { ClientIds } from "../../platform/ids";
import { acknowledgeEdit, applyEditorInput, beginEditorLock, confirmEditorReset, createEditor, getEditorField, recordEditReceipt, rejectEditorLock,
  syncCleanEditorValues, validateEditorField, badgeEditorFields, filterToggleFields, filterTextFields, type CreateEditorModel, type EditorInput, type EditorValues, type FieldTarget, type InputFence } from "./editorModel";

export class CreateInputError extends Data.TaggedError("CreateInputError")<{ readonly field: string; readonly message: string }> {}
export type CreateError = ProductError | CreateInputError;
export interface CreateWorkspaceState {
  readonly draft: Draft | null;
  readonly editor: CreateEditorModel | null;
  readonly version: number;
  readonly error: string | null;
  readonly keywordRaw: { readonly include: string; readonly exclude: string };
  readonly timeRaw: { readonly days: string; readonly hour: string; readonly minute: string };
  readonly closed: boolean;
  readonly writesBlocked: boolean;
  readonly structuralPending: boolean;
}
export interface PreparedCreation { readonly identity: { readonly backendSessionId: string; readonly draftId: string }; readonly epoch: number; readonly version: number; readonly afterSequence: number; readonly mode: "immediate" | "reservation"; readonly reference: string }
export interface CreateWorkspace {
  readonly read: () => CreateWorkspaceState;
  readonly setWritesBlocked: (blocked: boolean) => void;
  readonly snapshot: Effect.Effect<CreateWorkspaceState>;
  readonly changes: Stream.Stream<CreateWorkspaceState>;
  readonly dispatch: (effect: Effect.Effect<void, never>) => void;
  readonly report: (cause: Cause.Cause<unknown>) => Effect.Effect<void>;
  readonly input: (input: EditorInput) => void;
  readonly setKeywordRaw: (kind: "include" | "exclude", raw: string) => void;
  readonly addKeyword: (kind: "include" | "exclude") => Effect.Effect<void, CreateError>;
  readonly removeKeyword: (kind: "include" | "exclude", keyword: string) => Effect.Effect<void, CreateError>;
  readonly timeInput: (field: "days" | "hour" | "minute", raw: string) => void;
  readonly ensure: Effect.Effect<void, CreateError>;
  readonly commitField: (target: FieldTarget, fence?: InputFence) => Effect.Effect<void, CreateError>;
  readonly flush: Effect.Effect<PreparedCreation, CreateError>;
  readonly load: Effect.Effect<void, CreateError>;
  readonly cancelLoad: Effect.Effect<void, CreateError>;
  readonly resetArticle: Effect.Effect<void, CreateError>;
  readonly resetFilters: Effect.Effect<void, CreateError>;
  readonly resetManual: Effect.Effect<void, CreateError>;
  readonly toggleParticipant: (id: string) => Effect.Effect<void, CreateError>;
  readonly setUnclassified: (included: boolean) => Effect.Effect<void, CreateError>;
  readonly addPrize: Effect.Effect<void, CreateError>;
  readonly removePrize: (id: string) => Effect.Effect<void, CreateError>;
  readonly prepareCreation: Effect.Effect<PreparedCreation, CreateError>;
  readonly cancelCreation: (prepared: PreparedCreation) => Effect.Effect<void>;
  readonly finalize: (prepared: PreparedCreation) => Effect.Effect<Collection, CreateError>;
}
const intent = (kind: DraftIntent["kind"], changes: Partial<DraftIntent> = {}): DraftIntent => ({ kind, filters: null, prizes: null, participantId: "", included: null, ...changes });
const error = (field: string, message: string) => new CreateInputError({ field, message });
const filterTargets: ReadonlyArray<FieldTarget> = [
  ...filterToggleFields.map((field) => ({ _tag: "filterToggle" as const, field })),
  ...filterTextFields.map((field) => ({ _tag: "filterText" as const, field })),
];
function values(draft: Draft): EditorValues { return { url: draft.article?.url ?? "", drawMode: draft.prizes.drawMode, prizeMode: draft.prizes.mode,
  filters: { ...draft.filters, timeCutEnabled: draft.filters.timeCut !== null, timeCut: draft.filters.timeCut ?? "", includeKeywords: draft.filters.includeKeywords.join("\n"), excludeKeywords: draft.filters.excludeKeywords.join("\n"),
    excludeAnonymous: draft.filters.excludeAnonymous || draft.filters.badgeRules?.anonymous.excluded === true,
    weightingEnabled: draft.filters.badgeRules?.weightingEnabled ?? false,
    excludeFixed: draft.filters.badgeRules?.fixed.excluded ?? false, excludeSemiFixed: draft.filters.badgeRules?.semiFixed.excluded ?? false,
    excludeMainManager: draft.filters.badgeRules?.mainManager.excluded ?? false, excludeSubManager: draft.filters.badgeRules?.subManager.excluded ?? false, excludeNewAccount: draft.filters.badgeRules?.newAccount.excluded ?? false,
    fixedWeight: String(draft.filters.badgeRules?.fixed.weight ?? 100), semiFixedWeight: String(draft.filters.badgeRules?.semiFixed.weight ?? 100), mainManagerWeight: String(draft.filters.badgeRules?.mainManager.weight ?? 100),
    subManagerWeight: String(draft.filters.badgeRules?.subManager.weight ?? 100), newAccountWeight: String(draft.filters.badgeRules?.newAccount.weight ?? 100), anonymousWeight: String(draft.filters.badgeRules?.anonymous.weight ?? 100) },
  single: { ...draft.prizes.single, count: String(draft.prizes.single.count) }, multiple: draft.prizes.multiple.map((item) => ({ ...item, count: String(item.count) })),
}; }
function targets(editor: CreateEditorModel): ReadonlyArray<FieldTarget> { return [ ...filterTargets, { _tag: "drawMode" }, { _tag: "prizeMode" }, { _tag: "singleName" }, { _tag: "singleCount" },
  ...editor.multiple.flatMap((item) => [{ _tag: "multipleName" as const, prizeId: item.id }, { _tag: "multipleCount" as const, prizeId: item.id }]) ]; }
function keywords(raw: string): ReadonlyArray<string> {
  const result = [...new Set(raw.split("\n").map((word) => word.trim()).filter((word) => word.length > 0))];
  if (result.length > 100 || result.some((word) => word.length > 100)) throw error("keywords", "단어는 종류별 100개, 각 100자까지 등록할 수 있습니다.");
  return result;
}
function count(raw: string): number { if (!/^\d+$/.test(raw) || Number(raw) < 1 || Number(raw) > 10) throw error("prizes", "당첨 인원은 1명부터 10명까지 입력해 주세요."); return Number(raw); }
function key(target: FieldTarget): string { return target._tag === "filterToggle" || target._tag === "filterText" ? `filters:${target.field}` : target._tag === "multipleName" || target._tag === "multipleCount" ? `prizes:${target.prizeId}:${target._tag}` : target._tag; }

export function makeCreateWorkspace(commands: ProductCommands, dispatch: CreateWorkspace["dispatch"]): Effect.Effect<CreateWorkspace, never, Scope.Scope | ClientIds> {
  return Effect.gen(function*() {
    const owner = yield* Scope.fork(yield* Scope.Scope, "sequential");
    const clock = yield* Clock.Clock; const ids = yield* ClientIds; const lock = yield* Semaphore.make(1);
    const events = yield* PubSub.unbounded<CreateWorkspaceState>({ replay: 1 });
    let state: CreateWorkspaceState = { draft: null, editor: null, version: 0, error: null, keywordRaw: { include: "", exclude: "" }, timeRaw: { days: "0", hour: "0", minute: "0" }, closed: false, writesBlocked: false, structuralPending: false };
    let latest: EditReceipt | null = null;
    let cutoff: { readonly epoch: number; readonly version: number; readonly value: string } | null = null;
    const publish = (next: CreateWorkspaceState) => { if (state.closed) return; state = Object.freeze(next); PubSub.publishUnsafe(events, state); };
    PubSub.publishUnsafe(events, state);
    yield* Scope.addFinalizer(owner, Effect.suspend(() => { state = { ...state, draft: null, editor: null, error: null, keywordRaw: { include: "", exclude: "" }, closed: true, structuralPending: false }; latest = null; cutoff = null; return PubSub.shutdown(events); }));
    const guard = Effect.suspend(() => state.closed ? Effect.fail(new ProductUnavailable({ reason: "closed" })) : Effect.void);
    const owned = <A, E>(action: Effect.Effect<A, E>) => guard.pipe(Effect.andThen(action.pipe(Effect.forkIn(owner), Effect.flatMap(Fiber.join))));
    let structuralCount=0;
    const structural=<A,E>(action:Effect.Effect<A,E>)=>owned(Effect.acquireUseRelease(
      Effect.sync(()=>{structuralCount++;publish({...state,structuralPending:true});}),
      ()=>action,
      ()=>Effect.sync(()=>{if(structuralCount<1)throw new Error("Structural edit ownership underflow");structuralCount--;publish({...state,structuralPending:structuralCount>0});}),
    ));
    const accept = (draft: Draft | null) => {
      if (state.closed) return;
      if (draft === null) { latest = null; cutoff = null; publish({ ...state, draft: null, editor: null, version: state.version + 1, keywordRaw: { include: "", exclude: "" } }); return; }
      const identity = { backendSessionId: draft.summary.backendSessionId, draftId: draft.summary.draftId };
      const previous = state.editor;
      const replaced = previous === null || previous.identity.backendSessionId !== identity.backendSessionId || previous.identity.draftId !== identity.draftId;
      if (!replaced && state.draft !== null && draft.summary.articleGeneration === state.draft.summary.articleGeneration && draft.summary.revision < state.draft.summary.revision) return;
      if (replaced) {
        let timeRaw = { days: "0", hour: "0", minute: "0" };
        if (draft.filters.timeCut !== null) {
          const before = new Date(Date.parse(draft.filters.timeCut) - 60000 + 9 * 3600000);
          const now = new Date(clock.currentTimeMillisUnsafe() + 9 * 3600000);
          const days = Math.max(0, Math.floor((Date.UTC(now.getUTCFullYear(),now.getUTCMonth(),now.getUTCDate())-Date.UTC(before.getUTCFullYear(),before.getUTCMonth(),before.getUTCDate()))/86400000));
          timeRaw = { days: String(days), hour: String(before.getUTCHours()), minute: String(before.getUTCMinutes()) };
        }
        state = { ...state, keywordRaw: { include: "", exclude: "" }, timeRaw, version: state.version + 1, error: null };
      }
      if (!replaced && state.draft?.summary.articleGeneration !== draft.summary.articleGeneration) { latest = null; cutoff = null; state = { ...state, version: state.version + 1 }; }
      let editor: CreateEditorModel | null;
      if (draft.summary.state === "finalized") editor = null;
      else if (previous === null || previous.identity.backendSessionId !== identity.backendSessionId || previous.identity.draftId !== identity.draftId) { editor = createEditor(identity, values(draft)); latest = null; cutoff = null; }
      else {
        const confirmed = values(draft);
        const sameRows = previous.single.id === confirmed.single.id && previous.multiple.length === confirmed.multiple.length && previous.multiple.every((item, index) => item.id === confirmed.multiple[index]?.id);
        editor = sameRows ? syncCleanEditorValues(previous, confirmed) : previous;
        if (state.draft?.summary.articleGeneration !== draft.summary.articleGeneration) {
          const clear = <A>(field: import("./editorModel").InputField<A>): import("./editorModel").InputField<A> => Object.freeze({ ...field, submitted: null, validation: field.validation._tag === "invalid" && (field.validation.messageKey === "SubmissionRejected" || field.validation.messageKey === "ReceiptExpired") ? { _tag: "unchecked" as const } : field.validation });
          editor = Object.freeze({ ...editor, editorEpoch: editor.editorEpoch + 1, url: clear(editor.url), drawMode: clear(editor.drawMode), prizeMode: clear(editor.prizeMode),
            filters: Object.fromEntries(Object.entries(editor.filters).map(([name, value]) => [name, clear<string | boolean>(value)])) as unknown as import("./editorModel").FilterInputs,
            single: { ...editor.single,name:clear(editor.single.name),count:clear(editor.single.count) },multiple:editor.multiple.map((item)=>({ ...item,name:clear(item.name),count:clear(item.count) })) });
        }
        if (editor.lock?.referenceId === "load" && draft.load?.state !== "loading" && draft.load?.state !== "cancelling") {
          editor = rejectEditorLock(editor, "load");
          if (draft.load?.state === "completed") editor = Object.freeze({ ...editor, url: Object.freeze({ ...editor.url, raw: draft.article?.url ?? editor.url.raw, dirty: false }) });
        }
      }
      publish({ ...state, draft, editor });
    };
    const report: CreateWorkspace["report"] = (cause) => Effect.sync(() => {
      if (Cause.hasInterruptsOnly(cause) || state.closed) return;
      const found = Cause.findErrorOption(cause);
      let message = "문제가 발생했습니다. 다시 확인해 주세요.";
      if (Option.isSome(found)) {
        const value = found.value;
        if (value instanceof CreateInputError) message = value.message;
        else if (value instanceof ProductUnavailable) message = value.reason === "outcome_unknown" ? "처리 결과가 아직 확인되지 않았습니다. 새 추첨을 만들지 않고 기존 작업을 확인합니다." : "편집 상태가 변경되었거나 아직 처리 중입니다. 상태를 다시 확인해 주세요.";
        else if (value instanceof BackendRejected || value instanceof ProtocolError || value instanceof TransportError) message = projectError(value).message;
      }
      publish({ ...state, error: message });
    });
    const input: CreateWorkspace["input"] = (action) => {
      if (state.closed || state.editor === null) return;
      const editor = applyEditorInput(state.editor, action);
      if (editor !== state.editor) publish({ ...state, editor, version: state.version + 1, error: null });
    };
    const need = () => { if (state.editor === null || state.draft === null) throw error("draft", "게시글을 먼저 불러와 주세요."); return { editor: state.editor, draft: state.draft }; };
    const fenceFor = (editor: CreateEditorModel, target: FieldTarget): InputFence => ({ identity: editor.identity, editorEpoch: editor.editorEpoch, inputVersion: getEditorField(editor, target).inputVersion });
    const parseFilters = (editor: CreateEditorModel): Effect.Effect<Filters, CreateInputError> => Effect.gen(function*() {
      let timeCut: string | null = null;
      if (editor.filters.timeCutEnabled.raw) {
        const raw = editor.filters.timeCut.raw;
        if (/^\d{4}-/.test(raw)) { if (!Number.isFinite(Date.parse(raw))) return yield* Effect.fail(error("timeCut", "시간컷을 확인해 주세요.")); timeCut = raw; }
        else {
          const parts = raw.split(",");
          if (parts.length !== 3 || parts.some((part) => !/^\d+$/.test(part)) || Number(parts[0]) > 365 || Number(parts[1]) > 23 || Number(parts[2]) > 59) return yield* Effect.fail(error("timeCut", "시간컷은 0~365일 전, 0~23시, 0~59분으로 입력해 주세요."));
          if (cutoff?.epoch === editor.editorEpoch && cutoff.version === editor.filters.timeCut.inputVersion) timeCut = cutoff.value;
          else {
            const now = yield* clock.currentTimeMillis; const kst = new Date(now + 9 * 3600000);
            const value = new Date(Date.UTC(kst.getUTCFullYear(), kst.getUTCMonth(), kst.getUTCDate() - Number(parts[0]), Number(parts[1]), Number(parts[2]) + 1) - 9 * 3600000).toISOString();
            cutoff = { epoch: editor.editorEpoch, version: editor.filters.timeCut.inputVersion, value }; timeCut = value;
          }
        }
      }
      return yield* Effect.try({ try: () => {
        const prior = state.draft?.filters.badgeRules;
        const includeRules = prior != null || editor.filters.weightingEnabled.dirty || badgeEditorFields.some((value) => editor.filters[value.weight].dirty || (value.category !== "anonymous" && editor.filters[value.excluded].dirty));
        const rule = (value: typeof badgeEditorFields[number]) => {
          const excluded = editor.filters[value.excluded].raw, raw = editor.filters[value.weight].raw;
          const valid = /^\d+$/.test(raw) && Number(raw) <= 100;
          if (!valid && editor.filters.weightingEnabled.raw && !excluded) throw error("filters", "비율은 0부터 100까지의 정수로 입력해 주세요.");
          return { excluded, weight: valid ? Number(raw) : prior?.[value.rule].weight ?? 100 };
        };
        const badgeRules = includeRules ? { weightingEnabled: editor.filters.weightingEnabled.raw,
          fixed: rule(badgeEditorFields[0]), semiFixed: rule(badgeEditorFields[1]), mainManager: rule(badgeEditorFields[2]), subManager: rule(badgeEditorFields[3]), newAccount: rule(badgeEditorFields[4]), anonymous: rule(badgeEditorFields[5]) } : undefined;
        return { excludeAnonymous: editor.filters.excludeAnonymous.raw, excludeAuthor: editor.filters.excludeAuthor.raw, excludeDcconOnly: editor.filters.excludeDcconOnly.raw,
          timeCut, includeKeywords: keywords(editor.filters.includeKeywords.raw), excludeKeywords: keywords(editor.filters.excludeKeywords.raw), ...(badgeRules === undefined ? {} : { badgeRules }) };
      }, catch: (cause) => cause instanceof CreateInputError ? cause : error("filters", "필터 입력을 확인해 주세요.") });
    });
    const parsePrizes = (editor: CreateEditorModel, draft: Draft, eligibility: boolean): Prizes => {
      const prize = (item: CreateEditorModel["single"]) => { if (item.name.raw.length > 20) throw error("prizes", "상품명은 20자까지 입력할 수 있습니다."); return { id: item.id, name: item.name.raw, count: count(item.count.raw) }; };
      const active = editor.prizeMode.raw === "single" ? [prize(editor.single)] : editor.multiple.map(prize);
      const total = active.reduce((sum, item) => sum + item.count, 0);
      if (total > 10 || (eligibility && (draft.included < 2 || total > draft.included))) throw error("prizes", "참가자는 2명 이상, 당첨 인원은 포함된 참가자 수와 10명 이하로 설정해 주세요.");
      const safe = (item: CreateEditorModel["single"], fallback: Prizes["single"]) => { try { return prize(item); } catch { return fallback; } };
      return { mode: editor.prizeMode.raw, drawMode: editor.drawMode.raw, single: editor.prizeMode.raw === "single" ? active[0] as Prizes["single"] : safe(editor.single, draft.prizes.single),
        multiple: editor.prizeMode.raw === "multiple" ? active : editor.multiple.map((item) => safe(item, draft.prizes.multiple.find((old) => old.id === item.id) ?? { id: item.id, name: "", count: 1 })) };
    };
    const submit = (group: "filters" | "prizes", requested?: FieldTarget): Effect.Effect<void, CreateError> => Effect.gen(function*() {
      const { editor, draft } = yield* Effect.try({ try: need, catch: (cause) => cause instanceof CreateInputError ? cause : error("draft", "초안 상태를 확인해 주세요.") });

      const fields = (group === "filters" ? filterTargets : targets(editor).filter((target) => !filterTargets.includes(target) && (target._tag !== "singleName" && target._tag !== "singleCount" || editor.prizeMode.raw === "single") && (target._tag !== "multipleName" && target._tag !== "multipleCount" || editor.prizeMode.raw === "multiple"))).filter((target) => {
        if (!getEditorField(editor, target).dirty) return false;
        const spec = target._tag === "filterText" ? badgeEditorFields.find(value => value.weight === target.field) : undefined;
        if (spec === undefined || editor.filters.weightingEnabled.raw && !editor.filters[spec.excluded].raw) return true;
        const raw = editor.filters[spec.weight].raw;
        // An inactive invalid raw value was not sent. Keep its version dirty;
        // a later bootstrap must not acknowledge it as the safe fallback number.
        return /^\d+$/.test(raw) && Number(raw) <= 100;
      });
      const fences = fields.map((target) => ({ target, fence: fenceFor(editor, target) }));
      return yield* Effect.gen(function*() {
      if (editor.lock !== null && (editor.lock.kind === "finalization" || group === "filters")) return yield* Effect.fail(new ProductUnavailable({ reason: "blocked" }));
      const pending = fields.find((target) => { const field = getEditorField(editor,target); return field.submitted?.inputVersion === field.inputVersion; });
      if (pending !== undefined) {
        const admitted = getEditorField(editor,pending).submitted;
        if (admitted === null) return yield* Effect.die(new Error("Admitted editor field has no receipt"));
        const confirmed = yield* commands.commitThrough(admitted.receipt);
        if (state.editor?.identity.backendSessionId !== editor.identity.backendSessionId || state.editor.identity.draftId !== editor.identity.draftId) return yield* Effect.fail(new ProductUnavailable({ reason: "stale" }));
        accept(confirmed);
        for (const target of fields) {
          const original = getEditorField(editor,target);
          if (original.submitted?.receipt.sequence === admitted.receipt.sequence && original.submitted.inputVersion === original.inputVersion && state.editor !== null)
            state = { ...state, editor: acknowledgeEdit(state.editor,target,fenceFor(editor,target),admitted.receipt.sequence,{ _tag: "confirmed" }) };
        }
        publish({ ...state,error:null });
        return yield* submit(group,requested);
      }
      if (fields.length === 0) return;
      const filters = group === "filters" ? yield* parseFilters(editor) : null;
      const prizes = group === "prizes" ? yield* Effect.try({ try: () => parsePrizes(editor, draft, false), catch: (cause) => cause instanceof CreateInputError ? cause : error("prizes", "상품 입력을 확인해 주세요.") }) : null;
      for (const { target, fence } of fences) if (state.editor !== null) state = { ...state, editor: validateEditorField(state.editor, target, fence, { _tag: "valid" }) };
      const receipt = yield* commands.edit(intent(group === "filters" ? "UpdateFilters" : "SetPrizes", { filters, prizes }), requested === undefined ? group : key(requested));
      latest = receipt;
      for (const { target, fence } of fences) if (state.editor !== null) state = { ...state, editor: recordEditReceipt(state.editor, target, fence, receipt) };
      const confirmed = yield* commands.commitThrough(receipt);
      accept(confirmed);
      for (const { target, fence } of fences) if (state.editor !== null) state = { ...state, editor: acknowledgeEdit(state.editor, target, fence, receipt.sequence, { _tag: "confirmed" }) };
      publish({ ...state, error: null });
      }).pipe(Effect.onError((cause) => Effect.sync(() => {
        if (group !== "filters" || state.closed || Cause.hasInterruptsOnly(cause) || state.editor === null) return;
        const found = Cause.findErrorOption(cause);
        const messageKey = Option.isSome(found) && found.value instanceof CreateInputError ? "InvalidInput"
          : Option.isSome(found) && found.value instanceof ProductUnavailable && found.value.reason === "receipt_expired" ? "ReceiptExpired" : "SubmissionRejected";
        let current = state.editor;
        // The input fence owns this failure. A late failure cannot invalidate
        // newer raw, a replacement editor, or a new article generation.
        for (const { target, fence } of fences) current = validateEditorField(current, target, fence, { _tag: "invalid", messageKey });
        if (current !== state.editor) publish({ ...state, editor: current });
      })));
    });
    const commitField: CreateWorkspace["commitField"] = (target, fence) => { const action=lock.withPermit(Effect.gen(function*() {
      if (state.editor === null) return yield* Effect.fail(error("draft", "초안을 확인해 주세요."));
      if (fence !== undefined && (fence.identity.draftId !== state.editor.identity.draftId || fence.identity.backendSessionId !== state.editor.identity.backendSessionId || fence.editorEpoch !== state.editor.editorEpoch || fence.inputVersion !== getEditorField(state.editor, target).inputVersion)) return;
      if (target._tag === "url") return;
      yield* submit(target._tag === "filterToggle" || target._tag === "filterText" ? "filters" : "prizes", target);
    }));return target._tag==="prizeMode"?structural(action):owned(action); };
    const flushEdits = Effect.gen(function*() { yield* submit("filters"); yield* submit("prizes"); });
    const flush = lock.withPermit(Effect.gen(function*() {
      const initial = yield* Effect.try({ try: need, catch: () => error("draft", "게시글을 먼저 불러와 주세요.") });
      const version = state.version;
      if (state.keywordRaw.include.trim() !== "" || state.keywordRaw.exclude.trim() !== "") return yield* Effect.fail(error("keywords", "입력한 단어를 추가하거나 입력란을 비워 주세요."));
      if (initial.editor.url.raw.trim() !== initial.draft.article?.url) return yield* Effect.fail(error("url", "입력한 게시글을 먼저 불러와 주세요."));
      yield* parseFilters(initial.editor);
      yield* Effect.try({ try: () => parsePrizes(initial.editor, initial.draft, false), catch: (cause) => cause instanceof CreateInputError ? cause : error("prizes", "상품 입력을 확인해 주세요.") });
      yield* flushEdits; const current = yield* Effect.try({ try: need, catch: () => error("draft", "초안을 확인해 주세요.") });
      yield* Effect.try({ try: () => parsePrizes(current.editor, current.draft, true), catch: (cause) => cause instanceof CreateInputError ? cause : error("prizes", "상품 입력을 확인해 주세요.") });
      return { identity: initial.editor.identity, epoch: initial.editor.editorEpoch, version, afterSequence: latest?.sequence ?? 0, mode: initial.editor.drawMode.raw, reference: `finalize-${version}` };
    }));
    const command = (request: DraftIntent) => owned(Effect.gen(function*() { const version = state.version; yield* lock.withPermit(submit("filters")); if (state.version !== version) return yield* Effect.fail(new ProductUnavailable({ reason: "changed_before_finalize" })); const receipt = yield* commands.edit(request); latest = receipt; accept(yield* commands.commitThrough(receipt)); }));
    const ensure = owned(Effect.gen(function*() { if (state.draft === null || state.editor === null) accept(yield* commands.ensureDraft); }));
    const reset = (kind: "ResetFilters" | "ResetArticle") => owned(lock.withPermit(Effect.gen(function*() {
      const { editor } = yield* Effect.try({ try: need, catch: () => error("draft", "초안을 확인해 주세요.") });
      const reference = kind === "ResetArticle" ? "reset-article" : "reset-filters";
      state = { ...state, editor: beginEditorLock(editor, { kind: kind === "ResetArticle" ? "article" : "filters", referenceId: reference }) };
      yield* Effect.gen(function*() {
        let draft: Draft;
        if (kind === "ResetArticle") { draft = yield* commands.resetArticle; latest = null; }
        else { const receipt = yield* commands.edit(intent(kind)); latest = receipt; draft = yield* commands.commitThrough(receipt); }
        accept(draft);
        if (state.editor !== null) publish({ ...state, editor: confirmEditorReset(state.editor, reference), version: state.version + 1, keywordRaw: { include: "", exclude: "" }, error: null });
      }).pipe(Effect.ensuring(Effect.sync(() => {
        if (state.editor !== null) publish({ ...state, editor: rejectEditorLock(state.editor, reference) });
      })));
    })));
    const workspace: CreateWorkspace = {
      read: () => state, setWritesBlocked: (blocked) => { if (!state.closed && state.writesBlocked !== blocked) publish({ ...state, writesBlocked: blocked }); }, snapshot: Effect.suspend(() => Effect.succeed(state)), changes: Stream.fromPubSub(events), dispatch, report, input, ensure, commitField, flush: owned(flush),
      setKeywordRaw: (kind, raw) => { if (!state.closed && state.editor?.lock === null) publish({ ...state, keywordRaw: { ...state.keywordRaw, [kind]: raw }, version: state.version + 1 }); },
      addKeyword: (kind) => owned(Effect.gen(function*() {
        const field = kind === "include" ? "includeKeywords" : "excludeKeywords";
        const { editor } = yield* Effect.try({ try: need, catch: () => error("draft", "초안을 확인해 주세요.") });
        const raw = state.keywordRaw[kind].trim(); if (raw === "") return;
        const next = yield* Effect.try({ try: () => keywords(`${editor.filters[field].raw}\n${raw}`).join("\n"), catch: (cause) => cause instanceof CreateInputError ? cause : error("keywords", "단어 입력을 확인해 주세요.") });
        input({ _tag: "FilterTextChanged", field, raw: next }); publish({ ...state, keywordRaw: { ...state.keywordRaw, [kind]: "" } }); yield* commitField({ _tag: "filterText", field });
      })),
      removeKeyword: (kind, keyword) => owned(Effect.gen(function*() { const field = kind === "include" ? "includeKeywords" : "excludeKeywords"; const { editor } = yield* Effect.try({ try: need, catch: () => error("draft", "초안을 확인해 주세요.") }); input({ _tag: "FilterTextChanged", field, raw: keywords(editor.filters[field].raw).filter((word) => word !== keyword).join("\n") }); yield* commitField({ _tag: "filterText", field }); })),
      timeInput: (field, raw) => { if (state.closed) return; const timeRaw = { ...state.timeRaw, [field]: raw }; publish({ ...state, timeRaw }); input({ _tag: "FilterTextChanged", field: "timeCut", raw: `${timeRaw.days},${timeRaw.hour},${timeRaw.minute}` }); },
      load: owned(Effect.gen(function*() { yield* ensure; const { editor } = yield* Effect.try({ try: need, catch: () => error("url", "게시글 주소를 입력해 주세요.") }); if (editor.url.raw.trim() === "") return yield* Effect.fail(error("url", "게시글 주소를 입력해 주세요.")); publish({ ...state, editor: beginEditorLock(editor, { kind: "article", referenceId: "load" }), error: null }); yield* commands.loadArticle(editor.url.raw).pipe(Effect.flatMap((draft) => Effect.sync(() => accept(draft))), Effect.onError(() => Effect.sync(() => { if (state.editor !== null) publish({ ...state, editor: rejectEditorLock(state.editor, "load") }); }))); })),
      cancelLoad: owned(commands.cancelLoad.pipe(Effect.tap((draft) => Effect.sync(() => accept(draft))), Effect.asVoid)),
      resetArticle: reset("ResetArticle"), resetFilters: reset("ResetFilters"), resetManual: command(intent("ResetManual")),
      toggleParticipant: (id) => command(intent("ToggleParticipant", { participantId: id })), setUnclassified: (included) => command(intent("SetAllUnclassified", { included })),
      addPrize: structural(lock.withPermit(Effect.gen(function*() {
        const captured = yield* Effect.try({ try: need, catch: () => error("prizes", "초안을 확인해 주세요.") });
        if (captured.editor.lock !== null) return yield* Effect.fail(new ProductUnavailable({ reason: "blocked" }));
        const id = yield* ids.operationId.pipe(Effect.mapError(() => error("prizes", "상품 식별자를 만들지 못했습니다.")));
        const { editor, draft } = yield* Effect.try({ try: need, catch: () => error("prizes", "초안을 확인해 주세요.") });
        if (editor.identity.backendSessionId !== captured.editor.identity.backendSessionId || editor.identity.draftId !== captured.editor.identity.draftId || editor.editorEpoch !== captured.editor.editorEpoch) return yield* Effect.fail(new ProductUnavailable({ reason: "stale" }));
        if (editor.lock !== null) return yield* Effect.fail(new ProductUnavailable({ reason: "blocked" }));
        if (editor.multiple.reduce((sum, item) => sum + (Number(item.count.raw) || 0), 0) >= 10 || editor.multiple.length >= 10) return yield* Effect.fail(error("prizes", "상품은 최대 10개까지 추가할 수 있습니다."));
        const fresh = createEditor(editor.identity, { ...values(draft), multiple: [...editor.multiple.map((item) => ({ id: item.id, name: item.name.raw, count: item.count.raw })), { id, name: "", count: "1" }] });
        publish({ ...state, editor: Object.freeze({ ...editor, multiple: Object.freeze([...editor.multiple, fresh.multiple[fresh.multiple.length - 1] as CreateEditorModel["single"]]) }) });
        input({ _tag: "MultipleCountChanged", prizeId: id, raw: "1" }); yield* submit("prizes", { _tag: "multipleCount", prizeId: id });
      }))),
      removePrize: (id) => structural(lock.withPermit(Effect.gen(function*() {
        const {editor}=yield*Effect.try({try:need,catch:()=>error("prizes","초안을 확인해 주세요.")});
        if(editor.lock!==null)return yield*Effect.fail(new ProductUnavailable({reason:"blocked"}));
        if(editor.multiple.length<=1)return yield*Effect.fail(error("prizes","마지막 상품은 삭제할 수 없습니다."));
        const multiple=editor.multiple.filter(item=>item.id!==id);if(multiple.length===editor.multiple.length)return yield*Effect.fail(error("prizes","삭제할 상품을 확인해 주세요."));
        publish({...state,editor:Object.freeze({...editor,multiple:Object.freeze(multiple)})});input({_tag:"PrizeModeChanged",raw:editor.prizeMode.raw});yield*submit("prizes",{_tag:"prizeMode"});
      }))),
      prepareCreation: owned(Effect.gen(function*() { const prepared = yield* flush; if (state.version !== prepared.version || state.editor === null) return yield* Effect.fail(new ProductUnavailable({ reason: "changed_before_finalize" })); publish({ ...state, editor: beginEditorLock(state.editor, { kind: "finalization", referenceId: prepared.reference }), error: null }); return prepared; })),
      cancelCreation: (prepared) => Effect.sync(() => { if (state.editor !== null) publish({ ...state, editor: rejectEditorLock(state.editor, prepared.reference) }); }),
      finalize: (prepared) => owned(Effect.gen(function*() { const current = state.editor; if (current === null || current.lock?.referenceId !== prepared.reference || current.editorEpoch !== prepared.epoch || current.identity.backendSessionId !== prepared.identity.backendSessionId || current.identity.draftId !== prepared.identity.draftId || state.version !== prepared.version) return yield* Effect.fail(new ProductUnavailable({ reason: "changed_before_finalize" })); const collection = yield* commands.createCollection(prepared.afterSequence, prepared.mode).pipe(Effect.onError((cause) => { const found = Cause.findErrorOption(cause); return Option.isSome(found) && found.value instanceof ProductUnavailable && found.value.reason === "outcome_unknown" ? Effect.void : workspace.cancelCreation(prepared); })); publish({ ...state, editor: null, keywordRaw: { include: "", exclude: "" }, error: null }); return collection; })),
    };
    accept(yield* commands.draft);
    yield* commands.changes.pipe(Stream.runForEach((draft) => Effect.sync(() => accept(draft))), Effect.catchCause(report), Effect.forkIn(owner));
    return workspace;
  });
}
