import { Deferred, Effect, Exit, Fiber, Scope } from "effect";
import { DomError, DomPlatform } from "../../platform/dom";
import { mountView, ViewUnavailable, type View } from "../view";

export interface DialogContents {
  readonly title: HTMLElement;
  readonly initialFocus: HTMLElement;
  readonly sensitiveInputs: ReadonlyArray<HTMLInputElement>;
  // Release caller locks and commit opener eligibility before native focus return.
  readonly beforeFocusRestore?: Effect.Effect<void>;
  readonly restoreFocus?: HTMLElement;
}
export interface DialogView extends View<DialogContents> {
  readonly element: HTMLDialogElement;
}
export type DialogEnd = "close" | "cancel" | "disposed";
export interface DialogSession {
  readonly closed: Effect.Effect<DialogEnd, DomError>;
  readonly close: Effect.Effect<DialogEnd, DomError>;
}

export function mountDialog<E, R>(host: HTMLElement, build: Effect.Effect<DialogView, E, R | Scope.Scope>): Effect.Effect<DialogSession, E | DomError | ViewUnavailable, R | DomPlatform | Scope.Scope> {
  return Effect.gen(function*() {
    const parent = yield* Scope.Scope;
    if (parent.state._tag === "Closed") return yield* Effect.fail(new ViewUnavailable({ reason: "closed" }));
    const dom = yield* DomPlatform;
    const scope = yield* Scope.fork(parent, "sequential");
    const end = yield* Deferred.make<DialogEnd, DomError>();
    const cleaned = yield* Deferred.make<DialogEnd, DomError>();
    const previous = host.ownerDocument.activeElement;
    let restore = previous instanceof HTMLElement ? previous : undefined;
    let explicitRestore = false;
    let beforeFocusRestore: Effect.Effect<void> = Effect.void;
    let owned: { readonly dialog: HTMLDialogElement; readonly sensitive: ReadonlyArray<HTMLInputElement> } | undefined;
    const finish = (reason: DialogEnd): void => {
      const current = owned;
      if (current === undefined) return;
      owned = undefined;
      let failed = false;
      try { if (current.dialog.open) current.dialog.close(); } catch { failed = true; }
      for (const input of current.sensitive) {
        try { input.value = ""; } catch { failed = true; }
      }
      Deferred.doneUnsafe(end, failed ? Effect.fail(new DomError()) : Effect.succeed(reason));
    };
    let cleaning = false;
    const cleanup = Effect.suspend(() => {
      if (cleaning) return Deferred.await(cleaned).pipe(Effect.asVoid, Effect.ignore);
      cleaning = true;
      return Effect.gen(function*() {
        yield* Scope.close(scope, Exit.void);
        // Caller cleanup happens once, even for native cancel or parent disposal.
        const prepared = yield* Effect.exit(beforeFocusRestore);
        const result = yield* Effect.exit(Deferred.await(end));
        const focused = yield* Effect.exit(Effect.try({
          try: () => { if (restore?.isConnected && restore.ownerDocument === host.ownerDocument && !restore.matches(":disabled") && (!explicitRestore || parent.state._tag !== "Closed")) restore.focus(); }, catch: () => new DomError(),
        }));
        yield* Deferred.complete(cleaned, Exit.isSuccess(prepared) && Exit.isSuccess(result) && Exit.isSuccess(focused)
          ? Effect.succeed(result.value) : Effect.fail(new DomError()));
      });
    });
    return yield* Effect.gen(function*() {
      const view = yield* build;
      const { element, value } = view;
      if (element.parentNode !== null) return yield* Effect.fail(new DomError());
      beforeFocusRestore = value.beforeFocusRestore ?? Effect.void;

      owned = { dialog: element, sensitive: value.sensitiveInputs.filter((input) => element.contains(input)) };
      yield* Effect.addFinalizer(() => Effect.sync(() => finish("disposed")));
      if (value.restoreFocus !== undefined) {
        if (!(value.restoreFocus instanceof HTMLElement) || value.restoreFocus.ownerDocument !== host.ownerDocument) return yield* Effect.fail(new DomError());
        restore = value.restoreFocus; explicitRestore = true;
      }
      yield* mountView(host, Effect.succeed(view));
      yield* Effect.try({
        try: () => {
          if (element.open || value.title.id.length === 0 || value.title.textContent.trim().length === 0 || !element.contains(value.title) || !element.contains(value.initialFocus) || value.sensitiveInputs.some((input) => !element.contains(input))) throw new DomError();
          element.setAttribute("aria-labelledby", value.title.id);
        }, catch: () => new DomError(),
      });
      yield* dom.listen(element, "cancel", (event) => { event.preventDefault(); finish("cancel"); });
      yield* dom.listen(element, "close", () => finish("close"));
      yield* Effect.try({ try: () => element.showModal(), catch: () => new DomError() });
      if (Deferred.isDoneUnsafe(end)) return yield* Effect.fail(new ViewUnavailable({ reason: "closed" }));
      yield* dom.focus(value.initialFocus);
      if (scope.state._tag === "Closed") return yield* Effect.fail(new ViewUnavailable({ reason: "closed" }));
      yield* Effect.try({ try: () => {
        if (host.ownerDocument.activeElement !== value.initialFocus) throw new DomError();
      }, catch: () => new DomError() });
      const watcher = yield* Deferred.await(end).pipe(Effect.onExit(() => cleanup), Effect.ignore, Effect.forkIn(parent, { startImmediately: true }));
      const closed = Deferred.await(cleaned).pipe(Effect.ensuring(Fiber.await(watcher)));
      return { closed, close: Effect.sync(() => finish("close")).pipe(Effect.andThen(closed)) };
    }).pipe(Scope.provide(scope), Effect.onExit((exit) => Exit.isFailure(exit) ? Effect.gen(function*() {
      yield* Scope.close(scope, exit);
      // A build failure before ownership never created an end signal.
      if (Deferred.isDoneUnsafe(end)) yield* cleanup;
    }) : Effect.void));
  });
}
