import { expect, it } from "@effect/vitest";
import { Cause, Deferred, Effect, Exit, Fiber, Scope, Stream } from "effect";
import { TestClock } from "effect/testing";
import { ProtocolError } from "../../src/contracts/backend";
import { makeProductCoordinator, ProductUnavailable } from "../../src/operations/productCoordinator";
import { makeCreateWorkspace } from "../../src/screens/create/productOwner";
import { ClientIds, clientIdsFrom } from "../../src/platform/ids";
import { backendHarness, bootstrap, reply } from "../helpers/productBackend";
import { commandHarness, draft } from "../helpers/productCommands";

const author = { _tag: "filterToggle", field: "excludeAuthor" } as const;
function setup() {
  return Effect.gen(function*() {
    const port = yield* commandHarness();
    const scope = yield* Scope.make();
    const workspace = yield* makeCreateWorkspace(port.commands, () => {}).pipe(Scope.provide(scope), Effect.provideService(ClientIds, clientIdsFrom(() => "filter-test")));
    return { port, scope, workspace, close: Scope.close(scope, Exit.void).pipe(Effect.andThen(port.close)) };
  });
}

it.effect("a rejected filter version remains visible after an unrelated prize input and successful confirmation", () => Effect.gen(function*() {
  const h = yield* setup();
  const original = h.port.commands.edit;
  let rejected = 0;
  Object.assign(h.port.commands, { edit: (intent: Parameters<typeof original>[0], fieldKey?: string) => intent.kind === "UpdateFilters"
    ? Effect.sync(() => { rejected++; }).pipe(Effect.andThen(Effect.fail(new ProductUnavailable({ reason: "blocked" }))))
    : original(intent, fieldKey) });
  try {
    h.workspace.input({ _tag: "FilterToggleChanged", field: "excludeAuthor", raw: false });
    const result = yield* Effect.result(h.workspace.commitField(author));
    expect(result._tag).toBe("Failure");
    if (result._tag === "Failure") yield* h.workspace.report(Cause.fail(result.failure));
    const failed = h.workspace.read().editor?.filters.excludeAuthor;
    expect(failed?.raw).toBe(false);
    expect(failed?.dirty).toBe(true);
    expect(failed?.submitted).toBeNull();
    expect(failed?.validation).toEqual({ _tag: "invalid", messageKey: "SubmissionRejected" });
    h.workspace.input({ _tag: "SingleCountChanged", raw: "2" });
    yield* h.workspace.commitField({ _tag: "singleCount" });
    expect(h.workspace.read().error).toBeNull();
    expect(h.workspace.read().editor?.filters.excludeAuthor).toEqual(failed);
    expect(h.workspace.read().draft?.filters.excludeAuthor).toBe(true);
    expect(rejected).toBe(1);
    expect(h.port.state.edits.map(intent => intent.kind)).toEqual(["SetPrizes"]);
  } finally { yield* h.close; }
  expect(h.workspace.read().closed).toBe(true);
}));
it.effect("actual Coordinator ID barrier rejection never dispatches and explicit reapply uses the latest confirmed revision", () => Effect.gen(function*() {
  const port = backendHarness(); const scope = yield* Scope.make();
  const started = yield* Deferred.make<void>(); const release = yield* Deferred.make<string>(); let ids = 0;
  const newId = Effect.suspend(() => ++ids === 1 ? Deferred.succeed(started, undefined).pipe(Effect.andThen(Deferred.await(release))) : Effect.succeed("filter-id-" + ids));
  const commands = yield* makeProductCoordinator({ ...port, newId }).pipe(Scope.provide(scope));
  yield* commands.acceptBootstrap(bootstrap()); commands.setWritesBlocked(false);
  const workspace = yield* makeCreateWorkspace(commands, () => {}).pipe(Scope.provide(scope), Effect.provideService(ClientIds, clientIdsFrom(() => "unused")));
  try {
    workspace.input({ _tag: "FilterToggleChanged", field: "excludeAuthor", raw: false });
    const work = yield* workspace.commitField(author).pipe(Effect.forkScoped);
    yield* Deferred.await(started); commands.setWritesBlocked(true); workspace.setWritesBlocked(true); yield* Deferred.succeed(release, "blocked-id");
    expect((yield* Fiber.await(work))._tag).toBe("Failure");
    expect(port.count("editDraft")).toBe(0); expect(workspace.read().editor?.filters.excludeAuthor.submitted).toBeNull();
    commands.setWritesBlocked(false); workspace.setWritesBlocked(false);
    expect(port.count("editDraft")).toBe(0);
    workspace.input({ _tag: "SingleCountChanged", raw: "2" }); yield* workspace.commitField({ _tag: "singleCount" });
    expect(workspace.read().editor?.filters.excludeAuthor.validation).toEqual({ _tag: "invalid", messageKey: "SubmissionRejected" });
    expect(port.current().filters.excludeAuthor).toBe(true);
    yield* workspace.commitField(author);
    expect(port.current().filters.excludeAuthor).toBe(false); expect(workspace.read().editor?.filters.excludeAuthor.dirty).toBe(false);
    const requests = port.calls.get("editDraft") as Array<import("../../src/contracts/product").DraftEditRequest>;
    expect(requests.map(request => [request.kind, request.expectedRevision, request.operationId])).toEqual([["SetPrizes", 1, "filter-id-2"], ["UpdateFilters", 2, "filter-id-3"]]);
    expect(ids).toBe(3);
  } finally { yield* Scope.close(scope, Exit.void); }
  expect(workspace.read().closed).toBe(true);
}));

