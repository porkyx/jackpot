import { expect, it, vi } from "@effect/vitest";
import { Deferred, Effect, Exit, Fiber, Scope, Stream } from "effect";
import { TestClock } from "effect/testing";
import { ProtocolError, BackendRejected } from "../../src/contracts/backend";
import type { Collection, Round, ProductReply } from "../../src/contracts/product";
import type { ProductCommands } from "../../src/operations/productCoordinator";
import { DomPlatform, makeDomPlatform } from "../../src/platform/dom";
import { mountResultProduct, kstSchedule } from "../../src/features/results/view";
import { mountExportProduct, projectResultExport, ResultExportFailure, type ProductDispatch } from "../../src/features/export/view";
import { resultExportText } from "../../src/platform/canvas";

const now="2026-10-06T01:00:00.000Z";
const person={id:"p1",nickname:"<script>문자열😀</script>",publicIdentifier:"uid",kind:"fixed" as const,classification:"included" as const,reason:"" as const,included:true,commentCount:1,previews:["댓글"]};
function round(change:Partial<Round>={}):Round{return{collectionId:"collection",roundId:"round-1",number:1,attempt:1,state:"completed",revision:1,roundVersion:1,mode:"immediate",message:"관리 메시지",prizes:[{id:"prize",name:"품목",count:1}],scheduledAt:null,executedAt:now,failureCode:null,winners:[{participant:person,prizeId:"prize",prizeName:"품목",slot:1}],algorithmVersion:"v1",appVersion:"0.1.0",...change};}
function collection(change:Partial<Collection>={}):Collection{return{collectionId:"collection",revision:1,article:{url:"https://gall.dcinside.com/board/view/?id=test&no=1",title:"<img onerror=bad()>제목",galleryId:"test",galleryName:"갤러리",galleryKind:"G",number:"1",authorNickname:"작성자",authorIdentifier:"uid",postedAt:null},
snapshot:{snapshotId:"snapshot",collectedAt:now,complete:true,pages:1,acceptedComments:3,deletedComments:0,unsupportedComments:0},
filters:{excludeAnonymous:false,excludeAuthor:true,excludeDcconOnly:false,timeCut:null,includeKeywords:[],excludeKeywords:[]},participantCount:3,selectedCount:3,remainingCount:2,rounds:[round()],roundTotal:change.rounds?.length??1,roundOffset:0,latestRound:change.rounds?.at(-1)??round(),...change};}
function reply<A>(data:A):ProductReply<A>{return{protocolVersion:1,backendSessionId:"session",occurredAt:now,operationId:null,revision:null,data};}
function harness(initial:Collection=collection()){
 let current=initial;const effects:Array<Effect.Effect<void,never,DomPlatform>>=[];const state={listeners:0,gets:0,commands:[] as Array<{kind:string;request:Parameters<ProductCommands["collectionCommand"]>[1]}>,getFails:false,visibility:"visible" as DocumentVisibilityState};
 const native=makeDomPlatform({request:()=>0,cancel:()=>{}},()=>state.visibility);
 const dom:typeof DomPlatform.Service={...native,listen:(...args)=>native.listen(...args).pipe(Effect.andThen(Effect.acquireRelease(Effect.sync(()=>{state.listeners++;}),()=>Effect.sync(()=>{state.listeners--;}))))};
 const fail=Effect.fail(new ProtocolError());
 const commands:{-readonly [K in keyof ProductCommands]:ProductCommands[K]}={
 draft:Effect.succeed(null),changes:Stream.empty,ensureDraft:fail,refreshDraft:fail,loadArticle:()=>fail,cancelLoad:fail,resetArticle:fail,edit:()=>fail,commitThrough:()=>fail,createCollection:()=>fail,queryParticipants:()=>fail,queryParticipantComments:()=>fail,queryFrozenParticipants:()=>fail,queryFrozenComments:()=>fail,
 getCollection:()=>Effect.suspend(()=>{state.gets++;return state.getFails?fail:Effect.succeed(reply(current));}),
 collectionCommand:(kind,request)=>Effect.sync(()=>{state.commands.push({kind,request});return current;}),
 };
 const dispatch:ProductDispatch=(effect)=>effects.push(effect);
 const runNext=()=>Effect.gen(function*(){const effect=effects.shift();if(effect===undefined)throw new Error("missing dispatched effect");const fiber=yield* effect.pipe(Effect.provideService(DomPlatform,dom),Effect.forkChild);yield* Effect.yieldNow;yield* Effect.yieldNow;return fiber;});
 return{state,commands,dom,dispatch,runNext,effects,set:(value:Collection)=>{current=value;}};
}
function host(){return Effect.acquireRelease(Effect.sync(()=>{const node=document.createElement("main");document.body.append(node);return node;}),(node)=>Effect.sync(()=>node.remove()));}
function button(root:HTMLElement,text:string):HTMLButtonElement{const value=[...root.querySelectorAll("button")].find((item)=>item.textContent===text);if(value===undefined)throw new Error("button "+text);return value;}
function submit(form:HTMLFormElement){form.dispatchEvent(new Event("submit",{bubbles:true,cancelable:true}));}
function waitForNode(root:HTMLElement,selector:string){return Effect.callback<HTMLElement>((resume)=>{const current=root.querySelector<HTMLElement>(selector);if(current!==null){resume(Effect.succeed(current));return;}const observer=new MutationObserver(()=>{const node=root.querySelector<HTMLElement>(selector);if(node!==null){observer.disconnect();resume(Effect.succeed(node));}});observer.observe(root,{childList:true,subtree:true});return Effect.sync(()=>observer.disconnect());});}

