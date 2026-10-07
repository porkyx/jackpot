import { expect, it, vi } from "@effect/vitest";
import { Deferred, Effect, Exit, Fiber, Scope } from "effect";
import { DomError, DomPlatform, makeDomPlatform } from "../../src/platform/dom";
import { mountDialog, type DialogView } from "../../src/ui/dialog/view";
import { connectField, patchFieldError } from "../../src/ui/fields/view";
import { ViewUnavailable } from "../../src/ui/view";

function environment() {
  const state = { listeners: 0, clicks: 0 };
  const native = makeDomPlatform({ request: () => 0, cancel: () => {} }, () => "visible");
  const dom: typeof DomPlatform.Service = { ...native, listen: (...args) => native.listen(...args).pipe(Effect.andThen(Effect.acquireRelease(
    Effect.sync(() => { state.listeners++; }), () => Effect.sync(() => { state.listeners--; }),
  ))) };
  const create = (): { readonly view: DialogView; readonly build: Effect.Effect<DialogView, DomError, Scope.Scope> } => {
    const element = document.createElement("dialog");
    const title = document.createElement("h2");
    const initialFocus = document.createElement("input");
    const second = document.createElement("input");
    const button = document.createElement("button");
    title.id = "test-dialog-title";
    title.textContent = "비밀번호 확인";
    initialFocus.type = second.type = "password";
    initialFocus.value = "first-secret";
    second.value = "second-secret";
    button.type = "button";
    button.textContent = "취소";
    element.append(title, initialFocus, second, button);
    const view: DialogView = { element, value: { title, initialFocus, sensitiveInputs: [initialFocus, second] } };
    return { view, build: dom.listen(button, "click", () => { state.clicks++; }).pipe(Effect.as(view)) };
  };
  return { state, dom, create };
}
function host() {
  return Effect.acquireRelease(Effect.sync(() => {
    const element = document.createElement("main");
    const trigger = document.createElement("button");
    trigger.type = "button";
    trigger.textContent = "열기";
    element.append(trigger);
    document.body.append(element);
    trigger.focus();
    return { element, trigger };
  }), ({ element }) => Effect.sync(() => element.remove()));
}

it.effect("native label links input, select and textarea and repeated connection has no writes", () => Effect.gen(function*() {
  const { element } = yield* host();
  for (const tag of ["input", "select", "textarea"] as const) {
    const label = document.createElement("label");
    const input = document.createElement(tag);
    const error = document.createElement("p");
    input.id = `field-${tag}`;
    error.id = `error-${tag}`;
    element.append(label, input, error);
    connectField(label, input, error);
    const write = vi.spyOn(label, "htmlFor", "set");
    const attr = vi.spyOn(error, "setAttribute");
    try {
      for (let index = 0; index < 100; index++) connectField(label, input, error);
      expect(write).not.toHaveBeenCalled();
      expect(attr).not.toHaveBeenCalled();
      expect(label.control).toBe(input);
      expect(error.getAttribute("role")).toBe("alert");
    } finally { write.mockRestore(); attr.mockRestore(); }
  }
}));

it.effect("field errors use literal visible text and associated aria then remove the error idempotently", () => Effect.gen(function*() {
  const { element } = yield* host();
  const input = document.createElement("input");
  const error = document.createElement("p");
  error.id = "field-error";
  element.append(input, error);
  const text = '<script>secret()</script><img onerror="bad()">';
  patchFieldError(input, error, text);
  expect(error.textContent).toBe(text);
  expect(error.childElementCount).toBe(0);
  expect(error.hidden).toBe(false);
  expect(input.getAttribute("aria-invalid")).toBe("true");
  expect(input.getAttribute("aria-describedby")).toBe(error.id);
  const hidden = vi.spyOn(error, "hidden", "set");
  try {
    patchFieldError(input, error, text);
    expect(hidden).not.toHaveBeenCalled();
    patchFieldError(input, error, null);
    patchFieldError(input, error, null);
    expect(hidden).toHaveBeenCalledExactlyOnceWith(true);
    expect(error.textContent).toBe("");
    expect(input.hasAttribute("aria-invalid")).toBe(false);
    expect(input.hasAttribute("aria-describedby")).toBe(false);
  } finally { hidden.mockRestore(); }
}));

