import { expect, it } from "@effect/vitest";
import { Deferred, Effect, Exit, Fiber, Scope } from "effect";
import { mountCreateProduct } from "../../src/features/create/view";
import { ProductUnavailable } from "../../src/operations/productCoordinator";
import { DomPlatform, makeDomPlatform } from "../../src/platform/dom";
import { ClientIds, clientIdsFrom } from "../../src/platform/ids";
import { makeCreateWorkspace } from "../../src/screens/create/productOwner";
import { commandHarness } from "../helpers/productCommands";

function setup() {
  return Effect.gen(function*() {
    const app = yield* Scope.make(); const route = yield* Scope.fork(app, "sequential"); const port = yield* commandHarness();
    const tasks: Array<Effect.Effect<void>> = []; const workspace = yield* makeCreateWorkspace(port.commands, effect => { tasks.push(effect); }).pipe(Scope.provide(app), Effect.provideService(ClientIds, clientIdsFrom(() => "unused-filter-prize")));
    let token = 0, listeners = 0; const frames = new Map<number, FrameRequestCallback>();
    const native = makeDomPlatform({ request: callback => { frames.set(++token, callback); return token; }, cancel: id => { frames.delete(id); } }, () => "visible");
    const dom: typeof DomPlatform.Service = { ...native, listen: (...args) => native.listen(...args).pipe(Effect.andThen(Effect.acquireRelease(Effect.sync(() => { listeners++; }), () => Effect.sync(() => { listeners--; })))) };
    const host = document.createElement("main"); document.body.append(host);
    yield* mountCreateProduct(host, port.commands, workspace, () => {}).pipe(Scope.provide(route), Effect.provideService(DomPlatform, dom));
    const settle = Effect.gen(function*() {
      for (let turn = 0; turn < 12; turn++) {
        for (const task of tasks.splice(0)) yield* task.pipe(Effect.forkIn(app));
        for (let tick = 0; tick < 20; tick++) yield* Effect.yieldNow;
        const waiting = [...frames.values()]; frames.clear(); for (const frame of waiting) frame(0);
      }
    });
    const close = Scope.close(app, Exit.void).pipe(Effect.andThen(port.close), Effect.andThen(Effect.sync(() => { tasks.length = 0; host.remove(); })));
    return { app, route, port, workspace, host, settle, close, resources: () => ({ listeners, frames: frames.size }) };
  });
}
function button(host: HTMLElement) { const result = [...host.querySelectorAll("button")].find(value => value.textContent === "필터 다시 적용"); if (result === undefined) throw Error("Retry button is missing"); return result; }
function field(host: HTMLElement, text: string) { const label = [...host.querySelectorAll("label")].find(value => value.textContent === text); if (label === undefined) throw Error("Field label is missing"); const input = host.querySelector<HTMLInputElement>("#" + label.htmlFor); if (input === null) throw Error("Native input is missing"); return input; }

