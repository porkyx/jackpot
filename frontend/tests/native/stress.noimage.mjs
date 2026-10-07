// Test-only image decoder isolation. Real Canvas, Blob, file save and scopes run.
export function installNoImagePreview({maxBytes=25*1024*1024}={},host=globalThis){
 if(!Number.isSafeInteger(maxBytes)||maxBytes<=0||maxBytes>25*1024*1024)throw Error("Invalid preview byte bound");
 const urls=new Map();let suppressed=0,disposed=false;
 const create=host.URL.createObjectURL,revoke=host.URL.revokeObjectURL;
 const descriptor=Object.getOwnPropertyDescriptor(host.HTMLImageElement.prototype,"src");
 const remove=host.Element.prototype.removeAttribute;
 host.URL.createObjectURL=function(blob){const url=create.call(this,blob);urls.set(url,{size:blob.size,type:blob.type});return url;};
 host.URL.revokeObjectURL=function(url){const result=revoke.call(this,url);urls.delete(url);return result;};
 Object.defineProperty(host.HTMLImageElement.prototype,"src",{...descriptor,set(url){
  if(disposed||this.alt!=="Jackpot 로컬 추첨 결과 미리보기"||typeof url!=="string"||!url.startsWith("blob:"))return descriptor.set.call(this,url);
  const blob=urls.get(url);if(blob===undefined||blob.type!=="image/png"||!Number.isSafeInteger(blob.size)||blob.size<=0||blob.size>maxBytes)throw Error("Invalid owned preview PNG");
  remove.call(this,"src");suppressed++;
 }});
 host.__jackpotNoImageTransport={snapshot:()=>({activeBlobURLs:urls.size,suppressed,disposed}),dispose:()=>{disposed=true;for(const url of urls.keys())host.URL.revokeObjectURL(url);}};
}