for (const [inputId, errorId] of [["", "error"], ["input", ""], ["same", "same"]]) {
  it(`field association rejects invalid ids ${inputId}/${errorId}`, () => {
    const label = document.createElement("label");
    const input = document.createElement("input");
    const error = document.createElement("p");
    input.id = inputId!; error.id = errorId!;
    expect(() => connectField(label, input, error)).toThrow(DomError);
    expect(label.htmlFor).toBe("");
    expect(error.hasAttribute("role")).toBe(false);
  });
}
for (const message of ["", " ", "\n\t"]) {
  it(`empty actionable error ${JSON.stringify(message)} is rejected before DOM writes`, () => {
    const input = document.createElement("input");
    const error = document.createElement("p");
    error.id = "field-error";
    expect(() => patchFieldError(input, error, message)).toThrow(DomError);
    expect(input.attributes).toHaveLength(0);
    expect(error.textContent).toBe("");
  });
}
it("missing error id is rejected for both error and clear", () => {
  const input = document.createElement("input");
  const error = document.createElement("p");
  for (const message of ["오류", null]) expect(() => patchFieldError(input, error, message)).toThrow(DomError);
});

it.effect("dialog opens with title and initial focus, then closes once, clears secrets and returns trigger focus", () => Effect.gen(function*() {
  const { element, trigger } = yield* host();
  const env = environment();
  const scope = yield* Scope.make();
  const { view, build } = env.create();
  try {
    const session = yield* mountDialog(element, build).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom));
    expect(view.element.open).toBe(true);
    expect(view.element.getAttribute("aria-labelledby")).toBe(view.value.title.id);
    expect(document.activeElement).toBe(view.value.initialFocus);
    expect(env.state.listeners).toBe(3);
    expect(yield* session.close).toBe("close");
    expect(yield* session.close).toBe("close");
    expect(yield* session.closed).toBe("close");
    expect(document.activeElement).toBe(trigger);
    expect(view.element.open).toBe(false);
    expect(view.element.isConnected).toBe(false);
    expect(view.value.sensitiveInputs.map((input) => input.value)).toEqual(["", ""]);
    view.element.querySelector("button")!.click();
    expect(env.state.clicks).toBe(0);
    expect(env.state.listeners).toBe(0);
    expect(element.childNodes).toHaveLength(1);
  } finally { yield* Scope.close(scope, Exit.void); }
}));

for (const event of ["cancel", "close"] as const) {
  it.effect(`native ${event} ends only dialog scope and releases detached handlers`, () => Effect.gen(function*() {
    const { element, trigger } = yield* host();
    const env = environment();
    const scope = yield* Scope.make();
    const { view, build } = env.create();
    try {
      const session = yield* mountDialog(element, build).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom));
      const notice = new Event(event, { cancelable: true });
      expect(view.element.open).toBe(true);
      if (event === "close") view.element.close(); else view.element.dispatchEvent(notice);
      expect(yield* session.closed).toBe(event);
      if (event === "cancel") expect(notice.defaultPrevented).toBe(true);
      view.element.dispatchEvent(new Event("cancel"));
      expect(yield* session.closed).toBe(event);
      expect(document.activeElement).toBe(trigger);
      expect(env.state.listeners).toBe(0);
      expect(scope.state._tag).toBe("Empty");
    } finally { yield* Scope.close(scope, Exit.void); }
  }));
}

it.effect("route scope disposal ends dialog with discarded secrets and duplicate cleanup is safe", () => Effect.gen(function*() {
  const { element, trigger } = yield* host();
  const env = environment();
  const scope = yield* Scope.make();
  const { view, build } = env.create();
  const session = yield* mountDialog(element, build).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom));
  yield* Scope.close(scope, Exit.void);
  yield* Scope.close(scope, Exit.void);
  expect(yield* session.closed).toBe("disposed");
  expect(yield* session.close).toBe("disposed");
  expect(view.value.sensitiveInputs.every((input) => input.value === "")).toBe(true);
  expect(env.state.listeners).toBe(0);
  expect(document.activeElement).toBe(trigger);
}));