it.effect("failed filter alert survives unrelated prize success and explicit native retry confirms exact raw", () => Effect.gen(function*() {
  const h = yield* setup(); const original = h.port.commands.edit; let blocked = true, attempts = 0;
  Object.assign(h.port.commands, { edit: (intent: Parameters<typeof original>[0], key?: string) => intent.kind === "UpdateFilters" ? Effect.suspend(() => { attempts++; return blocked ? Effect.fail(new ProductUnavailable({ reason: "blocked" })) : original(intent, key); }) : original(intent, key) });
  try {
    yield* h.settle; const retry = button(h.host); expect(retry.type).toBe("button"); expect(retry.hidden).toBe(true);
    const accordion=h.host.querySelector<HTMLDetailsElement>("details[data-filter-accordion]")!;const summary=accordion.querySelector("summary")!;expect(accordion.open).toBe(false);
    const author = field(h.host, "글쓴이 제외"); author.checked = false; author.dispatchEvent(new Event("change")); yield* h.settle;
    expect(retry.hidden).toBe(false); expect(author.getAttribute("aria-invalid")).toBe("true");
    const notice=h.host.querySelector<HTMLElement>("[data-filter-failure]")!;expect(accordion.open).toBe(true);expect(accordion.contains(notice)).toBe(false);expect(summary.getAttribute("aria-describedby")).toBe(notice.id);expect(summary.textContent).toContain("확인 필요");summary.click();expect(accordion.open).toBe(false);
    const errorId = author.getAttribute("aria-describedby"); expect(errorId).not.toBeNull(); expect(h.host.querySelector("#" + errorId)?.textContent).toContain("필터 다시 적용");
    const count = field(h.host, "당첨 인원"); count.value = "2"; count.dispatchEvent(new Event("input")); count.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", cancelable: true })); yield* h.settle;
    expect(h.workspace.read().error).toBeNull(); expect(h.host.querySelector<HTMLElement>("[data-filter-failure]")?.hidden).toBe(false);expect(accordion.open).toBe(false);
    expect(h.host.textContent).toContain("참가자 분류는 마지막 확인 값을 표시합니다."); expect(author.checked).toBe(false); expect(h.workspace.read().draft?.filters.excludeAuthor).toBe(true);
    h.workspace.setWritesBlocked(true); yield* h.settle; expect(retry.disabled).toBe(true); retry.dispatchEvent(new Event("click")); yield* h.settle; expect(attempts).toBe(1);
    blocked = false; h.workspace.setWritesBlocked(false); yield* h.settle; expect(attempts).toBe(1); expect(retry.disabled).toBe(false);
    retry.click(); yield* h.settle; expect(attempts).toBe(2); expect(h.workspace.read().draft?.filters.excludeAuthor).toBe(false);
    expect(retry.hidden).toBe(true); expect(author.getAttribute("aria-invalid")).toBeNull(); expect(author.getAttribute("aria-describedby")).toBeNull();
    expect(h.host.querySelector<HTMLElement>("[data-filter-failure]")?.hidden).toBe(true);expect(summary.getAttribute("aria-describedby")).toBeNull();expect(summary.textContent).toContain("선택 설정");expect(accordion.open).toBe(false); expect(h.port.state.edits.map(intent => intent.kind)).toEqual(["SetPrizes", "UpdateFilters"]);
  } finally { yield* h.close; }
  expect(h.resources()).toEqual({ listeners: 0, frames: 0 });
}));

it.effect("blocked writes still permit receipt-only confirmation and cannot duplicate an unknown filter mutation", () => Effect.gen(function*() {
  const h = yield* setup(); const original = h.port.commands.commitThrough; let checks = 0;
  Object.assign(h.port.commands, { commitThrough: (receipt: Parameters<typeof original>[0]) => ++checks === 1 ? Effect.fail(new ProductUnavailable({ reason: "outcome_unknown" })) : original(receipt) });
  try {
    yield* h.settle; const author = field(h.host, "글쓴이 제외"); author.checked = false; author.dispatchEvent(new Event("change")); yield* h.settle;
    expect(h.port.state.edits).toHaveLength(1); expect(checks).toBe(1); h.workspace.setWritesBlocked(true); yield* h.settle;
    const retry = button(h.host); expect(author.disabled).toBe(true); expect(retry.hidden).toBe(false); expect(retry.disabled).toBe(false);
    retry.click(); yield* h.settle; expect(checks).toBe(2); expect(h.port.state.edits).toHaveLength(1); expect(h.workspace.read().draft?.filters.excludeAuthor).toBe(false);
    expect(retry.hidden).toBe(true); expect(h.workspace.read().writesBlocked).toBe(true);
  } finally { yield* h.close; }
  expect(h.resources()).toEqual({ listeners: 0, frames: 0 });
}));

it.effect("a blocked mixed group with one unsent version cannot use the receipt-only retry allowance", () => Effect.gen(function*() {
  const h = yield* setup(); let checks = 0;
  Object.assign(h.port.commands, { commitThrough: () => { checks++; return Effect.fail(new ProductUnavailable({ reason: "outcome_unknown" })); } });
  try {
    yield* h.settle; const author = field(h.host, "글쓴이 제외"); author.checked = false; author.dispatchEvent(new Event("change")); yield* h.settle;
    h.workspace.input({ _tag: "FilterToggleChanged", field: "excludeAnonymous", raw: true }); h.workspace.setWritesBlocked(true); yield* h.settle;
    const retry = button(h.host); expect(retry.hidden).toBe(false); expect(retry.disabled).toBe(true); retry.dispatchEvent(new Event("click")); yield* h.settle;
    expect(h.port.state.edits).toHaveLength(1); expect(checks).toBe(1); expect(h.workspace.read().editor?.filters.excludeAnonymous.dirty).toBe(true);
  } finally { yield* h.close; }
  expect(h.resources()).toEqual({ listeners: 0, frames: 0 });
}));

