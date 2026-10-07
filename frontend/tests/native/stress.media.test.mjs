import {test} from "node:test";
import assert from "node:assert/strict";
import {inflateSync} from "node:zlib";
import {mediaPNG,mediaURL,installMediaFixture} from "./stress.media.mjs";
test("delivered media fixture is bounded RGBA PNG with exact 4 by 2 pixels",()=>{
 const bytes=mediaPNG();assert.deepEqual(bytes,mediaPNG());assert.ok(bytes.length<1024);assert.equal(bytes.subarray(0,8).toString("hex"),"89504e470d0a1a0a");
 assert.equal(bytes.readUInt32BE(16),4);assert.equal(bytes.readUInt32BE(20),2);assert.equal(bytes[24],8);assert.equal(bytes[25],6);
 let offset=8;const chunks=[];while(offset<bytes.length){const length=bytes.readUInt32BE(offset),kind=bytes.subarray(offset+4,offset+8).toString();chunks.push(kind);if(kind==="IDAT"){const pixels=inflateSync(bytes.subarray(offset+8,offset+8+length));assert.equal(pixels.length,34);for(let y=0;y<2;y++){assert.equal(pixels[y*17],0);for(let x=0;x<4;x++)assert.deepEqual([...pixels.subarray(y*17+1+x*4,y*17+5+x*4)],[10,90,180,255]);}}offset+=12+length;}assert.equal(offset,bytes.length);assert.deepEqual(chunks,["IHDR","IDAT","IEND"]);
});
function boundary(){const events=[];let handler;const page={route:async(url,callback)=>{assert.equal(url,mediaURL);handler=callback;},unroute:async(url,callback)=>{assert.equal(url,mediaURL);assert.equal(callback,handler);events.push("disposed");}};return{page,events,request:route=>handler(route)};}
test("first load, Nth failure, and continuous failures use scoped network routing only",async()=>{
 const b=boundary(),fixture=await installMediaFixture(b.page);const route={fulfill:async value=>{assert.equal(value.contentType,"image/png");assert.equal(value.headers["cache-control"],"no-store");assert.deepEqual(value.body,mediaPNG());b.events.push("load");},abort:async value=>{assert.equal(value,"failed");b.events.push("error");}};
 await b.request(route);fixture.setMode("error");await b.request(route);await b.request(route);assert.deepEqual(fixture.counts(),{fulfilled:1,aborted:2});fixture.setMode("load");await b.request(route);assert.deepEqual(fixture.counts(),{fulfilled:2,aborted:2});await fixture.dispose();assert.deepEqual(b.events,["load","error","error","load","disposed"]);
});
test("unsupported delivery mode does not change the current mode or counters",async()=>{const b=boundary(),fixture=await installMediaFixture(b.page);for(const invalid of ["",null,undefined,"success"])assert.throws(()=>fixture.setMode(invalid));assert.deepEqual(fixture.counts(),{fulfilled:0,aborted:0});await b.request({fulfill:async()=>{},abort:async()=>assert.fail()});assert.deepEqual(fixture.counts(),{fulfilled:1,aborted:0});await fixture.dispose();});
test("delivery and unroute dependency failures remain observable",async()=>{const b=boundary(),fixture=await installMediaFixture(b.page);const failure=Error("injected");await assert.rejects(b.request({fulfill:async()=>{throw failure;}}),error=>error===failure);fixture.setMode("error");await assert.rejects(b.request({abort:async()=>{throw failure;}}),error=>error===failure);b.page.unroute=async()=>{throw failure;};await assert.rejects(fixture.dispose(),error=>error===failure);});