for (const field of ["open", "title-id", "title-text", "title-outside", "focus-outside", "secret-outside"] as const) {
  it.effect(`invalid dialog ${field} rejects with no owned resources and preserves foreign input`, () => Effect.gen(function*() {
    const { element, trigger } = yield* host();
    const env = environment();
    const scope = yield* Scope.make();
    const fixture = env.create();
    const foreign = document.createElement("input");
    foreign.value = "foreign-secret";
    const title = field === "title-outside" ? document.createElement("h2") : fixture.view.value.title;
    title.id = field === "title-id" ? "" : "title";
    title.textContent = field === "title-text" ? " " : "제목";
    if (field === "open") fixture.view.element.open = true;
    const view: DialogView = { element: fixture.view.element, value: {
      title, initialFocus: field === "focus-outside" ? foreign : fixture.view.value.initialFocus,
      sensitiveInputs: field === "secret-outside" ? [...fixture.view.value.sensitiveInputs, foreign] : fixture.view.value.sensitiveInputs,
    } };
    try {
      const result = yield* Effect.result(mountDialog(element, fixture.build.pipe(Effect.as(view))).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom)));
      expect(result._tag).toBe("Failure");
      expect(fixture.view.element.isConnected).toBe(false);
      expect(fixture.view.value.sensitiveInputs.map((input) => input.value)).toEqual(["", ""]);
      expect(foreign.value).toBe("foreign-secret");
      expect(env.state.listeners).toBe(0);
      expect(document.activeElement).toBe(trigger);
    } finally { yield* Scope.close(scope, Exit.void); }
  }));
}

for (const faults of [[1], [3], [1, 2, 3]]) {
  it.effect(`showModal faults ${faults.join(",")} roll back partial open and allow independent later dialogs`, () => Effect.gen(function*() {
    const { element, trigger } = yield* host();
    const env = environment();
    let calls = 0;
    const show = vi.spyOn(HTMLDialogElement.prototype, "showModal").mockImplementation(function(this: HTMLDialogElement) {
      calls++; this.open = true;
      if (faults.includes(calls)) throw new Error("private native error");
    });
    try {
      for (let index = 1; index <= 3; index++) {
        const scope = yield* Scope.make();
        const fixture = env.create();
        try {
          const result = yield* Effect.result(mountDialog(element, fixture.build).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom)));
          expect(result._tag).toBe(faults.includes(index) ? "Failure" : "Success");
          if (result._tag === "Success") yield* result.success.close;
          expect(fixture.view.element.open).toBe(false);
          expect(fixture.view.element.isConnected).toBe(false);
          expect(fixture.view.value.sensitiveInputs.map((input) => input.value)).toEqual(["", ""]);
          expect(document.activeElement).toBe(trigger);
          expect(env.state.listeners).toBe(0);
        } finally { yield* Scope.close(scope, Exit.void); }
      }
    } finally { show.mockRestore(); }
  }));
}

it.effect("focus failure after native open rolls back title, listeners, secrets and focus", () => Effect.gen(function*() {
  const { element, trigger } = yield* host();
  const env = environment();
  const fixture = env.create();
  const scope = yield* Scope.make();
  try {
    const dom = { ...env.dom, focus: () => Effect.fail(new DomError()) };
    expect((yield* Effect.result(mountDialog(element, fixture.build).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, dom))))._tag).toBe("Failure");
    expect(fixture.view.element.isConnected).toBe(false);
    expect(fixture.view.value.sensitiveInputs.map((input) => input.value)).toEqual(["", ""]);
    expect(document.activeElement).toBe(trigger);
    expect(env.state.listeners).toBe(0);
  } finally { yield* Scope.close(scope, Exit.void); }
}));

