import { expect, test } from "vitest";
import { closeScreenModel, createScreenModel, failParticipantQuery, prepareParticipantQuery, projectCreateScreen, receiveParticipantPage, screenAcceptsRequest, updateScreenContext,
  type CreateScreenModel, type OpenCreateScreen, type ParticipantPage, type ParticipantRequest, type ParticipantSearch } from "../../src/screens/create/screenModel";
import { createInputScreen, decideInputEnter, reduceInputScreen, type InputScreenAction } from "../../src/screens/create/inputModel";

const context = { backendSessionId: "session", draftId: "draft", revision: 1, articleGeneration: 1 };
const search: ParticipantSearch = { group: "unclassified", search: "", offset: 0 };
function open(model: CreateScreenModel): OpenCreateScreen { if (model._tag !== "open") throw new Error("test expected open model"); return model; }
function start(model: CreateScreenModel = createScreenModel(1, context), query = search) {
  const plan = prepareParticipantQuery(model, query);
  if (plan.request === null) throw new Error("test expected a new request");
  return { model: plan.model, request: plan.request };
}
function page(request: ParticipantRequest, size = 1): ParticipantPage {
  return { context: { ...context, backendSessionId: request.backendSessionId, draftId: request.draftId, revision: request.revision, articleGeneration: request.articleGeneration }, group: request.group, search: request.search, offset: request.offset,
    rows: Array.from({ length: size }, (_, index) => ({ participantId: `p-${index}`, displayName: `닉네임 ${index}`, group: request.group })), groupCount: 10_000, matchCount: 10_000 };
}
test("screen explicitly owns query/page data and strips secrets and DOM additions from context/rows", () => {
  const original = { ...context, password: "do-not-retain", node: {} };
  const initial = createScreenModel(1, original);
  const pending = start(initial);
  const result = page(pending.request);
  const source = { ...result, password: "do-not-retain", rows: [{ ...result.rows[0]!, node: {}, password: "do-not-retain" }] };
  const ready = open(receiveParticipantPage(pending.model, pending.request, source));
  original.revision = 99;
  expect(initial.context?.revision).toBe(1);
  expect(JSON.stringify(ready)).not.toContain("do-not-retain");
  expect(ready.context).toEqual(context);
  expect(Object.isFrozen(ready)).toBe(true);
  expect(Object.isFrozen(ready.cache)).toBe(true);
  expect(Object.isFrozen(ready.visiblePage?.rows)).toBe(true);
  expect(Object.isFrozen(ready.visiblePage?.rows[0])).toBe(true);
  expect(Object.keys(ready)).toEqual(["_tag", "routeGeneration", "context", "queryGeneration", "search", "query", "visiblePage", "cache"]);
  expect(projectCreateScreen(ready)).toEqual({ _tag: "open", loading: false, error: null, search, page: ready.visiblePage });
});
test.each([0, 1, 100])("participant page %i rows is bounded and accepted", (size) => {
  const pending = start();
  const ready = open(receiveParticipantPage(pending.model, pending.request, page(pending.request, size)));
  expect(ready.visiblePage?.rows.length).toBe(size);
  expect(ready.query._tag).toBe("ready");
});
test("query cache uses three-page LRU, cache hits invalidate old query generations, and eviction requests again", () => {
  let model: CreateScreenModel = createScreenModel(1, context);
  for (let offset = 0; offset <= 300; offset += 100) {
    const pending = start(model, { ...search, offset });
    model = receiveParticipantPage(pending.model, pending.request, page(pending.request));
  }
  expect(open(model).cache.map((entry) => entry.offset)).toEqual([100, 200, 300]);
  const hit = prepareParticipantQuery(model, { ...search, offset: 100 });
  expect(hit.request).toBeNull();
  expect(hit.model.queryGeneration).toBe(5);
  expect(hit.model.cache.map((entry) => entry.offset)).toEqual([200, 300, 100]);
  expect(prepareParticipantQuery(hit.model, search).request?.queryGeneration).toBe(6);
});
test("replacing a same-key page does not grow cache and changes the visible immutable value", () => {
  const pending = start();
  const first = receiveParticipantPage(pending.model, pending.request, page(pending.request));
  const replacement = { ...page(pending.request), rows: [{ participantId: "replacement", displayName: "변경", group: search.group }] };
  const second = open(receiveParticipantPage(first, pending.request, replacement));
  expect(second.cache).toHaveLength(1);
  expect(second.visiblePage?.rows[0]?.participantId).toBe("replacement");
});
for (const [field, value] of [["backendSessionId", "other"], ["draftId", "other"], ["revision", 2], ["articleGeneration", 2], ["routeGeneration", 2], ["queryGeneration", 2], ["group", "included"], ["search", "other"], ["offset", 100], ["limit", 99]] as const) {
  test(`request fence independently rejects changed ${field}`, () => {
    const pending = start();
    const old = { ...pending.request, [field]: value } as ParticipantRequest;
    expect(screenAcceptsRequest(pending.model, old)).toBe(false);
    expect(receiveParticipantPage(pending.model, old, page(pending.request))).toBe(pending.model);
    expect(failParticipantQuery(pending.model, old, "TransportError")).toBe(pending.model);
  });
}
for (const [field, value] of [["backendSessionId", "other"], ["draftId", "other"], ["revision", 2], ["articleGeneration", 2]] as const) {
  test(`response context independently rejects changed ${field}`, () => {
    const pending = start(); const response = page(pending.request);
    expect(receiveParticipantPage(pending.model, pending.request, { ...response, context: { ...response.context, [field]: value } })).toBe(pending.model);
  });
  test(`authoritative ${field} change clears pages and cache without resetting query generation`, () => {
    const pending = start(); const ready = receiveParticipantPage(pending.model, pending.request, page(pending.request));
    const changed = open(updateScreenContext(ready, { ...context, [field]: value }));
    expect(changed.cache).toEqual([]); expect(changed.visiblePage).toBeNull(); expect(changed.queryGeneration).toBe(2);
    expect(receiveParticipantPage(changed, pending.request, page(pending.request))).toBe(changed);
  });
}
for (const change of [{ group: "included" as const }, { search: "other" }, { offset: 100 }]) test(`response search key ${JSON.stringify(change)} must match the request`, () => {
  const pending = start();
  expect(receiveParticipantPage(pending.model, pending.request, { ...page(pending.request), ...change })).toBe(pending.model);
});
test("new search wins over old replies even with equal entity revision", () => {
  const first = start(); const second = start(first.model, { ...search, search: "새 검색" });
  expect(receiveParticipantPage(second.model, first.request, page(first.request))).toBe(second.model);
  expect(receiveParticipantPage(second.model, second.request, page(second.request))._tag).toBe("open");
});
test("query failure preserves last good page and never fabricates an empty ready result", () => {
  const first = start(); const ready = receiveParticipantPage(first.model, first.request, page(first.request));
  const pending = start(ready, { ...search, offset: 100 });
  const failed = open(failParticipantQuery(pending.model, pending.request, "TransportError"));
  expect(failed.visiblePage).toBe(open(ready).visiblePage);
  expect(failed.cache).toHaveLength(1);
  const loading = projectCreateScreen(pending.model); const error = projectCreateScreen(failed);
  expect(loading._tag === "open" && loading.loading).toBe(true);
  expect(error._tag === "open" && error.error).toBe("TransportError");
});
test("no draft and closed screen deny queries, drop caches and accept no later page", () => {
  const empty = createScreenModel(0, null); expect(updateScreenContext(empty, null)).toBe(empty);
  expect(() => prepareParticipantQuery(empty, search)).toThrow("open draft");
  const pending = start(); const closed = closeScreenModel();
  expect(() => prepareParticipantQuery(closed, search)).toThrow("open draft");
  expect(receiveParticipantPage(closed, pending.request, page(pending.request))).toBe(closed);
  expect(failParticipantQuery(closed, pending.request, "ProtocolError")).toBe(closed);
  expect(updateScreenContext(closed, context)).toBe(closed);
  expect(screenAcceptsRequest(closed, pending.request)).toBe(false);
  expect(screenAcceptsRequest(empty, pending.request)).toBe(false);
  expect(updateScreenContext(pending.model, context)).toBe(pending.model);
  expect(open(updateScreenContext(pending.model, null)).context).toBeNull();
  expect(projectCreateScreen(closed)).toEqual({ _tag: "closed" });
});
test.each([-1, 0.5, NaN, Infinity, Number.MAX_SAFE_INTEGER + 1])("screen counter %s is rejected in routes/context/search/page", (value) => {
  expect(() => createScreenModel(value, context)).toThrow("counter");
  expect(() => createScreenModel(0, { ...context, revision: value })).toThrow("counter");
  expect(() => start(undefined, { ...search, offset: value })).toThrow("counter");
  const pending = start(); expect(() => receiveParticipantPage(pending.model, pending.request, { ...page(pending.request), groupCount: value })).toThrow("counter");
});
test("query generation reaches MAX_SAFE_INTEGER then fails instead of reusing old tokens", () => {
  const initial = { ...createScreenModel(1, context), queryGeneration: Number.MAX_SAFE_INTEGER - 1 };
  const pending = start(initial); expect(pending.request.queryGeneration).toBe(Number.MAX_SAFE_INTEGER);
  expect(() => start(pending.model)).toThrow("cannot advance");
});
test.each([{ ...context, backendSessionId: "" }, { ...context, draftId: "" }])("empty identity fails before creating a query model", (invalid) => expect(() => createScreenModel(1, invalid)).toThrow("identity"));
test("unknown query group fails loudly", () => expect(() => start(undefined, { ...search, group: "unknown" } as unknown as ParticipantSearch)).toThrow("group"));
test.each(["too_many_rows", "counts", "short_total", "offset_total", "duplicate", "empty_id", "wrong_group"])("page invariant %s remains enforced", (failure) => {
  const pending = start(); const response = page(pending.request);
  let invalid: ParticipantPage = response;
  if (failure === "too_many_rows") invalid = page(pending.request, 101);
  if (failure === "counts") invalid = { ...response, groupCount: 0 };
  if (failure === "short_total") invalid = { ...response, matchCount: 0 };
  if (failure === "offset_total") invalid = { ...response, matchCount: 0, rows: response.rows };
  if (failure === "duplicate") invalid = { ...response, rows: [response.rows[0]!, response.rows[0]!] };
  if (failure === "empty_id") invalid = { ...response, rows: [{ ...response.rows[0]!, participantId: "" }] };
  if (failure === "wrong_group") invalid = { ...response, rows: [{ ...response.rows[0]!, group: "included" }] };
  expect(() => receiveParticipantPage(pending.model, pending.request, invalid)).toThrow();
});

