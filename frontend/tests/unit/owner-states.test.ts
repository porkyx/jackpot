import { describe, expect, it } from "vitest";
import { initialShellState, projectShell, reduceShell, MAX_APP_NOTICES, type AppNotice, type Route } from "../../src/app/shellState";
import { coordinatorFromBootstrap, disposeCoordinatorState, initialCoordinatorState, markConfirmedEntityStale, markDurableObservation,
  projectCoordinator, receiveConfirmedDraft, type ConfirmedDraft, type DurableDescriptor } from "../../src/operations/coordinatorState";
import type { BootstrapReply, StateNotice } from "../../src/contracts/backend";

function draft(revision = 1, articleGeneration = 1): ConfirmedDraft {
  return { backendSessionId: "session-a", draftId: "draft-a", revision, articleGeneration, state: "ready", collectionId: null,
    snapshot: { snapshotId: "snapshot", collectedAt: "2026-10-06T00:00:00Z", complete: true, pages: 1, acceptedComments: 10, deletedComments: 0, unsupportedComments: 0 } };
}
function descriptor(n = 1): DurableDescriptor {
  return { operationId: `op-${n}`, kind: "CreateCollection", collectionId: `collection-${n}`, roundId: `round-${n}`, status: "pending", revision: 1 };
}
function bootstrap(): BootstrapReply {
  return { protocolVersion: 1, backendSessionId: "session-a", occurredAt: "2026-10-06T00:00:00Z",
    data: { backendNow: "2026-10-06T00:00:00Z", theme: "system", activeDraft: draft(), pendingOperations: [descriptor()], pendingCursor: null,
      recentResults: [{ collectionId: "result-1", roundId: "round-1", revision: 3 }] } };
}
function notice(overrides: Partial<StateNotice> = {}): StateNotice {
  return { backendSessionId: "session-a", entityKind: "draft", entityId: "draft-a", revision: 2, operationId: "operation", ...overrides };
}
function appNotice(id: string): AppNotice { return { id, operationId: "op", target: { _tag: "round", collectionId: "collection", roundId: "round" }, read: false }; }

