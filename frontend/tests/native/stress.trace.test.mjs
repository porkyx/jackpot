import {test} from "node:test";
import assert from "node:assert/strict";
import {EventEmitter} from "node:events";
import {startMemoryTrace} from "./stress.trace.mjs";
function harness({failure,items=[{data:'{"traceEvents":[]}',eof:true}],missing=false,loss=false,timeout=false}={}){
 const events=new EventEmitter(),calls=[],callbacks=new Map();let index=0,clear=0,serial=0;
 const timers={setTimeout(callback){const id=++serial;callbacks.set(id,callback);return id},clearTimeout(id){clear++;callbacks.delete(id)}};
 const session={on:events.on.bind(events),off:events.off.bind(events),async send(name,args){calls.push({name,args});if(name===failure)throw new Error(name+" failed");if(name==="Tracing.end"&&!timeout)events.emit("Tracing.tracingComplete",{stream:missing?undefined:"owned-stream",dataLossOccurred:loss});if(name==="Tracing.requestMemoryDump")return{dumpGuid:"guid",success:true};if(name==="IO.read")return items[index++]??{data:"",eof:true};return{};}};
 return{session,timers,calls,events,callbacks,get clear(){return clear}};
}
test("trace uses bounded buffer and explicitly disables deterministic forced GC",async()=>{
 const h=harness();const trace=await startMemoryTrace(h.session,{timers:h.timers});assert.equal(h.calls[0].args.traceConfig.traceBufferSizeInKb,8192);assert.deepEqual(h.calls[0].args.traceConfig.memoryDumpConfig,{triggers:[]});assert.deepEqual(h.calls[0].args.traceConfig.excludedCategories,["*"]);
 assert.deepEqual(await trace.dump("before"),{dumpGuid:"guid",success:true});assert.equal(h.calls.find(c=>c.name==="Tracing.requestMemoryDump").args.deterministic,false);assert.equal(h.calls.find(c=>c.name==="Tracing.requestMemoryDump").args.levelOfDetail,"light");
 const promise=trace.finish();assert.equal(trace.finish()===promise,true);const result=await promise;assert.equal(result.data.toString(),'{"traceEvents":[]}');assert.equal(result.dumps[0].label,"before");assert.equal(h.calls.filter(c=>c.name==="IO.close").length,1);assert.equal(h.events.listenerCount("Tracing.tracingComplete"),0);assert.equal(h.callbacks.size,0);assert.equal(h.clear,8);
 await assert.rejects(trace.dump("after"),/closed/);assert.equal(h.calls.some(c=>/Garbage|HeapProfiler/.test(c.name)),false);
});
test("stream may mix base64 and text chunks and reports trace loss",async()=>{
 const h=harness({loss:true,items:[{data:Buffer.from("abc").toString("base64"),base64Encoded:true,eof:false},{data:"def",eof:true}]});const trace=await startMemoryTrace(h.session,{timers:h.timers,maxBytes:6});const result=await trace.finish();assert.equal(result.data.toString(),"abcdef");assert.equal(result.dataLossOccurred,true);
});
test("invalid bounds reject before touching CDP",async()=>{
 for(const options of [{maxBytes:0},{maxBytes:NaN},{maxBytes:64*1024*1024+1},{timeoutMs:0},{timeoutMs:Infinity},{levelOfDetail:"background"},{levelOfDetail:""}]){const h=harness();await assert.rejects(startMemoryTrace(h.session,{timers:h.timers,...options}));assert.equal(h.calls.length,0);}
});
test("first and continuous dump failures leave trace closeable without fabricated success",async()=>{
 for(const repeat of [1,3]){const h=harness({failure:"Tracing.requestMemoryDump"});const trace=await startMemoryTrace(h.session,{timers:h.timers});for(let i=0;i<repeat;i++)await assert.rejects(trace.dump("failure-"+i),/failed/);const result=await trace.finish();assert.equal(result.dumps.length,0);assert.equal(h.events.listenerCount("Tracing.tracingComplete"),0);}
});
test("start failure does not register event listener",async()=>{const h=harness({failure:"Tracing.start"});await assert.rejects(startMemoryTrace(h.session,{timers:h.timers}),/failed/);assert.equal(h.events.listenerCount("Tracing.tracingComplete"),0);assert.equal(h.calls.length,1)});
test("end, stream read and close failures surface and release event timer",async()=>{
 for(const failure of ["Tracing.end","IO.read","IO.close"]){const h=harness({failure});const trace=await startMemoryTrace(h.session,{timers:h.timers});await assert.rejects(trace.finish(),/failed/);assert.equal(h.events.listenerCount("Tracing.tracingComplete"),0);assert.equal(h.callbacks.size,0);assert.equal(h.calls.filter(c=>c.name==="IO.close").length,failure==="Tracing.end"?0:1);}
});
test("missing stream, oversized and stalled chunks never leave a stream open",async()=>{
 for(const options of [{missing:true},{items:[{data:"abcd",eof:true}]},{items:[{data:"",eof:false}]},{items:[{data:undefined,eof:true}]}]){const h=harness(options);const trace=await startMemoryTrace(h.session,{timers:h.timers,maxBytes:3});await assert.rejects(trace.finish());assert.equal(h.events.listenerCount("Tracing.tracingComplete"),0);assert.equal(h.callbacks.size,0);assert.equal(h.calls.filter(c=>c.name==="IO.close").length,options.missing?0:1);}
});
test("completion timeout is explicit failure with all listener and timer cleanup",async()=>{
 const h=harness({timeout:true});const trace=await startMemoryTrace(h.session,{timers:h.timers});const result=trace.finish();h.callbacks.values().next().value();await assert.rejects(result,/timeout/);assert.equal(h.events.listenerCount("Tracing.tracingComplete"),0);assert.equal(h.callbacks.size,0);
});
test("invalid dump label rejects without CDP mutation and trace can close",async()=>{
 const h=harness();const trace=await startMemoryTrace(h.session,{timers:h.timers});for(const label of ["","a".repeat(129),null])await assert.rejects(trace.dump(label));assert.equal(h.calls.filter(c=>c.name==="Tracing.requestMemoryDump").length,0);await trace.finish();
});
test("Nth read failure and an endless stream close their owned handle",async()=>{
 for(const failure of ["nth","bound"]){const h=harness();const send=h.session.send;let reads=0;h.session.send=async(name,args)=>{if(name==="IO.read"){if(++reads===2&&failure==="nth")throw new Error("Nth read failure");return{data:"x",eof:false}}return send(name,args)};const trace=await startMemoryTrace(h.session,{timers:h.timers});await assert.rejects(trace.finish(),failure==="nth"?/Nth/:/chunk bound/);assert.equal(h.calls.filter(c=>c.name==="IO.close").length,1);assert.equal(h.events.listenerCount("Tracing.tracingComplete"),0);assert.equal(h.callbacks.size,0);assert.equal(reads,failure==="nth"?2:1024);}
});
test("refused or malformed memory dumps fail and remain visible in diagnostic record",async()=>{
 for(const value of [{success:false,dumpGuid:"valid-guid"},{success:true,dumpGuid:""}]){const h=harness();const send=h.session.send;h.session.send=(name,args)=>name==="Tracing.requestMemoryDump"?Promise.resolve(value):send(name,args);const trace=await startMemoryTrace(h.session,{timers:h.timers});await assert.rejects(trace.dump("refused"));const result=await trace.finish();assert.deepEqual(result.dumps,[{label:"refused",...value}]);}
});
test("minimum and maximum byte bounds permit exact valid stream output",async()=>{
 for(const maxBytes of [1,64*1024*1024]){const h=harness({items:[{data:"x",eof:true}]});const trace=await startMemoryTrace(h.session,{timers:h.timers,maxBytes});assert.equal((await trace.finish()).data.toString(),"x");}
});
test("unresolved CDP start and stream reads have bounded command cancellation",async()=>{
 for(const name of ["Tracing.start","IO.read"]){const h=harness();const send=h.session.send;h.session.send=(method,args)=>method===name?new Promise(()=>{}):send(method,args);
 const promise=name==="Tracing.start"?startMemoryTrace(h.session,{timers:h.timers}):(await startMemoryTrace(h.session,{timers:h.timers})).finish();
 for(let step=0;step<20;step++)await Promise.resolve();
 const callbacks=[...h.callbacks.values()];assert.ok(callbacks.length>0);callbacks.at(-1)();await assert.rejects(promise,/timeout/);assert.equal(h.events.listenerCount("Tracing.tracingComplete"),0);assert.equal(h.callbacks.size,0);if(name==="IO.read")assert.equal(h.calls.filter(c=>c.name==="IO.close").length,1);
 }
});
test("failed phase marker rejects the observation and still permits trace cleanup",async()=>{const h=harness({failure:"Tracing.recordClockSyncMarker"});const trace=await startMemoryTrace(h.session,{timers:h.timers});await assert.rejects(trace.dump("marker"),/failed/);assert.equal(h.calls.some(c=>c.name==="Tracing.requestMemoryDump"),false);assert.equal((await trace.finish()).dumps.length,0);assert.equal(h.callbacks.size,0)});

test("explicit detailed observation is recorded without forced GC",async()=>{const h=harness(),trace=await startMemoryTrace(h.session,{timers:h.timers,levelOfDetail:"detailed"});await trace.dump("detail");assert.equal(h.calls.find(c=>c.name==="Tracing.requestMemoryDump").args.levelOfDetail,"detailed");assert.equal(h.calls.find(c=>c.name==="Tracing.requestMemoryDump").args.deterministic,false);await trace.finish();assert.equal(h.callbacks.size,0);});
