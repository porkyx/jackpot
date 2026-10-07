import { expect, it } from "@effect/vitest";
import { vi } from "vitest";
import { Cause, Deferred, Effect, Exit, Fiber, Scope } from "effect";
import { TestClock } from "effect/testing";
import { bindCreateTextInput, type CreateInputPort } from "../../src/screens/create/view";
import { DomPlatform, makeDomPlatform } from "../../src/platform/dom";
import { applyEditorInput, createEditor, editorFieldNeedsAdmission, getEditorField, recordEditReceipt, validateEditorField,
  type CreateEditorModel, type EditorValues, type InputFence } from "../../src/screens/create/editorModel";

const identity = { backendSessionId: "session", draftId: "draft" };
const values: EditorValues = { url: "", drawMode: "immediate", prizeMode: "single", filters: { excludeAnonymous: false, excludeAuthor: false, excludeDcconOnly: false, timeCutEnabled: false, timeCut: "", includeKeywords: "", excludeKeywords: "" }, single: { id: "single", name: "", count: "1" }, multiple: [{ id: "many", name: "상품", count: "1" }] };
const field = { _tag: "singleCount" } as const;
function fence(editor: CreateEditorModel): InputFence { return { identity: editor.identity, editorEpoch: editor.editorEpoch, inputVersion: getEditorField(editor, field).inputVersion }; }
function editorPort(fail: (attempt: number) => boolean = () => false) {
  let model = createEditor(identity, values);
  const state = { rawWrites: 0, ready: 0, submit: 0, errors: 0 };
  const port: CreateInputPort<Error, never> = {
    readRaw: () => model.single.count.raw,
    writeRaw: (raw) => {
      state.rawWrites++;
      model = applyEditorInput(model, { _tag: "SingleCountChanged", raw });
      model = validateEditorField(model, field, fence(model), /^\d+$/.test(raw) ? { _tag: "valid" } : { _tag: "invalid", messageKey: "InvalidInput" });
    },
    candidate: () => editorFieldNeedsAdmission(model, field) ? fence(model) : null,
    onDebounced: (_fence) => Effect.suspend(() => { state.ready++; return fail(state.ready) ? Effect.fail(new Error("admission-readiness failed")) : Effect.void; }),
    onSubmit: Effect.sync(() => { state.submit++; }),
  };
  return { port, state, model: () => model, admit: () => { model = recordEditReceipt(model, field, fence(model), { intentId: "harness-receipt", draftId: identity.draftId, articleGeneration: 1, sequence: 1 }); } };
}
function harness(port: CreateInputPort<Error, never>) {
  const input = document.createElement("input");
  const work: Array<Effect.Effect<void>> = [];
  let errors = 0;
  const dom = makeDomPlatform({ request: () => { throw new Error("input binding must not request frames"); }, cancel: () => {} }, () => "visible");
  const mount = bindCreateTextInput(input, port, (effect) => { work.push(effect); }, (_cause) => Effect.sync(() => { errors++; })).pipe(Effect.provideService(DomPlatform, dom));
  const flush = Effect.suspend(() => Effect.gen(function*() { while (work.length > 0) for (const effect of work.splice(0)) yield* effect; }));
  return { input, mount, flush, errors: () => errors,
    inputRaw: (raw: string) => { input.value = raw; input.dispatchEvent(new Event("input")); },
    key: (key = "Enter", isComposing = false, keyCode = 13) => input.dispatchEvent(new KeyboardEvent("keydown", { key, isComposing, keyCode, cancelable: true })),
  };
}

