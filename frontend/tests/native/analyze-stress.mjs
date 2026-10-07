import assert from "node:assert/strict";
import {readFile,writeFile} from "node:fs/promises";
import {resolve} from "node:path";
import {fileURLToPath} from "node:url";
const task=fileURLToPath(new URL("../../../.task/",import.meta.url));
const directory=resolve(process.argv[2]??task);
const report=JSON.parse(await readFile(resolve(directory,"product-stress-report.json"),"utf8"));
const records=(await readFile(resolve(directory,"product-stress-memory.jsonl"),"utf8")).split("\n").filter(Boolean).map(line=>JSON.parse(line));
const sampleCount=report.samples.length;
const check=sample=>sample.listeners===report.baseline.listeners&&sample.domNodes===report.baseline.domNodes&&sample.frames===0&&sample.objectUrls===0&&sample.canvasPixels===0;
assert.ok(report.samples.every(check),"recorded resource baseline changed");
const nativeCount=values=>values.filter(item=>item.registered).length;
const points=report.resourceCheckpoints.filter(item=>["100-route-dialog-export-cycles","route-dialog-export-cycles"].includes(item.phase)).map(point=>{
 const rows=records.filter(item=>item.phase===point.phase&&item.cycle>=point.cycle-9&&item.cycle<=point.cycle&&item.processes?.length===report.ownedPIDs.length);
 const pids={};
 for(const pid of report.ownedPIDs){const all=rows.flatMap(row=>row.processes.filter(part=>part.id===pid));pids[pid]={role:report.processRoles[pid]??"owned-auxiliary-unclassified",workingSetMin:Math.min(...all.map(part=>part.workingSet64)),workingSetMax:Math.max(...all.map(part=>part.workingSet64)),privateBytesMin:Math.min(...all.map(part=>part.privateBytes64)),privateBytesMax:Math.max(...all.map(part=>part.privateBytes64))};}
 return {cycle:point.cycle,js:point.js,go:point.go,ownedProcessWindow:pids};
});
const result={
 overallStatus:report.status,overallFailure:report.failure,
 scopeIterations:sampleCount,recordedScopeBaselinePassed:report.samples.every(check),
 baseline:report.baseline,baselineRegisteredNative:nativeCount(report.baselineListenerDetails),
 finalRegisteredNative:nativeCount(report.lastListenerDetails),
 finalRegisteredDetached:report.lastListenerDetails.filter(item=>item.registered&&!item.connected).length,
 exit:report.exit,ownedProcessesExited:report.ownedProcessesExited,cleanup:report.cleanup,
 drawSampleCount:report.drawSamplesMs.length,drawP95Ms:report.drawP95Ms,drawSamplesMs:report.drawSamplesMs,
 searchP95UpperBoundMs:report.searchP95Ms,firstCreateMs:report.firstCreateMs??report.firstCreateIncludingKDFMs,drawObservationOnly:report.drawObservationOnly??false,statisticalSampleSufficient:report.statisticalSampleSufficient??report.drawSamplesMs.length>=20,
 workingSetPeakBytes:report.workingSetPeakBytes,peakMiB:report.workingSetPeakBytes/1048576,
 peakPhase:report.peakWorkingSetPhase,peakParts:report.peakWorkingSetParts,
 png:report.PNG,checkpointSeries:points,
 noForcedGC:true,noWorkingSetTrim:true,
 traceWarning:"series groups observed cycle windows; it does not invent missing absolute timestamps",
};
await writeFile(resolve(directory,"product-stress-analysis.json"),JSON.stringify(result,null,2)+"\n");
console.log(JSON.stringify({scopeIterations:result.scopeIterations,overallStatus:result.overallStatus,drawP95Ms:result.drawP95Ms,peakMiB:result.peakMiB,baseline:result.baseline,finalRegisteredDetached:result.finalRegisteredDetached,cleanup:result.cleanup}));