it.effect("cancel emitted during native open cannot move focus back into the ended dialog", () => Effect.gen(function*() {
  const { element, trigger } = yield* host();
  const env = environment();
  const scope = yield* Scope.make();
  const fixture = env.create();
  const show = vi.spyOn(fixture.view.element, "showModal").mockImplementation(() => {
    fixture.view.element.open = true;
    fixture.view.element.dispatchEvent(new Event("cancel", { cancelable: true }));
  });
  try {
    const result = yield* Effect.result(mountDialog(element, fixture.build).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom)));
    expect(result._tag).toBe("Failure");
    if (result._tag === "Failure") expect(result.failure).toEqual(new ViewUnavailable({ reason: "closed" }));
    expect(document.activeElement).toBe(trigger);
    expect(env.state.listeners).toBe(0);
    expect(fixture.view.element.isConnected).toBe(false);
  } finally { show.mockRestore(); yield* Scope.close(scope, Exit.void); }
}));

it.effect("native close failure remains typed while all other cleanup continues", () => Effect.gen(function*() {
  const { element, trigger } = yield* host();
  const env = environment();
  const fixture = env.create();
  const scope = yield* Scope.make();
  const session = yield* mountDialog(element, fixture.build).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom));
  const close = vi.spyOn(fixture.view.element, "close").mockImplementation(() => { throw new Error("secret must not leak"); });
  try {
    const result = yield* Effect.result(session.close);
    expect(result._tag).toBe("Failure");
    if (result._tag === "Failure") expect(result.failure).toEqual(new DomError());
    expect(fixture.view.element.isConnected).toBe(false);
    expect(fixture.view.value.sensitiveInputs.map((input) => input.value)).toEqual(["", ""]);
    expect(document.activeElement).toBe(trigger);
    expect(env.state.listeners).toBe(0);
  } finally { close.mockRestore(); yield* Scope.close(scope, Exit.void); }
}));

it.effect("one failing secret setter cannot prevent other secret cleanup or focus return", () => Effect.gen(function*() {
  const { element, trigger } = yield* host();
  const env = environment();
  const fixture = env.create();
  const scope = yield* Scope.make();
  const session = yield* mountDialog(element, fixture.build).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom));
  const secret = fixture.view.value.sensitiveInputs[0]!;
  const setter = vi.spyOn(secret, "value", "set").mockImplementation(() => { throw new Error("native setter fault"); });
  try {
    expect((yield* Effect.result(session.close))._tag).toBe("Failure");
    expect(fixture.view.value.sensitiveInputs[1]!.value).toBe("");
    expect(document.activeElement).toBe(trigger);
    expect(env.state.listeners).toBe(0);
    expect(fixture.view.element.isConnected).toBe(false);
  } finally { setter.mockRestore(); secret.value = ""; yield* Scope.close(scope, Exit.void); }
}));

it.effect("detached trigger is not focused again and empty sensitive list is valid", () => Effect.gen(function*() {
  const { element, trigger } = yield* host();
  const env = environment();
  const scope = yield* Scope.make();
  const fixture = env.create();
  const view: DialogView = { ...fixture.view, value: { ...fixture.view.value, sensitiveInputs: [] } };
  const session = yield* mountDialog(element, fixture.build.pipe(Effect.as(view))).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom));
  trigger.remove();
  const focus = vi.spyOn(trigger, "focus");
  try { yield* session.close; expect(focus).not.toHaveBeenCalled(); expect(env.state.listeners).toBe(0); }
  finally { focus.mockRestore(); yield* Scope.close(scope, Exit.void); }
}));

it.effect("focus return failure remains typed and does not retain owned DOM", () => Effect.gen(function*() {
  const { element, trigger } = yield* host();
  const env = environment();
  const scope = yield* Scope.make();
  const fixture = env.create();
  const session = yield* mountDialog(element, fixture.build).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom));
  const focus = vi.spyOn(trigger, "focus").mockImplementation(() => { throw new Error("focus fault"); });
  try {
    expect((yield* Effect.result(session.close))._tag).toBe("Failure");
    expect(env.state.listeners).toBe(0);
    expect(fixture.view.element.isConnected).toBe(false);
  } finally { focus.mockRestore(); yield* Scope.close(scope, Exit.void); }
}));

