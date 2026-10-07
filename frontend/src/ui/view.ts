import { Data, Effect, Exit, Scope, Semaphore } from "effect";
import { DomError } from "../platform/dom";

export class ViewUnavailable extends Data.TaggedError("ViewUnavailable")<{
  readonly reason: "closed" | "failed";
}> {}

export function patchText(target: Node, value: string): void {
  if (target.textContent !== value) target.textContent = value;
}

export function patchInputValue(target: HTMLInputElement | HTMLTextAreaElement, value: string, composing: boolean): void {
  if (!composing && target.value !== value) target.value = value;
}

export function patchDisabled(target: HTMLInputElement | HTMLTextAreaElement | HTMLButtonElement, disabled: boolean): void {
  if (target.disabled !== disabled) target.disabled = disabled;
}

export type AriaAttribute = "aria-invalid" | "aria-describedby" | "aria-busy" | "aria-expanded" | "aria-checked";
export function patchAria(target: Element, attribute: AriaAttribute, value: string | null): void {
  if (target.getAttribute(attribute) === value) return;
  if (value === null) target.removeAttribute(attribute);
  else target.setAttribute(attribute, value);
}

export interface View<A> {
  readonly element: HTMLElement;
  readonly value: A;
}
export interface MountedView<A> extends View<A> {
  readonly close: Effect.Effect<void>;
}

// Build detached nodes/listeners first. A failed build or append closes only
// this mount, and never replaces or removes another view's nodes.
export function mountView<A, E, R>(host: ParentNode, build: Effect.Effect<View<A>, E, R | Scope.Scope>): Effect.Effect<MountedView<A>, E | DomError | ViewUnavailable, R | Scope.Scope> {
  return Effect.gen(function*() {
    const parent = yield* Scope.Scope;
    if (parent.state._tag === "Closed") return yield* Effect.fail(new ViewUnavailable({ reason: "closed" }));
    const scope = yield* Scope.fork(parent, "sequential");
    return yield* Effect.gen(function*() {
      const view = yield* build;
      yield* Effect.acquireRelease(Effect.try({
        try: () => {
          if (scope.state._tag === "Closed") throw new ViewUnavailable({ reason: "closed" });
          if (view.element.parentNode !== null) throw new DomError();
          try { host.append(view.element); }
          catch (error) { view.element.remove(); throw error; }
        },
        catch: (error) => error instanceof ViewUnavailable ? error : new DomError(),
      }), () => Effect.sync(() => view.element.remove()));
      return { ...view, close: Scope.close(scope, Exit.void) };
    }).pipe(Scope.provide(scope), Effect.onExit((exit) => Exit.isFailure(exit) ? Scope.close(scope, exit) : Effect.void));
  });
}

export interface RowView<A> {
  readonly element: HTMLElement;
  readonly value: { readonly patch: (model: A) => void };
}
export interface KeyedRows<A, E, R> {
  readonly patch: (models: ReadonlyArray<A>) => Effect.Effect<void, E | DomError | ViewUnavailable, R>;
}

// A small Map of owned rows, not a component tree or generic DOM diff. Views
// keep their element references and explicitly patch their own properties.
export function makeKeyedRows<A, E, R>(
  host: HTMLElement,
  maximum: number,
  key: (model: A) => string,
  build: (model: A) => Effect.Effect<RowView<A>, E, R | Scope.Scope>,
): Effect.Effect<KeyedRows<A, E, R>, DomError | ViewUnavailable, Scope.Scope> {
  return Effect.gen(function*() {
    const parent = yield* Scope.Scope;
    if (parent.state._tag === "Closed") return yield* Effect.fail(new ViewUnavailable({ reason: "closed" }));
    if (!Number.isSafeInteger(maximum) || maximum < 1 || maximum > 100 || host.childNodes.length !== 0) return yield* Effect.fail(new DomError());
    const scope = yield* Scope.fork(parent, "sequential");
    const lock = yield* Semaphore.make(1);
    let rows = new Map<string, MountedView<RowView<A>["value"]>>();
    yield* Scope.addFinalizer(scope, Effect.sync(() => { rows.clear(); }));

    return {
      patch: (models) => lock.withPermit(Effect.gen(function*() {
        if (scope.state._tag === "Closed") return yield* Effect.fail(new ViewUnavailable({ reason: "closed" }));
        const entries = yield* Effect.try({
          try: () => {
            if (models.length > maximum) throw new DomError();
            const seen = new Set<string>();
            return models.map((model) => {
              const id = key(model);
              if (typeof id !== "string" || id.length === 0 || seen.has(id)) throw new DomError();
              seen.add(id);
              return { id, model };
            });
          }, catch: () => new DomError(),
        });
        const staged: Array<MountedView<RowView<A>["value"]>> = [];
        const prepared: Array<{ readonly id: string; readonly model: A; readonly row: MountedView<RowView<A>["value"]>; readonly existing: boolean }> = [];
        const fragment = host.ownerDocument.createDocumentFragment();
        let committing = false;
        yield* Effect.gen(function*() {
          for (const { id, model } of entries) {
            const previous = rows.get(id);
            const row = previous ?? (yield* mountView(fragment, build(model)).pipe(Scope.provide(scope)));
            if (previous === undefined) staged.push(row);
            prepared.push({ id, model, row, existing: previous !== undefined });
          }
          yield* Effect.uninterruptible(Effect.gen(function*() {
            committing = true;
            const next = new Map<string, MountedView<RowView<A>["value"]>>();
            yield* Effect.try({
              try: () => {
                if (scope.state._tag === "Closed") throw new ViewUnavailable({ reason: "closed" });
                let cursor = host.firstChild;
                for (const { id, model, row, existing } of prepared) {
                  if (existing) row.value.patch(model);
                  if (row.element !== cursor) host.insertBefore(row.element, cursor);
                  cursor = row.element.nextSibling;
                  next.set(id, row);
                }
              }, catch: (error) => error instanceof ViewUnavailable ? error : new DomError(),
            });
            for (const [id, row] of rows) if (!next.has(id)) yield* row.close;
            if (scope.state._tag === "Closed") return yield* Effect.fail(new ViewUnavailable({ reason: "closed" }));
            rows = next;
          }));
        }).pipe(Effect.onExit((exit) => Exit.isSuccess(exit) ? Effect.void : committing
          ? Scope.close(scope, exit)
          : Effect.forEach(staged, (row) => row.close, { discard: true })));
      })),
    };
  });
}
