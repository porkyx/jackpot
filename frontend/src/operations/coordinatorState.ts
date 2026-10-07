import type { BootstrapReply, StateNotice } from "../contracts/backend";
import type { DraftSummary, OperationDescriptor, ResultReference } from "../contracts/schemas";

export type ConfirmedDraft = typeof DraftSummary.Type;
export type DurableDescriptor = typeof OperationDescriptor.Type;
export type ConfirmedResult = typeof ResultReference.Type;
export interface EditReceipt {
  readonly intentId: string;
  readonly draftId: string;
  readonly articleGeneration: number;
  readonly sequence: number;
}
export type DurableObservation = {
  readonly _tag: "pending" | "outcome_unknown" | "reconciling";
  readonly descriptor: DurableDescriptor;
};
export type RecoveryEnumeration = { readonly _tag: "complete" } | { readonly _tag: "enumerating"; readonly cursor: string };
export type CoordinatorState = { readonly _tag: "awaiting_bootstrap" } | {
  readonly _tag: "active";
  readonly backendSessionId: string;
  readonly backendNow: string;
  readonly activeDraft: ConfirmedDraft | null;
  readonly results: ReadonlyArray<ConfirmedResult>;
  readonly pending: ReadonlyArray<DurableObservation>;
  readonly recovery: RecoveryEnumeration;
  readonly stale: ReadonlyArray<StateNotice>;
  readonly staleOverflow: boolean;
} | { readonly _tag: "disposed" };
export type ActiveCoordinatorState = Extract<CoordinatorState, { readonly _tag: "active" }>;

function ownDraft(draft: ConfirmedDraft): ConfirmedDraft {
  const snapshot = draft.snapshot;
  return Object.freeze({ backendSessionId: draft.backendSessionId, draftId: draft.draftId, revision: draft.revision, articleGeneration: draft.articleGeneration, state: draft.state, collectionId: draft.collectionId,
    snapshot: snapshot === null ? null : Object.freeze({ snapshotId: snapshot.snapshotId, collectedAt: snapshot.collectedAt, complete: snapshot.complete, pages: snapshot.pages, acceptedComments: snapshot.acceptedComments, deletedComments: snapshot.deletedComments, unsupportedComments: snapshot.unsupportedComments }) });
}
function ownResult(result: ConfirmedResult): ConfirmedResult { return Object.freeze({ collectionId: result.collectionId, roundId: result.roundId, revision: result.revision }); }
function ownDescriptor(descriptor: DurableDescriptor): DurableDescriptor { return Object.freeze({ operationId: descriptor.operationId, kind: descriptor.kind, collectionId: descriptor.collectionId, roundId: descriptor.roundId, status: descriptor.status, revision: descriptor.revision }); }
function ownStateNotice(notice: StateNotice): StateNotice { return Object.freeze({ backendSessionId: notice.backendSessionId, entityKind: notice.entityKind, entityId: notice.entityId, revision: notice.revision, operationId: notice.operationId }); }
function ownActive(state: ActiveCoordinatorState): ActiveCoordinatorState {
  return Object.freeze({ _tag: "active", backendSessionId: state.backendSessionId, backendNow: state.backendNow,
    activeDraft: state.activeDraft === null ? null : ownDraft(state.activeDraft),
    results: Object.freeze(state.results.map(ownResult)),
    pending: Object.freeze(state.pending.map((pending) => Object.freeze({ _tag: pending._tag, descriptor: ownDescriptor(pending.descriptor) }))),
    recovery: Object.freeze(state.recovery._tag === "complete" ? { _tag: "complete" } : { _tag: "enumerating", cursor: state.recovery.cursor }),
    stale: Object.freeze(state.stale.map(ownStateNotice)),
    staleOverflow: state.staleOverflow,
  });
}
export function initialCoordinatorState(): CoordinatorState { return Object.freeze({ _tag: "awaiting_bootstrap" }); }

