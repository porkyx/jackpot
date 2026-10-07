import {test} from "node:test";
import assert from "node:assert/strict";
import {installDataPreview} from "./stress.preview.mjs";
function environment(){
 const readers=[];let urls=0;const revoked=[];
 class Element{constructor(){this.attributes=new Map();this.isConnected=true;}removeAttribute(name){this.attributes.delete(name);}}
 class Image extends Element{constructor(){super();this.alt="Jackpot 로컬 추첨 결과 미리보기";}set src(value){this.attributes.set("src",String(value));}get src(){return this.attributes.get("src");}}
 class Reader{constructor(){readers.push(this);this.aborts=0;}readAsDataURL(blob){this.blob=blob;if(blob.throwRead)throw Error("read failure");}abort(){this.aborts++;this.onabort?.();}}
 const host={URL:{createObjectURL:()=> "blob:owned-"+ ++urls,revokeObjectURL:url=>revoked.push(url)},Element,HTMLImageElement:Image,FileReader:Reader,WeakRef};
 return {host,Image,readers,revoked,stats:()=>host.__jackpotPreviewTransport.snapshot()};
}
const png=(size=1)=>({type:"image/png",size});
const value="data:image/png;base64,YWJj";
test("data preview byte bounds reject before changing browser APIs",()=>{
 for(const invalid of [0,-1,1.5,NaN,Infinity,25*1024*1024+1]){const e=environment();const original=e.host.URL.createObjectURL;assert.throws(()=>installDataPreview({maxBytes:invalid},e.host));assert.equal(e.host.URL.createObjectURL,original);}
});
test("exact PNG size bounds and stable content URI release active blob ownership",()=>{
 for(const size of [1,25*1024*1024]){const e=environment();installDataPreview({},e.host);const sources=[];
 for(let index=0;index<2;index++){const image=new e.Image(),url=e.host.URL.createObjectURL(png(size));assert.doesNotThrow(()=>{image.src=url;});assert.equal(image.src,undefined);assert.equal(e.stats().pendingReads,1);const reader=e.readers.at(-1);reader.result=value;reader.onload();sources.push(image.src);assert.equal(e.stats().pendingReads,0);assert.equal(e.stats().activeBlobURLs,1);image.removeAttribute("src");e.host.URL.revokeObjectURL(url);assert.equal(e.stats().activeBlobURLs,0);}
 assert.deepEqual(sources,[value,value]);assert.equal(e.stats().converted,2);assert.equal(e.stats().failures,0);e.host.__jackpotPreviewTransport.dispose();assert.equal(e.stats().disposed,true);}
});
test("foreign image, nonblob source, and disposed installation pass through unchanged",()=>{
 const e=environment();installDataPreview({},e.host);for(const [alt,src] of [["디시콘 이미지","blob:foreign"],["Jackpot 로컬 추첨 결과 미리보기","https://example.invalid/a.png"],["Jackpot 로컬 추첨 결과 미리보기",null]]){const image=new e.Image();image.alt=alt;assert.doesNotThrow(()=>{image.src=src;});assert.equal(image.src,String(src));}assert.equal(e.readers.length,0);
 e.host.__jackpotPreviewTransport.dispose();const image=new e.Image();assert.doesNotThrow(()=>{image.src="blob:owned";});assert.equal(image.src,"blob:owned");
});
test("unknown blob, non PNG, empty, unsafe, and oversized owned blobs fail visibly",()=>{
 const e=environment();installDataPreview({maxBytes:3},e.host);const image=new e.Image();assert.throws(()=>{image.src="blob:unknown";},/Invalid owned preview PNG/);
 for(const blob of [{type:"image/gif",size:1},png(0),png(4),png(NaN),png(1.5)]){const url=e.host.URL.createObjectURL(blob);assert.throws(()=>{image.src=url;},/Invalid owned preview PNG/);e.host.URL.revokeObjectURL(url);}assert.equal(e.stats().pendingReads,0);assert.equal(e.stats().activeBlobURLs,0);
});
test("Scope src removal aborts pending read and stale completion cannot republish",()=>{
 const e=environment();installDataPreview({},e.host);const image=new e.Image(),url=e.host.URL.createObjectURL(png());image.src=url;const reader=e.readers[0],stale=reader.onload;reader.result=value;image.removeAttribute("src");assert.equal(reader.aborts,1);assert.equal(reader.onload,null);stale();assert.equal(image.src,undefined);assert.equal(e.stats().converted,0);assert.equal(e.stats().pendingReads,0);e.host.URL.revokeObjectURL(url);
});
test("replacement generation discards old reply while the newest one may publish",()=>{
 const e=environment();installDataPreview({},e.host);const image=new e.Image(),first=e.host.URL.createObjectURL(png()),second=e.host.URL.createObjectURL(png());image.src=first;const old=e.readers[0],stale=old.onload;old.result=value;image.src=second;const latest=e.readers[1];stale();assert.equal(image.src,undefined);assert.equal(e.stats().pendingReads,1);latest.result=value;latest.onload();assert.equal(image.src,value);assert.equal(e.stats().pendingReads,0);e.host.URL.revokeObjectURL(first);e.host.URL.revokeObjectURL(second);
});
test("first, Nth, continuous failure and malformed result are visible without pending resources",()=>{
 const e=environment();installDataPreview({},e.host);for(const reason of ["error","load", "abort","load"]){const image=new e.Image(),url=e.host.URL.createObjectURL(png());image.src=url;const reader=e.readers.at(-1);if(reason==="load")reader.result= e.readers.length===2?undefined:"data:text/html;base64,abc";assert.doesNotThrow(()=>reader["on"+reason]());assert.equal(image.src,"data:image/png;base64,");assert.equal(e.stats().pendingReads,0);e.host.URL.revokeObjectURL(url);}assert.equal(e.stats().failures,4);assert.equal(e.stats().converted,0);assert.equal(e.stats().activeBlobURLs,0);
});
test("disconnected image and disposed installation discard late replies",()=>{
 for(const mode of ["disconnect","dispose"]){const e=environment();installDataPreview({},e.host);const image=new e.Image(),url=e.host.URL.createObjectURL(png());image.src=url;const reader=e.readers[0],reply=reader.onload;reader.result=value;if(mode==="disconnect")image.isConnected=false;else e.host.__jackpotPreviewTransport.dispose();reply();assert.equal(image.src,undefined);assert.equal(e.stats().pendingReads,0);assert.equal(e.stats().converted,0);e.host.URL.revokeObjectURL(url);e.host.__jackpotPreviewTransport.dispose();}
});
test("synchronous FileReader failure releases callbacks and remains observable",()=>{
 const e=environment();installDataPreview({},e.host);const image=new e.Image(),url=e.host.URL.createObjectURL({...png(),throwRead:true});assert.throws(()=>{image.src=url;},/read failure/);assert.equal(e.stats().pendingReads,0);assert.equal(e.readers[0].onload,null);image.removeAttribute("SRC");image.removeAttribute("alt");new e.host.Element().removeAttribute("src");e.host.URL.revokeObjectURL(url);assert.equal(e.stats().activeBlobURLs,0);
});
test("dispose aborts every live reader once and is idempotent",()=>{
 const e=environment();installDataPreview({},e.host);for(let i=0;i<2;i++){const image=new e.Image();image.src=e.host.URL.createObjectURL(png());}e.host.__jackpotPreviewTransport.dispose();e.host.__jackpotPreviewTransport.dispose();assert.equal(e.stats().pendingReads,0);assert.equal(e.stats().activeBlobURLs,0);assert.ok(e.readers.every(reader=>reader.aborts===1&&reader.onload===null));
});

