import { expect, it } from "@effect/vitest";
import { Cause, Deferred, Effect, Exit, Fiber, Scope } from "effect";
import { ProtocolError } from "../../src/contracts/backend";
import { ClientIds, clientIdsFrom } from "../../src/platform/ids";
import { makeCreateWorkspace } from "../../src/screens/create/productOwner";
import { commandHarness, draft } from "../helpers/productCommands";

function setup() {
  return Effect.gen(function*() {
    const port = yield* commandHarness();
    const scope = yield* Scope.make();
    const workspace = yield* makeCreateWorkspace(port.commands, () => {}).pipe(Scope.provide(scope), Effect.provideService(ClientIds, clientIdsFrom(() => "unused-id")));
    workspace.input({ _tag: "SingleNameChanged", raw: "로컬 상품" });
    workspace.input({ _tag: "FilterTextChanged", field: "includeKeywords", raw: "등록 단어" });
    workspace.setKeywordRaw("exclude", "미전송 단어");
    return { port, scope, workspace };
  });
}
for (const kind of ["article", "filters"] as const) it.effect(`confirmed ${kind} reset clears only its owned raw and unlocks once`, () => Effect.gen(function*() {
  const { port, scope, workspace } = yield* setup();
  let directCalls = 0;
  if (kind === "article") {
    const previous = draft();
    const confirmed = draft({ article: null, participants: 0, included: 0, excluded: 0, load: null,
      filters: { ...previous.filters, excludeAuthor: false },
      summary: { ...previous.summary, state: "empty", revision: 2, articleGeneration: 2 } });
    Object.assign(port.commands, { resetArticle: Effect.sync(() => { directCalls++; port.emit(confirmed); return confirmed; }) });
  }
  try {
    const before = workspace.read();
    yield* (kind === "article" ? workspace.resetArticle : workspace.resetFilters);
    const after = workspace.read();
    expect(after.editor?.lock).toBeNull();
    expect(after.editor!.editorEpoch).toBeGreaterThan(before.editor!.editorEpoch);
    expect(after.editor?.filters.includeKeywords.raw).toBe("");
    expect(after.editor?.filters.excludeAuthor.raw).toBe(false);
    expect(after.keywordRaw).toEqual({ include: "", exclude: "" });
    expect(after.editor?.single.name.raw).toBe("로컬 상품");
    expect(after.editor?.single.name.dirty).toBe(true);
    expect(after.editor?.url.raw).toBe(kind === "article" ? "" : draft().article!.url);
    expect(port.state.edits.map(edit => edit.kind)).toEqual(kind === "article" ? [] : ["ResetFilters"]);
    expect(directCalls).toBe(kind === "article" ? 1 : 0);
    expect(port.state.creates).toHaveLength(0);
  } finally { yield* Scope.close(scope, Exit.void); yield* port.close; }
  expect(workspace.read().closed).toBe(true);
  expect(workspace.read().editor).toBeNull();
}));
for (const kind of ["article", "filters"] as const) it.effect(`failed ${kind} reset preserves raw and authoritative draft while releasing its lock`, () => Effect.gen(function*() {
  const { port, scope, workspace } = yield* setup();
  let directCalls = 0;
  if (kind === "article") Object.assign(port.commands, { resetArticle: Effect.suspend(() => { directCalls++; return Effect.fail(new ProtocolError()); }) });
  else port.state.failCommit = 1;
  try {
    const before = workspace.read();
    const result = yield* Effect.result(kind === "article" ? workspace.resetArticle : workspace.resetFilters);
    expect(result._tag).toBe("Failure");
    if (result._tag === "Failure") expect(result.failure._tag).toBe("ProtocolError");
    const after = workspace.read();
    expect(after.draft).toBe(before.draft);
    expect(after.editor?.lock).toBeNull();
    expect(after.editor?.editorEpoch).toBe(before.editor?.editorEpoch);
    expect(after.editor?.filters.includeKeywords).toEqual(before.editor?.filters.includeKeywords);
    expect(after.editor?.single.name).toEqual(before.editor?.single.name);
    expect(after.editor?.url).toEqual(before.editor?.url);
    expect(after.keywordRaw).toEqual(before.keywordRaw);
    expect(port.state.edits.map(edit => edit.kind)).toEqual(kind === "article" ? [] : ["ResetFilters"]);
    expect(directCalls).toBe(kind === "article" ? 1 : 0);
    expect(port.state.creates).toHaveLength(0);
  } finally { yield* Scope.close(scope, Exit.void); yield* port.close; }
  expect(workspace.read().closed).toBe(true);
  expect(workspace.read().editor).toBeNull();
}));

for (const ending of ["cancel", "close"] as const) it.effect(`controlled reset ${ending} releases dependency ownership without pretending success`, () => Effect.gen(function*() {
  const { port, scope, workspace } = yield* setup();
  const started = yield* Deferred.make<void>();
  const held = yield* Deferred.make<ReturnType<typeof draft>>();
  let active = 0; let released = 0;
  Object.assign(port.commands, { resetArticle: Effect.acquireUseRelease(
    Effect.sync(() => { active++; Deferred.doneUnsafe(started, Effect.void); }),
    () => Deferred.await(held),
    () => Effect.sync(() => { active--; released++; }),
  ) });
  try {
    const before = workspace.read();
    const fiber = yield* Effect.exit(workspace.resetArticle).pipe(Effect.forkScoped);
    yield* Deferred.await(started);
    expect(active).toBe(1);
    if (ending === "cancel") yield* Deferred.interrupt(held);
    else yield* Scope.close(scope, Exit.void);
    const outcome = yield* Fiber.join(fiber);
    expect(Exit.isFailure(outcome)).toBe(true);
    if (Exit.isFailure(outcome)) expect(Cause.hasInterruptsOnly(outcome.cause)).toBe(true);
    expect([active, released]).toEqual([0, 1]);
    expect(port.state.edits).toHaveLength(0);
    expect(port.state.creates).toHaveLength(0);
    if (ending === "cancel") {
      expect(workspace.read().editor?.lock).toBeNull();
      expect(workspace.read().draft).toBe(before.draft);
      expect(workspace.read().editor?.single.name).toEqual(before.editor?.single.name);
      expect(workspace.read().keywordRaw).toEqual(before.keywordRaw);
    } else { expect(workspace.read().closed).toBe(true); expect(workspace.read().editor).toBeNull(); }
  } finally { yield* Scope.close(scope, Exit.void); yield* port.close; }
}));
