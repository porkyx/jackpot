import assert from "node:assert/strict";
import test from "node:test";
import config from "../vite.config.mjs";

const policyFor=command=>{
 const value=config({command,mode:command==="build"?"production":"development"});
 const tags=value.plugins[0].transformIndexHtml.handler();
 assert.equal(tags.length,1);
 assert.equal(tags[0].tag,"meta");assert.equal(tags[0].attrs["http-equiv"],"Content-Security-Policy");assert.equal(tags[0].injectTo,"head-prepend");
 return {value,directives:new Map(tags[0].attrs.content.split(";").map(item=>{const[name,...values]=item.trim().split(/\s+/);return[name,values]}))};
};
for(const command of ["build","serve"]){
 test(command+" policy rejects script origins inline evaluation and plugin/frame/worker execution",()=>{
  const {directives}=policyFor(command);
  assert.deepEqual(directives.get("default-src"),["'self'"]);
  assert.deepEqual(directives.get("script-src"),["'self'"]);
  for(const name of ["base-uri","object-src","frame-src","worker-src","form-action","media-src"]){assert.deepEqual(directives.get(name),["'none'"]);}
  assert.deepEqual(directives.get("font-src"),["'self'"]);
  for(const values of directives.values()){assert.ok(!values.includes("*")&&!values.includes("'unsafe-eval'"));}
 });
 test(command+" allows only the independently enumerated validated DC image hosts and owned blob preview",()=>{
  const {directives}=policyFor(command);
  const images=directives.get("img-src");assert.equal(images.length,7);assert.ok(images.includes("'self'")&&images.includes("blob:"));
  const hosts=images.filter(value=>value.startsWith("https:")).map(value=>new URL(value).hostname).sort();
  assert.deepEqual(hosts,["dccon.dcinside.com","dcimg1.dcinside.com","dcimg4.dcinside.com","dcimg5.dcinside.com","image.dcinside.com"].sort());
 });
}
test("production refuses inline style and all external or websocket connections",()=>{
 const {directives}=policyFor("build");assert.deepEqual(directives.get("style-src"),["'self'"]);assert.deepEqual(directives.get("connect-src"),["'self'"]);
});
test("development CSS HMR and WebSocket exceptions remain limited to configured loopback port",()=>{
 const {value,directives}=policyFor("serve");assert.equal(value.server.host,"127.0.0.1");assert.equal(value.server.port,5173);assert.equal(value.server.strictPort,true);
 assert.deepEqual(directives.get("style-src"),["'self'","'unsafe-inline'"]);
 const connections=directives.get("connect-src");assert.equal(connections.length,3);assert.ok(connections.includes("'self'"));
 for(const address of connections.filter(value=>value!=="'self'")){const url=new URL(address);assert.equal(url.protocol,"ws:");assert.ok(url.hostname==="localhost"||url.hostname==="127.0.0.1");assert.equal(url.port,"5173");}
});