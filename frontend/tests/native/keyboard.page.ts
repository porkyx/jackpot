// Native verification only. The production entry never imports this module.
import { Call } from "@wailsio/runtime";
import { Deferred, Effect, Layer } from "effect";
import { createAppRuntime } from "../../src/app/runtime";
import { DomPlatform, makeDomPlatform } from "../../src/platform/dom";
import { mountDialog, type DialogEnd } from "../../src/ui/dialog/view";
import { connectField, patchFieldError } from "../../src/ui/fields/view";
import { mountView } from "../../src/ui/view";

interface Observation { readonly type: string; readonly key: string; readonly target: string; readonly trusted: boolean; readonly composing: boolean }
interface ClosedDialog { readonly reason: DialogEnd; readonly secretCleared: boolean; readonly focusRestored: boolean }
interface NativeKeyboardProbe {
  phase: "starting" | "ready" | "done" | "failed";
  failure: "setup" | "driver" | "cleanup" | "invariant" | null;
  events: Observation[];
  invalids: number;
  submits: number;
  dialogsOpened: number;
  dialogsClosed: ClosedDialog[];
  activeListeners: number;
  dialogOpen: boolean;
  secretClearedOnDispose: boolean;
  probeRemoved: boolean;
  finish: (passed: boolean) => Promise<void>;
}
declare global { interface Window { __jackpotNativeKeyboard?: NativeKeyboardProbe } }