it.effect("absent previous active element needs no focus target and closes normally", () => Effect.gen(function*() {
  const { element } = yield* host();
  const env = environment();
  const scope = yield* Scope.make();
  const fixture = env.create();
  const active = vi.spyOn(element.ownerDocument, "activeElement", "get").mockReturnValueOnce(null);
  try {
    const session = yield* mountDialog(element, fixture.build).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom));
    expect(yield* session.close).toBe("close");
    expect(env.state.listeners).toBe(0);
  } finally { active.mockRestore(); yield* Scope.close(scope, Exit.void); }
}));

it.effect("a disabled initial input cannot silently claim initial focus succeeded", () => Effect.gen(function*() {
  const { element, trigger } = yield* host();
  const env = environment();
  const scope = yield* Scope.make();
  const fixture = env.create();
  fixture.view.value.sensitiveInputs[0]!.disabled = true;
  try {
    expect((yield* Effect.result(mountDialog(element, fixture.build).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom))))._tag).toBe("Failure");
    expect(document.activeElement).toBe(trigger);
    expect(fixture.view.element.isConnected).toBe(false);
    expect(env.state.listeners).toBe(0);
  } finally { yield* Scope.close(scope, Exit.void); }
}));

it.effect("a closed parent cannot execute dialog builder", () => Effect.gen(function*() {
  const { element } = yield* host();
  const env = environment();
  const scope = yield* Scope.make();
  yield* Scope.close(scope, Exit.void);
  let builds = 0;
  const result = yield* Effect.result(mountDialog(element, Effect.sync(() => { builds++; return env.create().view; })).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom)));
  expect(result._tag).toBe("Failure");
  if (result._tag === "Failure") expect(result.failure).toEqual(new ViewUnavailable({ reason: "closed" }));
  expect(builds).toBe(0);
}));

it.effect("a foreign attached dialog is preserved and its input is not cleared", () => Effect.gen(function*() {
  const { element } = yield* host();
  const env = environment();
  const scope = yield* Scope.make();
  const fixture = env.create();
  element.append(fixture.view.element);
  try {
    expect((yield* Effect.result(mountDialog(element, fixture.build).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom))))._tag).toBe("Failure");
    expect(fixture.view.element.isConnected).toBe(true);
    expect(fixture.view.value.sensitiveInputs[0]!.value).toBe("first-secret");
    expect(env.state.listeners).toBe(0);
  } finally { fixture.view.element.remove(); yield* Scope.close(scope, Exit.void); }
}));

it.effect("parent closing during initial focus cannot return a usable session", () => Effect.gen(function*() {
  const { element } = yield* host();
  const env = environment();
  const scope = yield* Scope.make();
  const fixture = env.create();
  const dom = { ...env.dom, focus: () => Scope.close(scope, Exit.void) };
  const result = yield* Effect.result(mountDialog(element, fixture.build).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, dom)));
  expect(result._tag).toBe("Failure");
  if (result._tag === "Failure") expect(result.failure).toEqual(new ViewUnavailable({ reason: "closed" }));
  expect(fixture.view.element.isConnected).toBe(false);
  expect(env.state.listeners).toBe(0);
}));

it.effect("parent closing during detached build clears returned owned inputs without attaching", () => Effect.gen(function*() {
  const { element } = yield* host();
  const env = environment();
  const scope = yield* Scope.make();
  const fixture = env.create();
  const entered = yield* Deferred.make<void>();
  const gate = yield* Deferred.make<void>();
  const build = Effect.gen(function*() { yield* Deferred.succeed(entered, undefined); yield* Deferred.await(gate); return fixture.view; });
  const pending = yield* mountDialog(element, build).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom), Effect.result, Effect.forkChild);
  yield* Deferred.await(entered);
  yield* Scope.close(scope, Exit.void);
  yield* Deferred.succeed(gate, undefined);
  expect((yield* Fiber.join(pending))._tag).toBe("Failure");
  expect(fixture.view.element.isConnected).toBe(false);
  expect(fixture.view.value.sensitiveInputs.map((input) => input.value)).toEqual(["", ""]);
}));

