// Every mutation is a private Vite transform; shared source stays untouched.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, writeFileSync, rmSync, rmdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
const frontend = fileURLToPath(new URL("../", import.meta.url));
const files = { size:"src/platform/querySize.ts", port:"src/platform/product.ts", schema:"src/contracts/product.ts", cache:"src/operations/collectionCache.ts", view:"src/features/results/view.ts", routes:"src/app/routes.ts" };
const originals = Object.fromEntries(Object.entries(files).map(([key,path])=>[key,readFileSync(resolve(frontend,path),"utf8")]));
const hash=value=>createHash("sha256").update(value).digest("hex");
const cases = [
 ["query guard precedes schema", "port", "if (query) assertQuerySize(raw);", "if (false) assertQuerySize(raw);"],
 ["eight MiB cap", "size", "8 * 1024 * 1024", "9 * 1024 * 1024"],
 ["ASCII fast path excludes escaped Unicode", "size", 'if (!/[\\u0000-\\u001f"\\\\<>&\\u007f-\\uffff]/.test(value))', 'if (true)'],
 ["exact byte bound succeeds", "size", "bytes > maximum", "bytes >= maximum"],
 ["HTML greater-than escaping", "size", "code === 62", "code === -1"],
 ["astral UTF8 byte count", "size", "{ add(4); i++; }", "{ add(3); i++; }"],
 ["multibyte UTF8 count", "size", "code < 2048 ? 2 : 3", "code < 2048 ? 2 : 2"],
 ["anchor rejects explicit offset", "port", "if (offset !== 0) throw new ProtocolError();", "if (false) throw new ProtocolError();"],
 ["anchor resolved offset aligns page", "port", "value.roundOffset % limit === 0 &&", "true &&"],
 ["query reply keeps requested offset", "port", "return value.roundOffset === (options.roundOffset ?? 0);", "return true;"],
 ["complete bounded page", "port", "value.rounds.length !== Math.min(limit, Math.max(0, value.roundTotal - value.roundOffset))", "false"],
 ["round page maximum fifty", "schema", "rounds: Schema.Array(RoundData).check(Schema.isMaxLength(50))", "rounds: Schema.Array(RoundData).check(Schema.isMaxLength(51))"],
 ["cache latest independent of page", "cache", "const latest=collection.latestRound;", "const latest=collection.rounds[collection.rounds.length-1];"],
 ["completed rerun offers immediate only", "view", 'if(current.state==="completed"&&value==="reservation")continue;', 'if(false)continue;'],
 ["completed forged reservation rejects before IPC", "view", 'if(current.state==="completed"&&drawMode.value==="reservation"){', 'if(false){'],
 ["older page cannot grant latest admin", "view", "const latest = () => collection?.latestRound ?? null;", "const latest = () => collection?.rounds[collection.rounds.length-1] ?? null;"],
 ["actual page query carries offset", "view", "commands.getCollection(route.collectionId,options)", "commands.getCollection(route.collectionId)"],
 ["round anchor survives link encoding", "routes", 'route.roundId === null ? "" :', 'true ? "" :'],
 ["round anchor survives local route parsing", "routes", 'const roundId = match[2] === undefined ? null : decodeURIComponent(match[2]);', 'const roundId = null;'],
];
const chosen=process.argv[2]===undefined?cases:cases.filter(([name])=>name===process.argv[2]);assert.ok(chosen.length>0);
const tests=["tests/integration/round-query.test.ts","tests/dom/result-product.test.ts","tests/unit/routes.test.ts"];
const temporary=mkdtempSync(resolve(frontend,"scripts/.round-mutants-"));assert.equal(dirname(temporary),resolve(frontend,"scripts"));
const config=resolve(temporary,"vitest.config.ts"),result=resolve(temporary,"result.json");
const report={startedAt:new Date().toISOString(),sourceHashes:Object.fromEntries(Object.entries(files).map(([key,path])=>[path,hash(originals[key])])),testHashes:Object.fromEntries(tests.map(path=>[path,hash(readFileSync(resolve(frontend,path)))])),baseline:null,cases:[]};
function execute(plugin){
 writeFileSync(config,'import{defineConfig}from"vitest/config";export default defineConfig({plugins:['+plugin+'],test:{environment:"happy-dom",include:'+JSON.stringify(tests)+'}});');rmSync(result,{force:true});
 const child=spawnSync(process.execPath,["node_modules/vitest/vitest.mjs","run","--config",config,"--testNamePattern","round|query byte|query size|query first|oversized|CollectionData","--reporter=json","--outputFile",result],{cwd:frontend,encoding:"utf8",windowsHide:true,timeout:20000,env:{...process.env,NO_COLOR:"1"}});
 const output=child.stdout+child.stderr;assert.ok(!child.error&&!child.signal&&!/Test timed out/i.test(output),"timeout/process failure is not a kill\n"+output);
 const parsed=JSON.parse(readFileSync(result,"utf8"));assert.equal(parsed.numRuntimeErrorTestSuites??0,0,"compile/runtime failure is not a kill");assert.ok(parsed.testResults.every(suite=>suite.status!=="failed"||suite.assertionResults.length>0));return{status:child.status,parsed};
}
try{
 const baseline=execute("");assert.equal(baseline.status,0);assert.equal(baseline.parsed.numFailedTests,0);report.baseline={passed:baseline.parsed.numPassedTests,status:"passed"};
 for(const[name,key,from,to]of chosen){assert.equal(originals[key].split(from).length,2,"anchor drift: "+name);
  const plugin='{name:"private-round-mutant",enforce:"pre",transform(code,id){if(id.replaceAll("\\\\","/").endsWith('+JSON.stringify("/"+files[key])+'))return{code:code.replace('+JSON.stringify(from)+','+JSON.stringify(to)+'),map:null};}}';
  const child=execute(plugin);const failed=child.parsed.testResults.flatMap(suite=>suite.assertionResults).filter(test=>test.status==="failed");assert.equal(child.status,1,"survived: "+name);
  assert.ok(failed.length>0&&failed.every(test=>test.failureMessages.some(message=>/AssertionError|expected/.test(message))),"not an assertion kill: "+name);
  report.cases.push({name,status:"assertion-killed",failedTests:failed.map(test=>test.fullName)});console.log("assertion killed: "+name);
 }
 report.status="passed";
}catch(error){report.status="failed";report.error=String(error);throw error;}finally{
 for(const[key,path]of Object.entries(files))assert.equal(readFileSync(resolve(frontend,path),"utf8"),originals[key],"production source changed: "+path);
 report.sourceUnchanged=true;rmSync(config,{force:true});rmSync(result,{force:true});rmdirSync(temporary);report.temporaryRemoved=true;report.endedAt=new Date().toISOString();
 writeFileSync(resolve(frontend,"../.task/round-query-mutations.json"),JSON.stringify(report,null,2)+"\n");console.log(JSON.stringify({status:report.status,baseline:report.baseline,killed:report.cases.length,planned:chosen.length,sourceUnchanged:report.sourceUnchanged}));
}
