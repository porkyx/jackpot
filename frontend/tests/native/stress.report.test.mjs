// Deterministic reporting failure regressions for the native stress driver.
// Cleanup below is a deterministic mock; it is not actual Go/PID/profile evidence.
import {test} from "node:test";
import assert from "node:assert/strict";
import {writeCleanupReport} from "./stress.report.mjs";

const stages = ["before-cleanup","after-cleanup","final-live"];
const danger = "DO_NOT_LOG_TEST_TOKEN C:\\private-fixture\\password-path";
function ownedReport(failed=false) {
 return {
  status: failed ? "failed" : "passed",
  phase:"fixed-phase", iterations:7, workingSetPeakBytes:64,
  ...(failed ? {failure:"original product assertion"} : {}),
 };
}
async function exerciseFinally({kind, failAt=[], productFailed=false, rejectAsync=false}) {
 const report=ownedReport(productFailed);
 const owned={intervals:1,go:1,pids:2,profiles:1,memoryStreams:1};
 const events=[], results=[], saved=[];
 let serialized=0, written=0;
 const serialize=(...args)=>{
  serialized++;
  if(kind==="serialize" && failAt.includes(serialized))throw new Error(danger);
  return JSON.stringify(...args);
 };
 const write=(path,body)=>{
  written++;
  events.push("write-"+written);
  if(kind==="writer" && failAt.includes(written)){
   if(rejectAsync)return Promise.reject(new Error(danger));
   throw new Error(danger);
  }
  saved.push({path,body});
  return Promise.resolve();
 };
 // The three lazy JSON/write closures have the same ownership/order as the
 // exact product.stress patch. Reporting failure never exits this sequence.
 results.push(await writeCleanupReport(report,"before-cleanup",()=>write("owned-report",serialize(report,null,2)+"\n")));
 events.push("stop-interval");owned.intervals=0;
 events.push("stop-go");owned.go=0;
 events.push("prove-pids");owned.pids=0;
 events.push("remove-profile");owned.profiles=0;
 events.push("close-memory-log");owned.memoryStreams=0;
 results.push(await writeCleanupReport(report,"after-cleanup",()=>write("owned-report",serialize(report,null,2)+"\n")));
 results.push(await writeCleanupReport(report,"final-live",()=>write("owned-live",serialize({status:report.status,phase:report.phase,iterations:report.iterations,peakBytes:report.workingSetPeakBytes,reportingFailures:report.reportingFailures??[]}))));
 const summary={status:report.status,phase:report.phase,iterations:report.iterations,reportingFailures:report.reportingFailures??[]};
 return {report,owned,events,results,saved,serialized,written,summary,exitCode:process.exitCode};
}
function expectedFailures(indices) {
 return indices.map(index=>({stage:stages[index-1],code:"REPORT_OUTPUT_FAILED"}));
}
test("successful reporting preserves outcome and invokes every cleanup stage",async()=>{
 const previous=process.exitCode;
 try{
  const result=await exerciseFinally({kind:"writer"});
  assert.deepEqual(result.results,[true,true,true]);
  assert.equal(result.report.status,"passed");
  assert.equal(Object.hasOwn(result.report,"reportingFailures"),false);
  assert.equal(result.exitCode,previous);
  assert.deepEqual(result.owned,{intervals:0,go:0,pids:0,profiles:0,memoryStreams:0});
  assert.deepEqual(result.events,["write-1","stop-interval","stop-go","prove-pids","remove-profile","close-memory-log","write-2","write-3"]);
  assert.equal(result.saved.length,3);
  assert.equal(JSON.parse(result.saved[1].body).status,"passed");
 }finally{process.exitCode=previous}
});
for(const kind of ["writer","serialize"]){
 for(const indices of [[1],[2],[3],[1,2,3]]){
  test(kind+" failure at "+indices.join(",")+" cannot skip cleanup or replace a product failure",async()=>{
   const previous=process.exitCode;
   try{
    for(const productFailed of [false,true]){
     let result;
     await assert.doesNotReject(async()=>{
      result=await exerciseFinally({kind,failAt:indices,productFailed,rejectAsync:true});
     });
     assert.deepEqual(result.results,[1,2,3].map(index=>!indices.includes(index)));
     assert.deepEqual(result.owned,{intervals:0,go:0,pids:0,profiles:0,memoryStreams:0});
     assert.equal(result.report.status,"failed");
     assert.equal(result.exitCode,1);
     assert.equal(result.report.failure,productFailed?"original product assertion":undefined);
     assert.deepEqual(result.report.reportingFailures,expectedFailures(indices));
     assert.equal(result.serialized,3);
     assert.equal(result.written,kind==="writer"?3:3-indices.length);
     assert.equal(result.events.filter(value=>value==="stop-go").length,1);
     assert.equal(result.events.filter(value=>value==="remove-profile").length,1);
     assert.equal(JSON.stringify(result.summary).includes(danger),false);
     assert.equal(JSON.stringify(result.report.reportingFailures).includes("private-fixture"),false);
     for(const record of result.report.reportingFailures){
      assert.deepEqual(Object.keys(record),["stage","code"]);
      assert.equal(record.code,"REPORT_OUTPUT_FAILED");
      assert.ok(stages.includes(record.stage));
     }
    }
   }finally{process.exitCode=previous}
  });
 }
}
test("synchronous writer throws are contained before cleanup",async()=>{
 const previous=process.exitCode;
 try{
  let result;
  await assert.doesNotReject(async()=>{result=await exerciseFinally({kind:"writer",failAt:[1],rejectAsync:false})});
  assert.equal(result.exitCode,1);
  assert.equal(result.report.status,"failed");
  assert.equal(result.owned.go,0);
  assert.equal(result.owned.profiles,0);
  assert.deepEqual(result.report.reportingFailures,expectedFailures([1]));
 }finally{process.exitCode=previous}
});
for(const poison of ["bigint","cycle","throwing-toJSON"]){
 test("actual JSON "+poison+" failure still permits cleanup and safe last live output",async()=>{
  const previous=process.exitCode;
  try{
   const report=ownedReport(true), saved=[], events=[];
   if(poison==="bigint")report.unserializable=1n;
   if(poison==="cycle")report.unserializable=report;
   if(poison==="throwing-toJSON")report.toJSON=()=>{throw new Error(danger)};
   const write=(path,body)=>{saved.push({path,body});return Promise.resolve()};
   let result;
   await assert.doesNotReject(async()=>{
    const before=await writeCleanupReport(report,"before-cleanup",()=>write("report",JSON.stringify(report,null,2)+"\n"));
    events.push("go-stopped","pids-zero","profile-removed","memory-log-closed");
    const after=await writeCleanupReport(report,"after-cleanup",()=>write("report",JSON.stringify(report,null,2)+"\n"));
    const live=await writeCleanupReport(report,"final-live",()=>write("live",JSON.stringify({status:report.status,reportingFailures:report.reportingFailures??[]})));
    result=[before,after,live];
   });
   assert.deepEqual(result,[false,false,true]);
   assert.deepEqual(events,["go-stopped","pids-zero","profile-removed","memory-log-closed"]);
   assert.equal(report.failure,"original product assertion");
   assert.equal(report.status,"failed");
   assert.equal(process.exitCode,1);
   assert.deepEqual(report.reportingFailures,expectedFailures([1,2]));
   assert.equal(saved.length,1);
   assert.equal(JSON.parse(saved[0].body).status,"failed");
   assert.equal(saved[0].body.includes(danger),false);
  }finally{process.exitCode=previous}
 });
}
test("diagnostics retain at most three fixed records and never stringify an unknown stage",async()=>{
 const previous=process.exitCode;
 try{
  const report=ownedReport();
  let conversions=0;
  const untrustedStage={toString(){conversions++;return danger}};
  const fail=()=>{throw new Error(danger)};
  assert.equal(await writeCleanupReport(report,untrustedStage,fail),false);
  assert.equal(await writeCleanupReport(report,undefined,fail),false);
  assert.equal(await writeCleanupReport(report,null,fail),false);
  const before=JSON.stringify(report.reportingFailures);
  assert.equal(await writeCleanupReport(report,"after-cleanup",fail),false);
  assert.equal(JSON.stringify(report.reportingFailures),before);
  assert.equal(report.reportingFailures.length,3);
  assert.ok(report.reportingFailures.every(record=>record.stage==="unknown"&&record.code==="REPORT_OUTPUT_FAILED"));
  assert.equal(conversions,0);
  assert.equal(report.status,"failed");
  assert.equal(process.exitCode,1);
  assert.equal(before.includes(danger),false);
 }finally{process.exitCode=previous}
});
test("controlled pending writer rejection settles before cleanup without sleep",async()=>{
 const previous=process.exitCode;
 try{
  const report=ownedReport();
  let reject, entered=false, cleaned=false;
  const writer=new Promise((_,fail)=>{reject=fail});
  const pending=writeCleanupReport(report,"before-cleanup",()=>{entered=true;return writer});
  assert.equal(entered,true);
  assert.equal(cleaned,false);
  reject(new Error(danger));
  assert.equal(await pending,false);
  cleaned=true;
  assert.equal(cleaned,true);
  assert.equal(process.exitCode,1);
  assert.deepEqual(report.reportingFailures,expectedFailures([1]));
 }finally{process.exitCode=previous}
});