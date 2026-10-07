import { expect, it } from "@effect/vitest";
import { Effect, Fiber } from "effect";
import { TestClock } from "effect/testing";
import { makeResultImage, resultExportText, resultImageForDocument, wrapResultText, RESULT_PNG_LIMIT, type ResultExportModel } from "../../src/platform/canvas";

function model(change: Partial<ResultExportModel> = {}): ResultExportModel {
  return { collectionId:"collection",roundId:"round",roundNumber:1,title:"한글 제목😀",galleryName:"갤러리",articleUrl:"https://gall.dcinside.com/board/view/?id=test&no=1",participantCount:3,
    prizes:[{prizeId:"prize",name:"선물",winners:[{participantId:"participant",nickname:"참가자😀",publicIdentifier:"uid",participantKind:"registered"}]}],
    message:"메시지",executedAt:"2026-10-06T01:00:00.000Z",scheduledAt:null,delayed:false,...change };
}
function canvasHost(options: { width?:number; mode?:string; blob?:Blob; fonts?:()=>Promise<unknown> } = {}) {
  const state = { created:0, text:[] as string[], callback:undefined as BlobCallback | undefined, draws:0,
    labels:[] as Array<{text:string;x:number;y:number;font:string;color:string}>, cards:[] as Array<{x:number;y:number;width:number;height:number;radius:number;fill:string;stroke:string}>, encoded:[] as Array<{width:number;height:number}> };
  const context = { font:"",fillStyle:"",textBaseline:"",measureText:(text:string)=>({width:[...text].length*12}),
    strokeStyle:"",lineWidth:0,beginPath:()=>{},roundRect:(x:number,y:number,width:number,height:number,radius:number)=>{state.cards.push({x,y,width,height,radius,fill:"",stroke:""});},
    fill:()=>{state.cards.at(-1)!.fill=String(context.fillStyle);},stroke:()=>{state.cards.at(-1)!.stroke=String(context.strokeStyle);},
    fillRect:()=>{state.draws++;},fillText:(text:string,x:number,y:number)=>{state.text.push(text);state.labels.push({text,x,y,font:context.font,color:String(context.fillStyle)});},
  } as unknown as CanvasRenderingContext2D;
  const canvas = { width:0,height:0,getContext:()=> options.mode==="context"?null:context,
    toBlob:(callback:BlobCallback)=>{
      state.encoded.push({width:canvas.width,height:canvas.height});
      state.callback=callback;
      if(options.mode==="throw")throw new Error("secret-canvas-error");
      if(options.mode==="late")return;
      callback(options.mode==="null"?null:options.blob??new Blob(["png"],{type:"image/png"}));
    },
  } as unknown as HTMLCanvasElement;
  const renderer=makeResultImage({createCanvas:()=>{state.created++;return canvas;},fontsReady:options.fonts??(()=>Promise.resolve()),...(options.width===undefined?{}:{width:options.width})});
  return {state,canvas,context,renderer};
}
it.effect("Canvas draws complete fixed result metadata and releases pixel references",()=>Effect.gen(function*(){
  const h=canvasHost();const blob=yield* h.renderer.renderPng(model({scheduledAt:"2026-10-06T00:59:00Z",delayed:true}));
  expect(blob.type).toBe("image/png");expect(h.state.draws).toBe(1);expect(h.canvas.width).toBe(0);expect(h.canvas.height).toBe(0);
  const text=h.state.text.join("\n");for(const expected of ["한글 제목😀","1회차 추첨 결과","갤러리","게시글 #1","3명","선물","참가자😀","uid","메시지","예정","실행","지연 실행"])expect(text).toContain(expected);
  expect(text).not.toContain("collection");expect(text).not.toContain("round");
  expect(h.state.text[0]).toBe("한글 제목😀");expect(text).not.toContain("Jackpot 로컬 추첨 결과");
}));
it("grapheme wrapping preserves Korean emoji and combining characters without splitting",()=>{
  const h=canvasHost();const input="한글👨‍👩‍👧‍👦é👍🏽한글";
  const lines=wrapResultText(h.context,input,160);
  expect(lines.join("")).toBe(input);expect(lines.some((line)=>line.includes("👨‍👩‍👧‍👦"))).toBe(true);expect(lines.some((line)=>line.includes("é"))).toBe(true);
  expect(()=>wrapResultText(h.context,"x",1)).toThrow();expect(()=>wrapResultText(h.context,"x",0)).toThrow();
});
it("text sharing starts with the article title without generator branding and preserves public results",()=>{
  const exported=resultExportText(model());expect(exported.split("\n")[0]).toBe("한글 제목😀");expect(exported).not.toContain("Jackpot 로컬 추첨 결과");expect(exported).toContain("https://gall.dcinside.com");expect(exported).toContain("회차: 1");expect(exported).toContain("품목: 선물");expect(exported).toContain("- 참가자😀 (uid)");expect(exported).not.toContain("password");expect(exported).not.toContain("DB");
});
it("removing generator branding preserves user supplied Jackpot names and messages",()=>{
  const input=model({title:"Jackpot 이벤트",message:"로컬 추첨기 당첨 축하",prizes:[{prizeId:"prize",name:"Jackpot 상품",winners:[{participantId:"participant",nickname:"Jackpot 참가자😀",publicIdentifier:"jackpot-user",participantKind:"registered"}]}]});
  const before=JSON.stringify(input);const exported=resultExportText(input);
  expect(exported.startsWith("Jackpot 이벤트\n")).toBe(true);expect(exported).toContain("품목: Jackpot 상품");expect(exported).toContain("- Jackpot 참가자😀 (jackpot-user)");expect(exported).toContain("관리 메시지: 로컬 추첨기 당첨 축하");expect(JSON.stringify(input)).toBe(before);
});
for(const mode of ["context","null","throw"]){
 it.effect("Canvas "+mode+" fails without retaining pixels",()=>Effect.gen(function*(){const h=canvasHost({mode});const result=yield* Effect.result(h.renderer.renderPng(model()));expect(result._tag).toBe("Failure");expect(h.canvas.width).toBe(0);expect(h.canvas.height).toBe(0);}));
}
for(const [name,blob]of [["empty",new Blob([],{type:"image/png"})],["mime",new Blob(["x"],{type:"text/plain"})],["over",new Blob([new Uint8Array(RESULT_PNG_LIMIT+1)],{type:"image/png"})]]as const){
 it.effect("PNG "+name+" encoding fails explicitly",()=>Effect.gen(function*(){const h=canvasHost({blob});const result=yield* Effect.result(h.renderer.renderPng(model()));expect(result._tag).toBe("Failure");if(result._tag==="Failure")expect(result.failure.reason).toBe(name==="over"?"size":"encoding");expect(h.canvas.width).toBe(0);}));
}
it.effect("PNG exact25MiB succeeds at the declared boundary",()=>Effect.gen(function*(){const h=canvasHost({blob:new Blob([new Uint8Array(RESULT_PNG_LIMIT)],{type:"image/png"})});const result=yield* h.renderer.renderPng(model());expect(result.size).toBe(RESULT_PNG_LIMIT);expect(h.canvas.width).toBe(0);}));
for(const width of [0,255,2049,NaN]){
 it.effect("Canvas invalid width "+width+" is rejected",()=>Effect.gen(function*(){const h=canvasHost({width});const result=yield* Effect.result(h.renderer.renderPng(model()));expect(result._tag).toBe("Failure");if(result._tag==="Failure")expect(result.failure.reason).toBe("dimensions");expect(h.canvas.width).toBe(0);}));
}
it.effect("Canvas long result exceeds height without saving a cut image",()=>Effect.gen(function*(){const h=canvasHost();const result=yield* Effect.result(h.renderer.renderPng(model({title:"한".repeat(65536)})));expect(result._tag).toBe("Failure");if(result._tag==="Failure")expect(result.failure.reason).toBe("dimensions");expect(h.canvas.width).toBe(0);expect(h.state.callback).toBeUndefined();}));
it.effect("font failure allocates no Canvas",()=>Effect.gen(function*(){const h=canvasHost({fonts:()=>Promise.reject(new Error("secret-font"))});const result=yield* Effect.result(h.renderer.renderPng(model()));expect(result._tag).toBe("Failure");if(result._tag==="Failure")expect(result.failure.reason).toBe("font");expect(h.state.created).toBe(0);}));
it.effect("font timeout uses TestClock and allocates no Canvas",()=>Effect.gen(function*(){const h=canvasHost({fonts:()=>new Promise(()=>{})});const fiber=yield* h.renderer.renderPng(model()).pipe(Effect.result,Effect.forkChild);yield* TestClock.adjust("5 seconds");const result=yield* Fiber.join(fiber);expect(result._tag).toBe("Failure");if(result._tag==="Failure")expect(result.failure.reason).toBe("font");expect(h.state.created).toBe(0);}));
it.effect("late PNG callback is ignored after cancellation and Canvas is released",()=>Effect.gen(function*(){const h=canvasHost({mode:"late"});const fiber=yield* h.renderer.renderPng(model()).pipe(Effect.forkChild);yield* Effect.yieldNow;yield* Effect.yieldNow;expect(h.state.callback).toBeDefined();yield* Fiber.interrupt(fiber);expect(h.canvas.width).toBe(0);h.state.callback?.(new Blob(["late"],{type:"image/png"}));expect(h.canvas.height).toBe(0);}));
it.effect("toBlob timeout uses TestClock and releases Canvas",()=>Effect.gen(function*(){const h=canvasHost({mode:"late"});const fiber=yield* h.renderer.renderPng(model()).pipe(Effect.result,Effect.forkChild);yield* TestClock.adjust("15 seconds");const result=yield* Fiber.join(fiber);expect(result._tag).toBe("Failure");expect(h.canvas.width).toBe(0);}));
for(const change of [{articleUrl:"javascript:unsafe"},{articleUrl:"https://x@gall.dcinside.com/test/1"},{participantCount:-1},{roundNumber:0},{prizes:[]},{executedAt:"bad"},{title:"x".repeat(65537)}]as Array<Partial<ResultExportModel>>){
 it.effect("invalid export model fails before font/canvas acquisition",()=>Effect.gen(function*(){const h=canvasHost();const result=yield* Effect.result(h.renderer.renderPng(model(change)));expect(result._tag).toBe("Failure");expect(h.state.created).toBe(0);}));
}