for (const nth of [1, 2, -1]) it.effect("filter first Nth continuous rejection " + nth + " has version-owned errors and no automatic retry", () => Effect.gen(function*() {
  const h = yield* setup(); const original = h.port.commands.edit; let attempts = 0;
  Object.assign(h.port.commands, { edit: (intent: Parameters<typeof original>[0], fieldKey?: string) => Effect.suspend(() => {
    attempts++; return nth === -1 || attempts === nth ? Effect.fail(new ProductUnavailable({ reason: "blocked" })) : original(intent, fieldKey);
  }) });
  try {
    for (let index = 1; index <= 3; index++) {
      h.workspace.input({ _tag: "FilterToggleChanged", field: "excludeAuthor", raw: index % 2 === 0 });
      const result = yield* Effect.result(h.workspace.commitField(author)); const fails = nth === -1 || index === nth;
      expect(result._tag).toBe(fails ? "Failure" : "Success");
      expect(h.workspace.read().editor?.filters.excludeAuthor.dirty).toBe(fails);
      expect(h.workspace.read().editor?.filters.excludeAuthor.validation._tag).toBe(fails ? "invalid" : "valid");
      h.workspace.setWritesBlocked(true); h.workspace.setWritesBlocked(false); expect(attempts).toBe(index);
    }
  } finally { yield* h.close; }
}));

for (const ending of ["new-input", "generation", "session", "draft", "null"] as const) it.effect("late filter failure cannot attach to " + ending, () => Effect.gen(function*() {
  const h = yield* setup(); const started = yield* Deferred.make<void>(); const release = yield* Deferred.make<void>();
  Object.assign(h.port.commands, { edit: () => Deferred.succeed(started, undefined).pipe(Effect.andThen(Deferred.await(release)), Effect.andThen(Effect.fail(new ProductUnavailable({ reason: "blocked" })))) });
  try {
    h.workspace.input({ _tag: "FilterToggleChanged", field: "excludeAuthor", raw: false }); const work = yield* h.workspace.commitField(author).pipe(Effect.forkScoped); yield* Deferred.await(started);
    if (ending === "new-input") h.workspace.input({ _tag: "FilterToggleChanged", field: "excludeAuthor", raw: true });
    else {
      const next = ending === "null" ? null : draft({ summary: { ...draft().summary, revision: 2, articleGeneration: ending === "generation" ? 2 : 1, backendSessionId: ending === "session" ? "replacement" : "session", draftId: ending === "draft" ? "replacement" : "draft" } });
      h.port.emit(next); yield* Stream.runHead(h.workspace.changes.pipe(Stream.filter(state => ending === "null" ? state.editor === null : state.draft?.summary === next?.summary)));
      if (ending === "session" || ending === "draft") h.workspace.input({ _tag: "FilterToggleChanged", field: "excludeAuthor", raw: false });
    }
    yield* Deferred.succeed(release, undefined); expect((yield* Fiber.await(work))._tag).toBe("Failure");
    const field = h.workspace.read().editor?.filters.excludeAuthor; expect(field?.validation._tag).not.toBe("invalid");
    if (ending === "new-input") expect(field?.raw).toBe(true);
    if (ending === "generation") expect(field?.raw).toBe(false);
    expect(h.port.state.edits).toHaveLength(0);
  } finally { yield* h.close; }
}));

