// Runs only pure helper mutations, never native Process.Kill or production source writes.
import assert from "node:assert/strict";
import {readFile,mkdtemp,writeFile,rm} from "node:fs/promises";
import {spawnSync} from "node:child_process";
import {resolve,sep} from "node:path";
import {fileURLToPath} from "node:url";
import {createHash} from "node:crypto";
const root=fileURLToPath(new URL("../../../",import.meta.url)),task=resolve(root,".task");
const files=[resolve(root,"frontend/tests/native/webview-recovery.guards.mjs"),resolve(root,"frontend/tests/native/webview-recovery.guards.test.mjs")];
const source=await readFile(files[0],"utf8"),tests=await readFile(files[1],"utf8"),sha=value=>createHash("sha256").update(value).digest("hex");const originals=[sha(source),sha(tests)];
const mutations=[
 ["workspace parent",'assert.equal(win32.dirname(target).toLowerCase(), task.toLowerCase(), "Workspace .task child required");',""],
 ["native executable",'assert.equal(record.name?.toLowerCase(), "msedgewebview2.exe", "Only WebView2 processes allowed");',""],
 ["exact profile",'assert.ok(profile===expected || profile===win32.join(expected,"EBWebView").toLowerCase(),"Exact isolated profile or fixed EBWebView child required");',""],
 ["native creation",'assert.ok(typeof record.created === "string" && Number.isFinite(Date.parse(record.created)), "Creation timestamp required");',""],
 ["native start",'assert.ok(typeof record.started === "string" && Number.isFinite(Date.parse(record.started)), "Start timestamp required");',""],
 ["allowed role",'if (roles !== undefined) assert.ok(roles.includes(role), "Allowed process role required");',""],
 ["CDP PID proof",'cdps.some(cdp => cdp.id === record.id)',"true"],
 ["unique target",'assert.equal(matches.length, 1, "Exactly one owned crash target required");','assert.ok(matches.length > 0);'],
 ["Go session",'assert.equal(after.session, before.session, "WebView recovery must preserve Go session");',""],
 ["durable counts",'assert.deepEqual(after.Counts ?? after.counts, before.Counts ?? before.counts, "Recovery must not write durable state");',""],
 ["results and authority",'assert.deepEqual(after.latest, before.latest, "Results, revision and management authority must survive");',""],
 ["Go draft",'assert.deepEqual(after.activeDraft, before.activeDraft, "Go draft identity/revision must survive");',""],
 ["no recollection",'assert.equal(after.FixtureCalls ?? after.fixtureCalls, before.FixtureCalls ?? before.fixtureCalls, "Recovery must not recollect");',""]
];
const temporary=await mkdtemp(resolve(task,"verified-test-webview-guards-"));let count=0;
try {
 await writeFile(resolve(temporary,"webview-recovery.guards.test.mjs"),tests);await writeFile(resolve(temporary,"webview-recovery.guards.mjs"),source);
 const baseline=spawnSync(process.execPath,["--test",resolve(temporary,"webview-recovery.guards.test.mjs")],{cwd:root,encoding:"utf8",timeout:10000,windowsHide:true});assert.equal(baseline.status,0,"Mutation baseline failed");
 for(const [name,from,to] of mutations){assert.equal(source.split(from).length,2,"Unique mutation anchor required: "+name);await writeFile(resolve(temporary,"webview-recovery.guards.mjs"),source.replace(from,to));const result=spawnSync(process.execPath,["--test",resolve(temporary,"webview-recovery.guards.test.mjs")],{cwd:root,encoding:"utf8",timeout:10000,windowsHide:true});assert.equal(result.error,undefined,"Mutation runner failure");assert.notEqual(result.status,0,"SURVIVED "+name);assert.match(result.stdout,/AssertionError/,"Non-observable failure is not a killed mutation: "+name);assert.doesNotMatch(result.stdout+result.stderr,/SyntaxError|Cannot find module/,"Infrastructure/compile failure excluded");console.log("KILLED "+name);count++;}
} finally {
 for(let index=0;index<files.length;index++)assert.equal(sha(await readFile(files[index],"utf8")),originals[index],"Original helper/test changed");
 assert.ok(temporary.startsWith(task+sep)&&temporary.slice(task.length+1).startsWith("verified-test-webview-guards-"));await rm(temporary,{recursive:true,force:true});console.log("Source hashes and owned temporary cleanup PASS");
}
assert.equal(count,13);console.log("Native recovery pure guard mutation PASS13/13");