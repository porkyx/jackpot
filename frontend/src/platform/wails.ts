import { Events } from "@wailsio/runtime";
import { Effect, Layer, Schema } from "effect";
import * as ExportCalls from "../../bindings/github.com/porkyx/jackpot/internal/desktop/exportservice";
import { responseEnvelope } from "../contracts/schemas";
import { ResultExportFailure, type ResultExportPorts } from "../features/export/view";
import * as Bindings from "../../bindings/github.com/porkyx/jackpot/internal/desktop/service";
import * as Recovery from "../../bindings/github.com/porkyx/jackpot/internal/desktop/recoveryservice";
import * as DraftCalls from "../../bindings/github.com/porkyx/jackpot/internal/desktop/draftservice";
import * as RoundCalls from "../../bindings/github.com/porkyx/jackpot/internal/desktop/roundservice";
import type { CollectionCommandRequest, Filters, Prizes } from "../contracts/product";
import { makeWailsProductBackend } from "./product";
import { Backend } from "../contracts/backend";
import { makeWailsBackend, StateChangedTopic } from "./backend";

// Reads use their screen scope. The Coordinator supplies app-owned signals for
// admitted commands; a route change never cancels an admitted Go mutation.
const filtersInput = (value: Filters | null) => value === null ? null : ({ ...value, includeKeywords: [...value.includeKeywords], excludeKeywords: [...value.excludeKeywords] });
const prizesInput = (value: Prizes | null) => value === null ? null : ({ ...value, multiple: [...value.multiple] });
const collectionInput = (value: CollectionCommandRequest) => ({ ...value, quickDelaySeconds: value.quickDelaySeconds ?? null, prizes: [...value.prizes] });
export const WailsBackendLive = Layer.sync(Backend, () => makeWailsBackend({
  bootstrap: (signal) => Bindings.Bootstrap().cancelOn(signal),
  listPendingOperations: (request, signal) => Bindings.ListPendingOperations(request).cancelOn(signal),
  getOperation: (operationId, signal) => Recovery.GetOperation({ operationId }).cancelOn(signal),
  onStateChanged: (listener) => Events.On(StateChangedTopic, (event) => listener(event.data as unknown)),
}, makeWailsProductBackend({
 queryFrozenParticipants: (request, signal) => RoundCalls.QueryFrozenParticipants({ ...request, expectedRevision: request.expectedRevision ?? null }).cancelOn(signal),
 queryFrozenComments: (request, signal) => RoundCalls.QueryFrozenComments({ ...request, expectedRevision: request.expectedRevision ?? null }).cancelOn(signal),
 getDraftOperation: (operationId, signal) => DraftCalls.GetDraftOperation({ operationId }).cancelOn(signal),
 createDraft: (request, signal) => DraftCalls.CreateDraft(request).cancelOn(signal),
 getDraft: (request, signal) => DraftCalls.GetDraft(request).cancelOn(signal),
 loadArticle: (request, signal) => DraftCalls.LoadArticle(request).cancelOn(signal),
 editDraft: (request, signal) => DraftCalls.EditDraft({ ...request, filters: filtersInput(request.filters), prizes: prizesInput(request.prizes) }).cancelOn(signal),
 queryParticipants: (request, signal) => DraftCalls.QueryParticipants(request).cancelOn(signal),
 queryParticipantComments: (request, signal) => DraftCalls.QueryParticipantComments(request).cancelOn(signal),
 createCollection: (request, signal) => RoundCalls.CreateCollection(request).cancelOn(signal),
 getCollection: (collectionId, options, signal) => RoundCalls.GetCollection({ collectionId, roundOffset: options.roundOffset ?? 0, roundLimit: options.roundLimit ?? 0, roundId: options.roundId ?? "" }).cancelOn(signal),
 rerun: (request, signal) => RoundCalls.Rerun(collectionInput(request)).cancelOn(signal),
 setSchedule: (request, signal) => RoundCalls.SetSchedule(collectionInput(request)).cancelOn(signal),
 cancelSchedule: (request, signal) => RoundCalls.CancelSchedule(collectionInput(request)).cancelOn(signal),
 retryRound: (request, signal) => RoundCalls.RetryRound(collectionInput(request)).cancelOn(signal),
})));

const ExportData = Schema.Struct({ status: Schema.Literals(["saved", "cancelled", "copied", "opened"]) });
function exportCall(call: (signal: AbortSignal) => PromiseLike<unknown>, reason: "permission" | "clipboard" | "unavailable") {
 return Effect.tryPromise({ try: call, catch: () => new ResultExportFailure({ reason }) }).pipe(Effect.flatMap((raw) => Effect.try({ try: () => {
  const decoded = Schema.decodeUnknownSync(responseEnvelope(ExportData))(raw);
  if (!decoded.ok || decoded.data === undefined || decoded.data === null) throw new ResultExportFailure({ reason: decoded.code === "InvalidInput" ? "encoding" : reason });
  return decoded.data.status;
 }, catch: (error) => error instanceof ResultExportFailure ? error : new ResultExportFailure({ reason: "unavailable" }) })));
}
function pngBase64(blob: Blob): Effect.Effect<string, ResultExportFailure> {
 return Effect.gen(function*() {
  if (blob.type !== "image/png" || blob.size === 0 || blob.size > 25 * 1024 * 1024) return yield* Effect.fail(new ResultExportFailure({ reason: "size" }));
  const buffer = yield* Effect.tryPromise({ try: () => blob.arrayBuffer(), catch: () => new ResultExportFailure({ reason: "encoding" }) });
  return yield* Effect.try({ try: () => {
   const bytes = new Uint8Array(buffer); const chunks: Array<string> = [];
   for (let offset = 0; offset < bytes.length; offset += 16384) chunks.push(String.fromCharCode(...bytes.subarray(offset, offset + 16384)));
   return btoa(chunks.join(""));
  }, catch: () => new ResultExportFailure({ reason: "encoding" }) });
 });
}
export const WailsResultExportPorts: ResultExportPorts = {
 savePng: (blob, suggestedFilename) => pngBase64(blob).pipe(Effect.flatMap((dataBase64) => exportCall((signal) => ExportCalls.SavePNG({ suggestedFilename, dataBase64 }).cancelOn(signal), "permission")), Effect.flatMap((status) => status === "saved" || status === "cancelled" ? Effect.succeed(status) : Effect.fail(new ResultExportFailure({ reason: "unavailable" })))),
 copyText: (text) => exportCall((signal) => ExportCalls.CopyText({ text }).cancelOn(signal), "clipboard").pipe(Effect.flatMap((status) => status === "copied" ? Effect.void : Effect.fail(new ResultExportFailure({ reason: "clipboard" })))),
};
export const openWailsArticle = (url: string): Effect.Effect<void, ResultExportFailure> => exportCall((signal) => ExportCalls.OpenArticle({ url }).cancelOn(signal), "unavailable").pipe(Effect.flatMap((status) => status === "opened" ? Effect.void : Effect.fail(new ResultExportFailure({ reason: "unavailable" }))));
