import assert from "node:assert/strict";
import {spawnSync} from "node:child_process";
import {mkdtemp,readFile,writeFile,rm} from "node:fs/promises";
import {resolve,sep,basename} from "node:path";
import {fileURLToPath} from "node:url";
const directory=fileURLToPath(new URL(".",import.meta.url)),task=resolve(directory,"../../../.task"),work=await mkdtemp(resolve(task,"stress-noimage-mutations-"));
const source=await readFile(resolve(directory,"stress.noimage.mjs"),"utf8"),tests=await readFile(resolve(directory,"stress.noimage.test.mjs"),"utf8");
const cases=[
 ["bound-positive","maxBytes<=0","maxBytes<0"],
 ["bound-upper","maxBytes>25*1024*1024","maxBytes>25*1024*1024+1"],
 ["bound-safe","!Number.isSafeInteger(maxBytes)||",""],
 ["only-preview",'this.alt!=="Jackpot 로컬 추첨 결과 미리보기"||',""],
 ["source-string",'typeof url!=="string"||',""],
 ["source-blob",'||!url.startsWith("blob:")',""],
 ["disposed","disposed||this.alt","false||this.alt"],
 ["owned-blob","blob===undefined||",""],
 ["PNG-type",'blob.type!=="image/png"||',""],
 ["PNG-safe","!Number.isSafeInteger(blob.size)||",""],
 ["PNG-min","blob.size<=0","blob.size<0"],
 ["PNG-max","blob.size>maxBytes","blob.size>=maxBytes"],
 ["decoder-src-clear",'remove.call(this,"src");',""],
 ["observed-suppression","suppressed++;",""],
 ["revoke-owned-metadata","urls.delete(url);",""],
 ["native-revoke","const result=revoke.call(this,url);","const result=undefined;"],
 ["dispose-state","disposed=true;","disposed=false;"],
 ["dispose-owned-urls","for(const url of urls.keys())host.URL.revokeObjectURL(url);",""],
];
const results=[];
try{
 await writeFile(resolve(work,"stress.noimage.test.mjs"),tests);
 const original=spawnSync(process.execPath,["--test",resolve(directory,"stress.noimage.test.mjs")],{encoding:"utf8",timeout:30000,windowsHide:true});assert.equal(original.status,0,"original tests must pass");
 for(const [id,before,after]of cases){assert.ok(source.includes(before),"mutation seam "+id);await writeFile(resolve(work,"stress.noimage.mjs"),source.replace(before,after));const tested=spawnSync(process.execPath,["--test",resolve(work,"stress.noimage.test.mjs")],{encoding:"utf8",timeout:30000,windowsHide:true});const killed=tested.status!==null&&tested.status!==0&&(tested.stdout+tested.stderr).includes("ERR_ASSERTION")&&tested.error===undefined;results.push({id,killed,exitCode:tested.status,...(killed?{}:{output:(tested.stdout+tested.stderr).slice(-3500),error:tested.error?.message})});}
 const result={originalPassed:true,mutations:results.length,killed:results.filter(item=>item.killed).length,timeoutOrCompileAccepted:0,results};await writeFile(resolve(task,"stress-noimage-mutations.json"),JSON.stringify(result,null,2)+"\n");assert.equal(result.killed,result.mutations,"every selected mutation must fail assertions");console.log(JSON.stringify(result));
}finally{const absolute=resolve(work);assert.ok(absolute.startsWith(task+sep)&&basename(absolute).startsWith("stress-noimage-mutations-"));await rm(absolute,{recursive:true,force:true});}