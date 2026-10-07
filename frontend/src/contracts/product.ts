import { Schema, type Effect } from "effect";
import {isSafeDcMediaUrl} from "./media";
import type { BackendError, ReplyHeader } from "./backend";
import { OpaqueId, SafeCounter, UtcTimestamp, DraftSummary, SnapshotSummary } from "./schemas";

const nullableTime = Schema.NullOr(UtcTimestamp);
const maximum = (value: number) => SafeCounter.check(Schema.isLessThanOrEqualTo(value));
export const ArticleData = Schema.Struct({ url: Schema.String, title: Schema.String, galleryId: Schema.String, galleryName: Schema.String,
  galleryKind: Schema.String, number: Schema.String, authorNickname: Schema.String, authorIdentifier: Schema.NullOr(Schema.String), postedAt: nullableTime });
export const BadgeCategory = Schema.Literals(["fixed", "semi_fixed", "main_manager", "sub_manager", "new_account", "anonymous"]);
const BadgeRule = Schema.Struct({ excluded: Schema.Boolean, weight: maximum(100) });
export const BadgeRules = Schema.Struct({ weightingEnabled: Schema.Boolean, fixed: BadgeRule, semiFixed: BadgeRule,
  mainManager: BadgeRule, subManager: BadgeRule, newAccount: BadgeRule, anonymous: BadgeRule });
export const FilterConfiguration = Schema.Struct({ excludeAnonymous: Schema.Boolean, excludeAuthor: Schema.Boolean, excludeDcconOnly: Schema.Boolean,
  timeCut: nullableTime, includeKeywords: Schema.Array(Schema.String).check(Schema.isMaxLength(100)), excludeKeywords: Schema.Array(Schema.String).check(Schema.isMaxLength(100)),
  badgeRules: Schema.optionalKey(Schema.NullOr(BadgeRules)) });
export const PrizeInput = Schema.Struct({ id: OpaqueId, name: Schema.String.check(Schema.isMaxLength(20)), count: maximum(10).check(Schema.isGreaterThanOrEqualTo(1)) });
export const PrizeConfiguration = Schema.Struct({ mode: Schema.Literals(["single", "multiple"]), single: PrizeInput,
  multiple: Schema.Array(PrizeInput).check(Schema.isMinLength(1), Schema.isMaxLength(10)), drawMode: Schema.Literals(["immediate", "reservation"]) });
const FailureCode = Schema.Literals(["InvalidInput", "InvalidState", "StaleRevision", "StaleArticleContext", "BackendSessionChanged", "ProtocolError", "StorageUnavailable"]);
export const LoadProgress = Schema.Struct({ operationId: OpaqueId, sequence: SafeCounter, pages: maximum(20), comments: maximum(100000),
  state: Schema.Literals(["loading", "cancelling", "completed", "cancelled", "failed"]), failureCode: Schema.NullOr(FailureCode) });
export const DraftData = Schema.Struct({ summary: DraftSummary, article: Schema.NullOr(ArticleData), filters: FilterConfiguration, prizes: PrizeConfiguration,
  participants: maximum(100000), included: maximum(100000), excluded: maximum(100000), authorIdentifiable: Schema.Boolean, load: Schema.NullOr(LoadProgress),
}).check(Schema.makeFilter((draft) => draft.included + draft.excluded === draft.participants ? undefined : "participant count invariant"));
export const DraftOperation = Schema.Struct({ operationId: OpaqueId, state: Schema.Literals(["unknown", "pending", "succeeded", "failed"]), summary: Schema.NullOr(DraftSummary), failureCode: Schema.NullOr(FailureCode) }).check(Schema.makeFilter((operation) => operation.state === "unknown" ? operation.summary === null && operation.failureCode === null ? undefined : "unknown operation metadata" : operation.summary === null ? "known operation summary required" : operation.state === "failed" ? operation.failureCode === null ? "failure code required" : undefined : operation.failureCode !== null ? "unexpected failure code" : undefined));
export const ParticipantData = Schema.Struct({ id: OpaqueId, nickname: Schema.String, publicIdentifier: Schema.String,
  kind: Schema.Literals(["fixed", "semi_fixed", "anonymous"]), badgeCategory: Schema.optionalKey(BadgeCategory), classification: Schema.Literals(["unclassified", "included", "excluded"]),
  reason: Schema.Literals(["", "anonymous", "badge_category", "author", "dccon", "time_cut", "exclude_keyword", "include_keyword"]), included: Schema.Boolean,
  commentCount: maximum(100000), previews: Schema.Array(Schema.String.check(Schema.isMaxLength(256))).check(Schema.isMaxLength(3)) });