// This only constructs state from an already-decoded authoritative Bootstrap.
// Subscription/bootstrap request fences and page enumeration belong to T01.16.
export function coordinatorFromBootstrap(reply: BootstrapReply): ActiveCoordinatorState {
  const { data } = reply;
  if (data.pendingOperations === null || data.recentResults === null) throw new Error("Decoded Bootstrap arrays must be present");
  if (data.pendingOperations.length > 64 || data.recentResults.length > 16) throw new Error("Decoded Bootstrap exceeds cache limits");
  if (data.activeDraft !== null && data.activeDraft.backendSessionId !== reply.backendSessionId) throw new Error("Bootstrap draft session differs");
  const unique = new Set(data.pendingOperations.map((operation) => operation.operationId));
  if (unique.size !== data.pendingOperations.length) throw new Error("Duplicate recovered operation identity");
  return ownActive({ _tag: "active", backendSessionId: reply.backendSessionId, backendNow: data.backendNow,
    activeDraft: data.activeDraft, results: data.recentResults, pending: data.pendingOperations.map((descriptor) => ({ _tag: "pending", descriptor })),
    recovery: data.pendingCursor === null ? { _tag: "complete" } : { _tag: "enumerating", cursor: data.pendingCursor }, stale: [], staleOverflow: false,
  });
}
export function receiveConfirmedDraft(state: CoordinatorState, sessionId: string, draft: ConfirmedDraft): CoordinatorState {
  if (state._tag !== "active" || sessionId !== state.backendSessionId || draft.backendSessionId !== sessionId) return state;
  const current = state.activeDraft;
  if (current === null || current.draftId !== draft.draftId || draft.revision < current.revision || draft.articleGeneration < current.articleGeneration) return state;
  return ownActive({ ...state, activeDraft: draft, stale: state.stale.filter((notice) => notice.entityKind !== "draft" || notice.entityId !== draft.draftId || notice.revision > draft.revision) });
}
export function markConfirmedEntityStale(state: CoordinatorState, notice: StateNotice): CoordinatorState {
  if (state._tag !== "active" || notice.backendSessionId !== state.backendSessionId) return state;
  const knownRevisions = [...state.results.filter((result) => result.collectionId === notice.entityId).map((result) => result.revision),
    ...state.pending.filter((pending) => pending.descriptor.collectionId === notice.entityId).map((pending) => pending.descriptor.revision)];
  const knownRevision = notice.entityKind === "draft"
    ? state.activeDraft?.draftId === notice.entityId ? state.activeDraft.revision : undefined
    : knownRevisions.length === 0 ? undefined : Math.max(...knownRevisions);
  if (knownRevision !== undefined && notice.revision <= knownRevision) return state;
  const previous = state.stale.find((entry) => entry.entityKind === notice.entityKind && entry.entityId === notice.entityId);
  if (previous !== undefined && previous.revision >= notice.revision) return state;
  if (previous === undefined && state.stale.length >= 64) return ownActive({ ...state, staleOverflow: true });
  return ownActive({ ...state, stale: [...state.stale.filter((entry) => entry.entityKind !== notice.entityKind || entry.entityId !== notice.entityId), notice] });
}
export function markDurableObservation(state: CoordinatorState, operationId: string, phase: DurableObservation["_tag"]): CoordinatorState {
  if (state._tag !== "active") return state;
  const target = state.pending.find((pending) => pending.descriptor.operationId === operationId);
  if (target === undefined || target._tag === phase) return state;
  return ownActive({ ...state, pending: state.pending.map((pending) => pending === target ? { ...pending, _tag: phase } : pending) });
}
export function disposeCoordinatorState(): CoordinatorState { return Object.freeze({ _tag: "disposed" }); }
export function projectCoordinator(state: CoordinatorState) {
  if (state._tag !== "active") return Object.freeze({ _tag: state._tag, pendingCount: 0, writesBlocked: true } as const);
  return Object.freeze({ _tag: state._tag, backendSessionId: state.backendSessionId, pendingCount: state.pending.length,
    writesBlocked: state.recovery._tag !== "complete" || state.pending.length >= 64 || state.stale.length > 0 || state.staleOverflow,
    uncertainCount: state.pending.filter((pending) => pending._tag !== "pending").length,
    activeDraft: state.activeDraft === null ? null : ownDraft(state.activeDraft), results: Object.freeze(state.results.map(ownResult)),
  });
}