describe("ShellState owns route, theme, boot and references", () => {
  it("starts at create with system theme and no payload state", () => {
    const state = initialShellState("dark");
    expect(state).toEqual({ boot: { _tag: "starting" }, route: { _tag: "create" }, theme: "system", systemAppearance: "dark", notices: [] });
    expect(projectShell(state)).toEqual({ boot: { _tag: "starting" }, route: { _tag: "create" }, appearance: "dark", unreadCount: 0 });
    expect(Object.keys(state)).toEqual(["boot", "route", "theme", "systemAppearance", "notices"]);
  });
  it("structural route/boot/state additions cannot retain passwords, input buffers or DOM references", () => {
    const routes = [{ _tag: "create" }, { _tag: "result", collectionId: "collection", roundId: null }, { _tag: "not_found", hash: "unknown" }] as const;
    for (const route of routes) {
      const extra = { ...route, password: "SECRET", node: {}, raw: "SECRET" };
      const shell = reduceShell({ ...initialShellState(), password: "SECRET", boot: { _tag: "starting", password: "SECRET" } } as ReturnType<typeof initialShellState>, { _tag: "Navigate", route: extra });
      expect(JSON.stringify(shell)).not.toContain("SECRET");
      expect(projectShell(shell).route).toEqual(route);
    }
    expect(() => reduceShell(initialShellState(), { _tag: "Navigate", route: { _tag: "unknown" } as never })).toThrow("Unknown shell");
    expect(() => projectShell({ ...initialShellState(), boot: { _tag: "unknown" } as never })).toThrow("Unknown shell");
  });
  it("boot success and structured failure keep the current route/theme", () => {
    const original = reduceShell(initialShellState(), { _tag: "Navigate", route: { _tag: "result", collectionId: "collection", roundId: null } });
    const ready = reduceShell(original, { _tag: "BootReady" });
    const failed = reduceShell(ready, { _tag: "BootFailed", messageKey: "ProtocolError" });
    expect(ready.boot).toEqual({ _tag: "ready" });
    expect(failed.boot).toEqual({ _tag: "failed", messageKey: "ProtocolError" });
    expect(failed.route).toEqual({ _tag: "result", collectionId: "collection", roundId: null });
    expect(failed.theme).toBe("system");
    expect(original.boot._tag).toBe("starting");
  });
  it.each<Route>([{ _tag: "create" }, { _tag: "result", collectionId: "collection", roundId: null }, { _tag: "result", collectionId: "collection", roundId: "round" }, { _tag: "not_found", hash: "#bad" }])("route %j never changes domain owner state", (route) => {
    const domain = coordinatorFromBootstrap(bootstrap());
    const before = JSON.stringify(domain);
    const shell = reduceShell(initialShellState(), { _tag: "Navigate", route });
    expect(shell.route).toEqual(route);
    expect(JSON.stringify(domain)).toBe(before);
    expect(Object.isFrozen(shell.route)).toBe(true);
  });
  it.each(["light", "dark"] as const)("explicit %s theme ignores system appearance changes", (theme) => {
    let state = reduceShell(initialShellState(), { _tag: "ThemeChanged", theme });
    state = reduceShell(state, { _tag: "SystemAppearanceChanged", appearance: theme === "light" ? "dark" : "light" });
    expect(projectShell(state).appearance).toBe(theme);
    expect(projectShell(reduceShell(state, { _tag: "ThemeChanged", theme: "system" })).appearance).toBe(state.systemAppearance);
  });
  it("notices keep small references, own nested copies and read independently", () => {
    let state = reduceShell(initialShellState(), { _tag: "NoticeAdded", notice: appNotice("first") });
    state = reduceShell(state, { _tag: "NoticeAdded", notice: appNotice("second") });
    expect(projectShell(state).unreadCount).toBe(2);
    const read = reduceShell(state, { _tag: "NoticeRead", noticeId: "first" });
    expect(projectShell(read).unreadCount).toBe(1);
    expect(state.notices[0]?.read).toBe(false);
    expect(Object.isFrozen(read.notices[0]?.target)).toBe(true);
    expect(reduceShell(read, { _tag: "NoticeRead", noticeId: "missing" })).toEqual(read);
    expect(reduceShell(read, { _tag: "NoticeDismissed", noticeId: "first" }).notices.map((entry) => entry.id)).toEqual(["second"]);
  });
  it("duplicate notice identity and 127/128/129 cap stay bounded", () => {
    let state = initialShellState();
    for (let n = 0; n < 129; n++) {
      state = reduceShell(state, { _tag: "NoticeAdded", notice: appNotice(String(n)) });
      expect(state.notices).toHaveLength(Math.min(n + 1, MAX_APP_NOTICES));
    }
    expect(state.notices[0]?.id).toBe("1");
    state = reduceShell(state, { _tag: "NoticeAdded", notice: { ...appNotice("128"), read: true } });
    expect(state.notices).toHaveLength(128);
    expect(state.notices.at(-1)?.read).toBe(true);
  });
  it("empty notice identity and unknown action are loud invariants", () => {
    expect(() => reduceShell(initialShellState(), { _tag: "NoticeAdded", notice: appNotice("") })).toThrow("identity");
    expect(() => reduceShell(initialShellState(), { _tag: "unknown" } as never)).toThrow("Unknown shell");
    expect(() => reduceShell(initialShellState(), { _tag: "NoticeAdded", notice: { ...appNotice("unknown"), target: { _tag: "unknown" } as never } })).toThrow("Unknown shell");
  });
  it("all notification target types copy references without retaining extra secret or DOM properties", () => {
    const refs = [{ _tag: "draft", draftId: "draft" }, { _tag: "collection", collectionId: "collection" }, { _tag: "round", collectionId: "collection", roundId: "round" }] as const;
    for (const reference of refs) {
      const extra = { id: "notice", operationId: null, read: false, password: "SECRET", node: {}, target: { ...reference, password: "SECRET" } };
      const state = reduceShell(initialShellState(), { _tag: "NoticeAdded", notice: extra });
      expect(JSON.stringify(state)).not.toContain("SECRET");
      expect(Object.keys(state.notices[0]!)).toEqual(["id", "operationId", "target", "read"]);
      expect(state.notices[0]?.target).toEqual(reference);
    }
  });
  it("frozen projection cannot change shell or coordinator sources", () => {
    const shell = initialShellState();
    const projection = projectShell(shell);
    expect(() => Object.assign(projection.route, { _tag: "result", collectionId: "collection", roundId: null })).toThrow();
    expect(shell.route).toEqual({ _tag: "create" });
  });
});

