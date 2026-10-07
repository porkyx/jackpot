import { expect, it } from "@effect/vitest";
import { Deferred, Effect, Fiber } from "effect";
import { ProtocolError, BackendRejected } from "../../src/contracts/backend";
import type { FrozenCommentsPage, FrozenParticipantsPage, FrozenParticipantsQuery, FrozenCommentsQuery, Participant, ProductReply } from "../../src/contracts/product";
import { DomPlatform, makeDomPlatform } from "../../src/platform/dom";
import { mountFrozenParticipants, type FrozenReadCommands } from "../../src/features/results/frozen/view";
import type { ProductDispatch } from "../../src/features/export/view";
const person=(id:number):Participant=>({id:"p"+id,nickname:id===0?"<img src=x onerror=bad()>":"참가자"+id,publicIdentifier:"uid"+id,kind:"fixed",classification:id%3===0?"included":id%3===1?"excluded":"unclassified",reason:"",included:id%2===0,commentCount:55,previews:["<script>안전한 문자</script>"]});
const all=Array.from({length:110},(_,id)=>person(id));
const now="2026-10-06T01:00:00.000Z";
const reply=<A>(data:A):ProductReply<A>=>({protocolVersion:1,backendSessionId:"session",occurredAt:now,operationId:null,revision:null,data});
type P=typeof FrozenParticipantsPage.Type; type C=typeof FrozenCommentsPage.Type;
function harness(){
 const state={listeners:0,participants:[]as FrozenParticipantsQuery[],comments:[]as FrozenCommentsQuery[],fail:false,commentFail:false,revision:7,session:"session",pageChange:(page:P):P=>page,commentChange:(page:C):C=>page};
 const effects:Array<Effect.Effect<void,never,DomPlatform>>=[];
 const native=makeDomPlatform({request:()=>0,cancel:()=>{}},()=>"visible");
 const dom:typeof DomPlatform.Service={...native,listen:(...args)=>native.listen(...args).pipe(Effect.andThen(Effect.acquireRelease(Effect.sync(()=>{state.listeners++;}),()=>Effect.sync(()=>{state.listeners--;}))))};
 const reads:{-readonly [K in keyof FrozenReadCommands]:FrozenReadCommands[K]}={
  queryFrozenParticipants:(request)=>Effect.suspend(()=>{
   state.participants.push(request);if(state.fail)return Effect.fail(new BackendRejected({code:"StorageUnavailable",messageKey:"never-print-secret"}));
   const found=all.filter(p=>(request.query===""||(p.nickname+" "+p.publicIdentifier).includes(request.query))&&(request.group===""||p.classification===request.group));
   return Effect.succeed({...reply(state.pageChange({collectionId:"collection",revision:state.revision,total:all.length,matched:found.length,offset:request.offset,rows:found.slice(request.offset,request.offset+request.limit)})),backendSessionId:state.session});
  }),
  queryFrozenComments:(request)=>Effect.suspend(()=>{
   state.comments.push(request);if(state.commentFail)return Effect.fail(new ProtocolError());
   const comments=Array.from({length:55},(_,id)=>({id:"c"+id,parentId:id===0?null:"parent",kind:id%3===0?"dccon"as const:id%3===1?"voice"as const:"text"as const,text:id===0?"":"<script>댓글"+id+"</script>",postedAt:id===0?null:now,mediaUrls:[]}));
   return Effect.succeed({...reply(state.commentChange({collectionId:"collection",revision:state.revision,participantId:request.participantId,total:55,offset:request.offset,rows:comments.slice(request.offset,request.offset+request.limit)})),backendSessionId:state.session});
  }),
 };
 const dispatch:ProductDispatch=effect=>effects.push(effect);
 const next=()=>Effect.gen(function*(){const effect=effects.shift();if(effect===undefined)throw new Error("missing dispatch");yield* effect.pipe(Effect.provideService(DomPlatform,dom));});
 return{state,reads,dom,dispatch,next,effects};
}
const host=()=>Effect.acquireRelease(Effect.sync(()=>{const node=document.createElement("main");document.body.append(node);return node;}),node=>Effect.sync(()=>node.remove()));
const button=(root:HTMLElement,text:string)=>{const b=[...root.querySelectorAll("button")].find(x=>x.textContent===text);if(b===undefined)throw new Error("missing "+text);return b;};
const settle=Effect.yieldNow.pipe(Effect.andThen(Effect.yieldNow));
const mount=(root:HTMLElement,h:ReturnType<typeof harness>)=>mountFrozenParticipants(root,h.reads,"collection",h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom),Effect.tap(()=>settle));
const submit=(root:HTMLElement)=>root.querySelector("form")!.dispatchEvent(new Event("submit",{bubbles:true,cancelable:true}));