export const DraftContext = Schema.Struct({ backendSessionId: OpaqueId, draftId: OpaqueId, revision: SafeCounter, articleGeneration: SafeCounter });
export const ParticipantsPage = Schema.Struct({ context: DraftContext, total: maximum(100000), matched: maximum(100000), offset: SafeCounter,
  rows: Schema.Array(ParticipantData).check(Schema.isMaxLength(100)) });
export const CommentData = Schema.Struct({ id: OpaqueId, parentId: Schema.NullOr(OpaqueId), kind: Schema.Literals(["text", "dccon", "voice"]),
  text: Schema.String, postedAt: nullableTime, mediaUrls: Schema.Array(Schema.String.check(Schema.makeFilter((url)=>isSafeDcMediaUrl(url)?undefined:"unsafe media origin"))) });
export const CommentsPage = Schema.Struct({ context: DraftContext, participantId: OpaqueId, total: maximum(100000), offset: SafeCounter,
  rows: Schema.Array(CommentData).check(Schema.isMaxLength(50)) });
export const FrozenParticipantsPage = Schema.Struct({ collectionId: OpaqueId, revision: SafeCounter, total: maximum(100000), matched: maximum(100000), offset: SafeCounter, rows: Schema.Array(ParticipantData).check(Schema.isMaxLength(100)) });
export const FrozenCommentsPage = Schema.Struct({ collectionId: OpaqueId, revision: SafeCounter, participantId: OpaqueId, total: maximum(100000), offset: SafeCounter, rows: Schema.Array(CommentData).check(Schema.isMaxLength(50)) });
export interface FrozenParticipantsQuery { readonly collectionId: string; readonly expectedRevision?: number | null; readonly query: string; readonly group: string; readonly offset: number; readonly limit: number }
export interface FrozenCommentsQuery { readonly collectionId: string; readonly expectedRevision?: number | null; readonly participantId: string; readonly offset: number; readonly limit: number }
export const WinnerData = Schema.Struct({ participant: ParticipantData, prizeId: OpaqueId, prizeName: Schema.String, slot: maximum(10) });
export const RoundData = Schema.Struct({ collectionId: OpaqueId, roundId: OpaqueId, number: SafeCounter, attempt: SafeCounter,
  state: Schema.Literals(["pending_schedule", "scheduled", "executing", "completed", "cancelled", "failed"]), revision: SafeCounter, roundVersion: SafeCounter,
  mode: Schema.Literals(["immediate", "reservation"]), message: Schema.String, prizes: Schema.Array(PrizeInput).check(Schema.isMaxLength(10)),
  scheduledAt: nullableTime, executedAt: nullableTime, failureCode: Schema.NullOr(FailureCode), winners: Schema.Array(WinnerData).check(Schema.isMaxLength(10)),
  algorithmVersion: Schema.String, appVersion: Schema.String });
export const CollectionData = Schema.Struct({ collectionId: OpaqueId, revision: SafeCounter, article: ArticleData, snapshot: SnapshotSummary,
  filters: FilterConfiguration, participantCount: maximum(100000), selectedCount: maximum(100000), remainingCount: maximum(100000),
  rounds: Schema.Array(RoundData).check(Schema.isMaxLength(50)), roundTotal: maximum(0xffffffff).check(Schema.isGreaterThanOrEqualTo(1)),
  roundOffset: maximum(0xffffffff), latestRound: RoundData }).check(Schema.makeFilter((value) => {
    if (value.latestRound.collectionId !== value.collectionId || value.latestRound.number !== value.roundTotal) return "latest round invariant";
    if (value.rounds.length > Math.max(0, value.roundTotal - value.roundOffset)) return "round page bounds";
    if (new Set(value.rounds.map(round => round.roundId)).size !== value.rounds.length) return "duplicate round identity";
    const latest = value.rounds.find(round => round.number === value.roundTotal);
    if (latest !== undefined && (latest.roundId !== value.latestRound.roundId || latest.revision !== value.latestRound.revision || latest.roundVersion !== value.latestRound.roundVersion || latest.state !== value.latestRound.state)) return "latest page identity";
    const end = value.roundTotal - value.roundOffset;
    return value.rounds.every((round, index) => round.collectionId === value.collectionId && round.number === end - value.rounds.length + index + 1) ? undefined : "round page order";
  }));

