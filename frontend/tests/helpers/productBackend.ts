import { Deferred, Effect } from "effect";
import type { BootstrapReply, BackendError, OperationLookupReply } from "../../src/contracts/backend";
import type { Collection, Draft, ProductBackend, ProductReply } from "../../src/contracts/product";
import { ClientIdError } from "../../src/platform/ids";
import { draft } from "./productCommands";
import { round } from "./round";

export function bootstrap(active:Draft|null=draft(),session="session"):BootstrapReply{return {protocolVersion:1,backendSessionId:session,occurredAt:"2026-10-06T00:00:00Z",data:{backendNow:"2026-10-06T00:00:00Z",theme:"system",activeDraft:active?.summary??null,pendingOperations:[],pendingCursor:null,recentResults:[]}};}
export function reply<A>(data:A,session="session"):ProductReply<A>{return {protocolVersion:1,backendSessionId:session,occurredAt:"2026-10-06T00:00:00Z",operationId:null,revision:null,data};}
export function collection():Collection{return {collectionId:"collection",revision:1,article:draft().article as NonNullable<Draft["article"]>,snapshot:{snapshotId:"snapshot",collectedAt:"2026-10-06T00:00:00Z",complete:true,pages:1,acceptedComments:3,deletedComments:0,unsupportedComments:0},filters:draft().filters,participantCount:3,selectedCount:1,remainingCount:2,rounds:[round()],roundTotal:1,roundOffset:0,latestRound:round()};}
export function backendHarness(initial:Draft=draft()){
 let current=initial;let next=0;
 const calls=new Map<string,unknown[]>();const overrides=new Map<string,Array<Effect.Effect<unknown,BackendError>>>();
 const signals=new Map<string,Deferred.Deferred<void>>();
 const signal=(method:string,count:number)=>{const key=method+":"+count;let value=signals.get(key);if(value===undefined){value=Deferred.makeUnsafe<void>();signals.set(key,value);}return value;};
 const invoke=<A>(method:string,request:unknown,normal:()=>ProductReply<A>):Effect.Effect<ProductReply<A>,BackendError>=>Effect.suspend(()=>{
   const requests=calls.get(method)??[];requests.push(request);calls.set(method,requests);Deferred.doneUnsafe(signal(method,requests.length),Effect.void);
   const override=overrides.get(method)?.shift();return override===undefined?Effect.sync(normal):override as Effect.Effect<ProductReply<A>,BackendError>;
 });
 const backend:ProductBackend={
  queryFrozenParticipants:(request)=>invoke("queryFrozenParticipants",request,()=>reply({collectionId:request.collectionId,revision:request.expectedRevision??1,total:0,matched:0,offset:request.offset,rows:[]},current.summary.backendSessionId)),
  queryFrozenComments:(request)=>invoke("queryFrozenComments",request,()=>reply({collectionId:request.collectionId,revision:request.expectedRevision??1,participantId:request.participantId,total:0,offset:request.offset,rows:[]},current.summary.backendSessionId)),
  getDraftOperation:(id)=>invoke("getDraftOperation",id,()=>reply({operationId:id,state:"unknown",summary:null,failureCode:null},current.summary.backendSessionId)),
  createDraft:(request)=>invoke("createDraft",request,()=>{current={...draft(),summary:{...draft().summary,backendSessionId:request.backendSessionId}};return reply(current,request.backendSessionId);}),
  getDraft:(request)=>invoke("getDraft",request,()=>reply(current,request.backendSessionId)),
  loadArticle:(request)=>invoke("loadArticle",request,()=>{current={...current,summary:{...current.summary,revision:current.summary.revision+1,state:"loading"},load:{operationId:request.operationId,sequence:1,pages:0,comments:0,state:"loading",failureCode:null}};return reply(current,request.backendSessionId);}),
  editDraft:(request)=>invoke("editDraft",request,()=>{current={...current,summary:{...current.summary,revision:current.summary.revision+1},filters:request.filters??current.filters,prizes:request.prizes??current.prizes};if(request.kind==="CancelLoad")current={...current,summary:{...current.summary,state:"ready"},load:current.load===null?null:{...current.load,state:"cancelled"}};if(request.kind==="ResetArticle")current={...current,summary:{...current.summary,articleGeneration:current.summary.articleGeneration+1,state:"empty"},article:null};return reply(current,request.backendSessionId);}),
  queryParticipants:(request)=>invoke("queryParticipants",request,()=>reply({context:{backendSessionId:request.backendSessionId,draftId:request.draftId,revision:request.revision,articleGeneration:request.articleGeneration},total:0,matched:0,offset:request.offset,rows:[]},request.backendSessionId)),
  queryParticipantComments:(request)=>invoke("queryParticipantComments",request,()=>reply({context:{backendSessionId:request.backendSessionId,draftId:request.draftId,revision:request.revision,articleGeneration:request.articleGeneration},participantId:request.participantId,total:0,offset:request.offset,rows:[]},request.backendSessionId)),
  createCollection:(request)=>invoke("createCollection",request,()=>reply(collection(),request.backendSessionId)),
  getCollection:(id)=>invoke("getCollection",id,()=>reply({...collection(),collectionId:id},current.summary.backendSessionId)),
  rerun:(request)=>invoke("rerun",request,()=>reply(collection(),request.backendSessionId)),
  setSchedule:(request)=>invoke("setSchedule",request,()=>reply(collection(),request.backendSessionId)),
  cancelSchedule:(request)=>invoke("cancelSchedule",request,()=>reply(collection(),request.backendSessionId)),
  retryRound:(request)=>invoke("retryRound",request,()=>reply(collection(),request.backendSessionId)),
 };
 const lookup=(id:string):Effect.Effect<OperationLookupReply,BackendError>=>invoke("lookup",id,()=>reply({operationId:id,state:"unknown" as const,kind:null,collectionId:null,roundId:null,revision:null,failureCode:null},current.summary.backendSessionId));
 return {backend,lookup,calls,current:()=>current,set:(value:Draft)=>{current=value;},newId:Effect.sync(()=> "operation-"+(++next)),wait:(method:string,count=1)=>Deferred.await(signal(method,count)),count:(method:string)=>calls.get(method)?.length??0,override:(method:string,...values:Array<Effect.Effect<unknown,BackendError>>)=>{overrides.set(method,[...(overrides.get(method)??[]),...values]);}};
}