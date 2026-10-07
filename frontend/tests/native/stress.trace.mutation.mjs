import assert from "node:assert/strict";
import {spawnSync} from "node:child_process";
import {mkdtemp,readFile,writeFile,rm} from "node:fs/promises";
import {resolve,sep,basename} from "node:path";
import {fileURLToPath} from "node:url";
const sourceDirectory=fileURLToPath(new URL(".",import.meta.url));
const task=resolve(sourceDirectory,"../../../.task");
const work=await mkdtemp(resolve(task,"stress-trace-mutations-"));
const source=await readFile(resolve(sourceDirectory,"stress.trace.mjs"),"utf8");
const tests=await readFile(resolve(sourceDirectory,"stress.trace.test.mjs"),"utf8");
const cases=[
 ["only-memory-infra","excludedCategories:[\"*\"]","excludedCategories:[]"],
 ["no-periodic-dumps","memoryDumpConfig:{triggers:[]}","memoryDumpConfig:{}"],
 ["light-observation-default","levelOfDetail=\"light\"","levelOfDetail=\"detailed\""],

  [
    "disable-forced-gc",
    "deterministic:false",
    "deterministic:true"
  ],
  [
    "bounded-trace-buffer",
    "traceBufferSizeInKb:8192",
    "traceBufferSizeInKb:16384"
  ],
  [
    "byte-nonempty",
    "maxBytes>0",
    "maxBytes>=0"
  ],
  [
    "byte-max",
    "maxBytes<=64*1024*1024",
    "maxBytes<=64*1024*1024+1"
  ],
  [
    "timeout-positive",
    "timeoutMs>0",
    "true"
  ],
  [
    "dump-closed",
    "assert.equal(closed,false,\"trace closed\");",
    ""
  ],
  [
    "label-nonempty",
    "label.length>0",
    "label.length>=0"
  ],
  [
    "label-max",
    "label.length<=128",
    "label.length<=129"
  ],
  [
    "dump-success",
    "assert.equal(result.success,true,\"memory dump refused\");",
    ""
  ],
  [
    "dump-id",
    "assert.ok(typeof result.dumpGuid===\"string\"&&result.dumpGuid.length>0,\"memory dump id missing\");",
    ""
  ],
  [
    "finish-idempotent",
    "if(finishing)return finishing;",
    ""
  ],
  [
    "chunk-max",
    "++reads<=1024",
    "++reads<=1025"
  ],
  [
    "stream-byte-max",
    "length<=maxBytes",
    "length<=maxBytes+1"
  ],
  [
    "nonempty-stream",
    "assert.ok(bytes.length>0,\"trace stream stalled\");",
    ""
  ],
  [
    "listener-cleanup",
    "session.off(\"Tracing.tracingComplete\",listener);",
    ""
  ],
  [
    "timer-cleanup",
    "timers.clearTimeout(timeout);",
    ""
  ],
  [
    "handle-close",
    "if(handle!==undefined)await invoke(\"IO.close\",{handle});",
    ""
  ]
];
const results=[];
try{
 await writeFile(resolve(work,"stress.trace.test.mjs"),tests);
 const original=spawnSync(process.execPath,["--test",resolve(sourceDirectory,"stress.trace.test.mjs")],{encoding:"utf8",timeout:30000,windowsHide:true});
 assert.equal(original.status,0,"original tests must pass");
 for(const [id,before,after] of cases){
  assert.ok(source.includes(before),"mutation seam "+id);
  await writeFile(resolve(work,"stress.trace.mjs"),source.replace(before,after));
  const tested=spawnSync(process.execPath,["--test",resolve(work,"stress.trace.test.mjs")],{encoding:"utf8",timeout:30000,windowsHide:true});
  const assertionFailure=tested.status!==null&&tested.status!==0&&(tested.stdout+tested.stderr).includes("ERR_ASSERTION")&&tested.error===undefined;
  results.push({id,killed:assertionFailure,exitCode:tested.status,...(assertionFailure?{}:{output:(tested.stdout+tested.stderr).slice(-3500),error:tested.error?.message})});
 }
 const result={originalPassed:true,mutations:results.length,killed:results.filter(item=>item.killed).length,timeoutOrCompileAccepted:0,results};
 await writeFile(resolve(task,"stress-trace-mutations.json"),JSON.stringify(result,null,2)+"\n");
 assert.equal(result.killed,result.mutations,"every selected mutation must fail assertions");
 console.log(JSON.stringify(result));
}finally{
 const absolute=resolve(work);
 assert.ok(absolute.startsWith(task+sep)&&basename(absolute).startsWith("stress-trace-mutations-"),"mutation cleanup path");
 await rm(absolute,{recursive:true,force:true});
}