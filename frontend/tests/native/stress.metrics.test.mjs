import {test} from "node:test";
import assert from "node:assert/strict";
import {percentile95,pngDimensions,watchRoundCommit,finalizePeak} from "./stress.metrics.mjs";
import {localFilterTargets} from "./stress.metrics.mjs";
test("p95 uses nearest rank without mutating samples",()=>{const values=Array.from({length:20},(_,i)=>20-i);assert.equal(percentile95(values),19);assert.equal(values[0],20);assert.equal(percentile95([0]),0)});
test("p95 rejects empty or unsafe timing values",()=>{for(const value of [[],[-1],[NaN],[Infinity]])assert.throws(()=>percentile95(value))});
const header=(width,height)=>{const bytes=Buffer.alloc(24);Buffer.from("89504e470d0a1a0a","hex").copy(bytes);bytes.write("IHDR",12);bytes.writeUInt32BE(width,16);bytes.writeUInt32BE(height,20);return bytes};
test("PNG width and exact height boundaries are enforced",()=>{assert.deepEqual(pngDimensions(header(1200,1)),{width:1200,height:1});assert.deepEqual(pngDimensions(header(1200,8192)),{width:1200,height:8192});for(const bytes of [Buffer.alloc(0),header(1199,1),header(1200,0),header(1200,8193)])assert.throws(()=>pngDimensions(bytes));const bad=header(1200,1);bad[0]=0;assert.throws(()=>pngDimensions(bad));const type=header(1200,1);type.write("IDAT",12);assert.throws(()=>pngDimensions(type))});


function roundEnvironment(){
 const model={now:0,options:[{value:"old",textContent:"1회 완료"}],selected:"old",rows:10,listeners:new Map(),timer:undefined,observer:undefined,disconnects:0,clears:0};
 const screen={querySelectorAll:query=>query.includes(" option")?model.options:Array.from({length:model.rows}),querySelector:()=>({value:model.selected})};
 const environment={window:{},document:{querySelector:()=>screen,addEventListener:(type,callback)=>model.listeners.set(type,callback),removeEventListener:type=>model.listeners.delete(type)},performance:{now:()=>model.now},MutationObserver:class{constructor(callback){model.observer=callback}observe(){}disconnect(){model.disconnects++}},setTimeout:callback=>{model.timer=callback;return 1},clearTimeout:()=>model.clears++};
 return {model,environment};
}
test("round timing records first exact completed DOM and excludes assertion waits",async()=>{
 const {model,environment}=roundEnvironment();watchRoundCommit({optionCount:2,prizeCount:10},environment);
 const result=environment.window.__jackpotRoundTimer.promise;
 model.now=10;model.listeners.get("click")({target:{closest:()=>({textContent:"새 회차 추첨"})}});
 model.now=20;model.options.push({value:"new",textContent:"2회 추첨 중"});model.selected="new";model.observer();assert.equal(model.disconnects,0);
 model.now=30;model.options[1].textContent="2회 완료";model.rows=9;model.observer();assert.equal(model.disconnects,0);
 model.now=40;model.rows=10;model.selected="old";model.observer();assert.equal(model.disconnects,0);
 model.now=50;model.selected="new";model.observer();model.now=999;
 assert.deepEqual(await result,{ok:true,elapsedMs:40});assert.equal(model.disconnects,1);assert.equal(model.clears,1);assert.equal(model.listeners.size,0);
 model.observer();environment.window.__jackpotRoundTimer.dispose();assert.equal(model.disconnects,1);
});
test("round timing timeout and cancellation both disconnect and resolve failure",async()=>{
 for(const reason of ["timeout","cancel"]){const {model,environment}=roundEnvironment();watchRoundCommit({optionCount:2,prizeCount:10},environment);const timer=environment.window.__jackpotRoundTimer;model.now=25;if(reason==="timeout")model.timer();else timer.dispose();assert.deepEqual(await timer.promise,{ok:false,elapsedMs:25});assert.equal(model.disconnects,1);assert.equal(model.clears,1);assert.equal(model.listeners.size,0)}
});
test("round timing rejects invalid predicates or foreign owner before registering",()=>{
 for(const input of [{optionCount:1,prizeCount:10},{optionCount:NaN,prizeCount:10},{optionCount:2,prizeCount:0},{optionCount:2,prizeCount:11},{optionCount:2,prizeCount:10,timeoutMs:0},{optionCount:2,prizeCount:10,timeoutMs:Infinity}]){const {model,environment}=roundEnvironment();assert.throws(()=>watchRoundCommit(input,environment));assert.equal(model.listeners.size,0)}
 const absent=roundEnvironment();absent.environment.document.querySelector=()=>null;assert.throws(()=>watchRoundCommit({optionCount:2,prizeCount:10},absent.environment));
 const owned=roundEnvironment();owned.environment.window.__jackpotRoundTimer={};assert.throws(()=>watchRoundCommit({optionCount:2,prizeCount:10},owned.environment));
});