function roundPage(total:number,offset=0):Collection{const count=Math.min(50,Math.max(0,total-offset));return collection({roundTotal:total,roundOffset:offset,latestRound:round({roundId:"r"+total,number:total}),rounds:Array.from({length:count},(_,index)=>round({roundId:"r"+(total-offset-count+index+1),number:total-offset-count+index+1})),});}
it.effect("round pages show at most fifty options and retain latest management identity on older pages",()=>Effect.gen(function*(){
 const h=harness(roundPage(101));const root=yield*host();const requests:Array<Parameters<ProductCommands["getCollection"]>[1]>=[];
 h.commands.getCollection=(_id,options)=>Effect.sync(()=>{requests.push(options);return reply(roundPage(101,options?.roundOffset??0));});
 const view=yield*mountResultProduct(root,h.commands,{_tag:"result",collectionId:"collection",roundId:null},h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom));
 try{expect(root.querySelectorAll("select[name=resultRound] option")).toHaveLength(50);expect(root.textContent).toContain("전체 101회차 · 52~101회차");expect(button(root,"더 최근 회차").disabled).toBe(true);expect(button(root,"재추첨").disabled).toBe(false);
  button(root,"이전 회차 보기").click();yield*Fiber.join(yield*h.runNext());expect(requests.at(-1)).toEqual({roundOffset:50});expect(root.textContent).toContain("전체 101회차 · 2~51회차");expect(root.textContent).toContain("지난 회차");expect(button(root,"재추첨").disabled).toBe(true);
  button(root,"이전 회차 보기").click();yield*Fiber.join(yield*h.runNext());expect(root.querySelectorAll("select[name=resultRound] option")).toHaveLength(1);expect(root.textContent).toContain("전체 101회차 · 1~1회차");expect(button(root,"이전 회차 보기").disabled).toBe(true);
  button(root,"더 최근 회차").click();yield*Fiber.join(yield*h.runNext());button(root,"더 최근 회차").click();yield*Fiber.join(yield*h.runNext());expect(button(root,"재추첨").disabled).toBe(false);expect(h.state.commands).toHaveLength(0);
 }finally{yield*view.close;}expect(h.state.listeners).toBe(0);
}));
it.effect("direct old round route queries anchor page and refresh keeps the same historical selection",()=>Effect.gen(function*(){
 const h=harness(roundPage(101,100));const root=yield*host();const requests:Array<Parameters<ProductCommands["getCollection"]>[1]>=[];
 h.commands.getCollection=(_id,options)=>Effect.sync(()=>{requests.push(options);return reply(roundPage(101,100));});
 const view=yield*mountResultProduct(root,h.commands,{_tag:"result",collectionId:"collection",roundId:"r1"},h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom));
 try{expect(requests).toEqual([{roundId:"r1"}]);expect(root.querySelector<HTMLSelectElement>("[name=resultRound]")?.value).toBe("r1");expect(button(root,"재추첨").disabled).toBe(true);button(root,"다시 조회").click();yield*Fiber.join(yield*h.runNext());expect(requests.at(-1)).toEqual({roundId:"r1"});}finally{yield*view.close;}
}));
it.effect("round page dependency failure preserves known page and disables mutation until successful refresh",()=>Effect.gen(function*(){
 const h=harness(roundPage(51));const root=yield*host();const view=yield*mountResultProduct(root,h.commands,{_tag:"result",collectionId:"collection",roundId:null},h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom));
 try{h.state.getFails=true;button(root,"이전 회차 보기").click();yield*Fiber.join(yield*h.runNext());expect(root.querySelectorAll("[name=resultRound] option")).toHaveLength(50);expect(root.textContent).toContain("마지막 확인된 기록");expect(button(root,"재추첨").disabled).toBe(true);h.state.getFails=false;button(root,"다시 조회").click();yield*Fiber.join(yield*h.runNext());expect(button(root,"재추첨").disabled).toBe(false);}finally{yield*view.close;}
}));
it.effect("round page scope close cancels pending read and its late callback cannot patch or retain DOM",()=>Effect.gen(function*(){
 const h=harness(roundPage(51));const root=yield*host();const view=yield*mountResultProduct(root,h.commands,{_tag:"result",collectionId:"collection",roundId:null},h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom));
 const started=yield*Deferred.make<void>();let active=0,cancel=0;let late:(effect:Effect.Effect<ProductReply<Collection>>)=>void=()=>{};
 h.commands.getCollection=()=>Effect.callback(resume=>{late=resume;active++;Deferred.doneUnsafe(started,Effect.void);return Effect.sync(()=>{active--;cancel++;});});button(root,"이전 회차 보기").click();const read=yield*h.runNext();yield*Deferred.await(started);expect(button(root,"이전 회차 보기").disabled).toBe(true);
 yield*view.close;late(Effect.succeed(reply(roundPage(51,50))));expect((yield*Fiber.await(read))._tag).toBe("Failure");expect([active,cancel,h.state.listeners,root.childElementCount]).toEqual([0,1,0,0]);yield*view.close;
}));
it.effect("round page older revision and foreign collection replies cannot replace current selection",()=>Effect.gen(function*(){
 const h=harness({...roundPage(51),revision:3});const root=yield*host();const view=yield*mountResultProduct(root,h.commands,{_tag:"result",collectionId:"collection",roundId:null},h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom));
 try{for(const data of[{...roundPage(51,50),revision:2},{...roundPage(51,50),collectionId:"foreign",revision:4}]){h.commands.getCollection=()=>Effect.succeed(reply(data));button(root,"이전 회차 보기").click();yield*Fiber.join(yield*h.runNext());expect(root.querySelector<HTMLSelectElement>("[name=resultRound]")?.value).toBe("r51");expect(button(root,"재추첨").disabled).toBe(true);}}finally{yield*view.close;}
}));

