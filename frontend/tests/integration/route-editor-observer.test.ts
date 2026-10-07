import { expect, it } from "@effect/vitest";
import { Deferred, Effect, Fiber, Scope } from "effect";
import { TestClock } from "effect/testing";
import { makeRouteManager } from "../../src/app/routeManager";
import { bindCreateTextInput, type CreateInputPort } from "../../src/screens/create/view";
import { acknowledgeEdit, applyEditorInput, createEditor, editorFieldNeedsAdmission, getEditorField, recordEditReceipt, validateEditorField, type EditorValues, type InputFence } from "../../src/screens/create/editorModel";
import { DomPlatform, makeDomPlatform } from "../../src/platform/dom";

// This controls the future Coordinator admission/observation seam. It verifies
// real route + DOM + editor lifetimes, not a successful Go editing transport.
it.effect("one controlled receipt observation survives route scopes and reentry never readmits the same inputVersion", () => Effect.scoped(Effect.gen(function*() {
  const app = yield* Scope.Scope;
  const host = document.createElement("main");
  const field = { _tag: "singleCount" } as const;
  const identity = { backendSessionId: "session", draftId: "draft" };
  const values: EditorValues = { url: "", drawMode: "immediate", prizeMode: "single", filters: { excludeAnonymous: false, excludeAuthor: false, excludeDcconOnly: false, timeCutEnabled: false, timeCut: "", includeKeywords: "", excludeKeywords: "" }, single: { id: "single", name: "", count: "1" }, multiple: [{ id: "many", name: "상품", count: "1" }] };
  let editor = createEditor(identity, values);
  const fence = (): InputFence => ({ identity: editor.identity, editorEpoch: editor.editorEpoch, inputVersion: getEditorField(editor, field).inputVersion });
  const acknowledge = yield* Deferred.make<void>(); const observing = yield* Deferred.make<void>(); const observed = yield* Deferred.make<void>();
  let admissions = 0; let activeObservers = 0; let submits = 0;
  const work: Array<Effect.Effect<void>> = [];
  const mounts = new Map<number, Deferred.Deferred<void>>();
  let input: HTMLInputElement | undefined;
  const signal = (generation: number) => { let pending = mounts.get(generation); if (pending === undefined) { pending = Deferred.makeUnsafe<void>(); mounts.set(generation, pending); } return pending; };
  const port: CreateInputPort<never, never> = {
    readRaw: () => editor.single.count.raw,
    writeRaw: (raw) => { editor = applyEditorInput(editor, { _tag: "SingleCountChanged", raw }); editor = validateEditorField(editor, field, fence(), { _tag: "valid" }); },
    candidate: () => editorFieldNeedsAdmission(editor, field) ? fence() : null,
    onDebounced: (captured) => Effect.gen(function*() {
      admissions++;
      editor = recordEditReceipt(editor, field, captured, { intentId: "controlled-receipt", draftId: identity.draftId, articleGeneration: 1, sequence: admissions });
      const sequence = admissions;
      yield* Effect.gen(function*() {
        activeObservers++;
        yield* Deferred.succeed(observing, undefined);
        yield* Deferred.await(acknowledge);
        editor = acknowledgeEdit(editor, field, captured, sequence, { _tag: "confirmed" });
        yield* Deferred.succeed(observed, undefined);
      }).pipe(Effect.ensuring(Effect.sync(() => { activeObservers--; })), Effect.forkIn(app));
    }),
    onSubmit: Effect.sync(() => { submits++; }),
  };
  const dom = makeDomPlatform({ request: () => { throw new Error("unexpected frame"); }, cancel: () => {} }, () => "visible");
  const manager = yield* makeRouteManager({ mount: (lease) => Effect.gen(function*() {
    const element = document.createElement("section"); element.textContent = lease.route._tag;
    yield* Effect.acquireRelease(Effect.sync(() => host.append(element)), () => Effect.sync(() => element.remove()));
    if (lease.route._tag === "create") {
      const next = document.createElement("input"); element.append(next); input = next;
      yield* bindCreateTextInput(next, port, (effect) => { work.push(effect); }, () => Effect.die("unexpected intent failure")).pipe(Effect.provideService(DomPlatform, dom));
    }
    yield* Deferred.succeed(signal(lease.generation), undefined);
  }), onFailure: () => Effect.die("unexpected screen failure") });
  const flush = Effect.suspend(() => Effect.gen(function*() { while (work.length > 0) for (const effect of work.splice(0)) yield* effect; }));
  const navigate = (hash: string, generation: number) => manager.navigate(hash).pipe(Effect.andThen(Deferred.await(signal(generation))), Effect.andThen(flush));
  yield* navigate("#/create", 1);
  input!.value = "2"; input!.dispatchEvent(new Event("input")); yield* flush; yield* TestClock.adjust("149 millis");
  yield* navigate("#/unrecognized", 2); expect(admissions).toBe(0); expect(editor.single.count.raw).toBe("2"); expect(editor.single.count.inputVersion).toBe(1);
  yield* navigate("#/create", 3); yield* TestClock.adjust("150 millis"); yield* Deferred.await(observing);
  expect(admissions).toBe(1); expect(activeObservers).toBe(1); expect(editor.single.count.dirty).toBe(true);
  yield* navigate("#/unrecognized", 4); yield* navigate("#/create", 5); yield* TestClock.adjust("1 second");
  expect(admissions).toBe(1); expect(editor.single.count.dirty).toBe(true);
  yield* navigate("#/unrecognized", 6); yield* Deferred.succeed(acknowledge, undefined); yield* Deferred.await(observed); yield* Effect.yieldNow;
  expect(activeObservers).toBe(0); expect(editor.single.count.dirty).toBe(false); expect(host.textContent).toBe("not_found");
  yield* navigate("#/create", 7); yield* TestClock.adjust("1 second");
  expect(input!.value).toBe("2"); expect(editor.single.count.inputVersion).toBe(1); expect(admissions).toBe(1); expect(submits).toBe(0);
  yield* manager.close; expect(host.childElementCount).toBe(0); expect(activeObservers).toBe(0);
})));