test("a stale completion cannot remove the newer generation's cancellation owner",()=>{const e=environment();installDataPreview({},e.host);const image=new e.Image(),first=e.host.URL.createObjectURL(png()),second=e.host.URL.createObjectURL(png());image.src=first;const old=e.readers[0],stale=old.onload;old.result=value;image.src=second;stale();image.removeAttribute("src");assert.equal(e.readers[1].aborts,1);assert.equal(e.stats().pendingReads,0);assert.equal(image.src,undefined);e.host.__jackpotPreviewTransport.dispose();assert.equal(e.stats().activeBlobURLs,0);});
test("collected target and revoke failure remain observable with bounded retry cleanup",()=>{const e=environment();e.host.WeakRef=class{deref(){return undefined}};let failing=true;e.host.URL.revokeObjectURL=()=>{if(failing)throw Error("revoke failure");};installDataPreview({},e.host);const image=new e.Image(),url=e.host.URL.createObjectURL(png());image.src=url;const reader=e.readers[0];reader.result=value;reader.onload();assert.equal(e.stats().converted,0);assert.equal(e.stats().pendingReads,0);assert.throws(()=>e.host.URL.revokeObjectURL(url),/revoke failure/);assert.equal(e.stats().activeBlobURLs,1);failing=false;e.host.__jackpotPreviewTransport.dispose();assert.equal(e.stats().activeBlobURLs,0);});
