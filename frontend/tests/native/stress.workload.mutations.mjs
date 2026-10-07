import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import {mkdtemp,readFile,writeFile,rm} from 'node:fs/promises';
import {resolve,dirname} from 'node:path';
import {fileURLToPath} from 'node:url';
const dir=fileURLToPath(new URL('.',import.meta.url)),task=resolve(dir,'../../../.task'),work=await mkdtemp(resolve(task,'local-workload-mutants-'));
const source=await readFile(resolve(dir,'stress.workload.mjs'),'utf8'),tests=await readFile(resolve(dir,'stress.workload.test.mjs'),'utf8');
const cases=[
 ['fixture set',"fixtureComments === 100 || fixtureComments === 200","fixtureComments >= 100"],
 ['participant100',"fixtureComments === 100 ? 41 : 100","fixtureComments === 100 ? 40 : 100"],
 ['included author',"participants - 1","participants"],
 ['first round union',"Math.floor(included / 10) - 1","Math.floor(included / 10)"],
 ['rerun integer',"Number.isSafeInteger(drawSampleCount) && ",""],
 ['rerun minimum',"drawSampleCount >= 1","drawSampleCount >= 0"],
 ['rerun maximum',"drawSampleCount <= maxReruns","true"],
 ['scope integer',"Number.isSafeInteger(iterations) && ",""],
 ['scope minimum',"iterations >= 1","iterations >= 0"],
 ['scope maximum',"iterations <= 100","iterations <= 101"],
 ['winner count',"(drawSampleCount + 1) * 10","drawSampleCount * 10"],
 ['sufficient sample',"drawSampleCount >= 20","drawSampleCount >= 3"],
 ['owned immutable',"return Object.freeze(","return ("],
];
const results=[];
try {
 await writeFile(resolve(work,'stress.workload.test.mjs'),tests);
 const baseline=spawnSync(process.execPath,['--test',resolve(dir,'stress.workload.test.mjs')],{encoding:'utf8',windowsHide:true,timeout:10000});assert.equal(baseline.status,0);
 for(const [name,before,after] of cases){assert.equal(source.split(before).length,2,name);await writeFile(resolve(work,'stress.workload.mjs'),source.replace(before,after));const run=spawnSync(process.execPath,['--test',resolve(work,'stress.workload.test.mjs')],{encoding:'utf8',windowsHide:true,timeout:10000});const output=run.stdout+run.stderr;assert.equal(run.error,undefined,name);assert.equal(run.signal,null,name);assert.ok(run.status!==null&&run.status!==0,name);assert.match(output,/ERR_ASSERTION|AssertionError/,name);assert.doesNotMatch(output,/SyntaxError|Cannot find module/,name);results.push({name,assertionKilled:true});}
 await writeFile(resolve(task,'local-workload-mutations.json'),JSON.stringify({baselinePassed:true,assertionKills:results.length,total:cases.length,infrastructureKills:0,sourceUnchanged:source===await readFile(resolve(dir,'stress.workload.mjs'),'utf8'),results},null,2)+'\n');
 console.log('PASS '+results.length+'/'+cases.length+' isolated assertion mutants');
}finally{assert.equal(dirname(work),task);await rm(work,{recursive:true,force:true});}
