import { expect, it, vi } from "@effect/vitest";
import { Effect, Exit, Scope } from "effect";
import type { RecoveryEntry, RecoverySnapshot } from "../../src/operations/pendingRecovery";
import { projectError } from "../../src/app/errors";
import { ProtocolError } from "../../src/contracts/backend";
import { DomError, DomPlatform, makeDomPlatform } from "../../src/platform/dom";
import { mountRecoveryStatus } from "../../src/ui/recovery/view";
import { ViewUnavailable } from "../../src/ui/view";

function snapshot(count = 1, change: Partial<RecoverySnapshot> = {}): RecoverySnapshot {
  const pending: RecoveryEntry[] = Array.from({ length: count }, (_, index) => ({
    descriptor: { operationId: `SECRET-operation-${index}`, kind: "CreateCollection", collectionId: "SECRET-collection", roundId: "SECRET-round", revision: 1, status: "pending" },
    observation: null, failure: null,
  }));
  return { phase: "ready", backendSessionId: "SECRET-session", pending, terminal: [], cursor: null, enumerationComplete: true, failure: null, readsAllowed: true, writesBlocked: count > 0, ...change };
}
function environment(fault: ReadonlySet<number> = new Set()) {
  const state = { active: 0, acquired: 0, released: 0, frames: 0, clicks: 0, listeners: [] as EventListener[] };
  const native = makeDomPlatform({ request: () => { state.frames++; return 0; }, cancel: () => {} }, () => "visible");
  const dom: typeof DomPlatform.Service = { ...native, listen: (...args) => Effect.gen(function*() {
    yield* native.listen(...args);
    yield* Effect.acquireRelease(Effect.sync(() => { state.active++; state.acquired++; state.listeners.push(args[2]); }), () => Effect.sync(() => { state.active--; state.released++; }));
    if (fault.has(state.acquired)) return yield* Effect.fail(new DomError());
  }) };
  return { state, dom };
}
function host() { return Effect.acquireRelease(Effect.sync(() => {
  const element = document.createElement("form");
  const unrelated = document.createElement("a"); unrelated.href = "#/results/collection"; unrelated.textContent = "결과";
  element.append(unrelated); document.body.append(element);
  return { element, unrelated };
}), ({ element }) => Effect.sync(() => element.remove())); }
function button(element: HTMLElement): HTMLButtonElement {
  const target = element.querySelector("button"); if (target === null) throw new Error("Native recovery button absent"); return target;
}

for (const count of [0, 1, 63, 64]) it.effect(`${count} pending work projects safe Korean counts and keeps result navigation enabled`, () => Effect.gen(function*() {
  const { element, unrelated } = yield* host(); const scope = yield* Scope.make(); const env = environment();
  const view = yield* mountRecoveryStatus(element, () => { env.state.clicks++; }).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom));
  try {
    expect(button(view.element).disabled).toBe(true);
    const value = snapshot(count);
    yield* view.patch(value, false);
    expect(view.element.textContent).toContain(`확인 대기 ${count}개`);
    expect(view.element.textContent).toContain("결과 조회는 계속 이용할 수 있습니다.");
    expect(view.element.hidden).toBe(count === 0);
    expect(button(view.element).disabled).toBe(count === 0);
    expect(view.element.textContent?.includes("복구 대기 한도 도달")).toBe(count === 64);
    expect(unrelated.getAttribute("href")).toBe("#/results/collection");
    expect(unrelated.hasAttribute("aria-disabled")).toBe(false);
    expect(element.querySelectorAll("script,img,input")).toHaveLength(0);
    expect(view.element.outerHTML).not.toContain("SECRET");
    expect([env.state.active, env.state.frames]).toEqual([1, 0]);
  } finally { yield* Scope.close(scope, Exit.void); }
  expect(element.childNodes).toHaveLength(1); expect(element.firstChild).toBe(unrelated);
  expect([env.state.active, env.state.acquired, env.state.released]).toEqual([0, 1, 1]);
}));

for (const phase of ["starting", "enumerating", "saturated"] as const) it.effect(`${phase} is visibly incomplete instead of empty boot success`, () => Effect.gen(function*() {
  const { element } = yield* host(); const env = environment(); const scope = yield* Scope.make();
  const view = yield* mountRecoveryStatus(element, () => {}).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom));
  try {
    const value = snapshot(phase === "saturated" ? 63 : 0, { phase, enumerationComplete: phase === "saturated" });
    yield* view.patch(value, false);
    expect(view.element.hidden).toBe(false);
    expect(button(view.element).disabled).toBe(false);
    expect(view.element.textContent).toContain(phase === "saturated" ? "복구 대기 한도 도달" : "확인이 끝날 때까지 새 변경을 기다려 주세요.");
  } finally { yield* Scope.close(scope, Exit.void); }
}));