it.effect("result queries authoritative snapshot and renders external values as plain text",()=>Effect.gen(function*(){
 const h=harness();const root=yield* host();const mounted=yield* mountResultProduct(root,h.commands,{_tag:"result",collectionId:"collection",roundId:null},h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom));
 expect(h.state.gets).toBe(1);expect(root.textContent).toContain("<img onerror=bad()>제목");expect(root.textContent).toContain("<script>문자열😀</script>");expect(root.querySelector("script")).toBeNull();expect(root.querySelector("h3 img")).toBeNull();expect(button(root,"재추첨").disabled).toBe(false);expect(root.querySelector('input[type=password]')).toBeNull();expect([...root.querySelectorAll("button")].some(button=>button.textContent==="관리 잠금 해제")).toBe(false);
 yield* mounted.close;expect(h.state.listeners).toBe(0);
}));
it.effect("rerun has explicit prizes/message/mode and latest revision while cancellation makes no request",()=>Effect.gen(function*(){
 const h=harness(collection({}));const root=yield* host();const mounted=yield* mountResultProduct(root,h.commands,{_tag:"result",collectionId:"collection",roundId:null},h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom));
 button(root,"재추첨").click();const fiber=yield* h.runNext();const form=root.querySelector("dialog form") as HTMLFormElement;form.querySelector<HTMLInputElement>('[name=prizeName1]')!.value="선물";form.querySelector<HTMLInputElement>('[name=message]')!.value="  그대로  ";submit(form);yield* Fiber.join(yield* h.runNext());yield* Fiber.join(fiber);
 expect(h.state.commands).toHaveLength(1);const call=h.state.commands[0]!;expect(call.kind).toBe("rerun");expect(call.request.expectedRevision).toBe(1);expect(call.request.expectedVersion).toBe(1);expect(call.request.prizes).toEqual([{id:"rerun-prize-1",name:"선물",count:1}]);expect(call.request.mode).toBe("immediate");expect(call.request.message).toBe("  그대로  ");yield* mounted.close;
}));
for(const seconds of [10,30,60,120]as const){
 it.effect("quick schedule sends backend accepted-time delay "+seconds,()=>Effect.gen(function*(){
  const h=harness(collection({rounds:[round({state:"pending_schedule",mode:"reservation",winners:[],executedAt:null})]}));const root=yield* host();const mounted=yield* mountResultProduct(root,h.commands,{_tag:"result",collectionId:"collection",roundId:null},h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom));
  button(root,seconds===10?"10초 후":seconds===30?"30초 후":seconds===60?"1분 후":"2분 후").click();yield* Fiber.join(yield* h.runNext());expect(h.state.commands[0]?.kind).toBe("setSchedule");expect(h.state.commands[0]?.request.quickDelaySeconds).toBe(seconds);expect(h.state.commands[0]?.request.scheduledAt).toBeNull();yield* mounted.close;
 }));
}
it("KST direct schedule handles0..3days midnight/noon and rejects past/invalid boundaries",()=>{
 const base=Date.parse("2026-10-06T01:00:00Z");
 expect(kstSchedule(base,0,"pm",0,0)).toBe("2026-10-06T03:00:00.000Z");expect(kstSchedule(base,1,"am",0,0)).toBe("2026-10-06T15:00:00.000Z");expect(kstSchedule(base,3,"pm",11,59)).toBe("2026-10-09T14:59:00.000Z");
 for(const values of [[-1,0,0],[4,0,0],[0,12,0],[0,0,60],[0,0,-1],[0,0,0]]as const)expect(()=>kstSchedule(base,values[0],"am",values[1],values[2])).toThrow();
});
for(const offset of [9999,10000,10001]){
 it.effect("scheduled cancellation10second boundary "+offset,()=>Effect.gen(function*(){
  const h=harness(collection({rounds:[round({state:"scheduled",mode:"reservation",scheduledAt:new Date(Date.parse(now)+offset).toISOString(),executedAt:null,winners:[]})]}));const root=yield* host();const mounted=yield* mountResultProduct(root,h.commands,{_tag:"result",collectionId:"collection",roundId:null},h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom));
  expect(button(root,"예약 취소").disabled).toBe(offset<=10000);if(offset>10000){button(root,"예약 취소").click();yield* Fiber.join(yield* h.runNext());expect(h.state.commands[0]?.kind).toBe("cancelSchedule");}yield* mounted.close;
 }));
}
it.effect("countdown reaches due time using fresh query only and scope stops polling",()=>Effect.gen(function*(){
 const h=harness(collection({rounds:[round({state:"scheduled",mode:"reservation",scheduledAt:new Date(Date.parse(now)+1000).toISOString(),executedAt:null,winners:[]})]}));const root=yield* host();const mounted=yield* mountResultProduct(root,h.commands,{_tag:"result",collectionId:"collection",roundId:null},h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom));
 yield* TestClock.adjust("2 seconds");expect(h.state.gets).toBeGreaterThan(1);expect(h.state.commands).toEqual([]);expect(root.textContent).toContain("Go의 저장된 완료");yield* mounted.close;const count=h.state.gets;yield* TestClock.adjust("5 seconds");expect(h.state.gets).toBe(count);expect(h.state.listeners).toBe(0);
}));
it.effect("failed round exposes only explicit retry with round version",()=>Effect.gen(function*(){
 const h=harness(collection({rounds:[round({state:"failed",failureCode:"StorageUnavailable",winners:[],executedAt:null})]}));const root=yield* host();const mounted=yield* mountResultProduct(root,h.commands,{_tag:"result",collectionId:"collection",roundId:null},h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom));expect(button(root,"재추첨").disabled).toBe(true);expect(button(root,"실패 회차 재시도").disabled).toBe(false);button(root,"실패 회차 재시도").click();yield* Fiber.join(yield* h.runNext());expect(h.state.commands[0]?.kind).toBe("retryRound");expect(h.state.commands[0]?.request.expectedVersion).toBe(1);yield* mounted.close;
}));
it.effect("query failure preserves confirmed result and blocks writes until successful refresh",()=>Effect.gen(function*(){
 const h=harness(collection({}));const root=yield* host();const mounted=yield* mountResultProduct(root,h.commands,{_tag:"result",collectionId:"collection",roundId:null},h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom));h.state.getFails=true;button(root,"다시 조회").click();yield* Fiber.join(yield* h.runNext());expect(root.textContent).toContain("<script>문자열😀</script>");expect(button(root,"재추첨").disabled).toBe(true);h.state.getFails=false;button(root,"다시 조회").click();yield* Fiber.join(yield* h.runNext());expect(button(root,"재추첨").disabled).toBe(false);yield* mounted.close;
}));
it.effect("past round selection preserves readonly result and immutable export target",()=>Effect.gen(function*(){
 const old=round();const latest=round({roundId:"round-2",number:2,winners:[{...old.winners[0]!,participant:{...person,id:"p2",nickname:"현재"}}]});const data=collection({rounds:[old,latest]});const h=harness(data);const root=yield* host();const mounted=yield* mountResultProduct(root,h.commands,{_tag:"result",collectionId:"collection",roundId:"round-1"},h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom));expect(root.textContent).toContain("지난 회차");expect(button(root,"재추첨").disabled).toBe(true);const model=projectResultExport(data,old);expect(model.roundId).toBe("round-1");expect(resultExportText(model)).not.toContain("현재");yield* mounted.close;
}));
it.effect("export preview URL replacement and scope close revoke every URL",()=>Effect.gen(function*(){
 const h=harness();const root=yield* host();let created=0;const revoked:string[]=[];
 const mounted=yield* mountExportProduct(root,()=>({collection:collection(),round:round()}),h.dispatch,{savePng:()=>Effect.succeed("cancelled"),copyText:()=>Effect.void},{renderPng:()=>Effect.succeed(new Blob(["png"],{type:"image/png"}))},{createObjectURL:()=>`blob:test-${++created}`,revokeObjectURL:(url)=>revoked.push(url)}).pipe(Effect.provideService(DomPlatform,h.dom));
 button(root,"PNG 미리보기").click();yield* Fiber.join(yield* h.runNext());expect(root.querySelector("img")?.hidden).toBe(false);
 button(root,"PNG 미리보기").click();yield* Fiber.join(yield* h.runNext());expect(revoked).toEqual(["blob:test-1"]);
 button(root,"PNG 사진 저장").click();yield* Fiber.join(yield* h.runNext());expect(root.textContent).toContain("저장을 취소");yield* mounted.close;expect(revoked).toEqual(["blob:test-1","blob:test-2"]);expect(h.state.listeners).toBe(0);
}));
it.effect("clipboard failure preserves result and secret never enters export model",()=>Effect.gen(function*(){
 const h=harness();const root=yield* host();let text="";
 const mounted=yield* mountExportProduct(root,()=>({collection:collection(),round:round()}),h.dispatch,{savePng:()=>Effect.succeed("saved"),copyText:(value)=>{text=value;return Effect.fail(new ResultExportFailure({reason:"clipboard"}));}},{renderPng:()=>Effect.succeed(new Blob(["png"],{type:"image/png"}))}).pipe(Effect.provideService(DomPlatform,h.dom));
 button(root,"결과 텍스트 복사").click();yield* Fiber.join(yield* h.runNext());expect(text.startsWith(collection().article.title+"\n")).toBe(true);expect(text).not.toContain("Jackpot 로컬 추첨 결과");expect(text).toContain(person.nickname);expect(text).not.toContain("password");expect(root.textContent).toContain("결과는 유지");yield* mounted.close;
}));