it.effect("frozen snapshot is readonly plain text and requests exactly100 participants without password",()=>Effect.gen(function*(){
 const h=harness();const root=yield*host();const view=yield*mount(root,h);
 expect(h.state.participants).toEqual([{collectionId:"collection",expectedRevision:null,query:"",group:"",offset:0,limit:100}]);
 expect(root.querySelectorAll("ol[aria-label='확정 참가자']>li")).toHaveLength(100);
 expect(root.querySelector("script,img,input[type=checkbox],input[type=password]")).toBeNull();
 expect(root.textContent).toContain("<script>안전한 문자</script>");expect(root.textContent).toContain("자동 포함");expect(root.textContent).toContain("자동 제외");expect(root.textContent).toContain("미분류");expect(root.textContent).toContain("선택 안 됨");expect(root.querySelector("button[data-participant-id=p0]")?.parentElement?.textContent).toContain("확정 선택");expect(root.querySelector("button[data-participant-id=p1]")?.parentElement?.textContent).toContain("선택 안 됨");
 yield*view.close;expect(h.state.listeners).toBe(0);expect(root.children).toHaveLength(0);
}));
it.effect("participant pagination pins revision and preserves committed query while editing input",()=>Effect.gen(function*(){
 const h=harness();const root=yield*host();const view=yield*mount(root,h);
 root.querySelector<HTMLInputElement>("input")!.value="not-yet-applied";
 button(root,"다음 참가자 페이지").click();yield*h.next();
 expect(h.state.participants[1]).toMatchObject({offset:100,query:"",expectedRevision:7,limit:100});
 expect(root.querySelectorAll("ol[aria-label='확정 참가자']>li")).toHaveLength(10);expect(button(root,"다음 참가자 페이지").disabled).toBe(true);
 button(root,"이전 참가자 페이지").click();yield*h.next();expect(h.state.participants[2]?.offset).toBe(0);
 yield*view.close;
}));
it.effect("global nickname identifier search and all three groups precede paging and zero results disable pagers",()=>Effect.gen(function*(){
 const h=harness();const root=yield*host();const view=yield*mount(root,h);
 for(const group of ["included","excluded","unclassified",""]){
  root.querySelector<HTMLInputElement>("input")!.value="uid1";root.querySelector<HTMLSelectElement>("select")!.value=group;submit(root);yield*h.next();
  expect(h.state.participants.at(-1)).toMatchObject({offset:0,query:"uid1",group,expectedRevision:null});expect(root.textContent).toContain("검색");
 }
 root.querySelector<HTMLInputElement>("input")!.value="absent";submit(root);yield*h.next();expect(root.textContent).toContain("조건에 맞는 참가자가 없습니다");expect(button(root,"다음 참가자 페이지").disabled).toBe(true);expect(button(root,"이전 참가자 페이지").disabled).toBe(true);yield*view.close;
}));
it.effect("comments use50 rows pinned revision participantID safe text types dates and reply metadata",()=>Effect.gen(function*(){
 const h=harness();const root=yield*host();const view=yield*mount(root,h);
 root.querySelector<HTMLButtonElement>("button[data-participant-id=p0]")!.click();yield*h.next();
 expect(h.state.comments[0]).toEqual({collectionId:"collection",expectedRevision:7,participantId:"p0",offset:0,limit:50});
 expect(root.querySelectorAll("ol[aria-label='저장된 댓글']>li")).toHaveLength(50);expect(root.textContent).toContain("[디시콘]");expect(root.textContent).toContain("보이스 댓글");expect(root.textContent).toContain("텍스트 댓글");expect(root.textContent).toContain("시각 확인 불가");expect(root.textContent).toContain("답글(parent)");expect(root.querySelector("script")).toBeNull();
 button(root,"다음 댓글 페이지").click();yield*h.next();expect(h.state.comments[1]?.offset).toBe(50);expect(root.querySelectorAll("ol[aria-label='저장된 댓글']>li")).toHaveLength(5);expect(button(root,"다음 댓글 페이지").disabled).toBe(true);
 button(root,"이전 댓글 페이지").click();yield*h.next();expect(h.state.comments[2]?.offset).toBe(0);
 h.state.revision=8;button(root,"명단 다시 조회").click();yield*h.next();expect(h.state.participants.at(-1)?.expectedRevision).toBeNull();expect(root.querySelector<HTMLElement>("section[aria-label='확정 참가자의 댓글']")!.hidden).toBe(true);
 root.querySelector<HTMLButtonElement>("button[data-participant-id=p1]")!.click();yield*h.next();expect(h.state.comments.at(-1)?.expectedRevision).toBe(8);yield*view.close;
}));
it.effect("first and repeated participant read failures show safe error and a later retry works",()=>Effect.gen(function*(){
 const h=harness();h.state.fail=true;const root=yield*host();const view=yield*mount(root,h);expect(root.textContent).not.toContain("never-print-secret");expect(root.querySelector<HTMLElement>("[role=alert]")!.hidden).toBe(false);
 button(root,"명단 다시 조회").click();yield*h.next();expect(h.state.participants).toHaveLength(2);h.state.fail=false;button(root,"명단 다시 조회").click();yield*h.next();expect(root.querySelectorAll("button[data-participant-id]")).toHaveLength(100);
 h.state.fail=true;button(root,"다음 참가자 페이지").click();yield*h.next();expect(root.querySelectorAll("button[data-participant-id]")).toHaveLength(100);expect(h.state.participants.at(-1)?.offset).toBe(100);yield*view.close;
}));
it.effect("Nth comment failure preserves confirmed comments and retry does not mutate frozen selection",()=>Effect.gen(function*(){
 const h=harness();const root=yield*host();const view=yield*mount(root,h);root.querySelector<HTMLButtonElement>("button[data-participant-id=p0]")!.click();yield*h.next();const before=root.querySelector("ol[aria-label='저장된 댓글']")!.textContent;
 h.state.commentFail=true;button(root,"다음 댓글 페이지").click();yield*h.next();expect(root.querySelector("ol[aria-label='저장된 댓글']")!.textContent).toBe(before);h.state.commentFail=false;button(root,"다음 댓글 페이지").click();yield*h.next();expect(h.state.comments).toHaveLength(3);expect(root.textContent).toContain("확정 선택");yield*view.close;
}));
for(const [label,change]of[
 ["collection",(p:P)=>({...p,collectionId:"other"})],["offset",(p:P)=>({...p,offset:1})],["matched",(p:P)=>({...p,matched:p.total+1})],
 ["too many rows",(p:P)=>({...p,rows:[...p.rows,p.rows[0]!]})],["rows exceed matched",(p:P)=>({...p,matched:1})],
 ["duplicate ID",(p:P)=>({...p,rows:[p.rows[0]!,p.rows[0]!]})],
]as const){
 it.effect("invalid participant "+label+" reply is rejected before changing visible snapshot",()=>Effect.gen(function*(){
  const h=harness();const root=yield*host();const view=yield*mount(root,h);const before=root.querySelector("ol")!.textContent;h.state.pageChange=change;button(root,"명단 다시 조회").click();yield*h.next();expect(root.querySelector("ol")!.textContent).toBe(before);expect(root.querySelector<HTMLElement>("[role=alert]")!.hidden).toBe(false);yield*view.close;
 }));
}
for(const [label,change]of[
 ["collection",(p:C)=>({...p,collectionId:"other"})],["participant",(p:C)=>({...p,participantId:"other"})],["offset",(p:C)=>({...p,offset:1})],
 ["limit",(p:C)=>({...p,rows:[...p.rows,{...p.rows[0]!,id:"extra-unique"}]})],["total",(p:C)=>({...p,total:1})],["duplicate",(p:C)=>({...p,rows:[p.rows[0]!,p.rows[0]!]})],["revision",(p:C)=>({...p,revision:8})],
]as const){
 it.effect("invalid comment "+label+" reply is rejected without exposing unrelated rows",()=>Effect.gen(function*(){
  const h=harness();const root=yield*host();const view=yield*mount(root,h);h.state.commentChange=change;root.querySelector<HTMLButtonElement>("button[data-participant-id=p0]")!.click();yield*h.next();expect(root.querySelector("ol[aria-label='저장된 댓글']")!.children).toHaveLength(0);expect(root.querySelector<HTMLElement>("[role=alert]")!.hidden).toBe(false);yield*view.close;
 }));
}
it.effect("new backend session or revision reversal cannot overwrite visible frozen records",()=>Effect.gen(function*(){
 const h=harness();const root=yield*host();const view=yield*mount(root,h);const before=root.querySelector("ol")!.textContent;
 h.state.session="replacement";button(root,"명단 다시 조회").click();yield*h.next();expect(root.querySelector("ol")!.textContent).toBe(before);expect(root.querySelector<HTMLElement>("[role=alert]")!.hidden).toBe(false);
 h.state.session="session";h.state.revision=6;button(root,"명단 다시 조회").click();yield*h.next();expect(root.querySelector("ol")!.textContent).toBe(before);expect(root.querySelector<HTMLElement>("[role=alert]")!.hidden).toBe(false);
 h.state.revision=8;button(root,"다음 참가자 페이지").click();yield*h.next();expect(root.querySelector("ol")!.textContent).toBe(before);yield*view.close;
}));
it.effect("empty saved participant and comment pages render normal empty state",()=>Effect.gen(function*(){
 const h=harness();h.state.pageChange=p=>({...p,total:0,matched:0,rows:[]});const root=yield*host();const view=yield*mount(root,h);expect(button(root,"다음 참가자 페이지").disabled).toBe(true);yield*view.close;
 h.state.pageChange=p=>p;h.state.commentChange=p=>({...p,total:0,rows:[]});const second=yield*mount(root,h);root.querySelector<HTMLButtonElement>("button[data-participant-id=p0]")!.click();yield*h.next();expect(root.textContent).toContain("저장된 댓글이 없습니다");expect(button(root,"다음 댓글 페이지").disabled).toBe(true);yield*second.close;expect(h.state.listeners).toBe(0);
}));
it.effect("closing scope cancels initial read and a late native callback cannot restore detached DOM",()=>Effect.gen(function*(){
 const h=harness();const entered=yield*Deferred.make<void>();let resume:((reply:Effect.Effect<ProductReply<P>>)=>void)|undefined;let cancelled=0;
 h.reads.queryFrozenParticipants=()=>Effect.callback<ProductReply<P>>((complete)=>{resume=complete;Deferred.doneUnsafe(entered,Effect.void);return Effect.sync(()=>{cancelled++;});});
 const root=yield*host();const view=yield*mount(root,h);yield*Deferred.await(entered);expect(button(root,"명단 다시 조회").disabled).toBe(true);
 submit(root);yield*h.next();expect(button(root,"명단 다시 조회").disabled).toBe(true);
 yield*view.close;expect(cancelled).toBe(1);expect(h.state.listeners).toBe(0);expect(root.children).toHaveLength(0);
 resume?.(Effect.succeed(reply({collectionId:"collection",revision:7,total:1,matched:1,offset:0,rows:[all[0]!]})));yield*settle;expect(root.children).toHaveLength(0);
}));
it.effect("closing scope cancels an in-flight comment read and both query streams release listeners",()=>Effect.gen(function*(){
 const h=harness();const entered=yield*Deferred.make<void>();let cancelled=0;h.reads.queryFrozenComments=()=>Deferred.succeed(entered,undefined).pipe(Effect.andThen(Effect.never),Effect.onInterrupt(()=>Effect.sync(()=>{cancelled++;})));
 const root=yield*host();const view=yield*mount(root,h);root.querySelector<HTMLButtonElement>("button[data-participant-id=p0]")!.click();
 const dispatched=h.effects.shift()!;const fiber=yield*dispatched.pipe(Effect.provideService(DomPlatform,h.dom),Effect.forkChild);yield*Deferred.await(entered);yield*view.close;yield*Fiber.await(fiber);expect(cancelled).toBe(1);expect(h.state.listeners).toBe(0);expect(root.children).toHaveLength(0);
}));
it.effect("overlong synthetic search is rejected locally with no backend work",()=>Effect.gen(function*(){
 const h=harness();const root=yield*host();const view=yield*mount(root,h);root.querySelector<HTMLInputElement>("input")!.value="x".repeat(201);submit(root);yield*h.next();expect(h.state.participants).toHaveLength(1);expect(root.querySelector<HTMLElement>("[role=alert]")!.hidden).toBe(false);yield*view.close;
}));