export type Draft = typeof DraftData.Type;
export type Filters = typeof FilterConfiguration.Type;
export type Prizes = typeof PrizeConfiguration.Type;
export type Participant = typeof ParticipantData.Type;
export type Collection = typeof CollectionData.Type;
export type Round = typeof RoundData.Type;
export interface CollectionReadOptions { readonly roundOffset?: number; readonly roundLimit?: number; readonly roundId?: string | null }
export type ProductReply<A> = ReplyHeader & { readonly data: A; readonly operationId: string | null; readonly revision: number | null };
export interface MutationHeader { readonly protocolVersion: 1; readonly backendSessionId: string; readonly operationId: string; readonly expectedRevision: number }
export interface DraftMutationHeader extends MutationHeader { readonly draftId: string; readonly articleGeneration: number }
export interface DraftQuery { readonly backendSessionId: string; readonly draftId: string }
export interface LoadRequest extends DraftMutationHeader { readonly url: string }
export interface DraftEditRequest extends DraftMutationHeader {
  readonly kind: "UpdateFilters" | "SetPrizes" | "ToggleParticipant" | "SetAllUnclassified" | "ResetManual" | "ResetFilters" | "ResetArticle" | "CancelLoad";
  readonly filters: Filters | null; readonly prizes: Prizes | null; readonly participantId: string; readonly included: boolean | null;
}
export interface ParticipantQuery extends typeofContext { readonly query: string; readonly offset: number; readonly limit: number; readonly group: string }
type typeofContext = typeof DraftContext.Type;
export interface CommentsQuery extends typeofContext { readonly participantId: string; readonly offset: number; readonly limit: number }
export interface CreateCollectionRequest extends DraftMutationHeader { readonly afterSequence: number; readonly mode: "immediate" | "reservation" }
export interface CollectionCommandRequest extends MutationHeader {
  readonly collectionId: string; readonly roundId: string; readonly expectedVersion: number | null;
  readonly prizes: ReadonlyArray<typeof PrizeInput.Type>; readonly message: string; readonly mode: "immediate" | "reservation"; readonly scheduledAt: string | null; readonly quickDelaySeconds?: 10 | 30 | 60 | 120 | null;
}
export interface ProductBackend {
 readonly queryFrozenParticipants: (request: FrozenParticipantsQuery) => Effect.Effect<ProductReply<typeof FrozenParticipantsPage.Type>, BackendError>;
 readonly queryFrozenComments: (request: FrozenCommentsQuery) => Effect.Effect<ProductReply<typeof FrozenCommentsPage.Type>, BackendError>;
  readonly getDraftOperation: (id: string) => Effect.Effect<ProductReply<typeof DraftOperation.Type>, BackendError>;
  readonly createDraft: (request: MutationHeader) => Effect.Effect<ProductReply<Draft>, BackendError>;
  readonly getDraft: (request: DraftQuery) => Effect.Effect<ProductReply<Draft>, BackendError>;
  readonly loadArticle: (request: LoadRequest) => Effect.Effect<ProductReply<Draft>, BackendError>;
  readonly editDraft: (request: DraftEditRequest) => Effect.Effect<ProductReply<Draft>, BackendError>;
  readonly queryParticipants: (request: ParticipantQuery) => Effect.Effect<ProductReply<typeof ParticipantsPage.Type>, BackendError>;
  readonly queryParticipantComments: (request: CommentsQuery) => Effect.Effect<ProductReply<typeof CommentsPage.Type>, BackendError>;
  readonly createCollection: (request: CreateCollectionRequest) => Effect.Effect<ProductReply<Collection>, BackendError>;
  readonly getCollection: (collectionId: string, options?: CollectionReadOptions) => Effect.Effect<ProductReply<Collection>, BackendError>;
  readonly rerun: (request: CollectionCommandRequest) => Effect.Effect<ProductReply<Collection>, BackendError>;
  readonly setSchedule: (request: CollectionCommandRequest) => Effect.Effect<ProductReply<Collection>, BackendError>;
  readonly cancelSchedule: (request: CollectionCommandRequest) => Effect.Effect<ProductReply<Collection>, BackendError>;
  readonly retryRound: (request: CollectionCommandRequest) => Effect.Effect<ProductReply<Collection>, BackendError>;
}
