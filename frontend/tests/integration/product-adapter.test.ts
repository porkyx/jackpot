import { expect, it } from "@effect/vitest";
import { Deferred, Effect, Fiber } from "effect";
import { makeWailsProductBackend, type WailsProductPort } from "../../src/platform/product";
import type { ProductBackend } from "../../src/contracts/product";
import { draft } from "../helpers/productCommands";
import { collection } from "../helpers/productBackend";

const mutation={protocolVersion:1 as const,backendSessionId:"session",operationId:"operation",expectedRevision:1};
const context={backendSessionId:"session",draftId:"draft",revision:1,articleGeneration:1};
const query={...context,query:"",group:"",offset:0,limit:2};
const participant={id:"participant",nickname:"가",publicIdentifier:"user",kind:"fixed",classification:"included",reason:"",included:true,commentCount:1,previews:["<script>text</script>"]};
const participants=()=>({context:{...context},total:1,matched:1,offset:0,rows:[{...participant}]});
const comments=()=>({context:{...context},participantId:"participant",total:1,offset:0,rows:[{id:"comment",parentId:null,kind:"text",text:"<script>text</script>",postedAt:null,mediaUrls:[]}]});
const envelope=(data:unknown,extra:Record<string,unknown>={})=>({protocolVersion:1,backendSessionId:"session",occurredAt:"2026-10-06T00:00:00Z",ok:true,operationId:"operation",revision:1,data,...extra});
function harness(raw:unknown){
 let response=raw;const calls:Array<{method:string,args:unknown[],signal:AbortSignal}>=[];
 const port=new Proxy({} as WailsProductPort,{get:(_target,method:string)=>(...args:unknown[])=>{const signal=args.at(-1) as AbortSignal;calls.push({method,args:args.slice(0,-1),signal});return Promise.resolve(response);}});
 return {backend:makeWailsProductBackend(port),calls,set:(value:unknown)=>{response=value;}};
}
const command={...mutation,collectionId:"collection",roundId:"round",expectedVersion:1,prizes:[{id:"single",name:"상품",count:1}],message:"",mode:"immediate" as const,scheduledAt:null};
const successes:ReadonlyArray<{name:string,data:()=>unknown,call:(backend:ProductBackend)=>Effect.Effect<unknown,unknown>}>= [
 {name:"createDraft",data:draft,call:b=>b.createDraft(mutation)},
 {name:"getDraft",data:draft,call:b=>b.getDraft({backendSessionId:"session",draftId:"draft"})},
 {name:"loadArticle",data:draft,call:b=>b.loadArticle({...mutation,draftId:"draft",articleGeneration:1,url:"https://gall.dcinside.com/board/view/?id=a&no=1"})},
 {name:"editDraft",data:draft,call:b=>b.editDraft({...mutation,draftId:"draft",articleGeneration:1,kind:"ResetManual",filters:null,prizes:null,participantId:"",included:null})},
 {name:"queryParticipants",data:participants,call:b=>b.queryParticipants(query)},
 {name:"queryParticipantComments",data:comments,call:b=>b.queryParticipantComments({...context,participantId:"participant",offset:0,limit:2})},
 {name:"createCollection",data:collection,call:b=>b.createCollection({...mutation,draftId:"draft",articleGeneration:1,afterSequence:0,mode:"immediate"})},
 {name:"getCollection",data:collection,call:b=>b.getCollection("collection")},
 ...(["rerun","setSchedule","cancelSchedule","retryRound"] as const).map(name=>({name,data:collection,call:(b:ProductBackend)=>b[name](command)})),
 {name:"getDraftOperation",data:()=>({operationId:"operation",state:"unknown",summary:null,failureCode:null}),call:b=>b.getDraftOperation("operation")},
 {name:"queryFrozenParticipants",data:()=>({collectionId:"collection",revision:1,total:1,matched:1,offset:0,rows:[participant]}),call:b=>b.queryFrozenParticipants({collectionId:"collection",expectedRevision:1,query:"",group:"",offset:0,limit:2})},
 {name:"queryFrozenComments",data:()=>({collectionId:"collection",revision:1,participantId:"participant",total:1,offset:0,rows:comments().rows}),call:b=>b.queryFrozenComments({collectionId:"collection",expectedRevision:1,participantId:"participant",offset:0,limit:2})},
];
for(const entry of successes)it.effect(`product adapter decodes ${entry.name} without retry or altering transport arguments`,()=>Effect.gen(function*(){
 const state=harness(envelope(entry.data()));const result=yield*Effect.result(entry.call(state.backend));expect(result._tag).toBe("Success");expect(state.calls).toHaveLength(1);expect(state.calls[0]?.method).toBe(entry.name);expect(state.calls[0]?.signal.aborted).toBe(false);
}));
for(const [name,change]of [["foreign session",{backendSessionId:"other"}],["foreign operation",{operationId:"other"}],["unknown protocol",{protocolVersion:2}],["local time",{occurredAt:"2026-10-06T09:00:00+09:00"}],["null data",{data:null}],["missing data",{data:undefined}]] as const)it.effect(`product mutation rejects ${name} as protocol error`,()=>Effect.gen(function*(){const state=harness(envelope(draft(),change));const result=yield*Effect.result(state.backend.createDraft(mutation));expect(result._tag).toBe("Failure");if(result._tag==="Failure")expect(result.failure._tag).toBe("ProtocolError");expect(state.calls).toHaveLength(1);}));
it.effect("product backend rejection exposes only fixed safe code and does not retry",()=>Effect.gen(function*(){const {data:_discarded,...header}=envelope(null);const state=harness({...header,ok:false,code:"StaleRevision",messageKey:"StaleRevision"});const result=yield*Effect.result(state.backend.createDraft(mutation));expect(result._tag).toBe("Failure");if(result._tag==="Failure")expect(result.failure._tag).toBe("BackendRejected");expect(state.calls).toHaveLength(1);}));
for(const key of ["backendSessionId","draftId","revision","articleGeneration"] as const)it.effect(`participant query rejects reversed ${key} response`,()=>Effect.gen(function*(){const page=participants();const altered={...page,context:{...page.context,[key]:typeof page.context[key]==="number"?2:"other"}};const state=harness(envelope(altered));expect((yield*Effect.result(state.backend.queryParticipants(query)))._tag).toBe("Failure");}));
for(const [name,change]of [["offset",{offset:1}],["matched exceeds total",{matched:2}],["duplicate identities",{total:2,matched:2,rows:[participant,participant]}],["rows exceed requested limit",{total:3,matched:3,rows:[participant,{...participant,id:"p2"},{...participant,id:"p3"}]}],["past-end rows",{matched:0}]] as const)it.effect(`participant page rejects ${name}`,()=>Effect.gen(function*(){const state=harness(envelope({...participants(),...change}));expect((yield*Effect.result(state.backend.queryParticipants(query)))._tag).toBe("Failure");}));
for(const [name,change]of [["nil session",{backendSessionId:""}],["nil draft",{draftId:""}],["negative revision",{revision:-1}],["unsafe generation",{articleGeneration:Number.MAX_SAFE_INTEGER+1}],["negative offset",{offset:-1}],["zero limit",{limit:0}],["large limit",{limit:101}],["fractional limit",{limit:1.5}],["unknown group",{group:"all"}],["UTF8 search limit",{query:"가".repeat(334)}]] as const)it.effect(`participant query rejects ${name} before calling Go`,()=>Effect.gen(function*(){const state=harness(envelope(participants()));expect((yield*Effect.result(state.backend.queryParticipants({...query,...change})))._tag).toBe("Failure");expect(state.calls).toHaveLength(0);}));
it.effect("comment query rejects wrong participant or draft response",()=>Effect.gen(function*(){for(const altered of [{...comments(),participantId:"other"},{...comments(),context:{...context,revision:2}}]){const state=harness(envelope(altered));expect((yield*Effect.result(state.backend.queryParticipantComments({...context,participantId:"participant",offset:0,limit:2})))._tag).toBe("Failure");}}));
it.effect("interrupted read aborts one native transport and never retries",()=>Effect.gen(function*(){const started=yield*Deferred.make<AbortSignal>();let calls=0;let aborts=0;const port=new Proxy({} as WailsProductPort,{get:()=> (_request:unknown,signal:AbortSignal)=>{calls++;Deferred.doneUnsafe(started,Effect.succeed(signal));return new Promise((_ok,fail)=>signal.addEventListener("abort",()=>{aborts++;fail(new Error("private native cancellation"));},{once:true}));}});const fiber=yield*makeWailsProductBackend(port).getDraft({backendSessionId:"session",draftId:"draft"}).pipe(Effect.forkScoped);const signal=yield*Deferred.await(started);yield*Fiber.interrupt(fiber);expect(signal.aborted).toBe(true);expect(calls).toBe(1);expect(aborts).toBe(1);}));
for(const kind of ["queryParticipants","queryParticipantComments","queryFrozenParticipants","queryFrozenComments"] as const)it.effect(`${kind} rejects uint32 overflow before calling Go`,()=>Effect.gen(function*(){const state=harness(envelope(participants()));const offset=4294967296;const call:Effect.Effect<unknown,unknown>=kind==="queryParticipants"?state.backend.queryParticipants({...query,offset}):kind==="queryParticipantComments"?state.backend.queryParticipantComments({...context,participantId:"participant",offset,limit:2}):kind==="queryFrozenParticipants"?state.backend.queryFrozenParticipants({collectionId:"collection",query:"",group:"",offset,limit:2}):state.backend.queryFrozenComments({collectionId:"collection",participantId:"participant",offset,limit:2});expect((yield*Effect.result(call.pipe(Effect.asVoid)))._tag).toBe("Failure");expect(state.calls).toHaveLength(0);}));
it("product adapter exposes result and schedule capabilities without collection listing",()=>{
 const state=harness(envelope(collection()));
 expect("listCollections" in state.backend).toBe(false);
 expect([state.backend.getCollection,state.backend.rerun,state.backend.setSchedule,state.backend.cancelSchedule,state.backend.retryRound].every(call=>typeof call==="function")).toBe(true);
 expect(state.calls).toHaveLength(0);
});