it.effect("duplicate explicit retry waits one confirmation and close cleans listeners frames and pending ownership", () => Effect.gen(function*() {
  const h = yield* setup(); const original = h.port.commands.commitThrough; const entered = yield* Deferred.make<void>(); const release = yield* Deferred.make<void>(); let checks = 0;
  Object.assign(h.port.commands, { commitThrough: (receipt: Parameters<typeof original>[0]) => ++checks === 1 ? Effect.fail(new ProductUnavailable({ reason: "outcome_unknown" })) : Deferred.succeed(entered, undefined).pipe(Effect.andThen(Deferred.await(release)), Effect.andThen(original(receipt))) });
  try {
    yield* h.settle; const author = field(h.host, "글쓴이 제외"); author.checked = false; author.dispatchEvent(new Event("change")); yield* h.settle;
    const retry = button(h.host); retry.click(); retry.dispatchEvent(new Event("click")); yield* h.settle; yield* Deferred.await(entered);
    expect(checks).toBe(2); expect(h.port.state.edits).toHaveLength(1); yield* Deferred.succeed(release, undefined); yield* h.settle;
    expect(checks).toBe(2); expect(h.port.state.edits).toHaveLength(1); expect(retry.hidden).toBe(true);
    yield* Scope.close(h.route, Exit.void); expect(h.resources()).toEqual({ listeners: 0, frames: 0 }); expect(h.host.children).toHaveLength(0);
    retry.dispatchEvent(new Event("click")); yield* h.settle; expect(checks).toBe(2);
  } finally { yield* h.close; }
  expect(h.resources()).toEqual({ listeners: 0, frames: 0 });
}));
it.effect("local invalid cutoff asks for input correction and never offers unconfirmed-operation retry", () => Effect.gen(function*() {
  const h = yield* setup();
  try {
    yield* h.settle; const cutoff = field(h.host, "시간컷 사용"); cutoff.checked = true; cutoff.dispatchEvent(new Event("change")); yield* h.settle;
    expect(h.port.state.edits).toHaveLength(0); expect(button(h.host).hidden).toBe(true); expect(button(h.host).disabled).toBe(true);
    expect(h.host.querySelector("[data-filter-failure]")?.textContent).toContain("입력값을 수정");
    const accordion=h.host.querySelector<HTMLDetailsElement>("details[data-filter-accordion]")!;expect(accordion.open).toBe(true);expect(accordion.contains(h.host.querySelector("[data-filter-failure]"))).toBe(false);
    expect(h.host.querySelector("#" + cutoff.getAttribute("aria-describedby"))?.textContent).toBe("필터 입력값을 수정해 주세요.");
    cutoff.checked = false; cutoff.dispatchEvent(new Event("change")); yield* h.settle;
    expect(h.port.state.edits).toHaveLength(1); expect(h.host.querySelector<HTMLElement>("[data-filter-failure]")?.hidden).toBe(true); expect(cutoff.getAttribute("aria-invalid")).toBeNull();
  } finally { yield* h.close; }
  expect(h.resources()).toEqual({ listeners: 0, frames: 0 });
}));

it.effect("a local invalid value takes precedence over an older unconfirmed filter and prevents retry", () => Effect.gen(function*() {
  const h = yield* setup(); h.port.state.failEdit = 1;
  try {
    yield* h.settle; const author = field(h.host, "글쓴이 제외"); author.checked = false; author.dispatchEvent(new Event("change")); yield* h.settle;
    expect(button(h.host).hidden).toBe(false);
    const cutoff = field(h.host, "시간컷 사용"); cutoff.checked = true; cutoff.dispatchEvent(new Event("change")); yield* h.settle;
    expect(button(h.host).hidden).toBe(true); expect(button(h.host).disabled).toBe(true); expect(h.host.querySelector("[data-filter-failure]")?.textContent).toContain("입력값을 수정");
    expect(h.port.state.edits).toHaveLength(1); button(h.host).dispatchEvent(new Event("click")); yield* h.settle; expect(h.port.state.edits).toHaveLength(1);
  } finally { yield* h.close; }
  expect(h.resources()).toEqual({ listeners: 0, frames: 0 });
}));