it.effect("one hundred native dialog lifetimes restore focus and listener/node baseline", () => Effect.gen(function*() {
  const { element, trigger } = yield* host();
  const env = environment();
  const scope = yield* Scope.make();
  try {
    for (let index = 0; index < 100; index++) {
      const fixture = env.create();
      const session = yield* mountDialog(element, fixture.build).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom));
      expect(env.state.listeners).toBe(3);
      if (index % 2 === 0) yield* session.close;
      else { fixture.view.element.dispatchEvent(new Event("cancel", { cancelable: true })); yield* session.closed; }
      expect(element.childNodes).toHaveLength(1);
      expect(env.state.listeners).toBe(0);
      expect(scope.state._tag).toBe("Empty");
      expect(document.activeElement).toBe(trigger);
      expect(fixture.view.value.sensitiveInputs.every((input) => input.value === "")).toBe(true);
    }
  } finally { yield* Scope.close(scope, Exit.void); }
}));

for (const ending of ["cancel", "native-close", "programmatic"] as const) it.effect("dialog eligibility hook precedes focus exactly once for " + ending, () => Effect.gen(function*() {
  const { element, trigger } = yield* host(); const env = environment(); const scope = yield* Scope.make(); const fixture = env.create();
  let hooks = 0; const order: string[] = [];
  const nativeFocus = trigger.focus.bind(trigger); const focus = vi.spyOn(trigger, "focus").mockImplementation(() => { order.push("focus"); if (!trigger.disabled) nativeFocus(); });
  const view: DialogView = { ...fixture.view, value: { ...fixture.view.value, beforeFocusRestore: Effect.sync(() => { hooks++; order.push("release"); expect(fixture.view.element.isConnected).toBe(false); expect(env.state.listeners).toBe(0); trigger.disabled = false; }) } };
  try {
    const session = yield* mountDialog(element, fixture.build.pipe(Effect.as(view))).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom)); trigger.disabled = true;
    if (ending === "cancel") fixture.view.element.dispatchEvent(new Event("cancel", { cancelable: true }));
    else if (ending === "native-close") fixture.view.element.close(); else yield* session.close;
    expect(yield* session.closed).toBe(ending === "cancel" ? "cancel" : "close"); yield* session.close; yield* session.closed;
    expect(hooks).toBe(1); expect(order).toEqual(["release", "focus"]); expect(document.activeElement).toBe(trigger); expect(fixture.view.value.sensitiveInputs.every(input => input.value === "")).toBe(true);
  } finally { focus.mockRestore(); yield* Scope.close(scope, Exit.void); }
}));
it.effect("dialog hook failure stays typed after secret/listener cleanup and still attempts focus", () => Effect.gen(function*() {
  const { element, trigger } = yield* host(); const env = environment(); const scope = yield* Scope.make(); const fixture = env.create(); let calls = 0;
  const view: DialogView = { ...fixture.view, value: { ...fixture.view.value, beforeFocusRestore: Effect.sync(() => { calls++; throw Error("private hook fault"); }) } };
  try {
    const session = yield* mountDialog(element, fixture.build.pipe(Effect.as(view))).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom));
    const result = yield* Effect.result(session.close); expect(result._tag).toBe("Failure"); if(result._tag === "Failure")expect(result.failure).toEqual(new DomError());
    expect((yield*Effect.result(session.close))._tag).toBe("Failure"); expect(calls).toBe(1); expect(document.activeElement).toBe(trigger); expect(env.state.listeners).toBe(0); expect(fixture.view.element.isConnected).toBe(false); expect(fixture.view.value.sensitiveInputs.every(input => input.value === "")).toBe(true);
  } finally { yield* Scope.close(scope, Exit.void); }
}));
it.effect("dialog restore preserves original native input node and mid-composition caret", () => Effect.gen(function*() {
  const { element } = yield* host(); const input = document.createElement("input"); input.value = "한😀입력"; element.append(input); input.focus(); input.setSelectionRange(1,3); input.dispatchEvent(new CompositionEvent("compositionstart"));
  const env = environment(); const scope = yield* Scope.make(); const fixture = env.create();
  try { const session = yield* mountDialog(element, fixture.build).pipe(Scope.provide(scope),Effect.provideService(DomPlatform,env.dom)); yield*session.close; expect(document.activeElement).toBe(input); expect(input.value).toBe("한😀입력"); expect([input.selectionStart,input.selectionEnd]).toEqual([1,3]); expect(input.isConnected).toBe(true); expect(env.state.listeners).toBe(0); } finally { yield*Scope.close(scope,Exit.void); }
}));