it.effect("locked result opens persisted participant comments readonly and native close disposes all panel listeners",()=>Effect.gen(function*(){
 const h=harness();let lists=0,comments=0;
 h.commands.queryFrozenParticipants=()=>Effect.sync(()=>{lists++;return reply({collectionId:"collection",revision:1,total:1,matched:1,offset:0,rows:[person]});});
 h.commands.queryFrozenComments=()=>Effect.sync(()=>{comments++;return reply({collectionId:"collection",revision:1,participantId:person.id,total:1,offset:0,rows:[{id:"comment",parentId:null,kind:"text"as const,text:"<b>저장 댓글</b>",postedAt:null,mediaUrls:[]}]});});
 const root=yield*host();const view=yield*mountResultProduct(root,h.commands,{_tag:"result",collectionId:"collection",roundId:null},h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom));
 button(root,"확정 참가자 보기").click();const fiber=yield*h.runNext();const participantButton=yield*waitForNode(root,"button[data-participant-id]");
 expect(lists).toBe(1);expect(root.querySelector("dialog input[type=password],dialog input[type=checkbox]")).toBeNull();participantButton.click();yield*Fiber.join(yield*h.runNext());expect(comments).toBe(1);expect(root.querySelector("dialog")!.textContent).toContain("<b>저장 댓글</b>");expect(root.querySelector("dialog b")).toBeNull();
 button(root,"명단 닫기").click();yield*Fiber.join(fiber);expect(root.querySelector("dialog")).toBeNull();expect(h.state.commands).toEqual([]);yield*view.close;expect(h.state.listeners).toBe(0);
}));
it.effect("rerun preserves separate single and multiple values, inactive constraints, optional names and bounded counts",()=>Effect.gen(function*(){
 const h=harness(collection({}));const root=yield*host();const view=yield*mountResultProduct(root,h.commands,{_tag:"result",collectionId:"collection",roundId:null},h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom));
 button(root,"재추첨").click();const dialog=yield*h.runNext();const form=root.querySelector<HTMLFormElement>("dialog form")!;
 const single=form.querySelector<HTMLInputElement>("[name=singleCount]")!,singleName=form.querySelector<HTMLInputElement>("[name=prizeName1]")!,mode=form.querySelector<HTMLSelectElement>("[name=prizeMode]")!;
 singleName.value="단일 유지";button(root,"인원 늘리기").click();expect(single.value).toBe("2");expect(button(root,"인원 늘리기").disabled).toBe(true);button(root,"인원 줄이기").click();expect(single.value).toBe("1");expect(button(root,"인원 줄이기").disabled).toBe(true);
 mode.value="multiple";mode.dispatchEvent(new Event("change"));expect(single.disabled).toBe(true);const many=form.querySelector<HTMLInputElement>("[name=manyPrizeName1]")!;many.value="여럿 유지";button(root,"품목 추가").click();expect(form.querySelectorAll("input[name^=manyPrizeName]")).toHaveLength(2);expect(button(root,"품목 추가").disabled).toBe(true);
 const removes=form.querySelectorAll<HTMLButtonElement>("[data-delete-prize]");removes[1]!.click();expect(form.querySelectorAll("input[name^=manyPrizeName]")).toHaveLength(1);expect(form.querySelector<HTMLButtonElement>("[data-delete-prize]")!.disabled).toBe(true);
 mode.value="single";mode.dispatchEvent(new Event("change"));expect(singleName.value).toBe("단일 유지");expect(many.disabled).toBe(true);expect(many.value).toBe("여럿 유지");form.querySelector<HTMLInputElement>("[name=manyPrizeCount1]")!.value="";singleName.value="";submit(form);yield*Fiber.join(yield*h.runNext());yield*Fiber.join(dialog);
 expect(h.state.commands[0]?.request.prizes).toEqual([{id:"rerun-prize-1",name:"",count:1}]);yield*view.close;
}));
it.effect("duplicate synthetic export during pending encoding cannot reopen admission or allocate another URL",()=>Effect.gen(function*(){
 const h=harness();const root=yield*host();let renders=0,cancelled=0;const renderer={renderPng:()=>Effect.callback<Blob>(()=>{renders++;return Effect.sync(()=>{cancelled++;});})};
 const view=yield*mountExportProduct(root,()=>({collection:collection(),round:round()}),h.dispatch,{savePng:()=>Effect.succeed("saved"),copyText:()=>Effect.void},renderer,{createObjectURL:()=>"blob:unused",revokeObjectURL:()=>{}}).pipe(Effect.provideService(DomPlatform,h.dom));
 const preview=button(root,"PNG 미리보기");preview.click();const first=yield*h.runNext();expect(preview.disabled).toBe(true);preview.dispatchEvent(new Event("click"));yield*Fiber.join(yield*h.runNext());expect(renders).toBe(1);expect(preview.disabled).toBe(true);expect(button(root,"PNG 사진 저장").disabled).toBe(true);
 yield*view.close;yield*Fiber.await(first);expect(cancelled).toBe(1);expect(h.state.listeners).toBe(0);
}));
it.effect("object URL allocation failure preserves confirmed result and reports safe sharing failure",()=>Effect.gen(function*(){
 const h=harness();const root=yield*host();const view=yield*mountExportProduct(root,()=>({collection:collection(),round:round()}),h.dispatch,undefined,{renderPng:()=>Effect.succeed(new Blob(["png"],{type:"image/png"}))},{createObjectURL:()=>{throw new Error("secret-object-url");},revokeObjectURL:()=>{}}).pipe(Effect.provideService(DomPlatform,h.dom));
 button(root,"PNG 미리보기").click();yield*Fiber.join(yield*h.runNext());expect(root.textContent).toContain("결과는 유지");
 expect(root.textContent).not.toContain("secret-object-url");expect(root.querySelector("img")?.getAttribute("src")).toBeNull();yield*view.close;
}));