test("input screen owns only composition/caret and closes without retaining DOM or raw values", () => {
  let model = createInputScreen();
  model = reduceInputScreen(model, { _tag: "Focused", start: 1, end: 2 });
  expect(model).toEqual({ _tag: "open", composition: "idle", focus: { start: 1, end: 2 } });
  expect(Object.isFrozen(model)).toBe(true);
  model = reduceInputScreen(model, { _tag: "Blurred" }); expect(model._tag === "open" && model.focus).toBeNull();
  model = reduceInputScreen(model, { _tag: "Closed" });
  expect(model).toEqual({ _tag: "closed" });
  expect(reduceInputScreen(model, { _tag: "CompositionStarted" })).toBe(model);
  expect(decideInputEnter(model, false, 13).submit).toBe(false);
});
test("composition, event composing, legacy keycode independently block Enter; only one trailing Enter is swallowed", () => {
  const initial = createInputScreen();
  expect(decideInputEnter(initial, false, 13).submit).toBe(true);
  expect(decideInputEnter(initial, true, 13).submit).toBe(false);
  expect(decideInputEnter(initial, false, 229).submit).toBe(false);
  const composing = reduceInputScreen(initial, { _tag: "CompositionStarted" });
  expect(decideInputEnter(composing, false, 13).submit).toBe(false);
  expect(reduceInputScreen(composing, { _tag: "OtherKey" })).toEqual(composing);
  const ended = reduceInputScreen(composing, { _tag: "CompositionEnded" });
  const first = decideInputEnter(ended, false, 13); expect(first.submit).toBe(false);
  expect(decideInputEnter(first.model, false, 13).submit).toBe(true);
});
test.each([{ start: -1, end: 0 }, { start: 0, end: -1 }, { start: NaN, end: 0 }, { start: 0, end: NaN }, { start: 2, end: 1 }, { start: 0.5, end: 1 }])("invalid caret %j is rejected", (selection) => expect(() => reduceInputScreen(createInputScreen(), { _tag: "Focused", ...selection })).toThrow("Caret"));
test("unknown input-screen action violates a production invariant", () => expect(() => reduceInputScreen(createInputScreen(), { _tag: "Unknown" } as unknown as InputScreenAction)).toThrow("Unknown"));