it.effect("unknown count uses public primitives without retaining or rendering IDs, server errors or terminal payload", () => Effect.gen(function*() {
  const { element } = yield* host(); const env = environment(); const scope = yield* Scope.make();
  const view = yield* mountRecoveryStatus(element, () => {}).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom));
  try {
    const base = snapshot(3);
    const value = { ...base, pending: base.pending.map((entry, index) => ({ ...entry, observation: index === 0 ? null : index === 1
      ? { operationId: "<script>SECRET</script>", state: "unknown" as const, kind: null, collectionId: null, roundId: null, revision: null, failureCode: null }
      : { operationId: "SECRET-pending", state: "pending" as const, kind: "CreateCollection" as const, collectionId: "SECRET", roundId: "SECRET", revision: 1, failureCode: null } })) };
    yield* view.patch(value, false);
    expect(view.element.textContent).toContain("확인 대기 3개 · 결과 미확인 1개");
    expect(view.element.outerHTML).not.toMatch(/SECRET|operationId|collectionId|revision|password|script/);
    expect(Object.keys(view).sort()).toEqual(["close", "element", "patch"]);
    yield* view.patch(snapshot(1), false);
    expect(view.element.textContent).not.toContain("결과 미확인");
  } finally { yield* Scope.close(scope, Exit.void); }
}));

it.effect("read failure remains actionable even with zero descriptors and never renders backend text", () => Effect.gen(function*() {
  const { element } = yield* host(); const env = environment(); const scope = yield* Scope.make();
  const view = yield* mountRecoveryStatus(element, () => {}).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom));
  try {
    yield* view.patch(snapshot(0, { phase: "failed", failure: { ...projectError(new ProtocolError()), message: "<img>SECRET</img>" } }), false);
    expect(view.element.hidden).toBe(false);
    expect(view.element.textContent).toContain("처리 결과를 확인하지 못했습니다. 다시 확인해 주세요.");
    expect(view.element.outerHTML).not.toContain("SECRET");
    expect(button(view.element).disabled).toBe(false);
    yield* view.patch(snapshot(1, { readsAllowed: false }), false);
    expect(view.element.textContent).toContain("현재 결과 확인을 사용할 수 없습니다.");
    expect(button(view.element).disabled).toBe(true);
    button(view.element).dispatchEvent(new Event("click")); expect(env.state.clicks).toBe(0);
  } finally { yield* Scope.close(scope, Exit.void); }
}));

it.effect("native recovery button is labelled, non-submitting and latches immediately before checking patch", () => Effect.gen(function*() {
  const { element } = yield* host(); const env = environment(); const scope = yield* Scope.make(); let submissions = 0;
  element.addEventListener("submit", () => { submissions++; });
  const view = yield* mountRecoveryStatus(element, () => { env.state.clicks++; }).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom));
  try {
    const target = button(view.element);
    target.dispatchEvent(new Event("click")); expect(env.state.clicks).toBe(0);
    yield* view.patch(snapshot(), false);
    expect(target.type).toBe("button"); expect(target.textContent).toBe("처리 결과 다시 확인");
    expect(view.element.getAttribute("aria-label")).toBe("처리 결과 복구");
    const status = view.element.querySelector('[role="status"]');
    expect(status?.getAttribute("aria-live")).toBe("polite"); expect(status?.getAttribute("aria-atomic")).toBe("true");
    target.focus(); expect(document.activeElement).toBe(target);
    target.click(); target.click(); target.dispatchEvent(new Event("click"));
    expect([env.state.clicks, submissions, target.disabled]).toEqual([1, 0, true]);
    yield* view.patch(snapshot(), true);
    expect(view.element.getAttribute("aria-busy")).toBe("true");
    target.dispatchEvent(new Event("click")); expect(env.state.clicks).toBe(1);
    yield* view.patch(snapshot(), false);
    expect(view.element.hasAttribute("aria-busy")).toBe(false);
    target.click(); expect(env.state.clicks).toBe(2);
    yield* view.patch(snapshot(0), false);
    target.dispatchEvent(new Event("click")); expect(env.state.clicks).toBe(2);
  } finally { yield* Scope.close(scope, Exit.void); }
}));

