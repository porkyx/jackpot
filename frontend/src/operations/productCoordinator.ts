import { Data, Deferred, Effect, Exit, Fiber, PubSub, Scope, Semaphore, SubscriptionRef, type Stream } from "effect";
import { BackendRejected, ProtocolError, TransportError, type BackendError, type BootstrapReply } from "../contracts/backend";
import type { Collection, CollectionCommandRequest, Draft, DraftEditRequest, ProductBackend, ProductReply, ParticipantsPage, CommentsPage, FrozenParticipantsPage, FrozenCommentsPage } from "../contracts/product";
import { readProductQuery } from "./productQueries";
import { makeCollectionCache } from "./collectionCache";
import type { ClientIdError } from "../platform/ids";
import type { EditReceipt } from "./coordinatorState";

export class ProductUnavailable extends Data.TaggedError("ProductUnavailable")<{ readonly reason: "session_required" | "blocked" | "closed" | "stale" | "queue_full" | "receipt_expired" | "changed_before_finalize" | "outcome_unknown" }> {}
export type ProductError = BackendError | ProductUnavailable;
export type DraftIntent = Pick<DraftEditRequest, "kind" | "filters" | "prizes" | "participantId" | "included">;
export interface ProductCommands {
 readonly queryFrozenParticipants: (request: Parameters<ProductBackend["queryFrozenParticipants"]>[0]) => Effect.Effect<ProductReply<typeof FrozenParticipantsPage.Type>, ProductError>;
 readonly queryFrozenComments: (request: Parameters<ProductBackend["queryFrozenComments"]>[0]) => Effect.Effect<ProductReply<typeof FrozenCommentsPage.Type>, ProductError>;
 readonly draft: Effect.Effect<Draft | null>;
 readonly changes: Stream.Stream<Draft | null>;
 readonly ensureDraft: Effect.Effect<Draft, ProductError>;
 readonly refreshDraft: Effect.Effect<Draft, ProductError>;
 readonly loadArticle: (url: string) => Effect.Effect<Draft, ProductError>;
 readonly cancelLoad: Effect.Effect<Draft, ProductError>;
 readonly resetArticle: Effect.Effect<Draft, ProductError>;
 readonly edit: (intent: DraftIntent, fieldKey?: string) => Effect.Effect<EditReceipt, ProductError>;
 readonly commitThrough: (receipt: EditReceipt) => Effect.Effect<Draft, ProductError>;
 readonly createCollection: (afterSequence: number, mode: "immediate" | "reservation") => Effect.Effect<Collection, ProductError>;
 readonly queryParticipants: (request: Parameters<ProductBackend["queryParticipants"]>[0]) => Effect.Effect<import("../contracts/product").ProductReply<typeof ParticipantsPage.Type>, ProductError>;
 readonly queryParticipantComments: (request: Parameters<ProductBackend["queryParticipantComments"]>[0]) => Effect.Effect<import("../contracts/product").ProductReply<typeof CommentsPage.Type>, ProductError>;
 readonly getCollection: (id: string, options?: import("../contracts/product").CollectionReadOptions) => Effect.Effect<ProductReply<Collection>, ProductError>;
 readonly collectionCommand: (kind: "rerun" | "setSchedule" | "cancelSchedule" | "retryRound", request: Omit<CollectionCommandRequest,"operationId" | "protocolVersion" | "backendSessionId">) => Effect.Effect<Collection, ProductError>;
}
export interface ProductCoordinator extends ProductCommands {
 readonly acceptBootstrap: (reply: BootstrapReply) => Effect.Effect<void, ProductError>;
 readonly setWritesBlocked: (blocked: boolean) => void;
 readonly recheck: Effect.Effect<void, ProductError>;
}
interface Entry {
 readonly receipt: EditReceipt;
 readonly epoch: symbol;
 readonly session: string;
 intent: DraftIntent | null;
 readonly fieldKey: string | undefined;
 readonly done: Deferred.Deferred<void, ProductError>;
 state: "queued" | "dispatched" | "unknown" | "confirmed" | "rejected" | "superseded";
 replacement: number | null;
 error: ProductError | null;
}
interface DraftFlight { readonly token: symbol; readonly epoch: symbol; readonly session: string; readonly kind: string; id: string | null }
interface OperationFlight { readonly token: symbol; id: string | null }
interface UnknownDraft { readonly session: string; readonly epoch: symbol; readonly draftId: string | null; readonly kind: string }
export interface ProductCoordinatorSource {
 readonly backend: ProductBackend;
 readonly newId: Effect.Effect<string, ClientIdError>;
 readonly lookup: (id: string) => Effect.Effect<import("../contracts/backend").OperationLookupReply, BackendError>;
}
const unavailable = (reason: ProductUnavailable["reason"]) => new ProductUnavailable({ reason });
const blankIntent = (kind: DraftIntent["kind"]): DraftIntent => ({ kind, filters: null, prizes: null, participantId: "", included: null });
function ownDraft(draft: Draft): Draft {
 return Object.freeze({ ...draft, summary: Object.freeze({ ...draft.summary, snapshot: draft.summary.snapshot === null ? null : Object.freeze({ ...draft.summary.snapshot }) }),
  article: draft.article === null ? null : Object.freeze({ ...draft.article }), filters: Object.freeze({ ...draft.filters, includeKeywords: Object.freeze([...draft.filters.includeKeywords]), excludeKeywords: Object.freeze([...draft.filters.excludeKeywords]) }),
  prizes: Object.freeze({ ...draft.prizes, single: Object.freeze({ ...draft.prizes.single }), multiple: Object.freeze(draft.prizes.multiple.map((item) => Object.freeze({ ...item }))) }), load: draft.load === null ? null : Object.freeze({ ...draft.load }) });
}
function ownIntent(intent: DraftIntent): DraftIntent {
 return Object.freeze({ ...intent, filters: intent.filters === null ? null : Object.freeze({ ...intent.filters, includeKeywords: Object.freeze([...intent.filters.includeKeywords]), excludeKeywords: Object.freeze([...intent.filters.excludeKeywords]) }),
  prizes: intent.prizes === null ? null : Object.freeze({ ...intent.prizes, single: Object.freeze({ ...intent.prizes.single }), multiple: Object.freeze(intent.prizes.multiple.map((item) => Object.freeze({ ...item }))) }) });
}
export function makeProductCoordinator(source: ProductCoordinatorSource): Effect.Effect<ProductCoordinator, never, Scope.Scope> {
 return Effect.gen(function*() {
  const owner = yield* Scope.fork(yield* Scope.Scope,"sequential");
  const state = yield* SubscriptionRef.make<Draft|null>(null);
  const lock = yield* Semaphore.make(1);
  const collectionCache=makeCollectionCache();
  const confirmCollection=(collection:Collection)=>collectionCache.remember(collection)?Effect.succeed(collection):Effect.fail(unavailable("stale"));
  let session:string|null=null; let activeDraftId:string|null=null; let blocked=true; let sealed=false; let laneBlocked=false;
  let sequence=0; let successfulPrefix=0; let epoch=Symbol(); let worker:symbol|undefined;
  let flight:DraftFlight|undefined; let cancelFlight:DraftFlight|undefined; let finalizationFlight:OperationFlight|undefined;
  let readFlight:{readonly session:string;readonly draftId:string;readonly done:Deferred.Deferred<Draft,ProductError>}|undefined;
  let recheckFlight:Deferred.Deferred<void,ProductError>|undefined;
  const entries=new Map<number,Entry>();const queue:Entry[]=[];const barriers=new Set<object>();const retired=new Set<string>();
  const collectionFlights=new Map<string,OperationFlight>();const unknownDurable=new Map<string,string|null>();const unknownDraft=new Map<string,UnknownDraft>();
  const open=Effect.suspend(()=>owner.state._tag==="Closed"?Effect.fail(unavailable("closed")):Effect.void);
  const owned=<A,E>(effect:Effect.Effect<A,E>):Effect.Effect<A,E|ProductUnavailable>=>open.pipe(Effect.andThen(effect.pipe(Effect.forkIn(owner),Effect.flatMap(Fiber.join))));
  const readOwned=<A,E>(effect:Effect.Effect<A,E>):Effect.Effect<A,E|ProductUnavailable>=>open.pipe(Effect.andThen(Effect.gen(function*(){const fiber=yield* effect.pipe(Effect.forkIn(owner));return yield* Fiber.join(fiber).pipe(Effect.ensuring(Fiber.interrupt(fiber)));})));
  const nextId=source.newId.pipe(Effect.mapError(()=>unavailable("blocked")));
  const mutation=<A>(effect:Effect.Effect<A,BackendError>):Effect.Effect<A,ProductError>=>effect.pipe(Effect.timeoutOrElse({duration:30000,orElse:()=>Effect.fail(unavailable("outcome_unknown"))}));
  const unresolved=()=>{const active=new Set<string|symbol>();for(const entry of entries.values())if(entry.state==="queued"||entry.state==="dispatched"||entry.state==="unknown")active.add(entry.receipt.intentId);for(const id of unknownDurable.keys())active.add(id);for(const id of unknownDraft.keys())active.add(id);for(const value of [flight,cancelFlight,finalizationFlight,...collectionFlights.values()])if(value!==undefined)active.add(value.id??value.token);return active.size;};
  const guarded=Effect.suspend(()=>session===null?Effect.fail(unavailable("session_required")):blocked||sealed||laneBlocked||flight!==undefined||finalizationFlight!==undefined||unknownDraft.size>0?Effect.fail(unavailable("blocked")):open);
  const retireEntries=(reason:"stale"|"closed")=>Effect.gen(function*(){
    for(const entry of entries.values()){entry.intent=null;entry.state="rejected";entry.error=unavailable(reason);yield* Deferred.fail(entry.done,entry.error);}
    entries.clear();queue.length=0;sequence=0;successfulPrefix=0;worker=undefined;laneBlocked=false;
  });
  const accept=(draft:Draft,expectedSession:string)=>Effect.gen(function*(){
    yield* open;
    if(session!==expectedSession||draft.summary.backendSessionId!==session||(activeDraftId!==null&&activeDraftId!==draft.summary.draftId))return yield* Effect.fail(unavailable("stale"));
    const current=yield* SubscriptionRef.get(state);
    if(current!==null&&(draft.summary.revision<current.summary.revision||draft.summary.articleGeneration<current.summary.articleGeneration))return current;
    if(current!==null&&current.summary.articleGeneration!==draft.summary.articleGeneration){epoch=Symbol();yield* retireEntries("stale");}
    activeDraftId=draft.summary.draftId;const value=ownDraft(draft);yield* SubscriptionRef.set(state,value);return value;
  });
  const refreshDraft:Effect.Effect<Draft,ProductError>=owned(Effect.gen(function*(){
    if(session===null||activeDraftId===null)return yield* Effect.fail(unavailable("session_required"));
    const requestedSession=session;const requestedDraft=activeDraftId;
    if(readFlight?.session===requestedSession&&readFlight.draftId===requestedDraft)return yield* Deferred.await(readFlight.done);
    const pending={session:requestedSession,draftId:requestedDraft,done:yield* Deferred.make<Draft,ProductError>()};readFlight=pending;
    const action=readProductQuery(()=>source.backend.getDraft({backendSessionId:requestedSession,draftId:requestedDraft})).pipe(Effect.flatMap((reply)=>accept(reply.data,requestedSession)));
    const result=yield* Effect.exit(action);
    if(readFlight===pending)readFlight=undefined;
    if(Exit.isSuccess(result)){yield* Deferred.succeed(pending.done,result.value);return result.value;}
    yield* Deferred.failCause(pending.done,result.cause);return yield* Effect.failCause(result.cause);
  }));
  const observeDraft=(operationId:string,expectedSession:string)=>Effect.gen(function*(){
    if(session!==expectedSession)return yield* Effect.fail(unavailable("stale"));
    const reply=yield* source.backend.getDraftOperation(operationId).pipe(Effect.timeoutOrElse({duration:15000,orElse:()=>Effect.fail(unavailable("outcome_unknown"))}),Effect.mapError(()=>unavailable("outcome_unknown")));
    if(session!==expectedSession||reply.backendSessionId!==expectedSession||reply.data.operationId!==operationId)return yield* Effect.fail(unavailable("outcome_unknown"));
    if(reply.data.state==="failed")return yield* Effect.fail(new BackendRejected({code:reply.data.failureCode??"ProtocolError",messageKey:reply.data.failureCode??"ProtocolError"}));
    if(reply.data.state!=="succeeded"||reply.data.summary===null)return yield* Effect.fail(unavailable("outcome_unknown"));
    if(reply.data.summary.backendSessionId!==expectedSession)return yield* Effect.fail(unavailable("outcome_unknown"));
    if(activeDraftId!==null&&activeDraftId!==reply.data.summary.draftId)return yield* Effect.fail(unavailable("stale"));
    activeDraftId=reply.data.summary.draftId;return yield* refreshDraft;
  });
  const pollDraft=(operationId:string,expectedSession:string)=>Effect.gen(function*(){
    for(const delay of [0,1000,2000,4000]){
      if(delay>0)yield* Effect.sleep(delay);
      const result=yield* Effect.result(observeDraft(operationId,expectedSession));
      if(result._tag==="Success")return result.success;
      if(result.failure._tag==="BackendRejected"||result.failure._tag==="ProductUnavailable"&&result.failure.reason==="stale")return yield* Effect.fail(result.failure);
    }
    return yield* Effect.fail(unavailable("outcome_unknown"));
  });
  const observeDurable=(operationId:string,expectedCollection:string|null)=>Effect.gen(function*(){
    const expectedSession=session;
    const reply=yield* source.lookup(operationId).pipe(Effect.timeoutOrElse({duration:15000,orElse:()=>Effect.fail(unavailable("outcome_unknown"))}),Effect.mapError(()=>unavailable("outcome_unknown")));
    const observation=reply.data;
    if(session!==expectedSession||reply.backendSessionId!==expectedSession||observation.operationId!==operationId)return yield* Effect.fail(unavailable("outcome_unknown"));
    if(observation.state==="failed"){unknownDurable.delete(operationId);return yield* Effect.fail(new BackendRejected({code:observation.failureCode??"ProtocolError",messageKey:observation.failureCode??"ProtocolError"}));}
    const collectionId=observation.collectionId;
    if(observation.state!=="succeeded"||collectionId===null||(expectedCollection!==null&&expectedCollection!==collectionId))return yield* Effect.fail(unavailable("outcome_unknown"));
    const collectionReply=yield* readProductQuery(()=>source.backend.getCollection(collectionId));
    if(session!==expectedSession||collectionReply.backendSessionId!==expectedSession)return yield* Effect.fail(unavailable("outcome_unknown"));
    const collection=collectionReply.data;
    if(collection.collectionId!==collectionId)return yield* Effect.fail(unavailable("outcome_unknown"));
    const confirmed=yield* confirmCollection(collection);unknownDurable.delete(operationId);return confirmed;
  });
  const pollDurable=(operationId:string,expectedCollection:string|null)=>Effect.gen(function*(){
    for(const delay of [0,1000,2000,4000]){
      if(delay>0)yield* Effect.sleep(delay);
      const result=yield* Effect.result(observeDurable(operationId,expectedCollection));
      if(result._tag==="Success")return result.success;
      if(result._tag==="Failure"&&result.failure._tag==="BackendRejected")return yield* Effect.fail(result.failure);
    }
    return yield* Effect.fail(unavailable("outcome_unknown"));
  });
  const advance=()=>{while(true){let entry=entries.get(successfulPrefix+1);if(entry===undefined)break;let hops=0;while(entry.state==="superseded"){if(entry.replacement===null||++hops>160)throw new Error("Superseded receipt chain is invalid");const next=entries.get(entry.replacement);if(next===undefined)throw new Error("Superseded receipt replacement is missing");entry=next;}if(entry.state!=="confirmed")break;successfulPrefix++;}};
  const trim=()=>{if(barriers.size>0)return;const terminal=[...entries.values()].filter((entry)=>entry.state==="confirmed"||entry.state==="rejected"||entry.state==="superseded");for(const entry of terminal.slice(0,Math.max(0,terminal.length-128)))if(entry.receipt.sequence<=successfulPrefix)entries.delete(entry.receipt.sequence);};
  const finish=(entry:Entry,result:{readonly _tag:"Success";readonly success:Draft}|{readonly _tag:"Failure";readonly failure:ProductError})=>Effect.gen(function*(){
    entry.intent=null;
    if(entry.epoch!==epoch||entry.session!==session){yield* Deferred.fail(entry.done,unavailable("stale"));return;}
    if(result._tag==="Success"){entry.state="confirmed";entry.error=null;yield* Deferred.succeed(entry.done,undefined);advance();trim();}
    else{entry.error=result.failure;entry.state=result.failure._tag==="ProductUnavailable"&&result.failure.reason==="outcome_unknown"?"unknown":"rejected";laneBlocked=true;yield* Deferred.fail(entry.done,result.failure);}
  });
  const start=Effect.suspend(()=>Effect.gen(function*(){
    if(worker!==undefined||queue.length===0||laneBlocked||sealed)return;
    const token=Symbol();worker=token;
    yield* Effect.gen(function*(){
      while(worker===token&&queue.length>0&&!laneBlocked&&!sealed){
        const entry=queue.shift();if(entry===undefined)return yield* Effect.die(new Error("Nonempty edit queue lost its head"));
        if(entry.state!=="queued"||entry.intent===null)continue;
        const current=yield* SubscriptionRef.get(state);
        if(entry.epoch!==epoch||entry.session!==session||current===null||current.summary.draftId!==entry.receipt.draftId||current.summary.articleGeneration!==entry.receipt.articleGeneration){yield* finish(entry,{_tag:"Failure",failure:unavailable("stale")});continue;}
        entry.state="dispatched";
        const request:DraftEditRequest={...entry.intent,protocolVersion:1,backendSessionId:entry.session,operationId:entry.receipt.intentId,draftId:entry.receipt.draftId,articleGeneration:entry.receipt.articleGeneration,expectedRevision:current.summary.revision};
        let result=yield* Effect.result(mutation(source.backend.editDraft(request)).pipe(Effect.flatMap((reply)=>accept(reply.data,entry.session))));
        if(result._tag==="Failure"&&result.failure._tag!=="BackendRejected"&&entry.epoch===epoch){entry.state="unknown";laneBlocked=true;result=yield* Effect.result(pollDraft(entry.receipt.intentId,entry.session));}
        yield* finish(entry,result);
        if(result._tag==="Success"&&entry.epoch===epoch)laneBlocked=false;
      }
    }).pipe(Effect.catchCause(()=>Effect.sync(()=>{if(worker===token){laneBlocked=true;for(const entry of entries.values())if(entry.state==="dispatched"){entry.state="unknown";entry.intent=null;Deferred.doneUnsafe(entry.done,Effect.fail(unavailable("outcome_unknown")));}}})),Effect.ensuring(Effect.sync(()=>{if(worker===token)worker=undefined;})),Effect.forkIn(owner));
  }));
  const edit:ProductCommands["edit"]=(intent,fieldKey)=>owned(Effect.gen(function*(){
    const intentId=yield* nextId;
    return yield* lock.withPermit(Effect.gen(function*(){
      if(laneBlocked&&unknownDraft.size===0&&![...entries.values()].some((entry)=>entry.state==="unknown"||entry.state==="dispatched")){
        const current=yield* SubscriptionRef.get(state);
        if(current!==null){epoch=Symbol();yield* retireEntries("stale");}
      }
      yield* guarded;const current=yield* SubscriptionRef.get(state);
      if(current===null||(current.summary.state!=="ready"&&current.summary.state!=="empty"))return yield* Effect.fail(unavailable("blocked"));
      if(entries.size>=160||queue.length+[...entries.values()].filter((entry)=>entry.state==="dispatched"||entry.state==="unknown").length>=32||unresolved()>=64)return yield* Effect.fail(unavailable("queue_full"));
      if(sequence>=Number.MAX_SAFE_INTEGER)return yield* Effect.fail(unavailable("blocked"));
      const receipt=Object.freeze({intentId,draftId:current.summary.draftId,articleGeneration:current.summary.articleGeneration,sequence:++sequence});
      const entry:Entry={receipt,epoch,session:current.summary.backendSessionId,intent:ownIntent(intent),fieldKey,done:yield* Deferred.make<void,ProductError>(),state:"queued",replacement:null,error:null};
      const tail=queue.at(-1);
      if(fieldKey!==undefined&&barriers.size===0&&tail?.state==="queued"&&tail.fieldKey===fieldKey&&tail.intent?.kind===intent.kind&&(intent.kind==="UpdateFilters"||intent.kind==="SetPrizes")){tail.state="superseded";tail.replacement=receipt.sequence;tail.intent=null;queue.pop();}
      entries.set(receipt.sequence,entry);queue.push(entry);yield* start;return receipt;
    }));
  }));
  const commitThrough:ProductCommands["commitThrough"]=(receipt)=>readOwned(Effect.gen(function*(){
    if(barriers.size>=64)return yield* Effect.fail(unavailable("queue_full"));const registration={};barriers.add(registration);
    return yield* Effect.gen(function*(){
      const captured=epoch;const current=yield* SubscriptionRef.get(state);
      if(current===null||current.summary.draftId!==receipt.draftId||current.summary.articleGeneration!==receipt.articleGeneration)return yield* Effect.fail(unavailable("stale"));
      const target=entries.get(receipt.sequence);if(target===undefined||target.receipt.intentId!==receipt.intentId)return yield* Effect.fail(unavailable("receipt_expired"));
      for(let through=successfulPrefix+1;through<=receipt.sequence;through++){
        let entry=entries.get(through);if(entry===undefined)return yield* Effect.fail(unavailable("receipt_expired"));
        let hops=0;while(entry.state==="superseded"){if(++hops>160||entry.replacement===null)return yield* Effect.fail(unavailable("receipt_expired"));entry=entries.get(entry.replacement);if(entry===undefined)return yield* Effect.fail(unavailable("receipt_expired"));}
        if(entry.state==="unknown")return yield* Effect.fail(unavailable("outcome_unknown"));
        if(entry.state==="rejected")return yield* Effect.fail(entry.error??unavailable("stale"));
        if(entry.state!=="confirmed")yield* Deferred.await(entry.done);
      }
      if(captured!==epoch)return yield* Effect.fail(unavailable("stale"));const confirmed=yield* SubscriptionRef.get(state);if(confirmed===null)return yield* Effect.fail(unavailable("stale"));return confirmed;
    }).pipe(Effect.ensuring(Effect.sync(()=>{barriers.delete(registration);trim();})));
  }));
  const reserveDraft=(kind:string,allowCancel=false)=>lock.withPermit(Effect.gen(function*(){
    yield* open;if(session===null)return yield* Effect.fail(unavailable("session_required"));
    if(allowCancel){if(cancelFlight!==undefined)return yield* Effect.fail(unavailable("blocked"));}
    else{yield* guarded;if(worker!==undefined||queue.length>0)return yield* Effect.fail(unavailable("blocked"));}
    if(unresolved()>=64)return yield* Effect.fail(unavailable("queue_full"));
    const reservation:DraftFlight={token:Symbol(),epoch,session,kind,id:null};if(allowCancel)cancelFlight=reservation;else flight=reservation;return reservation;
  }));
  const runDraft=(reservation:DraftFlight,call:(operationId:string)=>Effect.Effect<ProductReply<Draft>,BackendError>)=>Effect.gen(function*(){
    const operationId=yield* nextId;reservation.id=operationId;
    if(reservation.epoch!==epoch||reservation.session!==session)return yield* Effect.fail(unavailable("stale"));
    const result=yield* Effect.result(mutation(call(operationId)).pipe(Effect.flatMap((reply)=>accept(reply.data,reservation.session))));
    if(result._tag==="Success")return result.success;
    if(result.failure._tag==="BackendRejected"||result.failure._tag==="ProductUnavailable"&&result.failure.reason==="stale")return yield* Effect.fail(result.failure);
    const current=yield* SubscriptionRef.get(state);
    unknownDraft.set(operationId,{session:reservation.session,epoch,draftId:current?.summary.draftId??null,kind:reservation.kind});
    const recovered=yield* Effect.result(pollDraft(operationId,reservation.session));
    if(recovered._tag==="Success"||recovered.failure._tag==="BackendRejected")unknownDraft.delete(operationId);
    return recovered._tag==="Success"?recovered.success:yield* Effect.fail(recovered.failure);
  }).pipe(Effect.ensuring(Effect.sync(()=>{if(flight===reservation)flight=undefined;if(cancelFlight===reservation)cancelFlight=undefined;})));
  const directEdit=(kind:DraftIntent["kind"])=>owned(Effect.gen(function*(){
    const reservation=yield* reserveDraft(kind,kind==="CancelLoad");
    return yield* Effect.gen(function*(){const current=yield* SubscriptionRef.get(state);if(current===null)return yield* Effect.fail(unavailable("session_required"));return yield* runDraft(reservation,(operationId)=>source.backend.editDraft({...blankIntent(kind),protocolVersion:1,backendSessionId:reservation.session,operationId,expectedRevision:current.summary.revision,draftId:current.summary.draftId,articleGeneration:current.summary.articleGeneration}));}).pipe(Effect.ensuring(Effect.sync(()=>{if(flight===reservation)flight=undefined;if(cancelFlight===reservation)cancelFlight=undefined;})));
  }));
  const fencedRead=<A>(call:()=>Effect.Effect<ProductReply<A>,BackendError>,validate:(data:A)=>boolean=()=>true)=>readOwned(Effect.gen(function*(){
    if(session===null)return yield* Effect.fail(unavailable("session_required"));const expected=session;
    const reply=yield* readProductQuery(call);if(session!==expected||reply.backendSessionId!==expected)return yield* Effect.fail(unavailable("stale"));if(!validate(reply.data))return yield* Effect.fail(new ProtocolError());return reply;
  }));
  yield* Scope.addFinalizer(owner,Effect.gen(function*(){
    blocked=true;sealed=true;epoch=Symbol();yield* retireEntries("closed");flight=undefined;cancelFlight=undefined;finalizationFlight=undefined;readFlight=undefined;recheckFlight=undefined;
    collectionFlights.clear();unknownDurable.clear();unknownDraft.clear();barriers.clear();retired.clear();collectionCache.clear();
    yield* SubscriptionRef.set(state,null);yield* PubSub.shutdown(state.pubsub);
  }));
  const coordinator:ProductCoordinator={
    queryFrozenParticipants:(request)=>fencedRead(()=>source.backend.queryFrozenParticipants(request),(page)=>page.collectionId===request.collectionId&&(request.expectedRevision===undefined||request.expectedRevision===null||page.revision===request.expectedRevision)),
    queryFrozenComments:(request)=>fencedRead(()=>source.backend.queryFrozenComments(request),(page)=>page.collectionId===request.collectionId&&page.participantId===request.participantId&&(request.expectedRevision===undefined||request.expectedRevision===null||page.revision===request.expectedRevision)),
    draft:SubscriptionRef.get(state),changes:SubscriptionRef.changes(state),setWritesBlocked:(next)=>{if(owner.state._tag!=="Closed")blocked=next;},
    acceptBootstrap:(reply)=>owned(Effect.gen(function*(){
      if(reply.data.recentResults===null)return yield* Effect.fail(new ProtocolError());
      if(retired.has(reply.backendSessionId))return yield* Effect.fail(unavailable("stale"));
      const changedSession=session!==null&&session!==reply.backendSessionId;
      const changedDraft=activeDraftId!==(reply.data.activeDraft?.draftId??null);
      if(changedSession){collectionCache.clear();retired.add(session as string);if(retired.size>64){const oldest=retired.values().next().value;if(oldest!==undefined)retired.delete(oldest);}}
      if(changedSession||changedDraft){epoch=Symbol();yield* retireEntries("stale");sealed=false;flight=undefined;cancelFlight=undefined;unknownDraft.clear();readFlight=undefined;yield* SubscriptionRef.set(state,null);}
      session=reply.backendSessionId;activeDraftId=reply.data.activeDraft?.draftId??null;
      for(const reference of reply.data.recentResults)collectionCache.seed(reference);
      const current=yield* SubscriptionRef.get(state);const summary=reply.data.activeDraft;
      if(summary!==null&&(current===null||summary.revision>current.summary.revision||summary.articleGeneration>current.summary.articleGeneration))yield* refreshDraft;
    })),
    ensureDraft:owned(Effect.gen(function*(){
      yield* open;if(session===null)return yield* Effect.fail(unavailable("session_required"));let current=yield* SubscriptionRef.get(state);
      if(sealed&&current?.summary.state!=="finalized")current=yield* refreshDraft;
      if(current!==null&&current.summary.state!=="finalized")return current;
      if(current?.summary.state==="finalized"){
        yield* lock.withPermit(Effect.gen(function*(){yield* open;if(blocked||flight!==undefined||unknownDraft.size>0)return yield* Effect.fail(unavailable("blocked"));epoch=Symbol();yield* retireEntries("stale");sealed=false;activeDraftId=null;yield* SubscriptionRef.set(state,null);}));
      }
      else if(activeDraftId!==null)return yield* refreshDraft;
      const reservation=yield* reserveDraft("CreateDraft");return yield* runDraft(reservation,(operationId)=>source.backend.createDraft({protocolVersion:1,backendSessionId:reservation.session,operationId,expectedRevision:0}));
    })),
    refreshDraft,
    loadArticle:(url)=>owned(Effect.gen(function*(){
      const reservation=yield* reserveDraft("LoadArticle");return yield* Effect.gen(function*(){const current=yield* SubscriptionRef.get(state);if(current===null)return yield* Effect.fail(unavailable("session_required"));return yield* runDraft(reservation,(operationId)=>source.backend.loadArticle({protocolVersion:1,backendSessionId:reservation.session,operationId,expectedRevision:current.summary.revision,draftId:current.summary.draftId,articleGeneration:current.summary.articleGeneration,url}));}).pipe(Effect.ensuring(Effect.sync(()=>{if(flight===reservation)flight=undefined;})));
    })),
    cancelLoad:directEdit("CancelLoad"),resetArticle:directEdit("ResetArticle"),edit,commitThrough,
    createCollection:(afterSequence,mode)=>owned(Effect.gen(function*(){
      const admitted=yield* lock.withPermit(Effect.gen(function*(){
        yield* guarded;const current=yield* SubscriptionRef.get(state);
        if(current===null||current.summary.state!=="ready"||[...entries.values()].some((entry)=>entry.state==="dispatched"||entry.state==="unknown")||queue.length>0||sequence!==afterSequence||successfulPrefix!==sequence)return yield* Effect.fail(unavailable("changed_before_finalize"));
        if(unresolved()>=64)return yield* Effect.fail(unavailable("queue_full"));sealed=true;const reservation:OperationFlight={token:Symbol(),id:null};finalizationFlight=reservation;return {current,epoch,reservation};
      }));
      return yield* Effect.gen(function*(){
      const operation=yield* Effect.result(nextId);
      if(operation._tag==="Failure"){if(epoch===admitted.epoch)sealed=false;return yield* Effect.fail(operation.failure);}
      const operationId=operation.success;admitted.reservation.id=operationId;
      if(epoch!==admitted.epoch||session!==admitted.current.summary.backendSessionId){return yield* Effect.fail(unavailable("stale"));}
      const result=yield* Effect.result(mutation(source.backend.createCollection({protocolVersion:1,backendSessionId:admitted.current.summary.backendSessionId,operationId,expectedRevision:admitted.current.summary.revision,draftId:admitted.current.summary.draftId,articleGeneration:admitted.current.summary.articleGeneration,afterSequence,mode})));
      if(result._tag==="Success"){if(epoch!==admitted.epoch)return yield* Effect.fail(unavailable("stale"));yield* Effect.result(refreshDraft);return yield* confirmCollection(result.success.data);}
      if(result.failure._tag==="BackendRejected"){if(epoch===admitted.epoch){const refreshed=yield* Effect.result(refreshDraft);if(refreshed._tag==="Success"&&refreshed.success.summary.state!=="finalized")sealed=false;}return yield* Effect.fail(result.failure);}
      unknownDurable.set(operationId,null);return yield* pollDurable(operationId,null);
      }).pipe(Effect.ensuring(Effect.sync(()=>{if(finalizationFlight===admitted.reservation)finalizationFlight=undefined;})));
    })),
    queryParticipants:(request)=>fencedRead(()=>source.backend.queryParticipants(request),(page)=>page.context.backendSessionId===request.backendSessionId&&page.context.draftId===request.draftId&&page.context.revision===request.revision&&page.context.articleGeneration===request.articleGeneration).pipe(Effect.flatMap((reply)=>Effect.gen(function*(){const current=yield* SubscriptionRef.get(state);if(current===null||current.summary.draftId!==request.draftId||current.summary.revision!==request.revision||current.summary.articleGeneration!==request.articleGeneration)return yield* Effect.fail(unavailable("stale"));return reply;}))),
    queryParticipantComments:(request)=>fencedRead(()=>source.backend.queryParticipantComments(request),(page)=>page.participantId===request.participantId&&page.context.backendSessionId===request.backendSessionId&&page.context.draftId===request.draftId&&page.context.revision===request.revision&&page.context.articleGeneration===request.articleGeneration).pipe(Effect.flatMap((reply)=>Effect.gen(function*(){const current=yield* SubscriptionRef.get(state);if(current===null||current.summary.draftId!==request.draftId||current.summary.revision!==request.revision||current.summary.articleGeneration!==request.articleGeneration)return yield* Effect.fail(unavailable("stale"));return reply;}))),
    getCollection:(collectionId,options)=>Effect.suspend(()=>{collectionCache.get(collectionId);return fencedRead(()=>source.backend.getCollection(collectionId,options),(value)=>value.collectionId===collectionId).pipe(Effect.flatMap((reply)=>confirmCollection(reply.data).pipe(Effect.as(reply))));}),
    collectionCommand:(kind,input)=>owned(Effect.gen(function*(){
      const admission=yield* lock.withPermit(Effect.gen(function*(){
        yield* open;if(session===null||blocked||collectionFlights.has(input.collectionId)||[...unknownDurable.values()].includes(input.collectionId))return yield* Effect.fail(unavailable("blocked"));
        if(unresolved()>=64)return yield* Effect.fail(unavailable("queue_full"));const reservation:OperationFlight={token:Symbol(),id:null};collectionFlights.set(input.collectionId,reservation);return {reservation,session};
      }));
      return yield* Effect.gen(function*(){
        const operationId=yield* nextId;admission.reservation.id=operationId;if(session!==admission.session)return yield* Effect.fail(unavailable("stale"));
        const request:CollectionCommandRequest={...input,prizes:input.prizes.map((item)=>({...item})),protocolVersion:1,backendSessionId:admission.session,operationId};
        const result=yield* Effect.result(mutation(source.backend[kind](request)));
        if(result._tag==="Success"&&result.success.data.collectionId===input.collectionId){if(session!==admission.session)return yield* Effect.fail(unavailable("stale"));return yield* confirmCollection(result.success.data);}
        if(result._tag==="Failure"&&result.failure._tag==="BackendRejected")return yield* Effect.fail(result.failure);
        unknownDurable.set(operationId,input.collectionId);return yield* pollDurable(operationId,input.collectionId);
      }).pipe(Effect.ensuring(Effect.sync(()=>{if(collectionFlights.get(input.collectionId)===admission.reservation)collectionFlights.delete(input.collectionId);})));
    })),
    recheck:owned(Effect.gen(function*(){
      if(recheckFlight!==undefined)return yield* Deferred.await(recheckFlight);const pending=yield* Deferred.make<void,ProductError>();recheckFlight=pending;
      const outcome=yield* Effect.exit(Effect.gen(function*(){
        for(const entry of entries.values())if(entry.state==="unknown"){
          const result=yield* Effect.result(observeDraft(entry.receipt.intentId,entry.session));yield* finish(entry,result);if(result._tag==="Success"&&entry.epoch===epoch)laneBlocked=false;
        }
        for(const [operationId,record]of [...unknownDraft]){const result=yield* Effect.result(observeDraft(operationId,record.session));if(result._tag==="Success"||result.failure._tag==="BackendRejected")unknownDraft.delete(operationId);}
        for(const [operationId,target]of [...unknownDurable]){const result=yield* Effect.result(observeDurable(operationId,target));}
        yield* start;
      }));
      if(recheckFlight===pending)recheckFlight=undefined;
      if(Exit.isSuccess(outcome)){yield* Deferred.succeed(pending,undefined);return;}
      yield* Deferred.failCause(pending,outcome.cause);return yield* Effect.failCause(outcome.cause);
    })),
  };
  return coordinator;
 });
}
