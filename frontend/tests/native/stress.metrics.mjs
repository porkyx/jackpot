import assert from "node:assert/strict";
export const LOCAL_MEMORY_LIMIT_MIB = 768;
export const LOCAL_FILTER_LIMIT_MS = 250;
export function percentile95(values) {
 assert.ok(values.length>0 && values.every(value=>Number.isFinite(value)&&value>=0),"finite nonempty timing samples required");
 return [...values].sort((a,b)=>a-b)[Math.ceil(values.length*.95)-1];
}
export function localFilterTargets(samples) {
 assert.ok(samples.length>=20,"At least twenty filter samples required");
 return {filterLimitMs:LOCAL_FILTER_LIMIT_MS,filterP95WithinGoal:percentile95(samples)<=LOCAL_FILTER_LIMIT_MS};
}
export function pngDimensions(bytes) {
 assert.ok(bytes.length>=24,"PNG header is truncated");
 assert.equal(bytes.subarray(0,8).toString("hex"),"89504e470d0a1a0a");
 assert.equal(bytes.subarray(12,16).toString(),"IHDR");
 const width=bytes.readUInt32BE(16),height=bytes.readUInt32BE(20);
 assert.equal(width,1200);
 assert.ok(height>0&&height<=8192&&width*height<=16000000);
 return {width,height};
}


export function watchRoundCommit({optionCount,prizeCount,timeoutMs=20000}, environment={document,window,MutationObserver,performance,setTimeout,clearTimeout}) {
 if(!Number.isSafeInteger(optionCount)||optionCount<2||!Number.isSafeInteger(prizeCount)||prizeCount<1||prizeCount>10||!Number.isFinite(timeoutMs)||timeoutMs<=0)throw new Error("Invalid timing predicate");
 const {document:doc,window:host,performance:clock}=environment;
 const screen=doc.querySelector('[data-product-screen="result"]');
 if(screen===null||host.__jackpotRoundTimer!==undefined)throw new Error("Round timing owner unavailable");
 let started=clock.now(),finished=false,didClick=false,timer;let listenerAttached=false;
 let resolve;
 const promise=new Promise(done=>{resolve=done});
 const cleanup=()=>{observer.disconnect();if(listenerAttached){doc.removeEventListener("click",clicked,true);listenerAttached=false}if(timer!==undefined){environment.clearTimeout.call(host,timer);timer=undefined}};
 const finish=(ok)=>{if(finished)return;finished=true;const ended=clock.now();cleanup();resolve({ok,elapsedMs:ended-started})};
 const clicked=event=>{const button=event.target?.closest?.("button");if(!didClick&&button?.textContent.trim()==="새 회차 추첨"){didClick=true;started=clock.now()}};
 const check=()=>{const options=screen.querySelectorAll('select[name="resultRound"] option');const last=options[options.length-1];const select=screen.querySelector('select[name="resultRound"]');const rows=screen.querySelectorAll('ol[aria-label="품목별 당첨자"] > li');if(didClick&&options.length===optionCount&&last!==undefined&&last.textContent.includes("완료")&&select!==null&&select.value===last.value&&rows.length===prizeCount)finish(true)};
 const observer=new environment.MutationObserver(check);
 host.__jackpotRoundTimer={promise,dispose:()=>finish(false)};
 try{listenerAttached=true;doc.addEventListener("click",clicked,true);observer.observe(screen,{childList:true,subtree:true,characterData:true,attributes:true});timer=environment.setTimeout.call(host,()=>finish(false),timeoutMs);if(finished&&timer!==undefined){environment.clearTimeout.call(host,timer);timer=undefined}}
 catch(error){finish(false);delete host.__jackpotRoundTimer;throw error}
}



export function finalizePeak(report){
 assert.ok(Number.isFinite(report.workingSetPeakBytes)&&report.workingSetPeakBytes>=0,"valid final sampled peak required");
 report.peakWorkingSetMiB=report.workingSetPeakBytes/1048576;
 if(report.targets!==undefined){
  const baseline=!report.traceDiagnostic&&!report.readbackDiagnostic&&!report.dataPreviewDiagnostic&&!report.noImagePreviewDiagnostic&&!report.goMemoryLimitDiagnostic;
  report.targets.memoryLimitMiB=LOCAL_MEMORY_LIMIT_MIB;
  report.targets.peakWithinMemoryGoal=baseline&&report.workingSetPeakBytes<=LOCAL_MEMORY_LIMIT_MIB*1048576;
  report.targets.peakUnder500MiB=baseline&&report.workingSetPeakBytes<=500*1048576;
 }
}