it.effect("same-value patch preserves exact nodes, keyboard focus and DOM setter baseline", () => Effect.gen(function*() {
  const { element } = yield* host(); const env = environment(); const scope = yield* Scope.make();
  const view = yield* mountRecoveryStatus(element, () => {}).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom));
  const value = snapshot(1);
  try {
    yield* view.patch(value, false); const target = button(view.element); target.focus();
    const nodes = [...view.element.childNodes];
    const texts = nodes.map((node) => vi.spyOn(node, "textContent", "set"));
    const hidden = vi.spyOn(view.element, "hidden", "set"); const disabled = vi.spyOn(target, "disabled", "set");
    const attrs = vi.spyOn(view.element, "setAttribute"); const removes = vi.spyOn(view.element, "removeAttribute");
    try {
      for (let index = 0; index < 100; index++) yield* view.patch(value, false);
      expect([...view.element.childNodes]).toEqual(nodes); expect(document.activeElement).toBe(target);
      for (const setter of texts) expect(setter).not.toHaveBeenCalled();
      expect(hidden).not.toHaveBeenCalled(); expect(disabled).not.toHaveBeenCalled(); expect(attrs).not.toHaveBeenCalled(); expect(removes).not.toHaveBeenCalled();
    } finally { for (const setter of texts) setter.mockRestore(); hidden.mockRestore(); disabled.mockRestore(); attrs.mockRestore(); removes.mockRestore(); }
  } finally { yield* Scope.close(scope, Exit.void); }
}));

it.effect("disposed status hides actions and queued detached clicks cannot run after close", () => Effect.gen(function*() {
  const { element } = yield* host(); const env = environment(); const scope = yield* Scope.make();
  const view = yield* mountRecoveryStatus(element, () => { env.state.clicks++; }).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom));
  try {
    yield* view.patch(snapshot(1, { phase: "disposed", enumerationComplete: false, failure: projectError(new ProtocolError()) }), false);
    expect(view.element.hidden).toBe(true); expect(button(view.element).disabled).toBe(true);
    button(view.element).dispatchEvent(new Event("click")); expect(env.state.clicks).toBe(0);
    yield* view.patch(snapshot(), false);
    yield* view.close; yield* view.close;
    const saved = env.state.listeners[0]; if (saved === undefined) throw new Error("Acquired listener absent");
    saved(new Event("click")); button(view.element).dispatchEvent(new Event("click"));
    expect(env.state.clicks).toBe(0); expect(env.state.active).toBe(0);
    expect(yield* Effect.flip(view.patch(snapshot(), false))).toEqual(new ViewUnavailable({ reason: "closed" }));
  } finally { yield* Scope.close(scope, Exit.void); }
}));

for (const invalid of ["65", "nil"] as const) it.effect(`invalid ${invalid} producer data fails closed and cleans an already mounted view`, () => Effect.gen(function*() {
  const { element, unrelated } = yield* host(); const env = environment(); const scope = yield* Scope.make();
  const view = yield* mountRecoveryStatus(element, () => {}).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom));
  try {
    yield* view.patch(snapshot(), false);
    const value = invalid === "65" ? snapshot(65) : { ...snapshot(), pending: null } as unknown as RecoverySnapshot;
    expect(yield* Effect.flip(view.patch(value, false))).toBeInstanceOf(DomError);
    expect(view.element.isConnected).toBe(false); expect(element.childNodes).toHaveLength(1); expect(element.firstChild).toBe(unrelated); expect(env.state.active).toBe(0);
  } finally { yield* Scope.close(scope, Exit.void); }
}));

for (const call of [1, 3, 5, "continuous"] as const) it.effect(`element construction fault ${call} leaves no partial recovery mount`, () => Effect.gen(function*() {
  const { element, unrelated } = yield* host(); const env = environment(); const scope = yield* Scope.make(); let calls = 0;
  const original = document.createElement.bind(document); const create = vi.spyOn(document, "createElement").mockImplementation((tag, options) => {
    calls++; if (call === "continuous" || calls === call) throw new Error("injected create failure"); return original(tag, options);
  });
  try {
    for (let attempt = 0; attempt < (call === "continuous" ? 3 : 1); attempt++) expect(yield* Effect.flip(mountRecoveryStatus(element, () => {}).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom)))).toBeInstanceOf(DomError);
    expect(element.firstChild).toBe(unrelated); expect(element.childNodes).toHaveLength(1); expect(env.state.active).toBe(0); expect(scope.state._tag).toBe("Empty");
  } finally { create.mockRestore(); yield* Scope.close(scope, Exit.void); }
}));