test("browser timers use the Window receiver and failures release partial observers",async()=>{
 const bound=roundEnvironment();
 bound.environment.setTimeout=function(callback){bound.model.timerThis=this;bound.model.timer=callback;return 1};
 bound.environment.clearTimeout=function(){bound.model.clearThis=this;bound.model.clears++};
 watchRoundCommit({optionCount:2,prizeCount:10},bound.environment);assert.equal(bound.model.timerThis===bound.environment.window,true);bound.model.timer();await bound.environment.window.__jackpotRoundTimer.promise;assert.equal(bound.model.clearThis===bound.environment.window,true);assert.equal(bound.model.clears,1);
 for(const failure of ["listener","observe","timer"]){const {model,environment}=roundEnvironment();
 if(failure==="listener")environment.document.addEventListener=(type,callback)=>{model.listeners.set(type,callback);throw new Error("listener failed")};
 if(failure==="observe")environment.MutationObserver=class{constructor(callback){model.observer=callback}observe(){throw new Error("observe failed")}disconnect(){model.disconnects++}};
 if(failure==="timer")environment.setTimeout=()=>{throw new Error("timer failed")};
 assert.throws(()=>watchRoundCommit({optionCount:2,prizeCount:10},environment));assert.equal(model.listeners.size,0);assert.equal(model.disconnects,1);assert.equal(environment.window.__jackpotRoundTimer,undefined);
 }
});

test("round timing requires the first submit click and duplicate clicks cannot shorten latency",async()=>{
 const {model,environment}=roundEnvironment();watchRoundCommit({optionCount:2,prizeCount:10},environment);
 const owner=environment.window.__jackpotRoundTimer;
 model.options.push({value:"new",textContent:"2회 완료"});model.selected="new";
 model.now=2;model.observer();assert.equal(model.disconnects,0);
 model.now=3;model.listeners.get("click")({target:{closest:()=>({textContent:"다른 동작"})}});model.observer();assert.equal(model.disconnects,0);
 model.now=5;model.listeners.get("click")({target:{closest:()=>({textContent:"새 회차 추첨"})}});
 model.now=30;model.listeners.get("click")({target:{closest:()=>({textContent:"새 회차 추첨"})}});
 model.now=50;model.observer();assert.deepEqual(await owner.promise,{ok:true,elapsedMs:45});assert.equal(model.disconnects,1);
});
test("missing select and wrong option count cannot satisfy a completed round",async()=>{
 const {model,environment}=roundEnvironment();watchRoundCommit({optionCount:2,prizeCount:10},environment);
 model.listeners.get("click")({target:{closest:()=>({textContent:"새 회차 추첨"})}});
 model.options.push({value:"new",textContent:"2회 완료"});model.selected="new";model.options.push({value:"unexpected",textContent:"3회 완료"});
 model.selected="unexpected";model.observer();assert.equal(model.disconnects,0);model.options.pop();model.selected="new";
 const screen=environment.document.querySelector();screen.querySelector=()=>null;model.observer();assert.equal(model.disconnects,0);
 screen.querySelector=()=>({value:model.selected});model.now=10;model.observer();assert.equal((await environment.window.__jackpotRoundTimer.promise).ok,true);
});
test("synchronous injected timeout still releases its later assigned timer",async()=>{
 const {model,environment}=roundEnvironment();environment.setTimeout=callback=>{callback();return 1};
 watchRoundCommit({optionCount:2,prizeCount:10},environment);
 assert.deepEqual(await environment.window.__jackpotRoundTimer.promise,{ok:false,elapsedMs:0});assert.equal(model.clears,1);assert.equal(model.disconnects,1);
});

