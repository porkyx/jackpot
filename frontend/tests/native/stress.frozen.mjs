// Test-only passive boundary tracing. Native fetch promises/responses/cancellation
// are returned unchanged; records contain bounded public query/page metadata.
export function installFrozenReadTrace({methodID,capacity=128},host=window){
 if(!Number.isSafeInteger(methodID)||methodID<=0||methodID>4294967295||!Number.isSafeInteger(capacity)||capacity<1||capacity>256)throw Error("Invalid frozen trace bounds");
 if(host.__jackpotFrozenReads!==undefined)throw Error("Frozen trace already owned");
 const original=host.fetch;const records=[];let alive=true,sequence=0,dropped=0,pendingRequests=0,pendingProjections=0;
 const now=()=>host.performance.now();
 const project=response=>({ok:response?.ok===true,code:typeof response?.code==="string"&&["InvalidInput","StaleState","StorageUnavailable","ProtocolError","NotFound"].includes(response.code)?response.code:undefined,revision:Number.isSafeInteger(response?.data?.revision)?response.data.revision:undefined,responseOffset:Number.isSafeInteger(response?.data?.offset)?response.data.offset:undefined,total:Number.isSafeInteger(response?.data?.total)?response.data.total:undefined,rows:Array.isArray(response?.data?.rows)?response.data.rows.length:undefined});
 const wrapper=function(input,init){
  let body;try{if(typeof init?.body==="string"&&init.body.length<=4096)body=JSON.parse(init.body);}catch{}
  const request=body?.args?.args?.[0];const targeted=alive&&body?.args?.methodID===methodID&&Number.isSafeInteger(request?.offset)&&Number.isSafeInteger(request?.limit);
  let entry;if(targeted){entry={sequence:++sequence,startedAt:now(),offset:request.offset,limit:request.limit,expectedRevision:Number.isSafeInteger(request.expectedRevision)?request.expectedRevision:undefined};records.push(entry);if(records.length>capacity){records.shift();dropped++;}pendingRequests++;}
  let result;try{result=Reflect.apply(original,this,[input,init]);}catch(error){if(entry){pendingRequests--;if(alive){entry.failedAt=now();entry.synchronousFailure=true;}}throw error;}
  if(entry)void Promise.resolve(result).then(response=>{
   pendingRequests--;if(!alive)return;entry.headersAt=now();entry.httpOK=response.ok===true;
   let clone;try{clone=response.clone();}catch{entry.projectionFailure=true;return;}
   pendingProjections++;void Promise.resolve().then(()=>clone.json()).then(value=>{if(alive){entry.completedAt=now();Object.assign(entry,project(value));}},()=>{if(alive){entry.projectionFailure=true;entry.completedAt=now();}}).finally(()=>{pendingProjections--;});
  },error=>{pendingRequests--;if(alive){entry.failedAt=now();entry.cancelled=error?.name==="AbortError";}});
  return result;
 };
 host.fetch=wrapper;
 const owner={snapshot:()=>({records:records.map(entry=>({...entry})),pendingRequests,pendingProjections,dropped}),dispose:()=>{if(!alive)return;alive=false;if(host.fetch!==wrapper)throw Error("Frozen trace fetch ownership lost");host.fetch=original;records.length=0;delete host.__jackpotFrozenReads;}};
 host.__jackpotFrozenReads=owner;return owner;
}