it.effect("article generation clears old submission failure metadata while retaining dirty raw", () => Effect.gen(function*() {
  const h = yield* setup(); h.port.state.failEdit = 1;
  try {
    h.workspace.input({ _tag: "FilterToggleChanged", field: "excludeAuthor", raw: false }); expect((yield* Effect.result(h.workspace.commitField(author)))._tag).toBe("Failure");
    const next = draft({ summary: { ...draft().summary, revision: 2, articleGeneration: 2 } }); h.port.emit(next);
    yield* Stream.runHead(h.workspace.changes.pipe(Stream.filter(state => state.draft?.summary.articleGeneration === 2)));
    expect(h.workspace.read().editor?.filters.excludeAuthor.raw).toBe(false); expect(h.workspace.read().editor?.filters.excludeAuthor.dirty).toBe(true);
    expect(h.workspace.read().editor?.filters.excludeAuthor.validation).toEqual({ _tag: "unchecked" });
    expect(h.workspace.read().editor?.filters.excludeAuthor.submitted).toBeNull(); expect(h.port.state.edits).toHaveLength(1);
  } finally { yield* h.close; }
}));

for (const reason of ["outcome_unknown", "receipt_expired"] as const) it.effect("explicit confirmation of " + reason + " keeps one admitted receipt and never resends mutation", () => Effect.gen(function*() {
  const h = yield* setup(); const original = h.port.commands.commitThrough; let checks = 0;
  Object.assign(h.port.commands, { commitThrough: (receipt: Parameters<typeof original>[0]) => { checks++; return checks <= 2 ? Effect.fail(new ProductUnavailable({ reason })) : original(receipt); } });
  try {
    h.workspace.input({ _tag: "FilterToggleChanged", field: "excludeAuthor", raw: false });
    for (let attempt = 0; attempt < 2; attempt++) {
      expect((yield* Effect.result(h.workspace.commitField(author)))._tag).toBe("Failure");
      expect(h.workspace.read().editor?.filters.excludeAuthor.validation).toEqual({ _tag: "invalid", messageKey: reason === "receipt_expired" ? "ReceiptExpired" : "SubmissionRejected" });
      expect(h.workspace.read().editor?.filters.excludeAuthor.submitted?.receipt.intentId).toBe("intent-1"); expect(h.port.state.edits).toHaveLength(1);
    }
    yield* h.workspace.commitField(author); expect(h.port.state.edits).toHaveLength(1); expect(checks).toBe(3); expect(h.workspace.read().draft?.filters.excludeAuthor).toBe(false);
    expect(h.workspace.read().editor?.filters.excludeAuthor.dirty).toBe(false);
  } finally { yield* h.close; }
}));

it.effect("invalid filter raw is owned by its current version and correction clears it without changing the confirmed snapshot", () => Effect.gen(function*() {
  const h = yield* setup();
  try {
    h.workspace.input({ _tag: "FilterToggleChanged", field: "timeCutEnabled", raw: true }); expect((yield* Effect.result(h.workspace.commitField({ _tag: "filterToggle", field: "timeCutEnabled" })))._tag).toBe("Failure");
    expect(h.workspace.read().editor?.filters.timeCutEnabled.validation).toEqual({ _tag: "invalid", messageKey: "InvalidInput" }); expect(h.port.state.edits).toHaveLength(0);
    h.workspace.input({ _tag: "FilterToggleChanged", field: "timeCutEnabled", raw: false }); expect(h.workspace.read().editor?.filters.timeCutEnabled.validation._tag).toBe("unchecked");
    yield* h.workspace.commitField({ _tag: "filterToggle", field: "timeCutEnabled" }); expect(h.workspace.read().draft?.filters.timeCut).toBeNull(); expect(h.port.state.edits).toHaveLength(1);
  } finally { yield* h.close; }
}));

