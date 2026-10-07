import assert from "node:assert/strict";
import {spawnSync} from "node:child_process";
import {mkdtemp,readFile,writeFile,rm} from "node:fs/promises";
import {resolve,sep,basename} from "node:path";
import {fileURLToPath} from "node:url";
const sourceDirectory=fileURLToPath(new URL(".",import.meta.url));
const task=resolve(sourceDirectory,"../../../.task");
const work=await mkdtemp(resolve(task,"stress-preview-mutations-"));
const source=await readFile(resolve(sourceDirectory,"stress.preview.mjs"),"utf8");
const tests=await readFile(resolve(sourceDirectory,"stress.preview.test.mjs"),"utf8");
const cases=[
  [
    "bound-positive",
    "maxBytes<=0",
    "maxBytes<0"
  ],
  [
    "bound-upper",
    "maxBytes>25*1024*1024",
    "maxBytes>25*1024*1024+1"
  ],
  [
    "bound-safe",
    "!Number.isSafeInteger(maxBytes)||",
    ""
  ],
  [
    "only-preview",
    "this.alt!==\"Jackpot 로컬 추첨 결과 미리보기\"||",
    ""
  ],
  [
    "source-string",
    "typeof url!==\"string\"||",
    ""
  ],
  [
    "source-blob",
    "||!url.startsWith(\"blob:\")",
    ""
  ],
  [
    "disposed-passthrough",
    "disposed||this.alt",
    "false||this.alt"
  ],
  [
    "owned-blob",
    "blob===undefined||",
    ""
  ],
  [
    "PNG-type",
    "blob.type!==\"image/png\"||",
    ""
  ],
  [
    "PNG-size-safe",
    "!Number.isSafeInteger(blob.size)||",
    ""
  ],
  [
    "PNG-size-min",
    "blob.size<=0",
    "blob.size<0"
  ],
  [
    "PNG-size-max",
    "blob.size>maxBytes",
    "blob.size>=maxBytes"
  ],
  [
    "generation-fence",
    "&&state.generation===generation",
    ""
  ],
  [
    "connected-fence",
    "&&image.isConnected",
    ""
  ],
  [
    "disposed-fence",
    "&&!disposed",
    ""
  ],
  [
    "reader-owner",
    "if(state.reader===reader)",
    "if(true)"
  ],
  [
    "reader-unregister",
    "reads.delete(reader);",
    ""
  ],
  [
    "load-release",
    "reader.onload=null;",
    ""
  ],
  [
    "read-cancel",
    "release(reader);reader.abort();",
    "release(reader);"
  ],
  [
    "conversion-count",
    "converted++;",
    ""
  ],
  [
    "failure-count",
    "failures++;",
    ""
  ],
  [
    "result-type",
    "typeof reader.result===\"string\"&&",
    ""
  ],
  [
    "result-PNG",
    "&&reader.result.startsWith(\"data:image/png;base64,\")",
    ""
  ],
  [
    "Blob-release",
    "blobs.delete(url);",
    ""
  ],
  [
    "src-cancel",
    "String(name).toLowerCase()===\"src\"",
    "false"
  ],
  [
    "read-throw-release",
    "release(reader);state.reader=undefined;throw error;",
    "state.reader=undefined;throw error;"
  ],
  [
    "dispose-owned-urls",
    "for(const url of blobs.keys())host.URL.revokeObjectURL(url);",
    ""
  ]
];
const results=[];
try{
 await writeFile(resolve(work,"stress.preview.test.mjs"),tests);
 const original=spawnSync(process.execPath,["--test",resolve(sourceDirectory,"stress.preview.test.mjs")],{encoding:"utf8",timeout:30000,windowsHide:true});
 assert.equal(original.status,0,"original tests must pass");
 for(const [id,before,after] of cases){
  assert.ok(source.includes(before),"mutation seam "+id);
  await writeFile(resolve(work,"stress.preview.mjs"),source.replace(before,after));
  const tested=spawnSync(process.execPath,["--test",resolve(work,"stress.preview.test.mjs")],{encoding:"utf8",timeout:30000,windowsHide:true});
  const assertionFailure=tested.status!==null&&tested.status!==0&&(tested.stdout+tested.stderr).includes("ERR_ASSERTION")&&tested.error===undefined;
  results.push({id,killed:assertionFailure,exitCode:tested.status,...(assertionFailure?{}:{output:(tested.stdout+tested.stderr).slice(-3500),error:tested.error?.message})});
 }
 const result={originalPassed:true,mutations:results.length,killed:results.filter(item=>item.killed).length,timeoutOrCompileAccepted:0,results};
 await writeFile(resolve(task,"stress-preview-mutations.json"),JSON.stringify(result,null,2)+"\n");
 assert.equal(result.killed,result.mutations,"every selected mutation must fail assertions");
 console.log(JSON.stringify(result));
}finally{
 const absolute=resolve(work);
 assert.ok(absolute.startsWith(task+sep)&&basename(absolute).startsWith("stress-preview-mutations-"),"mutation cleanup path");
 await rm(absolute,{recursive:true,force:true});
}
