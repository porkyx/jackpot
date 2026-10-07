// Test-only deterministic preview URI transport. Production files remain immutable.
export function installDataPreview({maxBytes=25*1024*1024}={},host=globalThis){
 if(!Number.isSafeInteger(maxBytes)||maxBytes<=0||maxBytes>25*1024*1024)throw Error("Invalid preview byte bound");
 const blobs=new Map(),states=new WeakMap(),reads=new Set();let converted=0,failures=0,disposed=false;
 const create=host.URL.createObjectURL,revoke=host.URL.revokeObjectURL;
 const descriptor=Object.getOwnPropertyDescriptor(host.HTMLImageElement.prototype,"src");
 const remove=host.Element.prototype.removeAttribute;
 const release=reader=>{reads.delete(reader);reader.onload=null;reader.onerror=null;reader.onabort=null;};
 const cancel=image=>{const state=states.get(image);if(state!==undefined){state.generation++;if(state.reader!==undefined){const reader=state.reader;state.reader=undefined;release(reader);reader.abort();}}};
 host.URL.createObjectURL=function(blob){const url=create.call(this,blob);blobs.set(url,blob);return url;};
 host.URL.revokeObjectURL=function(url){const result=revoke.call(this,url);blobs.delete(url);return result;};
 Object.defineProperty(host.HTMLImageElement.prototype,"src",{...descriptor,set(url){
  cancel(this);
  if(disposed||this.alt!=="Jackpot 로컬 추첨 결과 미리보기"||typeof url!=="string"||!url.startsWith("blob:"))return descriptor.set.call(this,url);
  const blob=blobs.get(url);if(blob===undefined||blob.type!=="image/png"||!Number.isSafeInteger(blob.size)||blob.size<=0||blob.size>maxBytes)throw Error("Invalid owned preview PNG");
  let state=states.get(this);if(state===undefined){state={generation:0};states.set(this,state);}
  const generation=state.generation,reference=new host.WeakRef(this),reader=new host.FileReader();state.reader=reader;reads.add(reader);
  const finish=success=>{const image=reference.deref();const current=image!==undefined&&image.isConnected&&state.generation===generation&&!disposed;
   release(reader);if(state.reader===reader)state.reader=undefined;
   if(!current)return;
   if(success&&typeof reader.result==="string"&&reader.result.startsWith("data:image/png;base64,")){converted++;descriptor.set.call(image,reader.result);}
   else{failures++;descriptor.set.call(image,"data:image/png;base64,");}
  };
  reader.onload=()=>finish(true);reader.onerror=()=>finish(false);reader.onabort=()=>finish(false);
  try{reader.readAsDataURL(blob);}catch(error){release(reader);state.reader=undefined;throw error;}
 }});
 host.Element.prototype.removeAttribute=function(name){if(this instanceof host.HTMLImageElement&&String(name).toLowerCase()==="src")cancel(this);return remove.call(this,name);};
 host.__jackpotPreviewTransport={snapshot:()=>({activeBlobURLs:blobs.size,pendingReads:reads.size,converted,failures,disposed}),dispose:()=>{disposed=true;for(const reader of reads){release(reader);reader.abort();}for(const url of blobs.keys())host.URL.revokeObjectURL(url);}};
}