it.effect("hidden scheduled screen stops polling and visible focus rechecks before one ticker restarts",()=>Effect.gen(function*(){const h=harness(collection({rounds:[round({state:"scheduled",mode:"reservation",scheduledAt:new Date(Date.parse(now)+20000).toISOString(),executedAt:null,winners:[]})]}));const root=yield*host();const mounted=yield*mountResultProduct(root,h.commands,{_tag:"result",collectionId:"collection",roundId:null},h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom));try{h.state.visibility="hidden";document.dispatchEvent(new Event("visibilitychange"));yield*Fiber.join(yield*h.runNext());const before=h.state.gets;yield*TestClock.adjust("5 seconds");expect(h.state.gets).toBe(before);h.state.visibility="visible";document.dispatchEvent(new Event("visibilitychange"));yield*Fiber.join(yield*h.runNext());for(let tick=0;tick<20;tick++)yield*Effect.yieldNow;expect(h.state.gets).toBe(before+1);yield*TestClock.adjust("1 second");expect(h.state.gets).toBe(before+2);window.dispatchEvent(new Event("focus"));yield*Fiber.join(yield*h.runNext());for(let tick=0;tick<20;tick++)yield*Effect.yieldNow;expect(h.state.gets).toBe(before+3);yield*TestClock.adjust("1 second");expect(h.state.gets).toBe(before+4);expect(h.state.commands).toHaveLength(0);}finally{yield*mounted.close;}const closed=h.state.gets;window.dispatchEvent(new Event("focus"));document.dispatchEvent(new Event("visibilitychange"));yield*TestClock.adjust("5 seconds");expect(h.state.gets).toBe(closed);expect(h.state.listeners).toBe(0);}));
it.effect("hide cancels resumed collection read and duplicate visible events cannot create two active tickers",()=>Effect.gen(function*(){const h=harness(collection({rounds:[round({state:"scheduled",mode:"reservation",scheduledAt:new Date(Date.parse(now)+1000).toISOString(),executedAt:null,winners:[]})]}));const root=yield*host();const mounted=yield*mountResultProduct(root,h.commands,{_tag:"result",collectionId:"collection",roundId:null},h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom));const released=yield*Deferred.make<ProductReply<Collection>>();let active=0,cancelled=0,requested=0;h.commands.getCollection=()=>Effect.acquireUseRelease(Effect.sync(()=>{active++;requested++;}),()=>Deferred.await(released),()=>Effect.sync(()=>{active--;cancelled++;}));try{window.dispatchEvent(new Event("focus"));yield*Fiber.join(yield*h.runNext());for(let tick=0;tick<20;tick++)yield*Effect.yieldNow;expect(active).toBe(1);window.dispatchEvent(new Event("focus"));document.dispatchEvent(new Event("visibilitychange"));yield*Fiber.join(yield*h.runNext());yield*Fiber.join(yield*h.runNext());expect(requested).toBe(1);h.state.visibility="hidden";document.dispatchEvent(new Event("visibilitychange"));yield*Fiber.join(yield*h.runNext());expect(active).toBe(0);expect(cancelled).toBe(1);yield*Deferred.succeed(released,reply(collection({article:{...collection().article,title:"폐기한 응답"}})));expect(root.textContent).not.toContain("폐기한 응답");yield*TestClock.adjust("5 seconds");expect(requested).toBe(1);expect(h.state.commands).toHaveLength(0);}finally{yield*mounted.close;}expect(h.state.listeners).toBe(0);}));
it.effect("initial hidden screen creates no polling timer and resume failure preserves known record without execution",()=>Effect.gen(function*(){const h=harness(collection({rounds:[round({state:"scheduled",mode:"reservation",scheduledAt:new Date(Date.parse(now)+1000).toISOString(),executedAt:null,winners:[]})]}));h.state.visibility="hidden";const root=yield*host();const mounted=yield*mountResultProduct(root,h.commands,{_tag:"result",collectionId:"collection",roundId:null},h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom));try{yield*TestClock.adjust("3 seconds");expect(h.state.gets).toBe(1);h.state.getFails=true;h.state.visibility="visible";document.dispatchEvent(new Event("visibilitychange"));yield*Fiber.join(yield*h.runNext());for(let tick=0;tick<20;tick++)yield*Effect.yieldNow;expect(h.state.gets).toBe(2);expect(root.textContent).toContain("마지막 확인된 기록입니다");expect(root.textContent).toContain("Go의 저장된 완료");expect(h.state.commands).toHaveLength(0);h.state.getFails=false;window.dispatchEvent(new Event("focus"));yield*Fiber.join(yield*h.runNext());for(let tick=0;tick<20;tick++)yield*Effect.yieldNow;expect(h.state.gets).toBe(3);}finally{yield*mounted.close;}expect(h.state.listeners).toBe(0);}));
it.effect("result confirmed time cut displays the selected inclusive KST minute instead of exclusive boundary",()=>Effect.gen(function*(){const h=harness(collection({filters:{...collection().filters,timeCut:"2026-10-06T03:31:00.000Z"}}));const root=yield*host();const mounted=yield*mountResultProduct(root,h.commands,{_tag:"result",collectionId:"collection",roundId:null},h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom));try{expect(root.textContent).toContain("시간컷");expect(root.textContent).toContain("12:30:00까지 (한국 시간)");expect(root.textContent).not.toContain("12:31:00");}finally{yield*mounted.close;}expect(h.state.listeners).toBe(0);}));
for(const kind of ["rerun","frozen"]as const)for(const ending of["cancel","close"]as const)it.effect("result "+kind+" "+ending+" commits dialog lock release before restoring disabled opener",()=>Effect.gen(function*(){
 const h=harness(collection());h.commands.queryFrozenParticipants=()=>Effect.succeed(reply({collectionId:"collection",revision:1,total:0,matched:0,offset:0,rows:[]}));
 const root=yield*host();const view=yield*mountResultProduct(root,h.commands,{_tag:"result",collectionId:"collection",roundId:null},h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom));const opener=button(root,kind==="rerun"?"재추첨":"확정 참가자 보기");opener.focus();const nativeFocus=opener.focus.bind(opener);let restores=0;opener.focus=()=>{if(!opener.disabled){restores++;nativeFocus();}};
 const disabledDescriptor=Object.getOwnPropertyDescriptor(HTMLButtonElement.prototype,"disabled")!;const disabled=vi.spyOn(opener,"disabled","set").mockImplementation(value=>{if(value)opener.blur();disabledDescriptor.set!.call(opener,value);});
 try{const baseline=h.state.listeners;opener.click();const fiber=yield*h.runNext();const dialog=yield*waitForNode(root,"dialog[open]");expect(opener.disabled).toBe(true);if(ending==="cancel")dialog.dispatchEvent(new Event("cancel",{cancelable:true}));else(dialog as HTMLDialogElement).close();yield*Fiber.join(fiber);expect(opener.disabled).toBe(false);expect(document.activeElement).toBe(opener);expect(restores).toBe(1);expect(root.querySelector("dialog")).toBeNull();expect(h.state.listeners).toBe(baseline);expect(h.state.commands).toHaveLength(0);}finally{disabled.mockRestore();yield*view.close;}expect(h.state.listeners).toBe(0);
}));

