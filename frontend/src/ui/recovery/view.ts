import { Effect, Exit, Scope } from "effect";
import type { RecoverySnapshot } from "../../operations/pendingRecovery";
import { DomError, DomPlatform } from "../../platform/dom";
import { mountView, patchAria, patchDisabled, patchText, ViewUnavailable } from "../view";

export interface RecoveryStatusView {
  readonly element: HTMLElement;
  readonly patch: (snapshot: RecoverySnapshot, checking: boolean) => Effect.Effect<void, DomError | ViewUnavailable>;
  readonly close: Effect.Effect<void>;
}

// Only the click latch is retained. Durable IDs, observations and errors remain
// in the app owner; this view derives fixed public text from each snapshot.
export function mountRecoveryStatus(host: HTMLElement, onRefresh: () => void): Effect.Effect<RecoveryStatusView, DomError | ViewUnavailable, DomPlatform | Scope.Scope> {
  return Effect.gen(function*() {
  const owner = yield* Scope.fork(yield* Scope.Scope, "sequential");
  const build = Effect.gen(function*() {
    const dom = yield* DomPlatform;
    const scope = yield* Scope.Scope;
    const nodes = yield* Effect.try({ try: () => {
      const element = host.ownerDocument.createElement("section");
      const status = host.ownerDocument.createElement("p");
      const counts = host.ownerDocument.createElement("p");
      const reads = host.ownerDocument.createElement("p");
      const button = host.ownerDocument.createElement("button");
      element.className = "recovery-status";
      element.setAttribute("aria-label", "처리 결과 복구");
      status.setAttribute("role", "status");
      status.setAttribute("aria-live", "polite");
      status.setAttribute("aria-atomic", "true");
      status.textContent = "처리 결과를 확인하고 있습니다.";
      button.type = "button";
      button.textContent = "처리 결과 다시 확인";
      button.disabled = true;
      element.append(status, counts, reads, button);
      return { element, status, counts, reads, button };
    }, catch: () => new DomError() });
    let blocked = true;
    yield* Scope.addFinalizer(scope, Effect.sync(() => { blocked = true; }));
    yield* dom.listen(nodes.button, "click", () => {
      if (blocked) return;
      blocked = true;
      patchDisabled(nodes.button, true);
      onRefresh();
    });
    const patch: RecoveryStatusView["patch"] = (snapshot, checking) => Effect.try({ try: () => {
      if (scope.state._tag === "Closed") throw new ViewUnavailable({ reason: "closed" });
      const count = snapshot.pending.length;
      if (count > 64) throw new DomError();
      const unknown = snapshot.pending.filter((entry) => entry.observation?.state === "unknown").length;
      const visible = snapshot.phase !== "disposed" && (count > 0 || !snapshot.enumerationComplete || snapshot.failure !== null);
      const saturated = snapshot.phase === "saturated" || count === 64;
      const status = snapshot.failure !== null ? "처리 결과를 확인하지 못했습니다. 다시 확인해 주세요."
        : saturated ? "복구 대기 한도 도달. 처리 결과를 다시 확인하면 복구를 이어갑니다."
        : !snapshot.enumerationComplete ? "이전 작업의 처리 결과를 확인하고 있습니다. 확인이 끝날 때까지 새 변경을 기다려 주세요."
        : count > 0 ? "아직 처리 결과가 확인되지 않은 작업이 있습니다."
        : "확인 대기 중인 작업이 없습니다.";
      const countText = `확인 대기 ${count}개${unknown > 0 ? ` · 결과 미확인 ${unknown}개` : ""}`;
      blocked = checking || !snapshot.readsAllowed || !visible;
      patchText(nodes.status, status);
      patchText(nodes.counts, countText);
      patchText(nodes.reads, snapshot.readsAllowed ? "결과 조회는 계속 이용할 수 있습니다." : "현재 결과 확인을 사용할 수 없습니다.");
      patchAria(nodes.element, "aria-busy", checking ? "true" : null);
      patchDisabled(nodes.button, blocked);
      if (nodes.element.hidden !== !visible) nodes.element.hidden = !visible;
    }, catch: (error) => error instanceof ViewUnavailable ? error : new DomError() }).pipe(Effect.onError(() => Scope.close(owner, Exit.void)));
    return { element: nodes.element, value: { patch } };
  });
  return yield* mountView(host, build).pipe(Scope.provide(owner), Effect.onError(() => Scope.close(owner, Exit.void)), Effect.map((mounted) => ({ element: mounted.element, patch: mounted.value.patch, close: Scope.close(owner, Exit.void) })));
  });
}