it.effect("app close cancels an awaiting filter and cannot publish a late failure into the closed editor", () => Effect.gen(function*() {
  const h = yield* setup(); const started = yield* Deferred.make<void>(); let active = 0, cancelled = 0;
  Object.assign(h.port.commands, { edit: () => Effect.callback(() => { active++; Deferred.doneUnsafe(started, Effect.void); return Effect.sync(() => { active--; cancelled++; }); }) });
  h.workspace.input({ _tag: "FilterToggleChanged", field: "excludeAuthor", raw: false }); const work = yield* h.workspace.commitField(author).pipe(Effect.forkScoped); yield* Deferred.await(started);
  yield* h.close; expect((yield* Fiber.await(work))._tag).toBe("Failure"); expect([active, cancelled]).toEqual([0, 1]); expect(h.workspace.read().editor).toBeNull(); expect(h.workspace.read().closed).toBe(true);
}));
it.effect("mixed typed failure and interruption remains a failure of that exact open filter version", () => Effect.gen(function*() {
  const h = yield* setup(); Object.assign(h.port.commands, { edit: () => Effect.failCause(Cause.combine(Cause.fail(new ProductUnavailable({ reason: "blocked" })), Cause.interrupt(101))) });
  try {
    h.workspace.input({ _tag: "FilterToggleChanged", field: "excludeAuthor", raw: false }); const work = yield* h.workspace.commitField(author).pipe(Effect.forkScoped);
    const result = yield* Fiber.await(work); expect(result._tag).toBe("Failure");
    expect(h.workspace.read().editor?.filters.excludeAuthor.validation).toEqual({ _tag: "invalid", messageKey: "SubmissionRejected" });
    expect(h.workspace.read().draft?.filters.excludeAuthor).toBe(true); expect(h.port.state.edits).toHaveLength(0);
  } finally { yield* h.close; }
}));

it.effect("new article retains local invalid-input metadata but retires prior receipt-expired metadata", () => Effect.gen(function*() {
  const h = yield* setup();
  try {
    h.workspace.input({ _tag: "FilterToggleChanged", field: "timeCutEnabled", raw: true }); expect((yield* Effect.result(h.workspace.commitField({ _tag: "filterToggle", field: "timeCutEnabled" })))._tag).toBe("Failure");
    const next = draft({ summary: { ...draft().summary, revision: 2, articleGeneration: 2 } }); h.port.emit(next); yield* Stream.runHead(h.workspace.changes.pipe(Stream.filter(state => state.draft?.summary.articleGeneration === 2)));
    expect(h.workspace.read().editor?.filters.timeCutEnabled.validation).toEqual({ _tag: "invalid", messageKey: "InvalidInput" }); expect(h.workspace.read().editor?.filters.timeCutEnabled.raw).toBe(true);
    h.workspace.input({ _tag: "FilterToggleChanged", field: "timeCutEnabled", raw: false }); const original = h.port.commands.commitThrough;
    Object.assign(h.port.commands, { commitThrough: () => Effect.fail(new ProductUnavailable({ reason: "receipt_expired" })) });
    expect((yield* Effect.result(h.workspace.commitField({ _tag: "filterToggle", field: "timeCutEnabled" })))._tag).toBe("Failure");
    expect(h.workspace.read().editor?.filters.timeCutEnabled.validation).toEqual({ _tag: "invalid", messageKey: "ReceiptExpired" });
    h.port.emit(draft({ summary: { ...draft().summary, revision: 3, articleGeneration: 3 } })); yield* Stream.runHead(h.workspace.changes.pipe(Stream.filter(state => state.draft?.summary.articleGeneration === 3)));
    expect(h.workspace.read().editor?.filters.timeCutEnabled.validation).toEqual({ _tag: "unchecked" }); expect(h.workspace.read().editor?.filters.timeCutEnabled.submitted).toBeNull(); Object.assign(h.port.commands, { commitThrough: original });
  } finally { yield* h.close; }
}));

