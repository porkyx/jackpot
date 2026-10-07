import { Data, Effect, Fiber, Scope } from "effect";
import { ResultImageError, resultExportText, resultImageForDocument, type ResultExportModel } from "../../platform/canvas";
import { DomPlatform } from "../../platform/dom";
import type { Collection, Round } from "../../contracts/product";
import { mountView, patchText } from "../../ui/view";

export type ProductDispatch = (effect: Effect.Effect<void, never, DomPlatform>) => void;
export class ResultExportFailure extends Data.TaggedError("ResultExportFailure")<{
  readonly reason: "cancelled" | "permission" | "clipboard" | "encoding" | "size" | "unavailable";
}> {}
export interface ResultExportPorts {
  readonly savePng: (blob: Blob, suggestedName: string) => Effect.Effect<"saved" | "cancelled", ResultExportFailure>;
  readonly copyText: (text: string) => Effect.Effect<void, ResultExportFailure>;
  readonly copyPng?: (blob: Blob) => Effect.Effect<void, ResultExportFailure>;
}
export function projectResultExport(collection: Collection, round: Round): ResultExportModel {
  if (round.collectionId !== collection.collectionId || round.state !== "completed" || round.executedAt === null || round.prizes.length === 0 || round.winners.length !== round.prizes.reduce((sum,prize)=>sum+prize.count,0) || round.prizes.some((prize)=>round.winners.filter((winner)=>winner.prizeId===prize.id).length!==prize.count)) throw new ResultImageError({ reason: "invalid-model" });
  return Object.freeze({
    collectionId: collection.collectionId, roundId: round.roundId, roundNumber: round.number,
    title: collection.article.title, galleryName: collection.article.galleryName, articleUrl: collection.article.url,
    participantCount: collection.selectedCount,
    prizes: Object.freeze(round.prizes.map((prize) => Object.freeze({
      prizeId: prize.id, name: prize.name,
      winners: Object.freeze(round.winners.filter((winner) => winner.prizeId === prize.id).map((winner) => Object.freeze({
        participantId: winner.participant.id, nickname: winner.participant.nickname,
        publicIdentifier: winner.participant.publicIdentifier,
        participantKind: winner.participant.kind === "fixed" ? "registered" as const : winner.participant.kind === "semi_fixed" ? "semi-registered" as const : "anonymous" as const,
      }))),
    }))),
    message: round.message, executedAt: round.executedAt, scheduledAt: round.scheduledAt,
    delayed: round.scheduledAt !== null && Date.parse(round.executedAt) > Date.parse(round.scheduledAt) + 1000,
  });
}
export function mountExportProduct(
  host: HTMLElement, selected: () => { readonly collection: Collection; readonly round: Round } | null,
  dispatch: ProductDispatch, ports?: ResultExportPorts,
  renderer = resultImageForDocument(host.ownerDocument),
  objectUrls: Pick<typeof URL, "createObjectURL" | "revokeObjectURL"> = URL,
) {
  return mountView(host, Effect.gen(function*() {
    const scope = yield* Scope.Scope;
    const dom = yield* DomPlatform;
    const doc = host.ownerDocument;
    const element = doc.createElement("section"); element.className = "result-share app-card"; element.setAttribute("aria-label", "결과 공유");
    const heading = doc.createElement("h3"); heading.textContent = "결과 공유";
    const actions = doc.createElement("div"); actions.className = "result-share-actions ui-actions";
    const save = doc.createElement("button"); save.className = "ui-button-primary"; save.type = "button"; save.textContent = "PNG 사진 저장";
    const copy = doc.createElement("button"); copy.type = "button"; copy.textContent = "결과 텍스트 복사";
    const preview = doc.createElement("button"); preview.type = "button"; preview.textContent = "PNG 미리보기";
    const close = doc.createElement("button"); close.className = "ui-button-ghost"; close.type = "button"; close.textContent = "미리보기 닫기"; close.hidden = true;
    const image = doc.createElement("img"); image.className = "result-preview"; image.alt = "Jackpot 로컬 추첨 결과 미리보기"; image.hidden = true; image.style.maxWidth = "100%";
    const status = doc.createElement("p"); status.setAttribute("role", "status");
    const error = doc.createElement("p"); error.setAttribute("role", "alert"); error.hidden = true;
    actions.append(save, copy, preview, close);
    element.append(heading, actions, status, error, image);
    let pending = false; let alive = true; let url: string | undefined;
    let selectedKey: string | undefined; let previewEpoch = 0;
    const revoke = () => { previewEpoch++; if (url !== undefined) { objectUrls.revokeObjectURL(url); url = undefined; } image.removeAttribute("src"); image.hidden = true; close.hidden = true; };
    yield* Effect.addFinalizer(() => Effect.sync(() => { alive = false; revoke(); }));
    const patch = () => {
      if (!alive) return;
      const target = selected();
      const ready = target?.round.state === "completed";
      const nextKey = ready ? JSON.stringify([target.collection.collectionId, target.round.roundId, target.round.roundVersion]) : undefined;
      if (nextKey !== selectedKey) { selectedKey = nextKey; revoke(); }
      save.disabled = !ready || pending || ports === undefined;
      copy.disabled = !ready || pending || ports === undefined;
      preview.disabled = !ready || pending;
      if (ports === undefined && ready) patchText(status, "사진 저장·텍스트 복사는 OS 연결이 필요합니다.");
    };
    const action = (kind: "save" | "copy" | "preview") => Effect.suspend(() => {
      let admitted = false; let requestEpoch: number | undefined;
      return Effect.gen(function*() {
      if (!alive || pending) return;
      const target = selected(); if (target === null || target.round.state !== "completed") return;
      pending = true; admitted = true; patch(); requestEpoch = previewEpoch; error.hidden = true;
      const model = yield* Effect.try({ try: () => projectResultExport(target.collection, target.round), catch: () => new ResultImageError({ reason: "invalid-model" }) });
      if (kind === "copy") {
        if (ports === undefined) return yield* Effect.fail(new ResultExportFailure({ reason: "unavailable" }));
        const text = yield* Effect.try({ try: () => resultExportText(model), catch: () => new ResultImageError({ reason: "invalid-model" }) });
        yield* ports.copyText(text);
        if (alive) patchText(status, "결과 텍스트를 복사했습니다.");
      } else {
        const blob = yield* renderer.renderPng(model);
        if (kind === "save") {
          if (ports === undefined) return yield* Effect.fail(new ResultExportFailure({ reason: "unavailable" }));
          const result = yield* ports.savePng(blob, `jackpot_${model.collectionId}_${model.roundNumber}.png`);
          if (alive) patchText(status, result === "cancelled" ? "사진 저장을 취소했습니다." : "PNG 사진을 저장했습니다.");
        } else if (alive) {
          patch();
          if (requestEpoch !== previewEpoch) return;
          revoke(); requestEpoch = previewEpoch;
          yield* Effect.try({ try: () => {
            url = objectUrls.createObjectURL(blob);
            if (url === undefined) throw new ResultExportFailure({ reason: "encoding" });
            image.src = url; image.hidden = false; close.hidden = false;
          }, catch: () => new ResultExportFailure({ reason: "encoding" }) });
          patchText(status, "완료 회차의 로컬 결과 미리보기입니다.");
        }
      }
    }).pipe(Effect.catch((failure) => Effect.sync(() => {
      if (!alive) return;
      if (kind === "preview") { patch(); if (requestEpoch !== undefined && requestEpoch !== previewEpoch) return; }
      revoke();
      if (failure instanceof ResultExportFailure && failure.reason === "cancelled") { patchText(status, "사진 저장을 취소했습니다."); return; }
      patchText(error, failure instanceof ResultImageError && (failure.reason === "dimensions" || failure.reason === "size")
        ? "사진 출력 한도를 초과했습니다. 결과 텍스트 복사를 사용해 주세요." : "결과 공유에 실패했습니다. 저장된 추첨 결과는 유지됩니다.");
      error.hidden = false;
    })), Effect.ensuring(Effect.sync(() => { if (admitted) { pending = false; if (alive) patch(); } })));
    });
    for (const [button, kind] of [[save, "save"], [copy, "copy"], [preview, "preview"]] as const) yield* dom.listen(button, "click", () => {
      dispatch(action(kind).pipe(Effect.forkIn(scope), Effect.flatMap(Fiber.join), Effect.ignore));
    });
    yield* dom.listen(close, "click", revoke);
    patch();
    return { element, value: { patch } };
  }));
}