for (const fault of [new Set([1]), new Set([3]), new Set([1, 2, 3])] as const) it.effect(`listener failures ${[...fault].join(",")} release first, Nth and continuous acquisitions`, () => Effect.gen(function*() {
  const { element, unrelated } = yield* host(); const env = environment(fault); const scope = yield* Scope.make();
  try {
    for (let attempt = 1; attempt <= 3; attempt++) {
      const result = yield* Effect.result(mountRecoveryStatus(element, () => {}).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom)));
      expect(result._tag).toBe(fault.has(attempt) ? "Failure" : "Success");
      if (result._tag === "Success") yield* result.success.close;
      expect(element.childNodes).toHaveLength(1); expect(element.firstChild).toBe(unrelated); expect(env.state.active).toBe(0); expect(scope.state._tag).toBe("Empty");
    }
    expect([env.state.acquired, env.state.released]).toEqual([3, 3]);
  } finally { yield* Scope.close(scope, Exit.void); }
}));

it.effect("append-after-insertion fault rolls back the root and its acquired listener", () => Effect.gen(function*() {
  const { element, unrelated } = yield* host(); const env = environment(); const scope = yield* Scope.make(); const original = element.append.bind(element);
  const append = vi.spyOn(element, "append").mockImplementation((...nodes) => { original(...nodes); throw new Error("partial append"); });
  try {
    expect(yield* Effect.flip(mountRecoveryStatus(element, () => {}).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom)))).toBeInstanceOf(DomError);
    expect(element.childNodes).toHaveLength(1); expect(element.firstChild).toBe(unrelated); expect(env.state.active).toBe(0); expect(scope.state._tag).toBe("Empty");
  } finally { append.mockRestore(); yield* Scope.close(scope, Exit.void); }
}));

it.effect("a patch fault after one successful write removes the whole view instead of leaving partial status", () => Effect.gen(function*() {
  const { element, unrelated } = yield* host(); const env = environment(); const scope = yield* Scope.make();
  const view = yield* mountRecoveryStatus(element, () => {}).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom));
  const counts = view.element.querySelectorAll("p")[1]; if (counts === undefined) throw new Error("Count paragraph absent");
  const write = vi.spyOn(counts, "textContent", "set").mockImplementation(() => { throw new Error("Nth patch failure"); });
  try {
    expect(yield* Effect.flip(view.patch(snapshot(), false))).toBeInstanceOf(DomError);
    expect(view.element.isConnected).toBe(false); expect(element.firstChild).toBe(unrelated); expect(element.childNodes).toHaveLength(1); expect(env.state.active).toBe(0);
    expect(yield* Effect.flip(view.patch(snapshot(), false))).toBeInstanceOf(ViewUnavailable);
  } finally { write.mockRestore(); yield* Scope.close(scope, Exit.void); }
}));

it.effect("one hundred mounts and patches return nodes, listeners, frames and parent Scope to baseline", () => Effect.gen(function*() {
  const { element, unrelated } = yield* host(); const env = environment(); const scope = yield* Scope.make();
  try {
    for (let index = 0; index < 100; index++) {
      const view = yield* mountRecoveryStatus(element, () => { env.state.clicks++; }).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom));
      yield* view.patch(snapshot(index % 2 === 0 ? 64 : 63), false); button(view.element).click();
      yield* view.patch(snapshot(1), true); yield* view.close; yield* view.close;
      button(view.element).dispatchEvent(new Event("click"));
      expect(element.childNodes).toHaveLength(1); expect(element.firstChild).toBe(unrelated); expect(env.state.active).toBe(0); expect(scope.state._tag).toBe("Empty");
    }
    expect([env.state.acquired, env.state.released, env.state.clicks, env.state.frames]).toEqual([100, 100, 100, 0]);
  } finally { yield* Scope.close(scope, Exit.void); }
}));

it.effect("closed parent cannot create or acquire a recovery view", () => Effect.gen(function*() {
  const { element } = yield* host(); const env = environment(); const scope = yield* Scope.make(); yield* Scope.close(scope, Exit.void);
  expect(yield* Effect.flip(mountRecoveryStatus(element, () => {}).pipe(Scope.provide(scope), Effect.provideService(DomPlatform, env.dom)))).toBeInstanceOf(ViewUnavailable);
  expect(env.state.acquired).toBe(0); expect(element.childNodes).toHaveLength(1);
}));