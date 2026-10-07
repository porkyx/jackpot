export interface DraftScreenContext {
  readonly backendSessionId: string;
  readonly draftId: string;
  readonly revision: number;
  readonly articleGeneration: number;
}
export type ParticipantGroup = "included" | "excluded" | "unclassified";
export interface ParticipantSearch { readonly group: ParticipantGroup; readonly search: string; readonly offset: number }
export interface ParticipantRow { readonly participantId: string; readonly displayName: string; readonly group: ParticipantGroup }
// A screen read seam. This is deliberately not a registered Backend method:
// T02/T03 must map the real, decoded Go page contract before dispatching it.
export interface ParticipantPage extends ParticipantSearch {
  readonly context: DraftScreenContext;
  readonly rows: ReadonlyArray<ParticipantRow>;
  readonly groupCount: number;
  readonly matchCount: number;
}
export interface ParticipantRequest extends ParticipantSearch, DraftScreenContext {
  readonly routeGeneration: number;
  readonly queryGeneration: number;
  readonly limit: 100;
}
export type ScreenQuery = { readonly _tag: "idle" } | { readonly _tag: "loading"; readonly request: ParticipantRequest }
  | { readonly _tag: "ready"; readonly page: ParticipantPage }
  | { readonly _tag: "failed"; readonly messageKey: "ProtocolError" | "TransportError" };
export type CreateScreenModel = { readonly _tag: "closed" } | {
  readonly _tag: "open";
  readonly routeGeneration: number;
  readonly context: DraftScreenContext | null;
  readonly queryGeneration: number;
  readonly search: ParticipantSearch;
  readonly query: ScreenQuery;
  readonly visiblePage: ParticipantPage | null;
  readonly cache: ReadonlyArray<ParticipantPage>;
};
export type OpenCreateScreen = Extract<CreateScreenModel, { readonly _tag: "open" }>;
function counter(value: number): void { if (!Number.isSafeInteger(value) || value < 0) throw new Error("Screen counter is invalid"); }
function next(value: number): number { counter(value); if (value === Number.MAX_SAFE_INTEGER) throw new Error("Screen counter cannot advance"); return value + 1; }
function ownContext(context: DraftScreenContext): DraftScreenContext {
  if (context.backendSessionId.length === 0 || context.draftId.length === 0) throw new Error("Screen context identity is required");
  counter(context.revision); counter(context.articleGeneration);
  return Object.freeze({ backendSessionId: context.backendSessionId, draftId: context.draftId, revision: context.revision, articleGeneration: context.articleGeneration });
}
function ownSearch(search: ParticipantSearch): ParticipantSearch {
  if (!["included", "excluded", "unclassified"].includes(search.group)) throw new Error("Unknown participant group");
  counter(search.offset);
  return Object.freeze({ group: search.group, search: search.search, offset: search.offset });
}
function sameContext(left: DraftScreenContext | null, right: DraftScreenContext | null): boolean {
  return left === null || right === null ? left === right : left.backendSessionId === right.backendSessionId && left.draftId === right.draftId && left.revision === right.revision && left.articleGeneration === right.articleGeneration;
}
function sameSearch(left: ParticipantSearch, right: ParticipantSearch): boolean { return left.group === right.group && left.search === right.search && left.offset === right.offset; }
function ownPage(page: ParticipantPage): ParticipantPage {
  counter(page.groupCount); counter(page.matchCount);
  if (page.rows.length > 100 || page.matchCount > page.groupCount || page.rows.length > page.matchCount || (page.rows.length > 0 && page.rows.length > page.matchCount - page.offset) || new Set(page.rows.map((row) => row.participantId)).size !== page.rows.length) throw new Error("Participant page bounds differ");
  const rows = page.rows.map((row) => {
    if (row.participantId.length === 0 || row.group !== page.group) throw new Error("Participant row identity/group is invalid");
    return Object.freeze({ participantId: row.participantId, displayName: row.displayName, group: row.group });
  });
  return Object.freeze({ ...ownSearch(page), context: ownContext(page.context), rows: Object.freeze(rows), groupCount: page.groupCount, matchCount: page.matchCount });
}
function ownScreen(model: OpenCreateScreen): OpenCreateScreen {
  return Object.freeze({ ...model, context: model.context === null ? null : ownContext(model.context), search: ownSearch(model.search), query: Object.freeze({ ...model.query }), cache: Object.freeze([...model.cache]) });
}
export function createScreenModel(routeGeneration: number, context: DraftScreenContext | null): OpenCreateScreen {
  counter(routeGeneration);
  return ownScreen({ _tag: "open", routeGeneration, context, queryGeneration: 0, search: { group: "unclassified", search: "", offset: 0 }, query: { _tag: "idle" }, visiblePage: null, cache: [] });
}
export function updateScreenContext(model: CreateScreenModel, context: DraftScreenContext | null): CreateScreenModel {
  if (model._tag === "closed" || sameContext(model.context, context)) return model;
  return ownScreen({ ...model, context, queryGeneration: next(model.queryGeneration), query: { _tag: "idle" }, visiblePage: null, cache: [] });
}
export function prepareParticipantQuery(model: CreateScreenModel, search: ParticipantSearch): { readonly model: OpenCreateScreen; readonly request: ParticipantRequest | null } {
  if (model._tag === "closed" || model.context === null) throw new Error("Participant query requires an open draft screen");
  const normalized = ownSearch(search);
  const queryGeneration = next(model.queryGeneration);
  const cached = model.cache.find((page) => sameSearch(page, normalized));
  if (cached !== undefined) return { model: ownScreen({ ...model, search: normalized, queryGeneration, query: { _tag: "ready", page: cached }, visiblePage: cached, cache: [...model.cache.filter((page) => page !== cached), cached] }), request: null };
  const request = Object.freeze({ ...model.context, ...normalized, routeGeneration: model.routeGeneration, queryGeneration, limit: 100 as const });
  return { model: ownScreen({ ...model, search: normalized, queryGeneration, query: { _tag: "loading", request } }), request };
}
export function screenAcceptsRequest(model: CreateScreenModel, request: ParticipantRequest): boolean {
  return model._tag === "open" && sameContext(model.context, request) && model.routeGeneration === request.routeGeneration && model.queryGeneration === request.queryGeneration && request.limit === 100 && sameSearch(model.search, request);
}
export function receiveParticipantPage(model: CreateScreenModel, request: ParticipantRequest, page: ParticipantPage): CreateScreenModel {
  if (model._tag !== "open" || !screenAcceptsRequest(model, request) || !sameContext(page.context, request) || !sameSearch(page, request)) return model;
  const owned = ownPage(page);
  return ownScreen({ ...model, query: { _tag: "ready", page: owned }, visiblePage: owned, cache: [...model.cache.filter((entry) => !sameSearch(entry, owned)), owned].slice(-3) });
}
export function failParticipantQuery(model: CreateScreenModel, request: ParticipantRequest, messageKey: "ProtocolError" | "TransportError"): CreateScreenModel {
  if (model._tag !== "open" || !screenAcceptsRequest(model, request)) return model;
  return ownScreen({ ...model, query: { _tag: "failed", messageKey } });
}
export function closeScreenModel(): CreateScreenModel { return Object.freeze({ _tag: "closed" }); }
export function projectCreateScreen(model: CreateScreenModel) {
  if (model._tag === "closed") return Object.freeze({ _tag: "closed" } as const);
  return Object.freeze({ _tag: "open", loading: model.query._tag === "loading", error: model.query._tag === "failed" ? model.query.messageKey : null, search: model.search, page: model.visiblePage } as const);
}