it("plain text export uses real newlines and wrapping honors source newlines",()=>{
 const text=resultExportText(model({title:"제목1\n제목2"}));expect(text.split("\n")[0]).toBe("제목1");expect(text).not.toContain("\\n");expect(text.split("\n")).toContain("제목2");
 const h=canvasHost();expect(wrapResultText(h.context,"첫째\n둘째\n",1000)).toEqual(["첫째","둘째",""]);
});

it.effect("valid line count can still exceed16million pixels at2048width",()=>Effect.gen(function*(){
 const h=canvasHost({width:2048});const input=model({title:"가".repeat(164*166)});
 const title=wrapResultText(h.context,input.title,2048-70);expect(title).toHaveLength(166);
 // Fixed spacing/metadata remain below the height and line limits, but this
 // full-width result cannot be encoded within the pixel cap.
 const expectedHeight=556+title.length*44;expect(expectedHeight).toBeLessThanOrEqual(8192);expect(2048*expectedHeight).toBeGreaterThan(16000000);
 const result=yield*Effect.result(h.renderer.renderPng(input));expect(result._tag).toBe("Failure");if(result._tag==="Failure")expect(result.failure.reason).toBe("dimensions");expect(h.canvas.width).toBe(0);expect(h.state.callback).toBeUndefined();
}));
it("invalid font measurements and widths stop grapheme wrapping without unbounded text",()=>{
 const h=canvasHost();for(const width of [NaN,Infinity,-1])expect(()=>wrapResultText(h.context,"a",width)).toThrow();
 for(const measured of [NaN,-1,Infinity]){h.context.measureText=()=>({width:measured})as TextMetrics;expect(()=>wrapResultText(h.context,"a",100)).toThrow();}
});
it.effect("Canvas creation draw and encoder exceptions project safely and cleanup acquired pixels",()=>Effect.gen(function*(){
 const h=canvasHost();h.context.fillRect=()=>{throw new Error("draw-secret");};const result=yield*Effect.result(h.renderer.renderPng(model()));expect(result._tag).toBe("Failure");expect(h.canvas.width).toBe(0);expect(h.canvas.height).toBe(0);
 const broken=makeResultImage({createCanvas:()=>{throw new Error("create-secret");},fontsReady:()=>Promise.resolve()});const created=yield*Effect.result(broken.renderPng(model()));expect(created._tag).toBe("Failure");if(created._tag==="Failure")expect(created.failure.reason).toBe("encoding");
}));
for(const change of [
 {prizes:[{prizeId:"bad id",name:"",winners:[{participantId:"p",nickname:"n",publicIdentifier:"",participantKind:"anonymous"as const}]}]},
 {prizes:[{prizeId:"p",name:"x".repeat(21),winners:[{participantId:"p",nickname:"n",publicIdentifier:"",participantKind:"anonymous"as const}]}]},
 {prizes:[{prizeId:"p",name:"",winners:[]}]},
 {prizes:[{prizeId:"p",name:"",winners:[{participantId:"p",nickname:"",publicIdentifier:"",participantKind:"anonymous"as const}]}]},
 {prizes:[{prizeId:"p",name:"",winners:[{participantId:"p",nickname:"n",publicIdentifier:"x".repeat(4097),participantKind:"anonymous"as const}]}]},
 {prizes:[{prizeId:"p",name:"",winners:[{participantId:"p",nickname:"n",publicIdentifier:"",participantKind:"anonymous"as const},{participantId:"p",nickname:"n",publicIdentifier:"",participantKind:"anonymous"as const}]}]},
 {articleUrl:"https://gall.dcinside.com:8443/board/view/?id=test&no=1"},
 {scheduledAt:"bad"},
]as Array<Partial<ResultExportModel>>){
 it.effect("invalid prize identity nickname data or article port fails before Canvas acquisition",()=>Effect.gen(function*(){const h=canvasHost();const result=yield*Effect.result(h.renderer.renderPng(model(change)));expect(result._tag).toBe("Failure");expect(h.state.created).toBe(0);}));
}