it.effect("composition records raw immediately but defers validation readiness and Enter until one final input", () => Effect.scoped(Effect.gen(function*() {
  const editor = editorPort(); const dom = harness(editor.port); const binding = yield* dom.mount;
  const same = dom.input;
  dom.input.dispatchEvent(new CompositionEvent("compositionstart")); dom.inputRaw("ㅎ"); dom.key(); yield* dom.flush;
  yield* TestClock.adjust("1 second"); expect(editor.state.ready).toBe(0); expect(editor.state.submit).toBe(0);
  expect(editor.model().single.count.raw).toBe("ㅎ"); expect(editor.model().single.count.inputVersion).toBe(1);
  dom.input.value = "12"; dom.input.dispatchEvent(new CompositionEvent("compositionend")); dom.input.dispatchEvent(new Event("input"));
  dom.key(); yield* dom.flush;
  expect(editor.state.rawWrites).toBe(2); expect(editor.model().single.count.inputVersion).toBe(2); expect(editor.state.submit).toBe(0);
  yield* TestClock.adjust("149 millis"); expect(editor.state.ready).toBe(0);
  yield* TestClock.adjust("1 millis"); expect(editor.state.ready).toBe(1);
  dom.key(); yield* dom.flush; expect(editor.state.submit).toBe(1); expect(dom.input).toBe(same);
  yield* binding.close; expect(binding.snapshot()).toEqual({ _tag: "closed" });
})));
it.effect("an already observed final composition value is written once and a changed post-composition input is a new version", () => Effect.scoped(Effect.gen(function*() {
  const editor = editorPort(); const dom = harness(editor.port); const binding = yield* dom.mount;
  dom.input.dispatchEvent(new CompositionEvent("compositionstart")); dom.inputRaw("12");
  dom.input.dispatchEvent(new CompositionEvent("compositionend")); dom.inputRaw("12"); dom.inputRaw("13"); yield* dom.flush;
  expect(editor.state.rawWrites).toBe(2); expect(editor.model().single.count.raw).toBe("13"); expect(editor.model().single.count.inputVersion).toBe(2);
  yield* TestClock.adjust("150 millis"); expect(editor.state.ready).toBe(1);
  yield* binding.close;
})));
it.effect("new input and composition cancel prior timer; only the latest validated version becomes ready", () => Effect.scoped(Effect.gen(function*() {
  const editor = editorPort(); const dom = harness(editor.port); const binding = yield* dom.mount;
  dom.inputRaw("2"); yield* dom.flush; yield* TestClock.adjust("100 millis");
  dom.inputRaw("3"); yield* dom.flush; yield* TestClock.adjust("149 millis"); expect(editor.state.ready).toBe(0);
  dom.input.dispatchEvent(new CompositionEvent("compositionstart")); yield* dom.flush; yield* TestClock.adjust("1 second"); expect(editor.state.ready).toBe(0);
  dom.input.dispatchEvent(new CompositionEvent("compositionend")); yield* dom.flush; yield* TestClock.adjust("150 millis"); expect(editor.state.ready).toBe(1);
  yield* binding.close;
})));
it.effect("invalid raw and current admitted version never become debounce admission candidates on reentry", () => Effect.scoped(Effect.gen(function*() {
  const editor = editorPort(); let dom = harness(editor.port); let binding = yield* dom.mount;
  dom.inputRaw("-"); yield* dom.flush; yield* TestClock.adjust("150 millis"); expect(editor.state.ready).toBe(0);
  yield* binding.close; dom = harness(editor.port); binding = yield* dom.mount; yield* dom.flush;
  expect(dom.input.value).toBe("-"); yield* TestClock.adjust("150 millis"); expect(editor.state.ready).toBe(0);
  dom.inputRaw("2"); editor.admit(); yield* dom.flush; yield* TestClock.adjust("150 millis"); expect(editor.state.ready).toBe(0);
  yield* binding.close; dom = harness(editor.port); binding = yield* dom.mount; yield* dom.flush; yield* TestClock.adjust("150 millis");
  expect(editor.state.ready).toBe(0); expect(editor.model().single.count.dirty).toBe(true); yield* binding.close;
})));
it.effect("unadmitted valid dirty value restarts exactly one debounce on reentry", () => Effect.scoped(Effect.gen(function*() {
  const editor = editorPort(); const first = harness(editor.port); const old = yield* first.mount;
  first.inputRaw("7"); yield* first.flush; yield* TestClock.adjust("149 millis"); yield* old.close;
  const next = harness(editor.port); const mounted = yield* next.mount; yield* next.flush;
  expect(next.input.value).toBe("7"); expect(editor.model().single.count.inputVersion).toBe(1);
  yield* TestClock.adjust("150 millis"); expect(editor.state.ready).toBe(1); expect(editor.state.submit).toBe(0);
  yield* mounted.close;
})));
it.effect("closing during composition preserves last raw and produces no composition completion or submit", () => Effect.scoped(Effect.gen(function*() {
  const editor = editorPort(); const dom = harness(editor.port); const binding = yield* dom.mount;
  dom.input.dispatchEvent(new CompositionEvent("compositionstart")); dom.inputRaw("한");
  yield* binding.close; yield* binding.close;
  dom.inputRaw("discarded"); dom.input.dispatchEvent(new CompositionEvent("compositionend")); dom.key(); yield* dom.flush;
  yield* TestClock.adjust("1 second"); expect(editor.model().single.count.raw).toBe("한");
  expect(editor.state).toEqual({ rawWrites: 1, ready: 0, submit: 0, errors: 0 }); expect(binding.snapshot()).toEqual({ _tag: "closed" });
})));
it.effect("native composing flags and keycode229 block Enter while unrelated key clears trailing Enter suppression", () => Effect.scoped(Effect.gen(function*() {
  const editor = editorPort(); const dom = harness(editor.port); const binding = yield* dom.mount;
  dom.key("Enter", true); dom.key("Enter", false, 229); yield* dom.flush; expect(editor.state.submit).toBe(0);
  dom.input.dispatchEvent(new CompositionEvent("compositionend")); dom.key("ArrowLeft"); dom.key(); yield* dom.flush;
  expect(editor.state.submit).toBe(1); yield* binding.close;
})));
it.effect("caret belongs only to mounted input and equal initial value is never reassigned", () => Effect.scoped(Effect.gen(function*() {
  const editor = editorPort(); const dom = harness(editor.port); dom.input.value = "1";
  const setter = vi.spyOn(dom.input, "value", "set"); const binding = yield* dom.mount;
  expect(setter).not.toHaveBeenCalled(); dom.input.setSelectionRange(0, 1); dom.input.dispatchEvent(new FocusEvent("focus"));
  expect(binding.snapshot()).toEqual({ _tag: "open", composition: "idle", focus: { start: 0, end: 1 } });
  dom.input.dispatchEvent(new FocusEvent("blur")); expect(binding.snapshot()).toEqual({ _tag: "open", composition: "idle", focus: null });
  yield* binding.close; expect(binding.snapshot()).toEqual({ _tag: "closed" }); setter.mockRestore();
})));
it.effect("number input without selection does not retain a fake caret", () => Effect.scoped(Effect.gen(function*() {
  const editor = editorPort(); const dom = harness(editor.port); dom.input.type = "number"; const binding = yield* dom.mount;
  dom.input.dispatchEvent(new FocusEvent("focus")); expect(binding.snapshot()).toEqual({ _tag: "open", composition: "idle", focus: null }); yield* binding.close;
})));
for (const failing of [1, 2, "always"] as const) it.effect(`first/Nth/continuous debounce readiness failure ${failing} is shown once per new input and never retried`, () => Effect.scoped(Effect.gen(function*() {
  const editor = editorPort((attempt) => failing === "always" || attempt === failing); const dom = harness(editor.port); const binding = yield* dom.mount;
  for (let attempt = 1; attempt <= 3; attempt++) { dom.inputRaw(String(attempt + 1)); yield* dom.flush; yield* TestClock.adjust("150 millis"); }
  expect(editor.state.ready).toBe(3); expect(dom.errors()).toBe(failing === "always" ? 3 : 1);
  yield* TestClock.adjust("10 seconds"); expect(editor.state.ready).toBe(3); yield* binding.close;
})));
it.effect("100 bind/unbind cycles preserve app raw, do not submit, and remove every listener/timer", () => Effect.scoped(Effect.gen(function*() {
  const editor = editorPort();
  for (let index = 0; index < 100; index++) {
    const dom = harness(editor.port); const binding = yield* dom.mount;
    dom.input.dispatchEvent(new CompositionEvent("compositionstart")); dom.inputRaw("-"); yield* dom.flush;
    yield* binding.close; yield* binding.close;
    dom.inputRaw("after-close"); dom.key(); dom.input.dispatchEvent(new FocusEvent("focus")); dom.input.dispatchEvent(new FocusEvent("blur"));
    yield* dom.flush; expect(binding.snapshot()).toEqual({ _tag: "closed" });
    expect(editor.model().single.count.raw).toBe("-"); expect(editor.model().single.count.inputVersion).toBe(index + 1);
  }
  yield* TestClock.adjust("1 second"); expect(editor.state.ready).toBe(0); expect(editor.state.submit).toBe(0);
})));
it.effect("a queued debounce after close allocates no timer and closing parent forbids allocation", () => Effect.gen(function*() {
  const scope = yield* Scope.make(); const editor = editorPort(); const dom = harness(editor.port);
  const binding = yield* dom.mount.pipe(Scope.provide(scope)); dom.inputRaw("4"); yield* binding.close; yield* dom.flush;
  yield* TestClock.adjust("1 second"); expect(editor.state.ready).toBe(0);
  yield* Scope.close(scope, Exit.void);
  const result = yield* Effect.result(dom.mount.pipe(Scope.provide(scope))); expect(result._tag).toBe("Failure");
}));
it.effect("partial listener acquisition failure removes earlier listeners before returning failure", () => Effect.scoped(Effect.gen(function*() {
  const editor = editorPort(); const dom = harness(editor.port);
  const active = new Set<EventListener>(); const originalAdd = dom.input.addEventListener.bind(dom.input); const originalRemove = dom.input.removeEventListener.bind(dom.input);
  let calls = 0;
  vi.spyOn(dom.input, "addEventListener").mockImplementation((type, listener, options) => { if (++calls === 3) throw new Error("third listener fails"); if (typeof listener === "function") active.add(listener); originalAdd(type, listener, options); });
  vi.spyOn(dom.input, "removeEventListener").mockImplementation((type, listener, options) => { if (typeof listener === "function") active.delete(listener); originalRemove(type, listener, options); });
  const result = yield* Effect.result(dom.mount); expect(result._tag).toBe("Failure"); expect(active.size).toBe(0);
  dom.inputRaw("2"); expect(editor.state.rawWrites).toBe(0); vi.restoreAllMocks();
})));