export async function runNativeKeyboardProbe(host: HTMLElement): Promise<void> {
  if (window.__jackpotNativeKeyboard !== undefined) throw new Error("Native keyboard probe already mounted");
  const finished = Deferred.makeUnsafe<boolean>();
  let driverFinished = false;
  let completionResolve: () => void = () => {};
  let completionReject: (error: Error) => void = () => {};
  const completion = new Promise<void>((resolve, reject) => { completionResolve = resolve; completionReject = reject; });
  void completion.catch(() => {});
  const state: NativeKeyboardProbe = {
    phase: "starting", failure: null, events: [], invalids: 0, submits: 0, dialogsOpened: 0,
    dialogsClosed: [], activeListeners: 0, dialogOpen: false, secretClearedOnDispose: false, probeRemoved: false,
    finish: (passed) => {
      if (!driverFinished) { driverFinished = true; Deferred.doneUnsafe(finished, Effect.succeed(passed)); }
      return completion;
    },
  };
  window.__jackpotNativeKeyboard = state;
  const base = makeDomPlatform({ request: (callback) => window.requestAnimationFrame(callback), cancel: (id) => window.cancelAnimationFrame(id) }, () => document.visibilityState);
  const dom: typeof DomPlatform.Service = { ...base, listen: (...args) => Effect.gen(function*() {
    yield* base.listen(...args);
    state.activeListeners++;
    yield* Effect.addFinalizer(() => Effect.sync(() => { state.activeListeners--; }));
  }) };
  let section: HTMLElement | undefined;
  let password: HTMLInputElement | undefined;
  const fail = (): void => {
    if (!driverFinished) { state.failure = "invariant"; void state.finish(false); }
  };
  const owner = createAppRuntime(Layer.succeed(DomPlatform, dom), Effect.gen(function*() {
    const platform = yield* DomPlatform;
    section = document.createElement("section"); section.id = "native-keyboard-probe";
    const heading = document.createElement("h3"); heading.textContent = "Native keyboard verification";
    const form = document.createElement("form"); form.id = "native-keyboard-form";
    const label = document.createElement("label"); label.textContent = "검증 이메일";
    const email = document.createElement("input"); email.id = "native-keyboard-email"; email.type = "email"; email.required = true;
    const error = document.createElement("p"); error.id = "native-keyboard-error";
    connectField(label, email, error); patchFieldError(email, error, null);
    const submit = document.createElement("button"); submit.id = "native-keyboard-submit"; submit.type = "submit"; submit.textContent = "검증 제출";
    form.append(label, email, error, submit);
    const trigger = document.createElement("button"); trigger.id = "native-keyboard-dialog-trigger"; trigger.type = "button"; trigger.textContent = "검증 대화상자";
    const checkedLabel = document.createElement("label"); checkedLabel.textContent = "검증 선택";
    const checkbox = document.createElement("input"); checkbox.id = "native-keyboard-checkbox"; checkbox.type = "checkbox"; checkedLabel.append(checkbox);
    const imeLabel = document.createElement("label"); imeLabel.textContent = "검증 조합 입력";
    const ime = document.createElement("input"); ime.id = "native-keyboard-ime"; imeLabel.htmlFor = ime.id;
    const dialogHost = document.createElement("div"); dialogHost.id = "native-keyboard-dialog-host";
    section.append(heading, form, trigger, checkedLabel, imeLabel, ime, dialogHost);
    yield* mountView(host, Effect.succeed({ element: section, value: undefined }));
    for (const type of ["keydown", "keyup", "click", "submit", "invalid", "cancel", "close", "input", "change", "focusin", "focusout", "compositionstart", "compositionupdate", "compositionend"]) {
      yield* platform.listen(document, type, (event) => {
        if (state.events.length === 512) { fail(); return; }
        const key = event instanceof KeyboardEvent && ["Tab", "Enter", "Escape", " ", "Shift"].includes(event.key) ? event.key : "";
        const target = event.target instanceof HTMLElement ? event.target.id || event.target.tagName : "";
        state.events.push({ type: event.type, key, target, trusted: event.isTrusted, composing: event instanceof KeyboardEvent && event.isComposing });
      }, { capture: true });
    }
    yield* platform.listen(email, "invalid", () => { state.invalids++; patchFieldError(email, error, "이메일 형식을 확인해 주세요."); });
    yield* platform.listen(form, "submit", (event) => { event.preventDefault(); state.submits++; patchFieldError(email, error, null); });
    yield* platform.listen(trigger, "click", () => {
      if (state.dialogOpen) { fail(); return; }
      state.dialogOpen = true;
      const element = document.createElement("dialog"); element.id = "native-keyboard-dialog";
      const title = document.createElement("h3"); title.id = "native-keyboard-dialog-title"; title.textContent = "검증 대화상자";
      const dialogForm = document.createElement("form"); dialogForm.method = "dialog";
      const input = document.createElement("input"); input.type = "password"; input.id = "native-keyboard-password"; input.setAttribute("aria-label", "검증용 임시 입력"); password = input;
      const confirm = document.createElement("button"); confirm.id = "native-keyboard-confirm"; confirm.type = "submit"; confirm.textContent = "닫기";
      const cancel = document.createElement("button"); cancel.id = "native-keyboard-cancel"; cancel.type = "submit"; cancel.textContent = "취소";
      dialogForm.append(input, confirm, cancel); element.append(title, dialogForm);
      void owner.mount(mountDialog(dialogHost, Effect.succeed({ element, value: { title, initialFocus: input, sensitiveInputs: [input] } }))).then((session) => {
        state.dialogsOpened++;
        void owner.run(session.closed).then((reason) => {
          state.dialogsClosed.push({ reason, secretCleared: input.value === "", focusRestored: document.activeElement === trigger });
          state.dialogOpen = false;
        }).catch(fail);
      }).catch(fail);
    });
  }));
  let passed = false;
  try {
    await owner.ready;
    state.phase = "ready";
    await Call.ByName("main.Probe.KeyboardReady");
    passed = await owner.run(Deferred.await(finished));
    if (!passed) { state.failure ??= "driver"; throw new Error("Native keyboard driver failed"); }
  } catch (error) {
    state.failure ??= "setup";
    throw error;
  } finally {
    try {
      await owner.dispose(); await owner.dispose();
      state.secretClearedOnDispose = password?.value === "";
      state.probeRemoved = section !== undefined && !section.isConnected;
      if (!passed || state.failure !== null || state.activeListeners !== 0 || !state.secretClearedOnDispose || !state.probeRemoved) throw new Error("Native keyboard cleanup invariant failed");
      state.phase = "done"; completionResolve();
    } catch (error) {
      state.failure ??= "cleanup"; state.phase = "failed"; completionReject(new Error("Native keyboard verification failed"));
      throw error;
    }
  }
}
