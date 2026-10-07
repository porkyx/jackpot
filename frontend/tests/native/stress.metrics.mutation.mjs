import assert from "node:assert/strict";
import {spawnSync} from "node:child_process";
import {mkdtemp,readFile,writeFile,rm} from "node:fs/promises";
import {resolve,sep,basename} from "node:path";
import {fileURLToPath} from "node:url";
const sourceDirectory=fileURLToPath(new URL(".",import.meta.url));
const task=resolve(sourceDirectory,"../../../.task");
const work=await mkdtemp(resolve(task,"stress-metrics-mutations-"));
const source=await readFile(resolve(sourceDirectory,"stress.metrics.mjs"),"utf8");
const tests=await readFile(resolve(sourceDirectory,"stress.metrics.test.mjs"),"utf8");
const cases=[
 ["local-filter-boundary","percentile95(samples)<=LOCAL_FILTER_LIMIT_MS","percentile95(samples)<LOCAL_FILTER_LIMIT_MS"],
 ["local-filter-limit","LOCAL_FILTER_LIMIT_MS = 250","LOCAL_FILTER_LIMIT_MS = 251"],
 ["local-filter-samples","samples.length>=20","samples.length>20"],
 ["local-memory-boundary","workingSetPeakBytes<=LOCAL_MEMORY_LIMIT_MIB*1048576","workingSetPeakBytes<LOCAL_MEMORY_LIMIT_MIB*1048576"],
 ["local-memory-limit","LOCAL_MEMORY_LIMIT_MIB = 768","LOCAL_MEMORY_LIMIT_MIB = 769"],
 ["local-memory-diagnostic","peakWithinMemoryGoal=baseline&&","peakWithinMemoryGoal="],
 ["final-peak-boundary","workingSetPeakBytes<=500*1048576","workingSetPeakBytes<500*1048576"],
 ["final-peak-unit","report.workingSetPeakBytes/1048576","report.workingSetPeakBytes/1000000"],
 ["final-peak-trace-guard","!report.traceDiagnostic&&",""],
 ["final-peak-readback-guard","!report.readbackDiagnostic&&",""],
 ["final-peak-data-guard","!report.dataPreviewDiagnostic&&",""],
 ["final-peak-noimage-guard","!report.noImagePreviewDiagnostic&&",""],
 ["final-peak-gomemory-guard","!report.goMemoryLimitDiagnostic;","true;"],
 ["final-peak-nonnegative","report.workingSetPeakBytes>=0","report.workingSetPeakBytes>0"],
 ["final-peak-valid","Number.isFinite(report.workingSetPeakBytes)&&",""],

 ["nonempty", "values.length>0 && ",""],
 ["finite","Number.isFinite(value)&&",""],
 ["nonnegative","value>=0","true"],
 ["p95-rank","Math.ceil(values.length*.95)","Math.floor(values.length*.95)"],
 ["p95-order","sort((a,b)=>a-b)","sort((a,b)=>b-a)"],
 ["png-width","assert.equal(width,1200)","assert.ok(true)"],
 ["png-height","height<=8192","height<=8193"],
 ["png-signature",'assert.equal(bytes.subarray(0,8).toString("hex"),"89504e470d0a1a0a")',"assert.ok(true)"],
 ["png-chunk",'assert.equal(bytes.subarray(12,16).toString(),"IHDR")',"assert.ok(true)"],
 ["first-click-required","if(didClick&&options.length","if(options.length"],
 ["duplicate-click","if(!didClick&&button","if(button"],
 ["round-count","options.length===optionCount&&",""],
 ["completed-label",'last.textContent.includes("완료")&&',""],
 ["latest-selected","select.value===last.value&&",""],
 ["prize-count","rows.length===prizeCount","true"],
 ["finish-idempotent","if(finished)return;",""],
 ["observer-cleanup","observer.disconnect();",""],
 ["listener-cleanup",'doc.removeEventListener("click",clicked,true);',""],
 ["timer-cleanup","environment.clearTimeout.call(host,timer);","void timer;"],
 ["timer-receiver","environment.setTimeout.call(host,","environment.setTimeout("],
 ["synchronous-timer-cleanup","if(finished&&timer!==undefined)","if(false)"],
];
const results=[];
try{
 await writeFile(resolve(work,"stress.metrics.test.mjs"),tests);
 const original=spawnSync(process.execPath,["--test",resolve(sourceDirectory,"stress.metrics.test.mjs")],{encoding:"utf8",timeout:30000,windowsHide:true});
 assert.equal(original.status,0,"original tests must pass");
 for(const [id,before,after] of cases){
  assert.ok(source.includes(before),"mutation seam "+id);
  await writeFile(resolve(work,"stress.metrics.mjs"),source.replace(before,after));
  const tested=spawnSync(process.execPath,["--test",resolve(work,"stress.metrics.test.mjs")],{encoding:"utf8",timeout:30000,windowsHide:true});
  const assertionFailure=tested.status!==null&&tested.status!==0&&(tested.stdout+tested.stderr).includes("ERR_ASSERTION")&&tested.error===undefined;
  results.push({id,killed:assertionFailure,exitCode:tested.status,...(assertionFailure?{}:{output:(tested.stdout+tested.stderr).slice(-3500),error:tested.error?.message})});
 }
 const result={originalPassed:true,mutations:results.length,killed:results.filter(item=>item.killed).length,timeoutOrCompileAccepted:0,results};
 await writeFile(resolve(task,"stress-metrics-mutations.json"),JSON.stringify(result,null,2)+"\n");
 assert.equal(result.killed,result.mutations,"every selected mutation must fail assertions");
 console.log(JSON.stringify(result));
}finally{
 const absolute=resolve(work);
 assert.ok(absolute.startsWith(task+sep)&&basename(absolute).startsWith("stress-metrics-mutations-"),"mutation cleanup path");
 await rm(absolute,{recursive:true,force:true});
}
