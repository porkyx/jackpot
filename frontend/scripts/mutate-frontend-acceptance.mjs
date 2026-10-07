import {readFileSync,writeFileSync,rmSync} from "node:fs";
import {fileURLToPath} from "node:url";
import {spawnSync} from "node:child_process";
const root=fileURLToPath(new URL("../",import.meta.url));
const files={cache:"src/operations/collectionCache.ts",result:"src/features/results/view.ts"};
const originals=Object.fromEntries(Object.entries(files).map(([key,path])=>[key,readFileSync(new URL("../"+path,import.meta.url),"utf8")]));
const cases=[
 ["LRU16 inclusive capacity","cache","entries.size>16","entries.size>17"],
 ["LRU touch retains recent reference","cache","entries.delete(id);entries.set(id,entry);","entries.set(id,entry);"],
 ["cache revision cannot reverse","cache","collection.revision<current.revision","false"],
 ["Bootstrap reference advances revision floor","cache","reference.revision<=current.revision","true"],
 ["cached result readonly ownership","cache","result:latest===undefined?null:Object.freeze({collectionId:collection.collectionId,roundId:latest.roundId,revision:latest.revision})","result:latest===undefined?null:({collectionId:collection.collectionId,roundId:latest.roundId,revision:latest.revision})"],
 ["hidden result owns no ticker","result","if(!visible||!alive)return;","if(!alive)return;"],
 ["hidden result interrupts pending scoped read","result","if(previous!==undefined)yield* Scope.close(previous,Exit.void);",""],
 ["resume rechecks Go before displaying ticker","result","if(requery){yield* load();}",""],
 ["broken iterator raises LRU invariant","cache","if(oldest===undefined)","if(false)"],
 ["result cutoff displays inclusive selected minute","result","Date.parse(f.timeCut)-60000","Date.parse(f.timeCut)"],
];
const chosen=process.argv[2]==="tail"?cases.slice(8):process.argv[2]==="cache"?cases.slice(0,5):process.argv[2]==="views"?cases.slice(5):cases;
const config=new URL("./.acceptance-mutant.config.ts",import.meta.url);const report=new URL("./.acceptance-mutant-result.json",import.meta.url);
let killed=0;
try{
 for(const [name,key,from,to] of chosen){
  if(originals[key].split(from).length!==2)throw new Error("Mutation target drifted: "+name);
  const suffix="/"+files[key];
  writeFileSync(config, 'import {defineConfig} from "vitest/config";export default defineConfig({plugins:[{name:"isolated-acceptance-mutant",enforce:"pre",transform(code,id){if(id.replaceAll("\\\\","/").endsWith('+JSON.stringify(suffix)+')){if(code.split('+JSON.stringify(from)+').length!==2)throw new Error("Mutation source mismatch");return {code:code.replace('+JSON.stringify(from)+','+JSON.stringify(to)+'),map:null};}}}],test:{environment:"happy-dom",include:["tests/integration/collection-cache.test.ts","tests/dom/result-product.test.ts"]}});');
  rmSync(report,{force:true});
  const run=spawnSync(process.execPath,["node_modules/vitest/vitest.mjs","run","--config",fileURLToPath(config),"--testNamePattern","scheduled screen|hide cancels|initial hidden|sixteen|older revision|result reference|cache owns|cached latest|concurrent older|Bootstrap recent|nonempty LRU|confirmed time cut","--reporter=json","--outputFile",fileURLToPath(report)],{cwd:root,encoding:"utf8",timeout:15000,windowsHide:true,env:{...process.env,NO_COLOR:"1"}});
  const output=run.stdout+run.stderr;
  if(run.error||run.signal||/Test timed out/i.test(output))throw new Error("Harness failure, not a kill: "+name+"\n"+output);
  const result=JSON.parse(readFileSync(report,"utf8"));const failures=result.testResults.flatMap(suite=>suite.assertionResults).filter(test=>test.status==="failed");
  if(run.status===0||failures.length===0)throw new Error("Survived or no assertion: "+name+"\n"+output);
  if(failures.some(test=>!test.failureMessages.some(message=>/AssertionError|expected/.test(message))))throw new Error("Non-assertion failure, not a kill: "+name+"\n"+output);
  killed++;console.log("killed by assertion: "+name);
 }
 console.log("PASS "+killed+"/"+chosen.length+"; compile/import/timeout failures count0");
}finally{
 rmSync(config,{force:true});rmSync(report,{force:true});
 for(const [key,path]of Object.entries(files))if(readFileSync(new URL("../"+path,import.meta.url),"utf8")!==originals[key])throw new Error("Shared source changed during mutation: "+path);
}