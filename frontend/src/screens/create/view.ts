import { Cause, Effect, Exit, Fiber, Scope, Semaphore } from "effect";
import { DomPlatform, type DomError } from "../../platform/dom";
import { patchInputValue } from "../../ui/view";
import type { InputFence } from "./editorModel";
import { createInputScreen, decideInputEnter, reduceInputScreen, type InputScreenModel } from "./inputModel";
import { ScreenUnavailable } from "./screenOwner";

export interface CreateInputPort<E, R> {
  readonly readRaw: () => string;
  readonly writeRaw: (raw: string) => void;
  // Reads the current editor version after local validation. Null means this
  // field is invalid, clean, locked or already admitted; no automatic resend.
  readonly candidate: () => InputFence | null;
  readonly onDebounced: (fence: InputFence) => Effect.Effect<void, E, R>;
  readonly onSubmit: Effect.Effect<void, E, R>;
}
export interface CreateInputBinding { readonly snapshot: () => InputScreenModel; readonly close: Effect.Effect<void> }
export function bindCreateTextInput<E, R>(
  input: HTMLInputElement | HTMLTextAreaElement,
  editor: CreateInputPort<E, R>,
  dispatch: (effect: Effect.Effect<void, never, R>) => void,
  reportError: (cause: Cause.Cause<E>) => Effect.Effect<void, never, R>,
): Effect.Effect<CreateInputBinding, DomError | ScreenUnavailable, DomPlatform | Scope.Scope> {
  return Effect.gen(function*() {
    const parent = yield* Scope.Scope;
    if (parent.state._tag === "Closed") return yield* Effect.fail(new ScreenUnavailable({ reason: "closed" }));
    const scope = yield* Scope.fork(parent, "sequential");
    return yield* Effect.gen(function*() {
    const dom = yield* DomPlatform;
    const lock = yield* Semaphore.make(1);
    let node: HTMLInputElement | HTMLTextAreaElement | null = input;
    let port: CreateInputPort<E, R> | undefined = editor;
    let report: typeof reportError | undefined = reportError;
    let model = createInputScreen();
    let epoch = {};
    let trailingRaw: string | null = null;
    let timer: Fiber.Fiber<void, never> | undefined;
    const ready = () => model._tag === "open" && scope.state._tag !== "Closed";
    const cancel = () => {
      epoch = {};
      const previous = timer; timer = undefined;
      if (previous !== undefined) dispatch(Fiber.interrupt(previous).pipe(Effect.asVoid));
    };
    const handle = (action: Effect.Effect<void, E, R>) => action.pipe(Effect.catchCause((cause) => !ready() || Cause.hasInterruptsOnly(cause) || report === undefined ? Effect.void : report(cause)));
    const schedule = () => {
      const requested = {}; epoch = requested;
      dispatch(lock.withPermit(Effect.gen(function*() {
        if (!ready() || requested !== epoch) return;
        const previous = timer; timer = undefined;
        if (previous !== undefined) yield* Fiber.interrupt(previous);
        if (!ready() || requested !== epoch) return;
        timer = yield* Effect.gen(function*() {
          yield* Effect.sleep("150 millis");
          if (!ready() || requested !== epoch || model._tag !== "open" || model.composition === "composing" || port === undefined) return;
          const fence = port.candidate();
          if (fence !== null) yield* port.onDebounced(fence);
        }).pipe(handle, Effect.forkIn(scope));
      })));
    };
    yield* Scope.addFinalizer(scope, Effect.sync(() => {
      epoch = {}; timer = undefined; trailingRaw = null; node = null; port = undefined; report = undefined;
      model = reduceInputScreen(model, { _tag: "Closed" });
    }));
    patchInputValue(input, editor.readRaw(), false);
    yield* dom.listen(input, "compositionstart", () => {
      if (!ready()) return;
      model = reduceInputScreen(model, { _tag: "CompositionStarted" }); trailingRaw = null; cancel();
    }).pipe(Scope.provide(scope));
    yield* dom.listen(input, "input", () => {
      if (!ready() || node === null || port === undefined) return;
      if (trailingRaw === node.value) { trailingRaw = null; return; }
      trailingRaw = null;
      port.writeRaw(node.value);
      if (model._tag === "open" && model.composition !== "composing") schedule();
    }).pipe(Scope.provide(scope));
    yield* dom.listen(input, "compositionend", () => {
      if (!ready() || node === null || port === undefined) return;
      model = reduceInputScreen(model, { _tag: "CompositionEnded" });
      if (port.readRaw() !== node.value) port.writeRaw(node.value);
      trailingRaw = node.value; schedule();
    }).pipe(Scope.provide(scope));
    yield* dom.listen(input, "keydown", (event) => {
      if (!ready() || port === undefined) return;
      const key = event as KeyboardEvent;
      if (key.key !== "Enter") { model = reduceInputScreen(model, { _tag: "OtherKey" }); return; }
      const decision = decideInputEnter(model, key.isComposing, key.keyCode); model = decision.model;
      key.preventDefault();
      if (decision.submit) { cancel(); dispatch(handle(port.onSubmit)); }
    }).pipe(Scope.provide(scope));
    yield* dom.listen(input, "focus", () => {
      if (ready() && node !== null && node.selectionStart !== null && node.selectionEnd !== null) model = reduceInputScreen(model, { _tag: "Focused", start: node.selectionStart, end: node.selectionEnd });
    }).pipe(Scope.provide(scope));
    yield* dom.listen(input, "blur", () => { if (ready()) model = reduceInputScreen(model, { _tag: "Blurred" }); }).pipe(Scope.provide(scope));
    if (editor.candidate() !== null) schedule();
    return { snapshot: () => model, close: Scope.close(scope, Exit.void) };
    }).pipe(Effect.onExit((exit) => Exit.isFailure(exit) ? Scope.close(scope, exit) : Effect.void));
  });
}
