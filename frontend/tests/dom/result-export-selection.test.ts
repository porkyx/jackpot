import { expect, it, vi } from "@effect/vitest";
import { Deferred, Effect, Fiber } from "effect";
import type { Collection, Round } from "../../src/contracts/product";
import { mountExportProduct, type ProductDispatch } from "../../src/features/export/view";
import { ResultImageError, type ResultImage } from "../../src/platform/canvas";
import { DomError, DomPlatform, makeDomPlatform } from "../../src/platform/dom";

const now = "2026-10-06T01:00:00.000Z";
function target(roundChange: Partial<Round> = {}, collectionChange: Partial<Collection> = {}) {
  const round: Round = { collectionId: "collection", roundId: "round-1", number: 1, attempt: 1, state: "completed", revision: 1, roundVersion: 1, mode: "immediate", message: "", prizes: [{ id: "prize", name: "선물", count: 1 }], scheduledAt: null, executedAt: now, failureCode: null, winners: [{ participant: { id: "p1", nickname: "참가자", publicIdentifier: "uid", kind: "fixed", classification: "included", reason: "", included: true, commentCount: 1, previews: ["댓글"] }, prizeId: "prize", prizeName: "선물", slot: 1 }], algorithmVersion: "v1", appVersion: "0.1.0", ...roundChange };
  const collection: Collection = { collectionId: round.collectionId, revision: 1, article: { url: "https://gall.dcinside.com/board/view/?id=test&no=1", title: "로컬 추첨", galleryId: "test", galleryName: "갤러리", galleryKind: "G", number: "1", authorNickname: "작성자", authorIdentifier: "uid", postedAt: null }, snapshot: { snapshotId: "snapshot", collectedAt: now, complete: true, pages: 1, acceptedComments: 3, deletedComments: 0, unsupportedComments: 0 }, filters: { excludeAnonymous: false, excludeAuthor: true, excludeDcconOnly: false, timeCut: null, includeKeywords: [], excludeKeywords: [] }, participantCount: 3, selectedCount: 3, remainingCount: 2, rounds: [round], roundTotal: 1, roundOffset: 0, latestRound: round, ...collectionChange };
  return { collection, round };
}
type Target = ReturnType<typeof target> | null;
const png = new Blob(["png"], { type: "image/png" });
function setup(renderer?: typeof ResultImage.Service, urls?: Pick<typeof URL, "createObjectURL" | "revokeObjectURL">) {
  return Effect.gen(function*() {
    const root = yield* Effect.acquireRelease(Effect.sync(() => { const node = document.createElement("main"); document.body.append(node); return node; }), node => Effect.sync(() => node.remove()));
    let selected: Target = target();
    const state = { listeners: 0, reads: 0, created: 0, renders: 0, models: [] as string[], revoked: [] as string[], live: new Set<string>() };
    const native = makeDomPlatform({ request: () => 0, cancel: () => {} }, () => "visible");
    const dom: typeof DomPlatform.Service = { ...native, listen: (...args) => native.listen(...args).pipe(Effect.andThen(Effect.acquireRelease(Effect.sync(() => { state.listeners++; }), () => Effect.sync(() => { state.listeners--; })))) };
    const queue: Array<Effect.Effect<void, never, DomPlatform>> = [];
    const dispatch: ProductDispatch = effect => queue.push(effect);
    const objectUrls = urls ?? { createObjectURL: () => { const value = "blob:test-" + ++state.created; state.live.add(value); return value; }, revokeObjectURL: value => { state.revoked.push(value); state.live.delete(value); } };
    const imageRenderer = renderer ?? { renderPng: model => Effect.sync(() => { state.renders++; state.models.push(model.roundId); return png; }) };
    const view = yield* mountExportProduct(root, () => { state.reads++; return selected; }, dispatch, { savePng: () => Effect.succeed("saved"), copyText: () => Effect.void }, imageRenderer, objectUrls).pipe(Effect.provideService(DomPlatform, dom));
    const button = (text: string) => { const node = [...root.querySelectorAll("button")].find(node => node.textContent === text); if (node === undefined) throw Error("button " + text); return node; };
    const next = () => Effect.suspend(() => { const effect = queue.shift(); if (effect === undefined) throw Error("missing dispatch"); return effect.pipe(Effect.provideService(DomPlatform, dom), Effect.forkChild); });
    const render = () => Effect.gen(function*() { button("PNG 미리보기").click(); yield* Fiber.join(yield* next()); });
    return { root, view, state, button, next, render, image: root.querySelector("img")!, alert: root.querySelector<HTMLElement>("[role=alert]")!, set: (value: Target) => { selected = value; } };
  });
}

