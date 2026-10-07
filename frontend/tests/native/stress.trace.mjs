// Diagnostic MemoryInfra only: no forced collection, heap snapshot or WS trim.
import assert from "node:assert/strict";
export async function startMemoryTrace(session,{maxBytes=32*1024*1024,timeoutMs=15000,timers=globalThis,levelOfDetail="light"}={}){
 assert.ok(Number.isSafeInteger(maxBytes)&&maxBytes>0&&maxBytes<=64*1024*1024);
 assert.ok(Number.isFinite(timeoutMs)&&timeoutMs>0);
 assert.ok(levelOfDetail==="light"||levelOfDetail==="detailed");
 const invoke=async(name,args)=>{let timer;try{return await Promise.race([Promise.resolve().then(()=>session.send(name,args)),new Promise((_,reject)=>{timer=timers.setTimeout(()=>reject(new Error(name+" timeout")),timeoutMs);})]);}finally{timers.clearTimeout(timer);}};
 await invoke("Tracing.start",{transferMode:"ReturnAsStream",streamFormat:"json",streamCompression:"none",traceConfig:{recordMode:"recordUntilFull",traceBufferSizeInKb:8192,memoryDumpConfig:{triggers:[]},includedCategories:["disabled-by-default-memory-infra"],excludedCategories:["*"]}});
 let closed=false,finishing;const dumps=[];
 return {
  async dump(label){
   assert.equal(closed,false,"trace closed");assert.ok(typeof label==="string"&&label.length>0&&label.length<=128);
   await invoke("Tracing.recordClockSyncMarker",{syncId:"before-"+label});
   const result=await invoke("Tracing.requestMemoryDump",{deterministic:false,levelOfDetail});
   await invoke("Tracing.recordClockSyncMarker",{syncId:"after-"+label});
   dumps.push({label,...result});assert.equal(result.success,true,"memory dump refused");assert.ok(typeof result.dumpGuid==="string"&&result.dumpGuid.length>0,"memory dump id missing");return result;
  },
  finish(){
   if(finishing)return finishing;closed=true;
   finishing=(async()=>{
    let timeout,listener,handle;
    const completed=new Promise((resolve,reject)=>{
     listener=event=>resolve(event);session.on("Tracing.tracingComplete",listener);
     timeout=timers.setTimeout(()=>reject(new Error("Memory trace completion timeout")),timeoutMs);
    });
    void completed.catch(()=>{});
    try{
     await invoke("Tracing.end");const result=await completed;
     handle=result.stream;assert.ok(typeof handle==="string"&&handle.length>0,"trace stream absent");
     const parts=[];let length=0,reads=0;
     while(true){
      assert.ok(++reads<=1024,"trace chunk bound");
      const item=await invoke("IO.read",{handle,size:65536});
      assert.ok(typeof item.data==="string","trace stream data missing");
      const bytes=Buffer.from(item.data,item.base64Encoded?"base64":"utf8");length+=bytes.length;
      assert.ok(length<=maxBytes,"trace size bound");parts.push(bytes);
      if(item.eof)break;
      assert.ok(bytes.length>0,"trace stream stalled");
     }
     return{data:Buffer.concat(parts,length),dumps:[...dumps],dataLossOccurred:result.dataLossOccurred===true};
    }finally{
     timers.clearTimeout(timeout);session.off("Tracing.tracingComplete",listener);
     if(handle!==undefined)await invoke("IO.close",{handle});
    }
   })();return finishing;
  }
 };
}