it.effect("actual unknown journal is observed without resend and becomes clean only after explicit Coordinator recheck", () => Effect.gen(function*() {
  const port = backendHarness(); const scope = yield* Scope.make(); const commands = yield* makeProductCoordinator(port).pipe(Scope.provide(scope));
  yield* commands.acceptBootstrap(bootstrap()); commands.setWritesBlocked(false);
  const workspace = yield* makeCreateWorkspace(commands, () => {}).pipe(Scope.provide(scope), Effect.provideService(ClientIds, clientIdsFrom(() => "unused")));
  try {
    port.override("editDraft", Effect.fail(new ProtocolError())); workspace.input({ _tag: "FilterToggleChanged", field: "excludeAuthor", raw: false });
    const work = yield* workspace.commitField(author).pipe(Effect.forkScoped); yield* port.wait("getDraftOperation", 1); yield* TestClock.adjust(1000); yield* port.wait("getDraftOperation", 2); yield* TestClock.adjust(2000); yield* port.wait("getDraftOperation", 3); yield* TestClock.adjust(4000); yield* port.wait("getDraftOperation", 4);
    expect((yield* Fiber.await(work))._tag).toBe("Failure"); const receipt = workspace.read().editor?.filters.excludeAuthor.submitted?.receipt; if (receipt === undefined) throw Error("Accepted receipt missing");
    commands.setWritesBlocked(true); workspace.setWritesBlocked(true); expect((yield* Effect.result(workspace.commitField(author)))._tag).toBe("Failure");
    expect(port.count("editDraft")).toBe(1); expect(port.count("getDraftOperation")).toBe(4); expect(workspace.read().editor?.filters.excludeAuthor.submitted?.receipt).toEqual(receipt);
    const confirmed = draft({ summary: { ...draft().summary, revision: 2 }, filters: { ...draft().filters, excludeAuthor: false } }); port.set(confirmed);
    port.override("getDraftOperation", Effect.succeed(reply({ operationId: receipt.intentId, state: "succeeded", summary: confirmed.summary, failureCode: null })));
    yield* commands.recheck; yield* workspace.commitField(author);
    expect(port.count("editDraft")).toBe(1); expect(port.count("getDraftOperation")).toBe(5); expect(workspace.read().editor?.filters.excludeAuthor.dirty).toBe(false);
    expect(workspace.read().draft?.filters.excludeAuthor).toBe(false); expect(workspace.read().writesBlocked).toBe(true);
  } finally { yield* Scope.close(scope, Exit.void); }
}));it.effect("pure interruption preserves the open raw without inventing a submission failure", () => Effect.gen(function*() {
  const h = yield* setup(); Object.assign(h.port.commands, { edit: () => Effect.failCause(Cause.interrupt(101)) });
  try {
    h.workspace.input({ _tag: "FilterToggleChanged", field: "excludeAuthor", raw: false });
    const work = yield* h.workspace.commitField(author).pipe(Effect.forkScoped); expect((yield* Fiber.await(work))._tag).toBe("Failure");
    expect(h.workspace.read().editor?.filters.excludeAuthor.validation).toEqual({ _tag: "valid" });
    expect(h.workspace.read().editor?.filters.excludeAuthor.dirty).toBe(true); expect(h.workspace.read().editor?.filters.excludeAuthor.submitted).toBeNull();
    expect(h.workspace.read().draft?.filters.excludeAuthor).toBe(true); expect(h.port.state.edits).toHaveLength(0); expect(h.workspace.read().closed).toBe(false);
  } finally { yield* h.close; }
}));

it.effect("a late successful receipt cannot clean or invalidate a newer raw filter version", () => Effect.gen(function*() {
  const h = yield* setup(); const original = h.port.commands.commitThrough; const started = yield* Deferred.make<void>(); const release = yield* Deferred.make<void>();
  Object.assign(h.port.commands, { commitThrough: (receipt: Parameters<typeof original>[0]) => Deferred.succeed(started, undefined).pipe(Effect.andThen(Deferred.await(release)), Effect.andThen(original(receipt))) });
  try {
    h.workspace.input({ _tag: "FilterToggleChanged", field: "excludeAuthor", raw: false }); const work = yield* h.workspace.commitField(author).pipe(Effect.forkScoped); yield* Deferred.await(started);
    h.workspace.input({ _tag: "FilterToggleChanged", field: "excludeAuthor", raw: true }); const version = h.workspace.read().editor?.filters.excludeAuthor.inputVersion;
    yield* Deferred.succeed(release, undefined); expect((yield* Fiber.await(work))._tag).toBe("Success");
    const field = h.workspace.read().editor?.filters.excludeAuthor; expect(field?.raw).toBe(true); expect(field?.dirty).toBe(true); expect(field?.inputVersion).toBe(version); expect(field?.validation).toEqual({ _tag: "unchecked" });
    expect(h.workspace.read().draft?.filters.excludeAuthor).toBe(false); expect(h.port.state.edits).toHaveLength(1);
    Object.assign(h.port.commands, { commitThrough: original }); yield* h.workspace.commitField(author); expect(h.workspace.read().draft?.filters.excludeAuthor).toBe(true); expect(h.workspace.read().editor?.filters.excludeAuthor.dirty).toBe(false); expect(h.port.state.edits).toHaveLength(2);
  } finally { yield* h.close; }
}));
