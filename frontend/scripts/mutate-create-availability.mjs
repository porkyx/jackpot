import { readFileSync, writeFileSync, rmSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

// Mutations exist only in this child Vitest transform. Production source,
// root builds and the native driver never consume a mutant file.
const root = fileURLToPath(new URL("../", import.meta.url));
const files = {
 main: "src/main.ts",
 owner: "src/screens/create/productOwner.ts",
 view: "src/features/create/view.ts",
};
const originals = Object.fromEntries(Object.entries(files).map(([key,path]) => [key,readFileSync(new URL("../"+path,import.meta.url),"utf8")]));
const cases = [
 ["main publishes actual global gate","main","setProductAvailability?.(blocked);",""],
 ["main registers workspace availability","main","setProductAvailability = workspace.setWritesBlocked;",""],
 ["same availability does not republish","owner","state.writesBlocked !== blocked","true"],
 // Removing the outer closed guard is equivalent: publish itself rejects closed owners.
 // URL-first patch is also redundant: the final bindings loop owns raw disabled state.
 ["button observes gate before paint","view","(!writing || !workspace.read().writesBlocked)","true"],
 ["Enter observes gate at execution","view","workspace.read().writesBlocked ? Effect.void : effect","effect"],
 ["raw inputs remain editable during resync","view","patchDisabled(value.input,isEditorFieldLocked(editor,value.target)||","patchDisabled(value.input,blocked||isEditorFieldLocked(editor,value.target)||"],
 ["active load cancellation stays enabled","view","patchDisabled(cancel,!loading)","patchDisabled(cancel,blocked||!loading)"],
];
const chosen = process.argv[2]==="quick" ? cases.slice(0,2) : process.argv[2]==="remaining" ? cases.slice(2) : cases;
const config = new URL("./.availability-mutant.config.ts",import.meta.url);
const report = new URL("./.availability-mutant-result.json",import.meta.url);
let killed = 0;
try {
 for (const [name,key,from,to] of chosen) {
  if (originals[key].split(from).length !== 2) throw new Error("Mutation target drifted: "+name);
  const path = "/"+files[key];
  writeFileSync(config,`import {defineConfig} from "vitest/config";
export default defineConfig({plugins:[{name:"isolated-availability-mutant",enforce:"pre",transform(code,id){if(id.replaceAll("\\\\","/").endsWith(${JSON.stringify(path)})){if(code.split(${JSON.stringify(from)}).length!==2)throw new Error("Mutation source mismatch");return {code:code.replace(${JSON.stringify(from)},${JSON.stringify(to)}),map:null};}}}],test:{environment:"happy-dom",include:["tests/integration/main-product-boot.test.ts","tests/integration/create-product-owner.test.ts","tests/dom/create-product.test.ts"]}});`);
  rmSync(report,{force:true});
  const result=spawnSync(process.execPath,["node_modules/vitest/vitest.mjs","run","--config",fileURLToPath(config),"--testNamePattern","global write gate|public availability projection|actual window focus","--reporter=json","--outputFile",fileURLToPath(report)],{cwd:root,encoding:"utf8",timeout:15000,env:{...process.env,NO_COLOR:"1"}});
  if(result.error || result.signal || /Test timed out/i.test(result.stdout+result.stderr)) throw new Error("Harness failure, not a kill: "+name+"\n"+result.stdout+result.stderr);
  const outcome=JSON.parse(readFileSync(report,"utf8"));
  const failures=outcome.testResults.flatMap((suite)=>suite.assertionResults).filter((test)=>test.status==="failed");
  if(result.status===0 || failures.length===0) throw new Error("Survived or no assertion executed: "+name+"\n"+result.stdout+result.stderr);
  if(failures.some((failure)=>!failure.failureMessages.some((message)=>/AssertionError|expected|controlled runnable fibers/.test(message))))throw new Error("Non-assertion failure, not a kill: "+name);
  killed++;console.log("killed by assertion: "+name);
 }
 console.log(`PASS: ${killed}/${chosen.length}; compile/timeout/import failures counted as0`);
} finally {
 rmSync(config,{force:true});rmSync(report,{force:true});
 for(const [key,path] of Object.entries(files))if(readFileSync(new URL("../"+path,import.meta.url),"utf8")!==originals[key])throw new Error("Shared production source changed during mutation run: "+path);
}