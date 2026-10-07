import assert from "node:assert/strict";
import {mkdtemp,readFile,writeFile,rm} from "node:fs/promises";
import {resolve,dirname,basename} from "node:path";
import {fileURLToPath} from "node:url";
import {spawnSync} from "node:child_process";
import {createHash} from "node:crypto";
const root=fileURLToPath(new URL("../../../",import.meta.url)),task=resolve(root,".task");
const sourcePath=resolve(root,"frontend/tests/native/reservationrestart.invariants.mjs"),testPath=resolve(root,"frontend/tests/native/reservationrestart.invariants.test.mjs");
const original=await readFile(sourcePath,"utf8"),tests=await readFile(testPath,"utf8"),sha=value=>createHash("sha256").update(value).digest("hex");
const beforeSource=sha(original),beforeTests=sha(tests),directory=await mkdtemp(resolve(task,"verified-test-reservation-guards-"));
const mutations=[
 ["capitalized fixture field required","raw.fixtureCalls??raw.FixtureCalls","raw.fixtureCalls"],
 ["required durable count presence",'assert.ok(Object.hasOwn(counts,key),"Required durable count missing")','assert.ok(true)'],
 ["casefold unique JSON aliases",'return {...rest,counts,latest,fixtureCalls,fixtureBodiesClosed};','return {...raw,counts,latest,fixtureCalls,fixtureBodiesClosed};'],
 ["body close field must be real",'&&Number.isSafeInteger(fixtureBodiesClosed)&&fixtureBodiesClosed>=0',''],
 ["owner revision excluded but stored outcome preserved","return durable;","return round;"],
 ["all historical winners are unique","assert.equal(new Set(ids).size,ids.length);",""]
];
let killed=0;
try{
 await writeFile(resolve(directory,"reservationrestart.invariants.test.mjs"),tests);
 for(const[name,before,after]of mutations){
  assert.ok(original.includes(before),"Mutation anchor missing "+name);
  await writeFile(resolve(directory,"reservationrestart.invariants.mjs"),original.replace(before,after));
  const syntax=spawnSync(process.execPath,["--check",resolve(directory,"reservationrestart.invariants.mjs")],{cwd:root,windowsHide:true,encoding:"utf8",timeout:10000});
  assert.equal(syntax.status,0,"Compile/syntax failure is not a kill");
  const result=spawnSync(process.execPath,["--test",resolve(directory,"reservationrestart.invariants.test.mjs")],{cwd:root,windowsHide:true,encoding:"utf8",timeout:10000});
  assert.ok(result.status!==0&&result.stdout.includes("ERR_ASSERTION"),"Observable assertion mutation survived "+name);
  console.log("KILLED "+name);killed++;
 }
 assert.equal(killed,mutations.length);console.log("Reservation guard mutation PASS: "+killed+"/"+mutations.length);
}finally{
 assert.equal(sha(await readFile(sourcePath,"utf8")),beforeSource);assert.equal(sha(await readFile(testPath,"utf8")),beforeTests);
 assert.equal(dirname(directory),task);assert.ok(basename(directory).startsWith("verified-test-reservation-guards-"));await rm(directory,{recursive:true,force:false});console.log("Original 2SHA and owned mutation artifact cleanup PASS");
}