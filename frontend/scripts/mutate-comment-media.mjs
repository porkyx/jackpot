import{readFileSync,writeFileSync,rmSync}from"node:fs";
import{fileURLToPath}from"node:url";
import{spawnSync}from"node:child_process";
const root=fileURLToPath(new URL("../",import.meta.url));
const files={media:"src/contracts/media.ts",schema:"src/contracts/product.ts",view:"src/ui/comment/view.ts",owner:"src/screens/create/productOwner.ts",create:"src/features/create/view.ts"};
const originals=Object.fromEntries(Object.entries(files).map(([key,path])=>[key,readFileSync(new URL("../"+path,import.meta.url),"utf8")]));
const cases=[
 ["required verified dcimg5 origin","media",'"dcimg5.dcinside.com",',''],
 ["malicious extra origin","media",'"image.dcinside.com"]','"image.dcinside.com","evil.invalid"]'],
 ["control space and backslash rejection","media","||"+originals.media.split("||")[1],""],
 ["malformed percent rejection","media",String.raw`||/%(?![0-9a-fA-F]{2})/.test(raw)`,''],
 ["typed DTO media validation","schema",'isSafeDcMediaUrl(url)?undefined:"unsafe media origin"','undefined'],
 ["direct row safety boundary","view",'if(!comment.mediaUrls.every(isSafeDcMediaUrl))return yield*Effect.fail(new ProtocolError());',''],
 ["only dccon loads media","view",'if(comment.kind==="dccon")','if(comment.kind!=="dccon")'],
 ["permanent DC classification","view",'value.kind==="dccon"?"[디시콘]"','value.kind==="dccon"?""'],
 ["failed image src cleared","view",'failed=true;image.hidden=true;image.removeAttribute("src");','failed=true;image.hidden=true;'],
 ["late load cannot revive error","view",'if(alive&&!failed)','if(alive)'],
 ["retired error callback guard","view","if(alive){failed=true","if(true){failed=true"],
 ["closed media src release","view",'for(const image of images){image.removeAttribute("src");image.remove();}','for(const image of images){image.remove();}'],
 ["pending structural admission","owner",'structuralCount++;publish({...state,structuralPending:true});','structuralCount++;publish({...state,structuralPending:false});'],
 ["queued structural pending retained","owner",'structuralPending:structuralCount>0','structuralPending:false'],
 ["structural native add disabled","create","patchDisabled(addPrize,blocked||state.structuralPending||editor.lock","patchDisabled(addPrize,blocked||editor.lock"],
];
const chosen=process.argv[2]===undefined?cases:cases.filter(([name])=>name===process.argv[2]);
const config=new URL(".media-mutant.config.ts",import.meta.url);const report=new URL(".media-mutant.json",import.meta.url);let killed=0;
try{for(const[name,key,from,to]of chosen){if(originals[key].split(from).length!==2)throw new Error("Target drift "+name+" "+JSON.stringify(from));const include=key==="media"||key==="schema"?["tests/unit/comment-media.test.ts"]:key==="view"?["tests/dom/comment-media.test.ts"]:key==="create"?["tests/dom/create-product.test.ts"]:["tests/dom/create-product.test.ts","tests/integration/create-product-owner.test.ts"];writeFileSync(config,'import{defineConfig}from"vitest/config";export default defineConfig({plugins:[{name:"isolated-media",enforce:"pre",transform(code,id){if(id.replaceAll("\\\\","/").endsWith('+JSON.stringify("/"+files[key])+'))return{code:code.replace('+JSON.stringify(from)+','+JSON.stringify(to)+'),map:null};}}],test:{environment:"happy-dom",include:'+JSON.stringify(include)+'}});');rmSync(report,{force:true});const run=spawnSync(process.execPath,["node_modules/vitest/vitest.mjs","run","--config",fileURLToPath(config),"--testNamePattern","DC media|media |retained queued|URL parser|load failure|empty media|duplicate verified|pinned media|unsafe direct|closed Scope|100 keyed|structural |optimistic prize","--reporter=json","--outputFile",fileURLToPath(report)],{cwd:root,encoding:"utf8",windowsHide:true,timeout:18000,env:{...process.env,NO_COLOR:"1"}});const output=run.stdout+run.stderr;if(run.error||run.signal||/Test timed out/i.test(output))throw new Error("Harness failure, not kill: "+name+"\n"+output);const results=JSON.parse(readFileSync(report,"utf8"));const failed=results.testResults.flatMap(s=>s.assertionResults).filter(t=>t.status==="failed");if(run.status===0||failed.length===0||!failed.some(t=>t.failureMessages.some(m=>/AssertionError|expected/.test(m))))throw new Error("Survived/no assertion: "+name+"\n"+JSON.stringify(failed)+"\n"+output);killed++;console.log("killed by assertion: "+name);}console.log("PASS "+killed+"/"+chosen.length+" isolated transforms; compile/timeout kills0");}finally{rmSync(config,{force:true});rmSync(report,{force:true});for(const[key,path]of Object.entries(files))if(readFileSync(new URL("../"+path,import.meta.url),"utf8")!==originals[key])throw new Error("Production source changed: "+path);}