it.effect("frozen row renders up to three literal previews while full comments stay behind50-row detail",()=>Effect.gen(function*(){const h=harness();h.state.pageChange=p=>({...p,rows:[{...p.rows[0]!,previews:["<script>first</script>","second😀","third"]}]});const root=yield*host();const view=yield*mount(root,h);expect([...root.querySelectorAll("[data-comment-previews] p")].map(p=>p.textContent)).toEqual(["<script>first</script>","second😀","third"]);expect(h.state.comments).toHaveLength(0);expect(root.querySelector("script")).toBeNull();root.querySelector<HTMLButtonElement>("button[data-participant-id=p0]")!.click();yield*h.next();expect(h.state.comments[0]?.limit).toBe(50);yield*view.close;expect(h.state.listeners).toBe(0);}));
it.effect("frozen latest search cancels older success and failure without overwriting newer rows",()=>Effect.gen(function*(){
 for(const failure of [false,true]){const h=harness();const root=yield*host();const view=yield*mount(root,h);const started=yield*Deferred.make<void>();let cancelled=0;let late:((value:Effect.Effect<ProductReply<P>,BackendRejected>)=>void)|undefined;
 const original=h.reads.queryFrozenParticipants;h.reads.queryFrozenParticipants=request=>request.query==="old"?Effect.callback<ProductReply<P>,BackendRejected>(resume=>{late=resume;Deferred.doneUnsafe(started,Effect.void);return Effect.sync(()=>{cancelled++;});}):original(request);
 const input=root.querySelector<HTMLInputElement>("input")!;input.value="old";input.dispatchEvent(new Event("input"));submit(root);const first=yield*h.next().pipe(Effect.forkChild);yield*Deferred.await(started);expect(button(root,"명단 검색").disabled).toBe(false);
 input.value="uid109";input.dispatchEvent(new Event("input"));submit(root);yield*h.next();expect(cancelled).toBe(1);yield*Fiber.await(first);const before=root.querySelector("ol")!.textContent;expect(before).toContain("참가자109");late?.(failure?Effect.fail(new BackendRejected({code:"StorageUnavailable",messageKey:"private"})):Effect.succeed(reply({collectionId:"collection",revision:7,total:1,matched:1,offset:0,rows:[all[0]!]})));yield*settle;expect(root.querySelector("ol")!.textContent).toBe(before);expect(root.querySelector<HTMLElement>("[role=alert]")!.hidden).toBe(true);yield*view.close;expect(h.state.listeners).toBe(0);}
}));
it.effect("frozen raw search change invalidates pending response before another submission",()=>Effect.gen(function*(){const h=harness();const root=yield*host();const view=yield*mount(root,h);const before=root.querySelector("ol")!.textContent;const held=yield*Deferred.make<ProductReply<P>>();const started=yield*Deferred.make<void>();h.reads.queryFrozenParticipants=()=>Deferred.succeed(started,undefined).pipe(Effect.andThen(Deferred.await(held)));const input=root.querySelector<HTMLInputElement>("input")!;input.value="uid1";input.dispatchEvent(new Event("input"));submit(root);const first=yield*h.next().pipe(Effect.forkChild);yield*Deferred.await(started);input.value="uid2";input.dispatchEvent(new Event("input"));yield*Deferred.succeed(held,reply({collectionId:"collection",revision:7,total:1,matched:1,offset:0,rows:[all[1]!]}));yield*Fiber.join(first);expect(root.querySelector("ol")!.textContent).toBe(before);expect(button(root,"명단 다시 조회").disabled).toBe(false);yield*view.close;}));
it.effect("frozen refresh uses applied query while preserving unsubmitted raw input",()=>Effect.gen(function*(){const h=harness();const root=yield*host();const view=yield*mount(root,h);const input=root.querySelector<HTMLInputElement>("input")!;input.value="unsubmitted";input.dispatchEvent(new Event("input"));button(root,"명단 다시 조회").click();yield*h.next();expect(h.state.participants.at(-1)?.query).toBe("");expect(input.value).toBe("unsubmitted");expect(root.querySelectorAll("button[data-participant-id]")).toHaveLength(100);yield*view.close;}));
it.effect("frozen replacement search interrupts comment read and preserves only current snapshot",()=>Effect.gen(function*(){const h=harness();const root=yield*host();const view=yield*mount(root,h);const entered=yield*Deferred.make<void>();let cancelled=0;h.reads.queryFrozenComments=()=>Deferred.succeed(entered,undefined).pipe(Effect.andThen(Effect.never),Effect.onInterrupt(()=>Effect.sync(()=>{cancelled++;})));root.querySelector<HTMLButtonElement>("button[data-participant-id=p0]")!.click();const first=yield*h.next().pipe(Effect.forkChild);yield*Deferred.await(entered);const input=root.querySelector<HTMLInputElement>("input")!;input.value="uid109";input.dispatchEvent(new Event("input"));submit(root);yield*h.next();expect(cancelled).toBe(1);yield*Fiber.await(first);expect(root.querySelector("ol")!.textContent).toContain("참가자109");expect(root.querySelector<HTMLElement>("section[aria-label='확정 참가자의 댓글']")!.hidden).toBe(true);yield*view.close;expect(h.state.listeners).toBe(0);}));
it.effect("frozen same query after changed raw has a new intent and cannot remain pending",()=>Effect.gen(function*(){const h=harness();const root=yield*host();const view=yield*mount(root,h);const original=h.reads.queryFrozenParticipants;const started=yield*Deferred.make<void>();let calls=0;let cancelled=0;h.reads.queryFrozenParticipants=request=>++calls===1?Deferred.succeed(started,undefined).pipe(Effect.andThen(Effect.never),Effect.onInterrupt(()=>Effect.sync(()=>{cancelled++;}))):original(request);const input=root.querySelector<HTMLInputElement>("input")!;input.value="uid109";input.dispatchEvent(new Event("input"));submit(root);const first=yield*h.next().pipe(Effect.forkChild);yield*Deferred.await(started);input.value="other";input.dispatchEvent(new Event("input"));input.value="uid109";input.dispatchEvent(new Event("input"));submit(root);yield*h.next();expect(calls).toBe(2);expect(cancelled).toBe(1);yield*Fiber.await(first);expect(root.querySelector("ol")!.textContent).toContain("참가자109");expect(button(root,"명단 다시 조회").disabled).toBe(false);yield*view.close;}));
it.effect("frozen raw edit before queued replacement cancels older read without issuing stale work",()=>Effect.gen(function*(){const h=harness();const root=yield*host();const view=yield*mount(root,h);const started=yield*Deferred.make<void>();let calls=0;let cancelled=0;const original=h.reads.queryFrozenParticipants;h.reads.queryFrozenParticipants=request=>{calls++;return calls===1?Deferred.succeed(started,undefined).pipe(Effect.andThen(Effect.never),Effect.onInterrupt(()=>Effect.sync(()=>{cancelled++;}))):original(request);};const input=root.querySelector<HTMLInputElement>("input")!;input.value="old";input.dispatchEvent(new Event("input"));submit(root);const first=yield*h.next().pipe(Effect.forkChild);yield*Deferred.await(started);input.value="new";input.dispatchEvent(new Event("input"));submit(root);input.value="changed before dispatch";input.dispatchEvent(new Event("input"));yield*h.next();expect(calls).toBe(1);expect(cancelled).toBe(1);yield*Fiber.await(first);expect(button(root,"명단 다시 조회").disabled).toBe(false);expect(root.querySelector<HTMLElement>("[role=alert]")!.hidden).toBe(true);yield*view.close;expect(h.state.listeners).toBe(0);}));
it.effect("frozen queued refresh admits only latest intent before dispatch and duplicate pending adds no read",()=>Effect.gen(function*(){const h=harness();const root=yield*host();const view=yield*mount(root,h);const before=h.state.participants.length;button(root,"명단 다시 조회").click();button(root,"명단 다시 조회").click();yield*h.next();expect(h.state.participants).toHaveLength(before);yield*h.next();expect(h.state.participants).toHaveLength(before+1);yield*view.close;expect(h.state.listeners).toBe(0);}));
it.effect("frozen latest queued intent fences older pagination response before replacement starts",()=>Effect.gen(function*(){const h=harness();const root=yield*host();const view=yield*mount(root,h);const before=root.querySelector("ol")!.textContent;const original=h.reads.queryFrozenParticipants;const started=yield*Deferred.make<void>();const held=yield*Deferred.make<ProductReply<P>>();h.reads.queryFrozenParticipants=request=>request.offset===100?Deferred.succeed(started,undefined).pipe(Effect.andThen(Deferred.await(held))):original(request);button(root,"다음 참가자 페이지").click();const first=yield*h.next().pipe(Effect.forkChild);yield*Deferred.await(started);submit(root);yield*Deferred.succeed(held,reply({collectionId:"collection",revision:7,total:110,matched:110,offset:100,rows:all.slice(100)}));yield*Fiber.join(first);expect(root.querySelector("ol")!.textContent).toBe(before);yield*h.next();expect(root.querySelectorAll("button[data-participant-id]")).toHaveLength(100);yield*view.close;expect(h.state.listeners).toBe(0);}));
it.effect("frozen raw intent condition witnesses independently fence version query and group",()=>Effect.gen(function*(){for(const condition of ["version","query","group"]as const){const h=harness();const root=yield*host();const view=yield*mount(root,h);const before=root.querySelector("ol")!.textContent;const started=yield*Deferred.make<void>();const held=yield*Deferred.make<ProductReply<P>>();h.reads.queryFrozenParticipants=()=>Deferred.succeed(started,undefined).pipe(Effect.andThen(Deferred.await(held)));button(root,"명단 다시 조회").click();const first=yield*h.next().pipe(Effect.forkChild);yield*Deferred.await(started);if(condition==="version")root.querySelector("input")!.dispatchEvent(new Event("input"));else if(condition==="query")root.querySelector<HTMLInputElement>("input")!.value="unsent";else root.querySelector<HTMLSelectElement>("select")!.value="excluded";yield*Deferred.succeed(held,reply({collectionId:"collection",revision:7,total:1,matched:1,offset:0,rows:[all[109]!]}));yield*Fiber.join(first);expect(root.querySelector("ol")!.textContent).toBe(before);expect(button(root,"명단 다시 조회").disabled).toBe(false);yield*view.close;expect(h.state.listeners).toBe(0);}}));
it.effect("frozen raw edit suppresses an older read error while preserving the confirmed snapshot",()=>Effect.gen(function*(){const h=harness();const root=yield*host();const view=yield*mount(root,h);const before=root.querySelector("ol")!.textContent;const started=yield*Deferred.make<void>();const held=yield*Deferred.make<ProductReply<P>,BackendRejected>();h.reads.queryFrozenParticipants=()=>Deferred.succeed(started,undefined).pipe(Effect.andThen(Deferred.await(held)));button(root,"명단 다시 조회").click();const first=yield*h.next().pipe(Effect.forkChild);yield*Deferred.await(started);const input=root.querySelector<HTMLInputElement>("input")!;input.value="changed";input.dispatchEvent(new Event("input"));yield*Deferred.fail(held,new BackendRejected({code:"StorageUnavailable",messageKey:"private"}));yield*Fiber.join(first);expect(root.querySelector("ol")!.textContent).toBe(before);expect(root.querySelector<HTMLElement>("[role=alert]")!.hidden).toBe(true);expect(button(root,"명단 다시 조회").disabled).toBe(false);yield*view.close;expect(h.state.listeners).toBe(0);}));