it.effect("queued native callbacks retained before close cannot revive raw, caret, composition or submission", () => Effect.scoped(Effect.gen(function*() {
  const editor = editorPort(); const dom = harness(editor.port); const listeners = new Map<string, EventListener>();
  const add = dom.input.addEventListener.bind(dom.input);
  vi.spyOn(dom.input, "addEventListener").mockImplementation((type, listener, options) => { if (typeof listener === "function") listeners.set(type, listener); add(type, listener, options); });
  const binding = yield* dom.mount; yield* binding.close;
  for (const [type, listener] of listeners) listener.call(dom.input, type === "keydown" ? new KeyboardEvent(type, { key: "Enter" }) : new Event(type));
  yield* dom.flush; expect(binding.snapshot()).toEqual({ _tag: "closed" }); expect(editor.state.rawWrites).toBe(0); expect(editor.state.submit).toBe(0);
  vi.restoreAllMocks();
})));

it.effect("a delayed cancellation dispatch cannot let the old timer submit during a new composition", () => Effect.scoped(Effect.gen(function*() {
  const editor = editorPort(); const dom = harness(editor.port); const binding = yield* dom.mount;
  dom.inputRaw("2"); yield* dom.flush;
  dom.input.dispatchEvent(new CompositionEvent("compositionstart"));
  yield* TestClock.adjust("150 millis"); expect(editor.state.ready).toBe(0);
  yield* dom.flush; yield* binding.close;
})));

