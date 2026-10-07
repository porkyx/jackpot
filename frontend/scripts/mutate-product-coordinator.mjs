import { readFileSync,writeFileSync,rmSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
const originals={
 coordinator:readFileSync(new URL("../src/operations/productCoordinator.ts",import.meta.url),"utf8"),
 queries:readFileSync(new URL("../src/operations/productQueries.ts",import.meta.url),"utf8"),
};
const cases=[
 ["bootstrap refresh","coordinator","summary.revision>current.summary.revision","false"],
 ["retired session","coordinator","if(retired.has(reply.backendSessionId))","if(false)"],
 ["mutation deadline","coordinator","duration:30000","duration:29999"],
 ["lane 32 cap","coordinator",'>=32||unresolved()>=64','>=33||unresolved()>=64'],
 ["barrier 64 cap","coordinator",'if(barriers.size>=64)','if(barriers.size>=65)'],
 ["barrier distinct registration","coordinator",'const registration={};barriers.add(registration);','const registration=receipt;barriers.add(registration);'],
 ["tail coalescing","coordinator",'fieldKey!==undefined&&barriers.size===0&&tail?.state','false&&barriers.size===0&&tail?.state'],
 ["tail barrier preservation","coordinator",'fieldKey!==undefined&&barriers.size===0&&tail?.state','fieldKey!==undefined&&true&&tail?.state'],
 ["terminal 128 cap","coordinator",'terminal.length-128','terminal.length-129'],
 ["metadata bounded","coordinator",'entries.size>=160||','entries.size>=161||'],
 ["successful prefix through superseded","coordinator",'while(entry.state==="superseded"){if(entry.replacement===null','while(false){if(entry.replacement===null'],
 ["keyword ownership","coordinator",'includeKeywords: Object.freeze([...intent.filters.includeKeywords])','includeKeywords: intent.filters.includeKeywords'],
 ["finalization sequence","coordinator",'sequence!==afterSequence','false'],
 ["finalization seal","coordinator",'sealed=true;const reservation:OperationFlight','sealed=false;const reservation:OperationFlight'],
 ["manual single flight","coordinator",'if(recheckFlight!==undefined)return yield* Deferred.await(recheckFlight);','if(false)return yield* Deferred.await(recheckFlight);'],
 ["operation identity","coordinator",'reply.data.operationId!==operationId','false'],
 ["durable identity","coordinator",'observation.operationId!==operationId','false'],
 ["new draft after finalized","coordinator",'current?.summary.state==="finalized"','false'],
 ["read retry three","queries",'attempt<3','attempt<2'],
 ["read retry transport discriminant","queries",'result.failure._tag!=="TransportError"','true'],
 ["read protocol no retry","queries",'result.failure._tag!=="TransportError"','false'],
 ["read first backoff","queries",'attempt===0?250:1000','attempt===0?251:1000'],
 ["read deadline","queries",'duration:15000','duration:14999'],
];
cases.push(
 ["unallocated reservations count","coordinator",'active.add(value.id??value.token)','{if(value.id!==null)active.add(value.id);}'],
 ["retired history 64","coordinator",'if(retired.size>64)','if(retired.size>65)'],
 ["cancel separate path","coordinator",'if(allowCancel){if(cancelFlight','if(false){if(cancelFlight'],
 ["cancel duplicate reservation","coordinator",'if(cancelFlight!==undefined)return','if(false)return'],
 ["read response session fence","coordinator",'reply.backendSessionId!==expected)return','false)return'],
 ["comment participant identity","coordinator",'(page)=>page.participantId===request.participantId&&page.context','(page)=>true&&page.context'],
 ["comment context revision","coordinator",'page.participantId===request.participantId&&page.context.backendSessionId===request.backendSessionId&&page.context.draftId===request.draftId&&page.context.revision===request.revision','page.participantId===request.participantId&&page.context.backendSessionId===request.backendSessionId&&page.context.draftId===request.draftId&&true'],
 ["read owner consumer cleanup","coordinator",'Fiber.join(fiber).pipe(Effect.ensuring(Fiber.interrupt(fiber)))','Fiber.join(fiber)'],
 ["read singleflight release","coordinator",'if(readFlight===pending)readFlight=undefined;','if(false)readFlight=undefined;'],
 ["known reject actual unseal","coordinator",'if(refreshed._tag==="Success"&&refreshed.success.summary.state!=="finalized")sealed=false;','if(true)sealed=false;'],
 ["unknown receipt retention","coordinator",'entry.state=result.failure._tag==="ProductUnavailable"&&result.failure.reason==="outcome_unknown"?"unknown":"rejected"','entry.state="rejected"'],
 ["manual unknown direct retention","coordinator",'if(result._tag==="Success"||result.failure._tag==="BackendRejected")unknownDraft.delete(operationId);','if(true)unknownDraft.delete(operationId);'],
 ["draft failure code fallback","coordinator",'code:reply.data.failureCode??"ProtocolError",messageKey:reply.data.failureCode??"ProtocolError"','code:reply.data.failureCode??"InvalidInput",messageKey:reply.data.failureCode??"ProtocolError"'],
 ["durable target fence","coordinator",'(expectedCollection!==null&&expectedCollection!==collectionId)','false'],
 ["durable read identity","coordinator",'if(collection.collectionId!==collectionId)','if(false)'],
 ["stream owner shutdown","coordinator",'yield* PubSub.shutdown(state.pubsub);','yield* Effect.void;'],
);
const selected=process.argv[2];
const start=selected?.startsWith("from:")?cases.findIndex(([name])=>name===selected.slice(5)):-1;
const chosen=selected?.startsWith("from:")?(start<0?[]:cases.slice(start)):cases.filter(([name])=>selected===undefined||selected===name);
if(chosen.length===0)throw new Error("Unknown product mutation");
let survivors=0;
const files=[new URL("./.product-coordinator-mutant.ts",import.meta.url),new URL("./.product-queries-mutant.ts",import.meta.url)];
try{
 for(const [name,kind,from,to]of chosen){
  const source=originals[kind];if(source.split(from).length!==2)throw new Error("Mutation target drifted: "+name);
  const mutant=files[kind==="coordinator"?0:1];
  const altered=source.replace(from,to).replaceAll('"../contracts/','"../src/contracts/').replaceAll('"../platform/','"../src/platform/').replaceAll('"./productQueries"','"../src/operations/productQueries"').replaceAll('"./productCoordinator"','"../src/operations/productCoordinator"').replaceAll('"./collectionCache"','"../src/operations/collectionCache"').replaceAll('"./coordinatorState"','"../src/operations/coordinatorState"');
  writeFileSync(mutant,altered);
  const variable=kind==="coordinator"?"JACKPOT_PRODUCT_COORDINATOR_MUTANT":"JACKPOT_PRODUCT_QUERIES_MUTANT";
  const result=spawnSync(process.execPath,["node_modules/vitest/vitest.mjs","run","--config","scripts/product-coordinator.vitest.config.ts"],{cwd:fileURLToPath(new URL("../",import.meta.url)),encoding:"utf8",timeout:20000,env:{...process.env,[variable]:fileURLToPath(mutant),NO_COLOR:"1"}});
  if(result.error||result.signal||/Test timed out/i.test(result.stdout+result.stderr))throw new Error("Mutation harness failed: "+name+"\n"+result.stdout+"\n"+result.stderr);
  if(result.status!==0&&!/Tests\s+\d+ failed/.test(result.stdout))throw new Error("Mutation tests did not execute: "+name+"\n"+result.stdout+"\n"+result.stderr);
  if(result.status!==0&&!/AssertionError|expected/i.test(result.stdout+result.stderr))throw new Error("No observable assertion failure: "+name);
  if(result.status===0)survivors++;
  console.log((result.status===0?"SURVIVED: ":"killed: ")+name);
 }
}finally{for(const file of files)rmSync(file,{force:true});}
if(survivors>0)process.exitCode=1;