test("late sampler peak supersedes an earlier threshold decision and is idempotent",()=>{
 const report={workingSetPeakBytes:499*1048576,targets:{peakUnder500MiB:true},samples:[]};finalizePeak(report);assert.equal(report.peakWorkingSetMiB,499);
 report.workingSetPeakBytes=501*1048576;finalizePeak(report);assert.equal(report.peakWorkingSetMiB,501);assert.equal(report.targets.peakUnder500MiB,false);const before=JSON.stringify(report);finalizePeak(report);assert.equal(JSON.stringify(report),before);assert.deepEqual(report.samples,[]);
});
test("final peak keeps exact 500MiB boundary and rejects independent diagnostic conditions",()=>{
 for(const bytes of [0,500*1048576,500*1048576+1]){const report={workingSetPeakBytes:bytes,targets:{}};finalizePeak(report);assert.equal(report.targets.peakUnder500MiB,bytes<=500*1048576);}
 for(const flag of ["traceDiagnostic","readbackDiagnostic","dataPreviewDiagnostic","noImagePreviewDiagnostic","goMemoryLimitDiagnostic"]){const report={workingSetPeakBytes:0,[flag]:true,targets:{}};finalizePeak(report);assert.equal(report.targets.peakUnder500MiB,false);}
 const missingTargets={workingSetPeakBytes:0};finalizePeak(missingTargets);assert.equal(missingTargets.targets,undefined);assert.equal(missingTargets.peakWorkingSetMiB,0);
 for(const invalid of [-1,NaN,Infinity,undefined,"1"])assert.throws(()=>finalizePeak({workingSetPeakBytes:invalid}));
});

test("local memory goal preserves measured bytes and accepts only baseline at the exact 768MiB boundary",()=>{
 for(const bytes of [0,660.3984375*1048576,768*1048576,768*1048576+1]){
  const report={workingSetPeakBytes:bytes,targets:{},samples:[{total:bytes}]};
  finalizePeak(report);
  assert.equal(report.targets.memoryLimitMiB,768);
  assert.equal(report.targets.peakWithinMemoryGoal,bytes<=768*1048576);
  assert.equal(report.targets.peakUnder500MiB,bytes<=500*1048576);
  assert.equal(report.workingSetPeakBytes,bytes);
  assert.deepEqual(report.samples,[{total:bytes}]);
  const once=JSON.stringify(report);finalizePeak(report);assert.equal(JSON.stringify(report),once);
 }
 for(const flag of ["traceDiagnostic","readbackDiagnostic","dataPreviewDiagnostic","noImagePreviewDiagnostic","goMemoryLimitDiagnostic"]){
  const report={workingSetPeakBytes:0,[flag]:true,targets:{}};finalizePeak(report);
  assert.equal(report.targets.peakWithinMemoryGoal,false);
 }
 const late={workingSetPeakBytes:768*1048576,targets:{}};finalizePeak(late);
 assert.equal(late.targets.peakWithinMemoryGoal,true);
 late.workingSetPeakBytes++;finalizePeak(late);assert.equal(late.targets.peakWithinMemoryGoal,false);
});

test("local filter response goal requires twenty samples and preserves the exact 250ms boundary",()=>{
 for(const elapsed of [0,207.7,250,250.001]){
  const samples=Array(20).fill(elapsed),before=[...samples];
  assert.deepEqual(localFilterTargets(samples),{filterLimitMs:250,filterP95WithinGoal:elapsed<=250});
  assert.deepEqual(samples,before);
 }
 for(const count of [0,1,19])assert.throws(()=>localFilterTargets(Array(count).fill(0)));
 assert.deepEqual(localFilterTargets(Array(21).fill(250)),{filterLimitMs:250,filterP95WithinGoal:true});
 for(const invalid of [-1,NaN,Infinity,undefined,"1"])assert.throws(()=>localFilterTargets(Array(20).fill(invalid)));
 assert.throws(()=>localFilterTargets(null));
});