it("wrapping measures fitting candidates once and only remeasures a new wrapped fragment",()=>{
 const calls:string[]=[];const context={measureText:(value:string)=>{calls.push(value);return{width:value.length};}}as unknown as CanvasRenderingContext2D;
 expect(wrapResultText(context,"ABCD",2)).toEqual(["AB","CD"]);
 expect(calls).toEqual(["A","AB","ABC","C","CD"]);
});
it("fitting shaped pairs use their measured width rather than summing isolated graphemes",()=>{
 const calls:string[]=[];const context={measureText:(value:string)=>{calls.push(value);return{width:value==="B"?2:1};}}as unknown as CanvasRenderingContext2D;
 expect(wrapResultText(context,"AB",1)).toEqual(["AB"]);
 expect(calls).toEqual(["A","AB"]);
});
for(const fragment of [NaN,-1,Infinity,2])it("new wrapped fragment invalid width "+fragment+" fails explicitly",()=>{
 const context={measureText:(value:string)=>({width:value==="A"?1:value==="AB"?2:fragment})}as unknown as CanvasRenderingContext2D;
 try{wrapResultText(context,"AB",1);throw new Error("expected rejection");}catch(error){expect(error).toHaveProperty("reason",fragment===2?"dimensions":"font");}
});
it("empty paragraphs need no font measurement and preserve exact224line bound",()=>{
 let calls=0;const context={measureText:()=>{calls++;return{width:0};}}as unknown as CanvasRenderingContext2D;
 expect(wrapResultText(context,"",1)).toEqual([""]);
 expect(wrapResultText(context,"\n".repeat(223),1)).toHaveLength(224);
 expect(calls).toBe(0);expect(()=>wrapResultText(context,"\n".repeat(224),1)).toThrow();
});
for(const failureAt of [1,3,-1])it.effect("first Nth continuous font measurement failure "+failureAt+" releases Canvas without encoding",()=>Effect.gen(function*(){
 const h=canvasHost();let calls=0;h.context.measureText=(value:string)=>{calls++;if(failureAt===-1||calls===failureAt)throw new Error("font-measure-secret");return{width:value.length}as TextMetrics;};
 const result=yield*Effect.result(h.renderer.renderPng(model()));expect(result._tag).toBe("Failure");if(result._tag==="Failure")expect(result.failure.reason).toBe("encoding");
 expect(h.canvas.width).toBe(0);expect(h.canvas.height).toBe(0);expect(h.state.callback).toBeUndefined();
}));
it.effect("reference layout pairs purple prize badges with winner identities and keeps metadata outside the result card",()=>Effect.gen(function*(){
 const input=model();const before=JSON.stringify(input),copied=resultExportText(input);const h=canvasHost();yield*h.renderer.renderPng(input);
 expect(h.state.cards).toHaveLength(2);const [result,badge]=h.state.cards;
 expect(result).toMatchObject({x:35,width:1130,fill:"#ffffff",stroke:"#806ab9",radius:22});expect(badge).toMatchObject({x:70,width:280,fill:"#eeedf8",stroke:"",radius:17});
 const prize=h.state.labels.find(label=>label.text==="선물")!,winner=h.state.labels.find(label=>label.text==="참가자😀 (uid)")!;
 expect(prize).toMatchObject({x:82,color:"#806ab9"});expect(winner).toMatchObject({x:376,color:"#1c1e2d",y:prize.y});
 expect(winner.font).toContain('400 31px "Jackpot Result"');expect(h.state.labels[0]?.font).toContain('700 35px "Jackpot Result"');
 const participants=h.state.labels.find(label=>label.text.includes("참가자 3명"))!;expect(participants.y).toBeGreaterThan(result!.y+result!.height);
 for(const text of h.state.labels){expect(text.x).toBeGreaterThanOrEqual(35);expect(text.y+44).toBeLessThanOrEqual(h.state.encoded[0]!.height);}
 expect(h.state.encoded[0]?.width).toBe(1200);expect(resultExportText(input)).toBe(copied);expect(JSON.stringify(input)).toBe(before);
}));
it.effect("multiple prize cards preserve prize order winner association rank and unnamed fallbacks",()=>Effect.gen(function*(){
 const prizes=Array.from({length:10},(_,index)=>({prizeId:"prize"+index,name:index===0?"":"품목"+index,winners:[{participantId:"person"+index,nickname:"당첨자"+index,publicIdentifier:"id"+index,participantKind:"registered"as const}]}));
 const h=canvasHost();yield*h.renderer.renderPng(model({prizes,message:""}));expect(h.state.cards).toHaveLength(11);
 for(const[index,prize]of prizes.entries()){const label=h.state.labels.find(text=>text.text===(index===0?"상품 A":prize.name))!;const winner=h.state.labels.find(text=>text.text===`당첨자${index} (id${index})`)!;expect(winner.y).toBe(label.y);if(index>0)expect(label.y).toBeGreaterThan(h.state.labels.find(text=>text.text===(index===1?"상품 A":"품목"+(index-1)))!.y);}
 expect(h.state.text.join("\n")).toContain("당첨자 10명");expect(h.state.text.join("\n")).not.toContain("관리 메시지");
 const ranks=canvasHost();yield*ranks.renderer.renderPng(model({prizes:[{prizeId:"p",name:"",winners:prizes.slice(0,2).flatMap(prize=>prize.winners)}]}));expect(ranks.state.text.filter(text=>text==="기본 상품")).toHaveLength(2);expect(ranks.state.text).toContain("1. 당첨자0 (id0)");expect(ranks.state.text).toContain("2. 당첨자1 (id1)");expect(ranks.state.text.join("\n")).not.toContain("번째");
}));
for(const width of [256,2048])it.effect("reference layout accepts declared width boundary "+width+" without clipping winners",()=>Effect.gen(function*(){
 const h=canvasHost({width});yield*h.renderer.renderPng(model({title:"제목",galleryName:"갤러리"}));expect(h.state.encoded[0]?.width).toBe(width);
 for(const card of h.state.cards){expect(card.x+card.width).toBeLessThanOrEqual(width);expect(card.y+card.height).toBeLessThanOrEqual(h.state.encoded[0]!.height);}
 const prize=h.state.labels.find(text=>text.text==="선물")!;
 if(width===256){const badge=h.state.cards[1]!;expect(h.state.labels.some(text=>text.x===70&&text.y>badge.y+badge.height)).toBe(true);}else expect(h.state.labels.find(text=>text.text==="참가자😀 (uid)")?.x).toBe(376);
 expect(prize).toBeDefined();expect(h.canvas.width*h.canvas.height).toBe(0);
}));
it.effect("combined structured blocks enforce224lines even when each source block fits individually",()=>Effect.gen(function*(){
 const h=canvasHost();const winners=Array.from({length:6},(_,index)=>({participantId:"person"+index,nickname:"\n".repeat(20),publicIdentifier:"",participantKind:"anonymous"as const}));
 // Parallel badge/identity columns exceed the total text budget while their
 // physical row heights still fit the height and pixel caps.
 const result=yield*Effect.result(h.renderer.renderPng(model({prizes:[{prizeId:"p",name:"\n".repeat(20),winners}]})));
 expect(result._tag).toBe("Failure");if(result._tag==="Failure")expect(result.failure.reason).toBe("dimensions");expect(h.state.encoded).toHaveLength(0);expect(h.canvas.width*h.canvas.height).toBe(0);
}));
it.effect("structured height8192 guard rejects fitting text count before the pixel cap",()=>Effect.gen(function*(){
 const h=canvasHost();const result=yield*Effect.result(h.renderer.renderPng(model({title:"\n".repeat(174)})));
 expect(175+8).toBeLessThan(224);expect(556+175*44).toBeGreaterThan(8192);expect(1200*(556+175*44)).toBeLessThan(16000000);
 expect(result._tag).toBe("Failure");if(result._tag==="Failure")expect(result.failure.reason).toBe("dimensions");expect(h.state.encoded).toHaveLength(0);expect(h.canvas.width*h.canvas.height).toBe(0);
}));
for(const method of ["roundRect","fillText"]as const)for(const at of [1,3,-1])it.effect("structured drawing "+method+" first Nth continuous failure "+at+" does not encode or retain pixels",()=>Effect.gen(function*(){
 const h=canvasHost();let calls=0;const original=h.context[method].bind(h.context);Object.assign(h.context,{[method]:(...args:unknown[])=>{calls++;if(at===-1||calls===at)throw new Error("drawing-private");return Reflect.apply(original,h.context,args);}});
 const prizes=Array.from({length:2},(_,index)=>({prizeId:"prize"+index,name:"선물",winners:[{participantId:"person"+index,nickname:"참가자",publicIdentifier:"uid",participantKind:"registered"as const}]}));
 const result=yield*Effect.result(h.renderer.renderPng(model({prizes})));expect(result._tag).toBe("Failure");if(result._tag==="Failure")expect(result.failure.reason).toBe("encoding");expect(calls).toBe(at===-1?1:at);expect(h.state.encoded).toHaveLength(0);expect(h.canvas.width*h.canvas.height).toBe(0);
}));
it.effect("document renderer loads all three local font weights before creating Canvas",()=>Effect.gen(function*(){
 const h=canvasHost(),loads:string[]=[];const doc={fonts:{ready:Promise.resolve(),load:(font:string)=>{loads.push(font);return Promise.resolve([]);}},createElement:()=>{expect(loads).toHaveLength(3);return h.canvas;}}as unknown as Document;
 yield*resultImageForDocument(doc).renderPng(model());expect(loads.map(font=>font.split(" ")[0])).toEqual(["400","600","700"]);expect(loads.every(font=>font.includes('"Jackpot Result"'))).toBe(true);expect(h.canvas.width*h.canvas.height).toBe(0);
}));
