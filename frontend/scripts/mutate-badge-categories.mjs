// A private Vite transform changes only the test module; production stays read-only.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, writeFileSync, rmSync, rmdirSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
const frontend=fileURLToPath(new URL("../",import.meta.url));
const files={owner:"src/screens/create/productOwner.ts",model:"src/screens/create/editorModel.ts",schema:"src/contracts/product.ts",view:"src/features/create/view.ts",projection:"src/app/participantCategories.ts"};
const sources=Object.fromEntries(Object.entries(files).map(([key,path])=>[key,readFileSync(resolve(frontend,path),"utf8")]));
const tests=["tests/integration/badge-categories.test.ts","tests/dom/create-product.test.ts"];
const cases=[
 ["weight maximum100", "schema", "weight: maximum(100)","weight: maximum(101)"],
 ["legacy defaults remain uniform", "model", "field(values.weightingEnabled ?? false, valid)","field(values.weightingEnabled ?? true, valid)"],
 ["active ratio integer bounds", "owner", "const valid = /^\\d+$/.test(raw) && Number(raw) <= 100;","const valid = true;"],
 ["inactive weighting accepts preserved raw", "owner", "!valid && editor.filters.weightingEnabled.raw && !excluded","!valid && !excluded"],
 ["excluded ratio accepts preserved raw", "owner", "!valid && editor.filters.weightingEnabled.raw && !excluded","!valid && editor.filters.weightingEnabled.raw"],
 ["safe prior weight stays authoritative", "owner", "prior?.[value.rule].weight ?? 100","100"],
 ["inactive unsent raw stays dirty", "owner", "return /^\\d+$/.test(raw) && Number(raw) <= 100;","return true;"],
 ["category exclusion independent of weighting", "owner", "return { excluded, weight: valid", "return { excluded: false, weight: valid"],
 ["actual badge takes precedence over identity", "projection", "return value.badgeCategory ?? value.kind;","return value.kind;"],
 ["ratio disabled before opt in", "view", "!editor.filters.weightingEnabled.raw||editor.filters[value.spec.excluded].raw||isEditorFieldLocked(editor,value.target)","editor.filters[value.spec.excluded].raw||isEditorFieldLocked(editor,value.target)"],
 ["excluded ratio disabled", "view", "!editor.filters.weightingEnabled.raw||editor.filters[value.spec.excluded].raw||isEditorFieldLocked(editor,value.target)","!editor.filters.weightingEnabled.raw||isEditorFieldLocked(editor,value.target)"],
];
const selected=process.argv[2]===undefined?cases:cases.filter(([name])=>name===process.argv[2]);assert.ok(selected.length>0);
const temp=mkdtempSync(resolve(frontend,"scripts/.badge-mutants-"));assert.equal(dirname(temp),resolve(frontend,"scripts"));
const config=resolve(temp,"vitest.config.ts"),result=resolve(temp,"result.json");
const hash=value=>createHash("sha256").update(value).digest("hex");
const report={startedAt:new Date().toISOString(),sourceHashes:Object.fromEntries(Object.entries(sources).map(([key,value])=>[files[key],hash(value)])),tests:Object.fromEntries(tests.map(path=>[path,hash(readFileSync(resolve(frontend,path)))])),baseline:null,cases:[],status:"pending",sourceUnchanged:false,temporaryRemoved:false};
function execute(plugin){
 writeFileSync(config,'import{defineConfig}from"vitest/config";export default defineConfig({plugins:['+plugin+'],test:{environment:"happy-dom",include:'+JSON.stringify(tests)+'}});');
 rmSync(result,{force:true});const child=spawnSync(process.execPath,["node_modules/vitest/vitest.mjs","run","--config",config,"--reporter=json","--outputFile",result],{cwd:frontend,encoding:"utf8",windowsHide:true,timeout:25000,env:{...process.env,NO_COLOR:"1"}});
 const output=child.stdout+child.stderr;assert.ok(!child.error&&!child.signal&&!/Test timed out/i.test(output),"timeout/process failure is not an assertion kill");
 const parsed=JSON.parse(readFileSync(result,"utf8"));assert.equal(parsed.numRuntimeErrorTestSuites??0,0,"runtime/import failure is not a kill");assert.ok(parsed.numTotalTests>0);assert.ok(parsed.testResults.every(suite=>suite.status!=="failed"||suite.assertionResults.length>0),"compile failure is not a kill");return {child,parsed};
}
try{
 const base=execute("");assert.equal(base.child.status,0);report.baseline={tests:base.parsed.numTotalTests,passed:base.parsed.numPassedTests};
 for(const[name,key,from,to]of selected){assert.equal(sources[key].split(from).length,2,"exact unique anchor: "+name);
  const plugin='{name:"private-badge-mutant",enforce:"pre",transform(code,id){if(id.replaceAll("\\\\","/").endsWith('+JSON.stringify("/"+files[key])+'))return{code:code.replace('+JSON.stringify(from)+','+JSON.stringify(to)+'),map:null};}}';
  const run=execute(plugin),failed=run.parsed.testResults.flatMap(suite=>suite.assertionResults).filter(test=>test.status==="failed");assert.equal(run.child.status,1,"mutant survived: "+name);const assertion=failed.filter(test=>test.failureMessages.some(message=>/AssertionError|expected/.test(message)));assert.ok(assertion.length>0,"no observable assertion kill: "+name);
  report.cases.push({name,status:"assertion-killed",failedTests:assertion.map(test=>test.fullName)});console.log("assertion killed: "+name);
 }
 report.status="passed";
}catch(error){report.status="failed";report.error=String(error);throw error;}
finally{
 for(const[key,path]of Object.entries(files))assert.equal(readFileSync(resolve(frontend,path),"utf8"),sources[key],"source changed: "+path);report.sourceUnchanged=true;
 for(const[path,expected]of Object.entries(report.tests))assert.equal(hash(readFileSync(resolve(frontend,path))),expected,"test changed: "+path);report.testsUnchanged=true;
 rmSync(config,{force:true});rmSync(result,{force:true});rmdirSync(temp);report.temporaryRemoved=true;report.endedAt=new Date().toISOString();writeFileSync(resolve(frontend,"../.task/badge-categories/mutations.json"),JSON.stringify(report,null,2)+"\n");
}
