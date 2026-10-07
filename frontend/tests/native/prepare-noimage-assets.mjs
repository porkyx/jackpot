// Recover the immutable pre-diagnostic C4 fixture, never restore production dist.
import assert from "node:assert/strict";
import {readFile,writeFile,mkdtemp,cp} from "node:fs/promises";
import {createHash} from "node:crypto";
import {resolve,sep} from "node:path";
const root=process.cwd(),task=resolve(root,".task"),production=resolve(root,"frontend/dist/index.html");
const manifest=JSON.parse(await readFile(resolve(task,"product-preview-assets-manifest.json"),"utf8"));
const archived=resolve(manifest.target);assert.ok(archived.startsWith(task+sep)&&archived.slice(task.length+1).startsWith("stress-assets-data-"));
const unchangedProduction=await readFile(production),diagnostic=await readFile(resolve(archived,"index.html"),"utf8");
const sha=value=>createHash("sha256").update(value).digest("hex");assert.equal(sha(diagnostic),manifest.diagnosticHTMLSHA256);
const before="img-src &#39;self&#39; blob: data:",after="img-src &#39;self&#39; blob:";
assert.equal(diagnostic.split(before).length,2,"exact test-only CSP seam");
const original=diagnostic.replace(before,after);assert.equal(sha(original),manifest.sourceHTMLSHA256,"exact archived original CSP");
const target=await mkdtemp(resolve(task,"stress-assets-c4-noimage-"));await cp(archived,target,{recursive:true});
await writeFile(resolve(target,"index.html"),original);assert.equal(sha(await readFile(production)),sha(unchangedProduction));
for(const asset of manifest.scripts){assert.equal(sha(await readFile(resolve(target,"."+asset))),sha(await readFile(resolve(archived,"."+asset))));}
const result={target,archivedSource:archived,htmlSHA256:sha(original),currentProductionHTMLSHA256:sha(unchangedProduction),scripts:manifest.scripts,change:"none relative to archived C4 original; current production dist immutable"};
await writeFile(resolve(task,"product-noimage-assets-manifest.json"),JSON.stringify(result,null,2)+"\n");console.log(JSON.stringify(result));