describe("Coordinator owns confirmed and pending metadata", () => {
  it("structurally extra secret/DOM/page fields never survive confirmed, pending, stale or projection ownership", () => {
    const original = bootstrap();
    const confirmed = { ...draft(), password: "SECRET", node: {}, snapshot: { ...draft().snapshot!, password: "SECRET", pageCache: ["SECRET"] } };
    const pendingExtra = [{ ...descriptor(), password: "SECRET", pageCache: ["SECRET"] }];
    const resultsExtra = [{ collectionId: "result-1", roundId: "round-1", revision: 3, password: "SECRET" }];
    const state = coordinatorFromBootstrap({ ...original, data: { ...original.data, activeDraft: confirmed,
      pendingOperations: pendingExtra, recentResults: resultsExtra } });
    const updated = receiveConfirmedDraft(state, "session-a", { ...confirmed, revision: 2 });
    const noticeExtra = { ...notice({ revision: 3 }), password: "SECRET", node: {} };
    const stale = markConfirmedEntityStale(updated, noticeExtra);
    const observed = markDurableObservation({ ...state, password: "SECRET" } as typeof state, "op-1", "outcome_unknown");
    for (const value of [state, updated, stale, observed, projectCoordinator(stale), projectCoordinator(observed)]) expect(JSON.stringify(value)).not.toContain("SECRET");
    if (stale._tag !== "active") throw new Error("Expected active state");
    expect(Object.keys(stale.stale[0]!)).toEqual(["backendSessionId", "entityKind", "entityId", "revision", "operationId"]);
    expect(stale.activeDraft?.snapshot).toEqual(draft().snapshot);
  });
  it("uninitialized and disposed owners block writes without pretending empty Bootstrap success", () => {
    const before = initialCoordinatorState();
    const disposed = disposeCoordinatorState();
    expect(projectCoordinator(before)).toEqual({ _tag: "awaiting_bootstrap", pendingCount: 0, writesBlocked: true });
    expect(projectCoordinator(disposed)).toEqual({ _tag: "disposed", pendingCount: 0, writesBlocked: true });
    expect(receiveConfirmedDraft(before, "session-a", draft())).toBe(before);
    expect(markConfirmedEntityStale(disposed, notice())).toBe(disposed);
    expect(markDurableObservation(before, "op-1", "outcome_unknown")).toBe(before);
  });
  it("Bootstrap owns copied committed summaries and descriptors without payload, raw or pages", () => {
    const source = bootstrap();
    const state = coordinatorFromBootstrap(source);
    expect(state.activeDraft).not.toBe(source.data.activeDraft);
    expect(state.activeDraft?.snapshot).not.toBe(source.data.activeDraft?.snapshot);
    expect(state.pending[0]?.descriptor).not.toBe(source.data.pendingOperations?.[0]);
    expect(state.results[0]).not.toBe(source.data.recentResults?.[0]);
    expect(state.recovery).toEqual({ _tag: "complete" });
    expect(Object.keys(state)).toEqual(["_tag", "backendSessionId", "backendNow", "activeDraft", "results", "pending", "recovery", "stale", "staleOverflow"]);
    expect(() => Object.assign(state.activeDraft!.snapshot!, { acceptedComments: 0 })).toThrow();
    expect(source.data.activeDraft?.snapshot?.acceptedComments).toBe(10);
  });
  it("incomplete enumeration and 64 occupied tracking slots block new mutations", () => {
    const source = bootstrap();
    expect(projectCoordinator(coordinatorFromBootstrap({ ...source, data: { ...source.data, pendingCursor: "cursor" } })).writesBlocked).toBe(true);
    for (const count of [0, 1, 63, 64]) {
      const state = coordinatorFromBootstrap({ ...source, data: { ...source.data, pendingOperations: Array.from({ length: count }, (_, n) => descriptor(n)) } });
      expect(projectCoordinator(state)).toMatchObject({ pendingCount: count, writesBlocked: count === 64, uncertainCount: 0 });
    }
  });
  it.each([null, Array.from({ length: 65 }, (_, n) => descriptor(n)), [descriptor(), descriptor()]])("malformed pending bootstrap %j fails rather than deleting records", (pendingOperations) => {
    const source = bootstrap();
    expect(() => coordinatorFromBootstrap({ ...source, data: { ...source.data, pendingOperations } })).toThrow();
  });
  it("missing/overflow result arrays and mismatched draft session fail", () => {
    const source = bootstrap();
    expect(() => coordinatorFromBootstrap({ ...source, data: { ...source.data, recentResults: null } })).toThrow();
    expect(() => coordinatorFromBootstrap({ ...source, data: { ...source.data, recentResults: Array.from({ length: 17 }, (_, n) => ({ collectionId: `c-${n}`, roundId: "r", revision: 1 })) } })).toThrow();
    expect(() => coordinatorFromBootstrap({ ...source, data: { ...source.data, activeDraft: { ...draft(), backendSessionId: "other" } } })).toThrow("session");
    expect(coordinatorFromBootstrap({ ...source, data: { ...source.data, activeDraft: null, recentResults: [] } }).activeDraft).toBeNull();
  });
  it.each(["response session", "draft session", "draft identity", "lower revision", "lower generation"])("%s response does not replace confirmed state", (wrong) => {
    const state = coordinatorFromBootstrap(bootstrap());
    const next = wrong === "draft session" ? { ...draft(), backendSessionId: "other" }
      : wrong === "draft identity" ? { ...draft(), draftId: "other" }
      : wrong === "lower revision" ? draft(0) : wrong === "lower generation" ? draft(2, 0) : draft(2);
    expect(receiveConfirmedDraft(state, wrong === "response session" ? "old-session" : "session-a", next)).toBe(state);
  });
  it("equal/max safe revision is accepted and stale hints remain until authoritative coverage", () => {
    const original = coordinatorFromBootstrap(bootstrap());
    expect(receiveConfirmedDraft(original, "session-a", draft(1))).toMatchObject({ activeDraft: draft(1) });
    const queuedHint = { ...original, stale: [notice({ revision: 1 })] };
    expect(receiveConfirmedDraft(queuedHint, "session-a", draft(1))).toMatchObject({ stale: [], activeDraft: draft(1) });
    const stale = markConfirmedEntityStale(markConfirmedEntityStale(original, notice()), notice({ revision: 3 }));
    expect(stale).toMatchObject({ stale: [notice({ revision: 3 })], activeDraft: draft(1) });
    const partial = receiveConfirmedDraft(stale, "session-a", draft(2));
    expect(partial).toMatchObject({ stale: [notice({ revision: 3 })], activeDraft: draft(2) });
    const covered = receiveConfirmedDraft(partial, "session-a", draft(Number.MAX_SAFE_INTEGER, 2));
    expect(covered).toMatchObject({ stale: [], activeDraft: draft(Number.MAX_SAFE_INTEGER, 2) });
  });
  it.each<Partial<StateNotice>>([{ backendSessionId: "old" }, { revision: 0 }, { revision: 1 }])("event %j never becomes a confirmed result", (override) => {
    const state = coordinatorFromBootstrap(bootstrap());
    expect(markConfirmedEntityStale(state, notice(override))).toBe(state);
  });
  it("event revision is monotonic, collection revision uses the newest known reference", () => {
    const source = bootstrap();
    const state = coordinatorFromBootstrap({ ...source, data: { ...source.data, recentResults: [{ collectionId: "same", roundId: "older", revision: 2 }, { collectionId: "same", roundId: "newer", revision: 5 }] } });
    const old = notice({ entityKind: "collection", entityId: "same", revision: 3 });
    expect(markConfirmedEntityStale(state, old)).toBe(state);
    const newer = notice({ entityKind: "collection", entityId: "same", revision: 6 });
    const stale = markConfirmedEntityStale(state, newer);
    expect(stale).toMatchObject({ stale: [newer] });
    expect(markConfirmedEntityStale(stale, { ...newer, revision: 5 })).toBe(stale);
    expect(markConfirmedEntityStale(stale, newer)).toBe(stale);
  });
  it("unknown entity remains stale and pending collection uses its confirmed descriptor revision", () => {
    const state = coordinatorFromBootstrap(bootstrap());
    const unknown = notice({ entityKind: "collection", entityId: "unknown", revision: 0 });
    const stale = markConfirmedEntityStale(state, unknown);
    expect(stale).toMatchObject({ stale: [unknown] });
    expect(projectCoordinator(stale).writesBlocked).toBe(true);
    expect(markConfirmedEntityStale(state, notice({ entityKind: "collection", entityId: "collection-1", revision: 1 }))).toBe(state);
    expect(markConfirmedEntityStale(state, notice({ entityKind: "collection", entityId: "collection-1", revision: 2 }))).toMatchObject({ stale: [notice({ entityKind: "collection", entityId: "collection-1", revision: 2 })] });
  });
  it("63/64/65 unknown hints are bounded and overflow remains visibly blocked", () => {
    let state = coordinatorFromBootstrap(bootstrap());
    for (let n = 0; n < 65; n++) {
      state = markConfirmedEntityStale(state, notice({ entityKind: "collection", entityId: `unknown-${n}` })) as typeof state;
      expect(state.stale).toHaveLength(Math.min(n + 1, 64));
      expect(state.staleOverflow).toBe(n === 64);
    }
    expect(projectCoordinator(state).writesBlocked).toBe(true);
    state = markConfirmedEntityStale(state, notice({ entityKind: "collection", entityId: "unknown-0", revision: 4 })) as typeof state;
    expect(state.stale.find((hint) => hint.entityId === "unknown-0")?.revision).toBe(4);
    expect(state.staleOverflow).toBe(true);
  });
  it("outcome uncertainty never deletes accepted descriptor or fabricates terminal result", () => {
    const original = coordinatorFromBootstrap(bootstrap());
    const unknown = markDurableObservation(original, "op-1", "outcome_unknown");
    const checking = markDurableObservation(unknown, "op-1", "reconciling");
    expect(unknown).toMatchObject({ pending: [{ _tag: "outcome_unknown", descriptor: descriptor() }] });
    expect(checking).toMatchObject({ pending: [{ _tag: "reconciling", descriptor: descriptor() }], results: original.results });
    expect(projectCoordinator(checking)).toMatchObject({ pendingCount: 1, uncertainCount: 1 });
    expect(markDurableObservation(checking, "missing", "pending")).toBe(checking);
    expect(markDurableObservation(checking, "op-1", "reconciling")).toBe(checking);
    expect(markDurableObservation(checking, "op-1", "pending")).toMatchObject({ pending: [{ _tag: "pending" }] });
  });
  it("new authoritative session rebuilds ephemeral confirmed state and preserves recovered durable tracking", () => {
    const previous = coordinatorFromBootstrap(bootstrap());
    const source = bootstrap();
    const next = coordinatorFromBootstrap({ ...source, backendSessionId: "session-b", data: { ...source.data, activeDraft: { ...draft(), backendSessionId: "session-b", draftId: "draft-b" } } });
    expect(next.backendSessionId).toBe("session-b");
    expect(next.activeDraft?.draftId).toBe("draft-b");
    expect(next.pending).toEqual(previous.pending);
    expect(receiveConfirmedDraft(next, "session-a", draft(5))).toBe(next);
    expect(previous.backendSessionId).toBe("session-a");
  });
  it("readonly projection cannot mutate confirmed summaries or results", () => {
    const state = coordinatorFromBootstrap(bootstrap());
    const projection = projectCoordinator(state);
    if (projection._tag !== "active") throw new Error("test fixture not active");
    expect(() => Object.assign(projection.results[0]!, { revision: 0 })).toThrow();
    expect(() => Object.assign(projection.activeDraft!, { revision: 0 })).toThrow();
    expect(state.results[0]?.revision).toBe(3);
    expect(state.activeDraft?.revision).toBe(1);
  });
  it("draft without snapshot and empty active draft projection remain explicit null", () => {
    const source = bootstrap();
    const withoutSnapshot = coordinatorFromBootstrap({ ...source, data: { ...source.data, activeDraft: { ...draft(), snapshot: null } } });
    expect(withoutSnapshot.activeDraft?.snapshot).toBeNull();
    const withoutDraft = coordinatorFromBootstrap({ ...source, data: { ...source.data, activeDraft: null } });
    expect(projectCoordinator(withoutDraft)).toMatchObject({ activeDraft: null });
    expect(receiveConfirmedDraft(withoutDraft, "session-a", draft())).toBe(withoutDraft);
    expect(markConfirmedEntityStale(withoutDraft, notice())).toMatchObject({ stale: [notice()] });
  });
  it("one observation transition leaves other pending descriptors untouched", () => {
    const source = bootstrap();
    const state = coordinatorFromBootstrap({ ...source, data: { ...source.data, pendingOperations: [descriptor(1), descriptor(2)] } });
    expect(markDurableObservation(state, "op-1", "outcome_unknown")).toMatchObject({ pending: [{ _tag: "outcome_unknown", descriptor: descriptor(1) }, { _tag: "pending", descriptor: descriptor(2) }] });
  });
});

// These assertions run at compile time only; pure owners expose no writable
// field or another owner's private data through their public readonly types.
function ownershipTypeAssertions() {
  const shell = initialShellState();
  // @ts-expect-error shell cannot own accepted commands
  shell.pending = [];
  // @ts-expect-error route is readonly
  shell.route = { _tag: "result", collectionId: "collection", roundId: null };
  const state = coordinatorFromBootstrap(bootstrap());
  // @ts-expect-error confirmed revision cannot be written by a view
  state.activeDraft!.revision = 0;
  // @ts-expect-error coordinator does not own raw editor buffers
  state.raw = "";
}
void ownershipTypeAssertions;