it.effect("explicit opener survives native disabled blur before dialog mount captures BODY",()=>Effect.gen(function*(){
 const{element,trigger}=yield*host();const env=environment();const scope=yield*Scope.make();const fixture=env.create();trigger.blur();trigger.disabled=true;expect(document.activeElement).not.toBe(trigger);
 const view:DialogView={...fixture.view,value:{...fixture.view.value,restoreFocus:trigger,beforeFocusRestore:Effect.sync(()=>{trigger.disabled=false;})}};
 try{const session=yield*mountDialog(element,fixture.build.pipe(Effect.as(view))).pipe(Scope.provide(scope),Effect.provideService(DomPlatform,env.dom));fixture.view.element.dispatchEvent(new Event("cancel",{cancelable:true}));expect(yield*session.closed).toBe("cancel");expect(document.activeElement).toBe(trigger);expect(env.state.listeners).toBe(0);}finally{yield*Scope.close(scope,Exit.void);}
}));

for(const kind of["foreign-document","null","wrong-type"]as const)it.effect("explicit opener rejects "+kind+" without focusing foreign or retaining secrets",()=>Effect.gen(function*(){
 const{element,trigger}=yield*host();const env=environment();const scope=yield*Scope.make();const fixture=env.create();const foreign=document.implementation.createHTMLDocument().createElement("button");const target=kind==="foreign-document"?foreign:kind==="null"?null:"button";const focus=vi.spyOn(foreign,"focus");const view:DialogView={...fixture.view,value:{...fixture.view.value,restoreFocus:target as unknown as HTMLElement}};
 try{const result=yield*Effect.result(mountDialog(element,fixture.build.pipe(Effect.as(view))).pipe(Scope.provide(scope),Effect.provideService(DomPlatform,env.dom)));expect(result._tag).toBe("Failure");expect(focus).not.toHaveBeenCalled();expect(document.activeElement).toBe(trigger);expect(env.state.listeners).toBe(0);expect(fixture.view.value.sensitiveInputs.every(input=>input.value==="")).toBe(true);}finally{focus.mockRestore();yield*Scope.close(scope,Exit.void);}
}));
for(const condition of["disabled","detached","retired-route"]as const)it.effect("explicit opener "+condition+" is never focused after dialog disposal",()=>Effect.gen(function*(){
 const{element,trigger}=yield*host();const env=environment();const scope=yield*Scope.make();const fixture=env.create();const view:DialogView={...fixture.view,value:{...fixture.view.value,restoreFocus:trigger}};
 const session=yield*mountDialog(element,fixture.build.pipe(Effect.as(view))).pipe(Scope.provide(scope),Effect.provideService(DomPlatform,env.dom));const focus=vi.spyOn(trigger,"focus");try{if(condition==="disabled")trigger.disabled=true;else if(condition==="detached")trigger.remove();if(condition==="retired-route")yield*Scope.close(scope,Exit.void);else yield*session.close;yield*session.closed;expect(focus).not.toHaveBeenCalled();expect(env.state.listeners).toBe(0);expect(fixture.view.element.isConnected).toBe(false);}finally{focus.mockRestore();yield*Scope.close(scope,Exit.void);}
}));
