// Bounded MemoryInfra projection; allocator parent/child values are not summed.
import assert from "node:assert/strict";
import {readFile,writeFile} from "node:fs/promises";
import {resolve} from "node:path";
const directory=resolve(process.argv[2]??".task");
const reportName=process.argv[3]??"product-stress-report.json";
const report=JSON.parse(await readFile(resolve(directory,reportName),"utf8"));
const buffer=await readFile(resolve(directory,"product-memory-infra.json"));assert.ok(buffer.length<=32*1024*1024);
const trace=JSON.parse(buffer.toString("utf8"));assert.ok(Array.isArray(trace.traceEvents)&&trace.traceEvents.length<=500000);
const windows=(report.memoryTrace?.dumps??[]).map(({label})=>{const before=trace.traceEvents.find(event=>event.name==="clock_sync"&&event.args?.sync_id==="before-"+label),after=trace.traceEvents.find(event=>event.name==="clock_sync"&&event.args?.sync_id==="after-"+label);return{label,before:before?.ts,after:after?.ts};});
const scalar=value=>value?.type==="scalar"&&value.units==="bytes"?Number.parseInt(value.value,16):undefined;
const dumps=trace.traceEvents.filter(event=>event.ph==="v"&&event.args?.dumps&&report.ownedPIDs.includes(event.pid)).map(event=>{
 const raw=event.args.dumps;const intervals=windows.filter(window=>Number.isFinite(window.before)&&Number.isFinite(window.after)&&window.before<=event.ts&&event.ts<=window.after);assert.ok(intervals.length<=1,"ambiguous trace phase");
 const providers=Object.entries(raw.allocators??{}).filter(([key])=>key.split("/").length<=3&&/^(malloc|partition_alloc|v8|blink_gc|skia|gpu|blink|discardable)/.test(key)).map(([name,value])=>({name,size:scalar(value.attrs?.size),effectiveSize:scalar(value.attrs?.effective_size),attrs:value.attrs}));
 return{phase:intervals[0]?.label??"outside-marker-window",traceId:event.id,ts:event.ts,pid:event.pid,role:report.processRoles[event.pid]??"owned-auxiliary-unclassified",name:event.name,processTotals:raw.process_totals,providerCount:Object.keys(raw.allocators??{}).length,providers};
});
const summary={reportStatus:report.status,dataLossOccurred:report.memoryTrace?.dataLossOccurred,dumpRequests:report.memoryTrace?.dumps,dumps,allocatorValuesAreOverlapping:true,clockMarkerWindows:windows,phaseAttribution:"actual trace clock_sync before/after interval; GUID is not assumed"};
await writeFile(resolve(directory,"product-memory-infra-analysis.json"),JSON.stringify(summary,null,2)+"\n");
console.log(JSON.stringify({requests:summary.dumpRequests,dumps:dumps.map(({phase,traceId,pid,role,providerCount})=>({phase,traceId,pid,role,providerCount})),dataLossOccurred:summary.dataLossOccurred}));