for (const [name, changed] of [
  ["round", target({ roundId: "round-2", number: 2 })],
  ["collection", target({ collectionId: "other-collection" })],
  ["version", target({ roundVersion: 2 })],
] as const) it.effect("preview selection " + name + " independently revokes the previous image before rendering another", () => Effect.gen(function*() {
  const h = yield* setup(); try { yield* h.render(); expect(h.image.getAttribute("src")).toBe("blob:test-1"); h.set(changed); h.view.value.patch(); expect(h.state.revoked).toEqual(["blob:test-1"]); expect(h.image.getAttribute("src")).toBeNull(); expect(h.image.hidden).toBe(true); expect(h.button("미리보기 닫기").hidden).toBe(true); yield* h.render(); expect(h.image.getAttribute("src")).toBe("blob:test-2"); expect(h.state.live.size).toBe(1); } finally { yield* h.view.close; } expect(h.state.live.size).toBe(0); expect(h.state.revoked).toEqual(["blob:test-1", "blob:test-2"]); expect(h.state.listeners).toBe(0);
}));
it.effect("preview same immutable round retains image node and URL through a newer collection revision", () => Effect.gen(function*() {
  const h = yield* setup(); try { yield* h.render(); const original = h.image; h.set(target({}, { revision: 2 })); h.view.value.patch(); h.view.value.patch(); expect(h.root.querySelector("img")).toBe(original); expect(original.getAttribute("src")).toBe("blob:test-1"); expect(h.state.revoked).toEqual([]); expect(h.state.renders).toBe(1); } finally { yield* h.view.close; } expect(h.state.live.size).toBe(0);
}));
for (const selected of [null, target({ state: "cancelled", winners: [], executedAt: null })]) it.effect("preview absent or noncompleted selection clears image and rejects synthetic export admission " + (selected === null ? "null" : "cancelled"), () => Effect.gen(function*() {
  const h = yield* setup(); try { yield* h.render(); h.set(selected); h.view.value.patch(); expect(h.state.revoked).toEqual(["blob:test-1"]); expect(h.image.getAttribute("src")).toBeNull(); for (const label of ["PNG 미리보기", "PNG 사진 저장", "결과 텍스트 복사"]) { expect(h.button(label).disabled).toBe(true); h.button(label).dispatchEvent(new Event("click")); yield* Fiber.join(yield* h.next()); } expect(h.state.renders).toBe(1); expect(h.state.created).toBe(1); } finally { yield* h.view.close; } expect(h.state.listeners).toBe(0);
}));
for (const order of ["B", "B-to-A", "unpatched-B"] as const) it.effect("preview late render cannot attach after selection " + order, () => Effect.gen(function*() {
  const started = yield* Deferred.make<void>(); const release = yield* Deferred.make<Blob, ResultImageError>(); let calls = 0;
  const h = yield* setup({ renderPng: () => ++calls === 1 ? Deferred.succeed(started, undefined).pipe(Effect.andThen(Deferred.await(release))) : Effect.succeed(png) });
  try { h.button("PNG 미리보기").click(); const first = yield* h.next(); yield* Deferred.await(started); h.set(target({ roundId: "round-2", number: 2 })); if (order !== "unpatched-B") h.view.value.patch(); if (order === "B-to-A") { h.set(target()); h.view.value.patch(); } yield* Deferred.succeed(release, png); yield* Fiber.join(first); expect(h.state.created).toBe(0); expect(h.image.getAttribute("src")).toBeNull(); expect(h.alert.hidden).toBe(true); expect(h.button("PNG 미리보기").disabled).toBe(false); yield* h.render(); expect(h.state.created).toBe(1); expect(h.image.hidden).toBe(false); } finally { yield* h.view.close; } expect(h.state.live.size).toBe(0);
}));
it.effect("preview close during replacement render prevents the late result from reopening", () => Effect.gen(function*() {
  const started = yield* Deferred.make<void>(); const release = yield* Deferred.make<Blob>(); let calls = 0;
  const h = yield* setup({ renderPng: () => ++calls === 1 ? Effect.succeed(png) : Deferred.succeed(started, undefined).pipe(Effect.andThen(Deferred.await(release))) });
  try { yield* h.render(); h.button("PNG 미리보기").click(); const second = yield* h.next(); yield* Deferred.await(started); h.button("미리보기 닫기").click(); expect(h.state.revoked).toEqual(["blob:test-1"]); yield* Deferred.succeed(release, png); yield* Fiber.join(second); expect(h.state.created).toBe(1); expect(h.image.getAttribute("src")).toBeNull(); expect(h.button("미리보기 닫기").hidden).toBe(true); } finally { yield* h.view.close; } expect(h.state.revoked).toEqual(["blob:test-1"]);
}));
it.effect("preview scope close cancels owned render and retained patch cannot query or attach after close", () => Effect.gen(function*() {
  const started = yield* Deferred.make<void>(); let active = 0, cancelled = 0; let late: (effect: Effect.Effect<Blob, ResultImageError>) => void = () => {};
  const h = yield* setup({ renderPng: () => Effect.callback<Blob, ResultImageError>(resume => { late = resume; active++; Deferred.doneUnsafe(started, Effect.void); return Effect.sync(() => { active--; cancelled++; }); }) });
  h.button("PNG 미리보기").click(); const fiber = yield* h.next(); yield* Deferred.await(started); yield* h.view.close; expect((yield* Fiber.await(fiber))._tag).toBe("Failure"); const reads = h.state.reads; h.view.value.patch(); late(Effect.succeed(png)); yield* h.view.close;
  expect([active, cancelled, h.state.listeners, h.state.created, h.root.childElementCount, h.state.reads]).toEqual([0, 1, 0, 0, 0, reads]); expect(h.image.getAttribute("src")).toBeNull();
}));
for (const patched of [true, false]) it.effect("preview obsolete render failure does not publish an alert for the new selection patched=" + patched, () => Effect.gen(function*() {
  const started = yield* Deferred.make<void>(); const release = yield* Deferred.make<Blob, ResultImageError>(); let calls = 0;
  const h = yield* setup({ renderPng: () => ++calls === 1 ? Deferred.succeed(started, undefined).pipe(Effect.andThen(Deferred.await(release))) : Effect.succeed(png) });
  try { h.button("PNG 미리보기").click(); const first = yield* h.next(); yield* Deferred.await(started); h.set(target({ roundId: "round-2" })); if (patched) h.view.value.patch(); yield* Deferred.fail(release, new ResultImageError({ reason: "font" })); yield* Fiber.join(first); expect(h.alert.hidden).toBe(true); expect(h.state.created).toBe(0); yield* h.render(); expect(h.image.hidden).toBe(false); } finally { yield* h.view.close; } expect(h.state.live.size).toBe(0);
}));
for (const failures of [[1], [2], [1, 2, 3]] as ReadonlyArray<ReadonlyArray<number>>) it.effect("preview renderer failures " + failures.join(",") + " release admission and revoke retained image", () => Effect.gen(function*() {
  let calls = 0; const h = yield* setup({ renderPng: () => failures.includes(++calls) ? Effect.fail(new ResultImageError({ reason: "encoding" })) : Effect.succeed(png) });
  try { for (let index = 1; index <= 4; index++) { yield* h.render(); expect(h.alert.hidden).toBe(!failures.includes(index)); expect(h.button("PNG 미리보기").disabled).toBe(false); expect(h.image.hidden).toBe(failures.includes(index)); expect(h.state.live.size).toBe(failures.includes(index) ? 0 : 1); } } finally { yield* h.view.close; } expect(h.state.live.size).toBe(0); expect(h.state.listeners).toBe(0);
}));
for (const failures of [[1], [2], [1, 2, 3]] as ReadonlyArray<ReadonlyArray<number>>) it.effect("preview URL allocation failures " + failures.join(",") + " report safely and leave no retained URL", () => Effect.gen(function*() {
  let calls = 0; const live = new Set<string>(); const h = yield* setup(undefined, { createObjectURL: () => { if (failures.includes(++calls)) throw Error("private-path-not-for-display"); const url = "blob:owned-" + calls; live.add(url); return url; }, revokeObjectURL: url => { expect(live.delete(url)).toBe(true); } });
  try { for (let index = 1; index <= 4; index++) { yield* h.render(); expect(h.alert.hidden).toBe(!failures.includes(index)); expect(h.button("PNG 미리보기").disabled).toBe(false); expect(live.size).toBe(failures.includes(index) ? 0 : 1); expect(h.root.textContent).not.toContain("private-path"); } } finally { yield* h.view.close; } expect(live.size).toBe(0); expect(h.state.listeners).toBe(0);
}));
it.effect("preview src assignment failure revokes the newly allocated URL and permits a fresh attempt", () => Effect.gen(function*() {
  const h = yield* setup(); const setSrc = vi.spyOn(h.image, "src", "set").mockImplementation(() => { throw Error("private-src-assignment"); });
  try { yield* h.render(); expect(h.state.revoked).toEqual(["blob:test-1"]); expect(h.state.live.size).toBe(0); expect(h.image.getAttribute("src")).toBeNull(); expect(h.alert.hidden).toBe(false); expect(h.root.textContent).not.toContain("private-src"); expect(h.button("PNG 미리보기").disabled).toBe(false); setSrc.mockRestore(); yield* h.render(); expect(h.image.getAttribute("src")).toBe("blob:test-2"); } finally { setSrc.mockRestore(); yield* h.view.close; } expect(h.state.live.size).toBe(0);
}));
it.effect("preview invalid foreign projection does not reach renderer or object URL allocation", () => Effect.gen(function*() {
  const h = yield* setup(); try { h.set(target({}, { collectionId: "foreign" })); h.view.value.patch(); yield* h.render(); expect([h.state.renders, h.state.created]).toEqual([0, 0]); expect(h.alert.hidden).toBe(false); expect(h.image.hidden).toBe(true); } finally { yield* h.view.close; } expect(h.state.listeners).toBe(0);
}));
for (const failAt of [1, 2, 4, "continuous"] as const) it.effect("preview partial mount listener failure " + failAt + " rolls back all acquired resources", () => Effect.gen(function*() {
  const root = yield* Effect.acquireRelease(Effect.sync(() => { const node = document.createElement("main"); document.body.append(node); const previous = document.createElement("p"); previous.textContent = "이전 화면"; node.append(previous); return node; }), node => Effect.sync(() => node.remove()));
  let calls = 0, listeners = 0, selectedReads = 0, urls = 0;
  const native = makeDomPlatform({ request: () => 0, cancel: () => {} }, () => "visible");
  const dom: typeof DomPlatform.Service = { ...native, listen: (...args) => Effect.suspend(() => { const fail = ++calls === failAt || failAt === "continuous"; return native.listen(...args).pipe(Effect.andThen(Effect.acquireRelease(Effect.sync(() => { listeners++; }), () => Effect.sync(() => { listeners--; }))), Effect.andThen(fail ? Effect.fail(new DomError()) : Effect.void)); }) };
  const exit = yield* mountExportProduct(root, () => { selectedReads++; return target(); }, () => {}, undefined, { renderPng: () => Effect.succeed(png) }, { createObjectURL: () => { urls++; return "blob:unused"; }, revokeObjectURL: () => { urls--; } }).pipe(Effect.provideService(DomPlatform, dom), Effect.exit);
  expect(exit._tag).toBe("Failure"); expect([listeners, selectedReads, urls, root.childElementCount]).toEqual([0, 0, 0, 1]); expect(root.textContent).toBe("이전 화면");
}));
