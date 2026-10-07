import { Effect, PubSub, Stream } from "effect";
import { ProtocolError } from "../../src/contracts/backend";
import type { Collection, Draft, ParticipantQuery, ProductReply } from "../../src/contracts/product";
import type { DraftIntent, ProductCommands, ProductError } from "../../src/operations/productCoordinator";
import { ProductUnavailable } from "../../src/operations/productCoordinator";
import { round } from "./round";
import type { EditReceipt } from "../../src/operations/coordinatorState";

export function draft(change:Partial<Draft>={}):Draft{return {summary:{backendSessionId:"session",draftId:"draft",revision:1,articleGeneration:1,state:"ready",snapshot:null,collectionId:null},article:{url:"https://gall.dcinside.com/board/view/?id=test&no=1",title:"실제 계약 제목",galleryId:"test",galleryName:"갤러리",galleryKind:"major",number:"1",authorNickname:"작성자",authorIdentifier:"author",postedAt:null},filters:{excludeAnonymous:false,excludeAuthor:true,excludeDcconOnly:false,timeCut:null,includeKeywords:[],excludeKeywords:[]},prizes:{mode:"single",drawMode:"immediate",single:{id:"single",name:"",count:1},multiple:[{id:"many",name:"",count:1}]},participants:3,included:3,excluded:0,authorIdentifiable:true,load:null,...change};}
function reply<A>(data:A):ProductReply<A>{return {protocolVersion:1,backendSessionId:"session",occurredAt:"2026-10-06T00:00:00.000Z",operationId:null,revision:null,data};}
export function commandHarness(initial:Draft|null=draft()){
 return Effect.gen(function*(){
  const events=yield* PubSub.unbounded<Draft|null>({replay:1});let current=initial;let sequence=0;
  const state={edits:[] as DraftIntent[],creates:[] as Array<{afterSequence:number;mode:string}>,queries:[] as ParticipantQuery[],comments:0,loads:[] as string[],cancelled:0,ensure:0,failEdit:0,failCommit:0,unknown:false};
  const pending=new Map<number,DraftIntent>(); const emit=(value:Draft|null)=>{current=value;PubSub.publishUnsafe(events,value);};PubSub.publishUnsafe(events,initial);
  const unavailable=Effect.fail(new ProtocolError());
  const commands:ProductCommands={
   queryFrozenParticipants:()=>unavailable,queryFrozenComments:()=>unavailable,
   draft:Effect.sync(()=>current),changes:Stream.fromPubSub(events),
   ensureDraft:Effect.sync(()=>{state.ensure++;if(current===null)emit(draft({article:null,participants:0,included:0,summary:{...draft().summary,state:"empty"}}));return current as Draft;}),
   refreshDraft:Effect.suspend(()=>current===null?unavailable:Effect.succeed(current)),
   loadArticle:(url)=>Effect.sync(()=>{state.loads.push(url);const updated=draft({article:{...draft().article as NonNullable<Draft["article"]>,url},load:{operationId:"load",sequence:1,pages:1,comments:3,state:"completed",failureCode:null}});emit(updated);return updated;}),
   cancelLoad:Effect.sync(()=>{state.cancelled++;return current as Draft;}),
   resetArticle:Effect.sync(()=>{const updated=draft({article:null,participants:0,included:0});emit(updated);return updated;}),
   edit:(intent)=>Effect.suspend(()=>{state.edits.push(intent);if(state.failEdit===state.edits.length)return unavailable;const receipt:EditReceipt={intentId:"intent-"+(++sequence),draftId:"draft",articleGeneration:current?.summary.articleGeneration??1,sequence};pending.set(sequence,intent);return Effect.succeed(receipt);}),
   commitThrough:(receipt)=>Effect.suspend(()=>{if(state.failCommit===receipt.sequence)return unavailable;const intent=pending.get(receipt.sequence);if(intent===undefined||current===null)return unavailable;let next=current;
    if(intent.filters!==null)next={...next,filters:intent.filters};
    if(intent.prizes!==null)next={...next,prizes:intent.prizes};
    if(intent.kind==="ResetFilters")next={...next,filters:{...draft().filters,excludeAuthor:false}};
    if(intent.kind==="ResetArticle")next={...next,article:null,participants:0,included:0,excluded:0};
    next={...next,summary:{...next.summary,revision:next.summary.revision+1}};emit(next);return Effect.succeed(next);}),
   createCollection:(afterSequence,mode)=>Effect.suspend<Collection,ProductError,never>(()=>{state.creates.push({afterSequence,mode});if(state.unknown)return Effect.fail(new ProductUnavailable({reason:"outcome_unknown"}));if(current===null||current.article===null)return unavailable;const collection:Collection={collectionId:"collection",revision:1,article:current.article,snapshot:{snapshotId:"snapshot",collectedAt:"2026-10-06T00:00:00Z",complete:true,pages:1,acceptedComments:3,deletedComments:0,unsupportedComments:0},filters:current.filters,participantCount:current.participants,selectedCount:1,remainingCount:current.participants-1,rounds:[round()],roundTotal:1,roundOffset:0,latestRound:round()};return Effect.succeed(collection);}),
   queryParticipants:(request)=>Effect.sync(()=>{state.queries.push(request);return reply({context:{backendSessionId:request.backendSessionId,draftId:request.draftId,revision:request.revision,articleGeneration:request.articleGeneration},total:201,matched:201,offset:request.offset,rows:Array.from({length:Math.min(100,Math.max(0,201-request.offset))},(_,index)=>({id:"participant-"+(request.offset+index),nickname:"참가자 "+(request.offset+index),publicIdentifier:"user",kind:"fixed" as const,classification:request.group as "unclassified",reason:"" as const,included:true,commentCount:51,previews:["<script>문자열</script>"]}))});}),
   queryParticipantComments:(request)=>Effect.sync(()=>{state.comments++;return reply({context:{backendSessionId:request.backendSessionId,draftId:request.draftId,revision:request.revision,articleGeneration:request.articleGeneration},participantId:request.participantId,total:51,offset:request.offset,rows:Array.from({length:Math.min(50,51-request.offset)},(_,index)=>({id:"comment-"+(request.offset+index),parentId:null,kind:"text" as const,text:"댓글 "+(request.offset+index),postedAt:null,mediaUrls:[]}))});}),
   getCollection:()=>unavailable,collectionCommand:()=>unavailable,
  };
  return {commands,state,emit,close:PubSub.shutdown(events)};
 });
}