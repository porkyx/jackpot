// Network bytes/failure are deterministic. The native decoder, CSP, DOM and
// scoped load/error listeners remain the product's actual implementations.
import assert from "node:assert/strict";
import {deflateSync} from "node:zlib";
import {expect} from "@playwright/test";
export const mediaURL="https://dcimg5.dcinside.com/test-owned-image.png";
function chunk(type,data){
 const name=Buffer.from(type);const payload=Buffer.concat([name,data]);let crc=0xffffffff;
 for(const byte of payload){crc^=byte;for(let bit=0;bit<8;bit++)crc=(crc>>>1)^((crc&1)?0xedb88320:0);}
 const length=Buffer.alloc(4);length.writeUInt32BE(data.length);
 const checksum=Buffer.alloc(4);checksum.writeUInt32BE((crc^0xffffffff)>>>0);
 return Buffer.concat([length,payload,checksum]);
}
export function mediaPNG(){
 const header=Buffer.alloc(13);header.writeUInt32BE(4,0);header.writeUInt32BE(2,4);header[8]=8;header[9]=6;
 const pixels=Buffer.concat(Array.from({length:2},()=>Buffer.concat([Buffer.from([0]),Buffer.from(Array.from({length:4},()=>[10,90,180,255]).flat())])));
 return Buffer.concat([Buffer.from("89504e470d0a1a0a","hex"),chunk("IHDR",header),chunk("IDAT",deflateSync(pixels)),chunk("IEND",Buffer.alloc(0))]);
}
export async function installMediaFixture(page){
 let mode="load",fulfilled=0,aborted=0;
 const bytes=mediaPNG();
 const handler=async route=>{if(mode==="load"){fulfilled++;await route.fulfill({status:200,contentType:"image/png",headers:{"cache-control":"no-store"},body:bytes});}else{aborted++;await route.abort("failed");}};
 await page.route(mediaURL,handler);
 return {setMode:value=>{assert.ok(value==="load"||value==="error");mode=value;},counts:()=>({fulfilled,aborted}),dispose:()=>page.unroute(mediaURL,handler)};
}
export async function verifyMediaPanel({page,renderer,fixture,label,open,close}){
 const cases=[];
 for(const mode of ["load","error"]){
  fixture.setMode(mode);await renderer.send("Network.clearBrowserCache");const before=fixture.counts();
  await open();const image=page.locator('dialog[open] img[alt="디시콘 이미지"]');
  await expect(image).toHaveCount(1);await image.evaluate(image=>{window.__jackpotMediaReference=new WeakRef(image);image.scrollIntoView();});
  if(mode==="load"){await expect.poll(()=>image.evaluate(image=>image.naturalWidth)).toBe(4);await expect(image).toBeVisible();assert.equal((await image.evaluate(image=>image.naturalHeight)),2);assert.equal(fixture.counts().fulfilled,before.fulfilled+1);}
  else{await expect.poll(()=>image.evaluate(image=>image.hidden&&image.getAttribute("src")===null)).toBe(true);assert.equal(fixture.counts().aborted,before.aborted+1);}
  assert.ok((await image.evaluate(image=>image.parentElement.textContent)).includes("[디시콘]"));
  await close();await expect(page.locator("dialog[open]")).toHaveCount(0);
  const native=await renderer.send("Runtime.evaluate",{expression:"(()=>{const image=window.__jackpotMediaReference?.deref();const result=image===undefined?{collected:true}:{collected:false,connected:image.isConnected,src:image.getAttribute('src'),listeners:Object.values(getEventListeners(image)).reduce((sum,rows)=>sum+rows.length,0)};delete window.__jackpotMediaReference;return result})()",includeCommandLineAPI:true,returnByValue:true});
  assert.equal(native.exceptionDetails,undefined);const cleanup=native.result.value;
  if(!cleanup.collected){assert.equal(cleanup.connected,false);assert.equal(cleanup.src,null);assert.equal(cleanup.listeners,0);}
  cases.push({panel:label,mode,width:mode==="load"?4:0,cleanup});
 }
 return cases;
}