it.effect("a new event during previous debounce cleanup discards the superseded schedule and interruption is not an error", () => Effect.scoped(Effect.gen(function*() {
  const editor = editorPort(); const entered = yield* Deferred.make<void>(); const closing = yield* Deferred.make<void>(); const release = yield* Deferred.make<void>();
  const port = { ...editor.port, onDebounced: (_fence: InputFence) => Deferred.succeed(entered, undefined).pipe(Effect.andThen(Effect.never), Effect.ensuring(Deferred.succeed(closing, undefined).pipe(Effect.andThen(Deferred.await(release))))) };
  const dom = harness(port); const binding = yield* dom.mount;
  dom.inputRaw("2"); yield* dom.flush; yield* TestClock.adjust("150 millis"); yield* Deferred.await(entered);
  dom.inputRaw("3"); const flushing = yield* dom.flush.pipe(Effect.forkChild); yield* Deferred.await(closing);
  dom.inputRaw("4"); yield* Deferred.succeed(release, undefined); yield* Fiber.join(flushing);
  expect(editor.model().single.count.raw).toBe("4"); expect(dom.errors()).toBe(0); yield* binding.close;
})));

it.effect("an interrupted submit intent is not reported as confirmed failure or retried", () => Effect.scoped(Effect.gen(function*() {
  const editor = editorPort(); const dom = harness({ ...editor.port, onSubmit: Effect.interrupt }); const binding = yield* dom.mount;
  dom.key(); yield* dom.flush; expect(dom.errors()).toBe(0); expect(editor.state.submit).toBe(0);
  yield* binding.close;
})));