for(const state of ["completed","cancelled"] as const)it.effect("round rerun offers only backend-permitted modes after "+state,()=>Effect.gen(function*(){
 const target=round({state,mode:state==="cancelled"?"reservation":"immediate",executedAt:state==="cancelled"?null:now,winners:state==="cancelled"?[]:round().winners});
 const h=harness(collection({rounds:[target]}));const root=yield*host();const view=yield*mountResultProduct(root,h.commands,{_tag:"result",collectionId:"collection",roundId:null},h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom));
 try{button(root,"재추첨").click();const dialogFiber=yield*h.runNext();const dialog=yield*waitForNode(root,"dialog[open]");const select=dialog.querySelector<HTMLSelectElement>("[name=drawMode]")!;
  expect([...select.options].map(option=>option.value)).toEqual(state==="completed"?["immediate"]:["immediate","reservation"]);
  select.value=state==="completed"?"immediate":"reservation";submit(dialog.querySelector("form")!);yield*Fiber.join(yield*h.runNext());yield*Fiber.join(dialogFiber);
  expect(h.state.commands).toHaveLength(1);expect(h.state.commands[0]?.request.mode).toBe(state==="completed"?"immediate":"reservation");expect(h.state.commands[0]?.request.expectedVersion).toBe(target.roundVersion);
 }finally{yield*view.close;}expect(h.state.listeners).toBe(0);
}));
it.effect("round rerun rejects forged reservation after completed and disabled submit before IPC",()=>Effect.gen(function*(){
 const h=harness(collection({}));const root=yield*host();const view=yield*mountResultProduct(root,h.commands,{_tag:"result",collectionId:"collection",roundId:null},h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom));
 try{button(root,"재추첨").click();const dialogFiber=yield*h.runNext();const dialog=yield*waitForNode(root,"dialog[open]");const form=dialog.querySelector("form")!;const select=dialog.querySelector<HTMLSelectElement>("[name=drawMode]")!;const submitButton=button(root,"새 회차 추첨");
  submitButton.disabled=true;submit(form);expect(h.effects).toHaveLength(0);expect(h.state.commands).toHaveLength(0);submitButton.disabled=false;
  const forged=document.createElement("option");forged.value="reservation";forged.disabled=true;select.append(forged);select.value="reservation";submit(form);
  expect(h.effects).toHaveLength(0);expect(h.state.commands).toHaveLength(0);expect(dialog.querySelector<HTMLElement>("[role=alert]")?.hidden).toBe(false);
  button(root,"취소").click();yield*Fiber.join(dialogFiber);expect(root.querySelector("dialog")).toBeNull();
 }finally{yield*view.close;}expect(h.state.listeners).toBe(0);
}));
for(const state of ["scheduled","completed"] as const)it.effect("result "+state+" retains local exit policy without collection-list capability",()=>Effect.gen(function*(){
 const target=state==="scheduled"?round({state,mode:"reservation",scheduledAt:new Date(Date.parse(now)+30000).toISOString(),executedAt:null,winners:[]}):round();
 const h=harness(collection({rounds:[target]}));const root=yield*host();const mounted=yield*mountResultProduct(root,h.commands,{_tag:"result",collectionId:"collection",roundId:null},h.dispatch).pipe(Effect.provideService(DomPlatform,h.dom));
 try{expect(root.textContent).toContain("앱 종료·절전 중에는 실행되지 않으며 다음 시작·복귀 시 만료 예약을 지연 실행합니다.");expect(root.textContent).not.toContain("전체 예정 예약");expect(h.state.gets).toBe(1);expect(h.state.commands).toHaveLength(0);expect(button(root,"재추첨").disabled).toBe(state==="scheduled");if(state==="scheduled")expect(button(root,"예약 취소").disabled).toBe(false);}
 finally{yield*mounted.close;}expect(h.state.listeners).toBe(0);
}));
