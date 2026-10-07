import {readFileSync,writeFileSync,mkdtempSync,rmSync}from"node:fs";
import {join}from"node:path";
import {fileURLToPath}from"node:url";
import {spawnSync}from"node:child_process";
const root=fileURLToPath(new URL("../",import.meta.url));const path=join(root,"internal/contracts/preview.go");const original=readFileSync(path,"utf8");const source=original.replaceAll("\r\n","\n");const temporary=mkdtempSync(join(root,".task/preview-mutations-"));
const cases=[
 ["max3 previews","MaxParticipantPreviews = 3","MaxParticipantPreviews = 4"],
 ["UTF16 256 inclusive","MaxPreviewUTF16 = 256","MaxPreviewUTF16 = 255"],
 ["astral surrogate units","if character > 0xffff {","if false && character > 0xffff {"],
 ["ellipsis reserve","if units < MaxPreviewUTF16 {","if units <= MaxPreviewUTF16 {"],
 ["invalid UTF8 rejected","if !utf8.ValidString(text) {","if false && !utf8.ValidString(text) {"],
 ["unchanged short body","return text, nil","return \"\", nil"],
 ["atomic projection failure","return nil, err","return previews, err"],
];let killed=0;
try{for(const[name,from,to]of cases){if(source.split(from).length!==2)throw new Error("Target drift "+name);const mutant=join(temporary,"preview.go");const overlay=join(temporary,"overlay.json");writeFileSync(mutant,source.replace(from,to));writeFileSync(overlay,JSON.stringify({Replace:{[path]:mutant}}));const run=spawnSync("go",["test","-json","-count=1","-overlay="+overlay,"-run=TestPreview","-timeout=10s","./internal/contracts"],{cwd:root,encoding:"utf8",windowsHide:true,timeout:30000});const output=run.stdout+run.stderr;if(run.error||run.signal||/test timed out|build failed|syntax error/i.test(output))throw new Error("Harness failure, not kill: "+name+"\n"+output);const events=run.stdout.split(/\r?\n/).flatMap(line=>{try{return[JSON.parse(line)];}catch{return[];}});if(run.status===0||!events.some(e=>e.Action==="fail"&&typeof e.Test==="string"))throw new Error("Survived/no assertion: "+name+"\n"+output);killed++;console.log("killed by assertion: "+name);}console.log("PASS "+killed+"/"+cases.length+" isolated overlay; compile/timeout kills0");}
finally{rmSync(temporary,{recursive:true,force:true});if(readFileSync(path,"utf8")!==original)throw new Error("Production source changed");}