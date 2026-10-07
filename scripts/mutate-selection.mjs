import { readFileSync,writeFileSync,mkdirSync,rmSync,existsSync } from "node:fs";
import { join,resolve,sep } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const root=fileURLToPath(new URL("../",import.meta.url));
const paths={selection:join(root,"internal","selection","selection.go"),items:join(root,"internal","selection","items.go")};
const originals=Object.fromEntries(Object.entries(paths).map(([name,path])=>[name,readFileSync(path,"utf8")]));
const source=Object.fromEntries(Object.entries(originals).map(([name,text])=>[name,text.replaceAll("\r\n","\n")]));
const cases=[
 ["include mismatch unclassified","selection",'group, reason := Unclassified, NoReason','group, reason := AutoExcluded, NoReason'],
 ["anonymous enable","selection",'filters.ExcludeAnonymous && value.Kind == Anonymous','false && value.Kind == Anonymous'],
 ["anonymous identity","selection",'filters.ExcludeAnonymous && value.Kind == Anonymous','filters.ExcludeAnonymous && value.Kind != Anonymous'],
 ["author enable","selection",'filters.ExcludeAuthor && author != nil && value.Key == *author','false && author != nil && value.Key == *author'],
 ["author absent evidence","selection",'filters.ExcludeAuthor && author != nil && value.Key == *author','filters.ExcludeAuthor && (author == nil || value.Key == *author)'],
 ["author structured identity","selection",'filters.ExcludeAuthor && author != nil && value.Key == *author','filters.ExcludeAuthor && author != nil && value.Key != *author'],
 ["dccon enable","selection",'filters.ExcludeDcconOnly && !hasText','false && !hasText'],
 ["dccon text witness","selection",'filters.ExcludeDcconOnly && !hasText','filters.ExcludeDcconOnly && hasText'],
 ["voice text witness","selection",'comment.Kind == Text || comment.Kind == Voice','comment.Kind == Text'],
 ["time minute boundary","selection",'comment.PostedAt.Before(*filters.TimeCut)','!comment.PostedAt.After(*filters.TimeCut)'],
 ["time early witness","selection",'filters.TimeCut != nil && !beforeCut','filters.TimeCut != nil && beforeCut'],
 ["unknown time fails closed","selection",'if missingTime {','if false && missingTime {'],
 ["keyword literal substring","selection",'strings.Contains(comment.Text, keyword)','strings.HasPrefix(comment.Text, keyword)'],
 ["keyword case sensitivity","selection",'strings.Contains(comment.Text, keyword)','strings.Contains(strings.ToLower(comment.Text), strings.ToLower(keyword))'],
 ["keyword second comment OR","selection",'for _, comment := range comments {','for _, comment := range comments[:min(1,len(comments))] {'],
 ["keyword second word OR","selection",'for _, keyword := range keywords {','for _, keyword := range keywords[:min(1,len(keywords))] {'],
 ["priority author before anonymous","selection",'case filters.ExcludeAnonymous && value.Kind == Anonymous:\n\t\tgroup, reason = AutoExcluded, AnonymousReason\n\tcase filters.ExcludeAuthor && author != nil && value.Key == *author:\n\t\tgroup, reason = AutoExcluded, AuthorReason','case filters.ExcludeAuthor && author != nil && value.Key == *author:\n\t\tgroup, reason = AutoExcluded, AuthorReason\n\tcase filters.ExcludeAnonymous && value.Kind == Anonymous:\n\t\tgroup, reason = AutoExcluded, AnonymousReason'],
 ["priority include before exclude","selection",'case contains(value.Comments, filters.ExcludeKeywords):\n\t\tgroup, reason = AutoExcluded, ExcludeKeywordReason\n\tcase contains(value.Comments, filters.IncludeKeywords):\n\t\tgroup, reason = AutoIncluded, IncludeKeywordReason','case contains(value.Comments, filters.IncludeKeywords):\n\t\tgroup, reason = AutoIncluded, IncludeKeywordReason\n\tcase contains(value.Comments, filters.ExcludeKeywords):\n\t\tgroup, reason = AutoExcluded, ExcludeKeywordReason'],
 ["manual default included","selection",'return ManualState{ManualIncluded: true}','return ManualState{ManualIncluded: false}'],
 ["manual nonexcluded source","selection",'included := value.Manual.ManualIncluded','included := value.Manual.OverrideExcluded'],
 ["manual excluded source","selection",'included = value.Manual.OverrideExcluded','included = value.Manual.ManualIncluded'],
 ["manual group dispatch","selection",'if group == AutoExcluded {','if group != AutoExcluded {'],
 ["manual excluded toggle","selection",'out[index].Manual.OverrideExcluded = !out[index].Manual.OverrideExcluded','out[index].Manual.OverrideExcluded = out[index].Manual.OverrideExcluded'],
 ["manual included toggle","selection",'out[index].Manual.ManualIncluded = !out[index].Manual.ManualIncluded','out[index].Manual.ManualIncluded = out[index].Manual.ManualIncluded'],
 ["bulk only unclassified","selection",'if row.Classification.Group == Unclassified {','if row.Classification.Group == Unclassified || row.Classification.Group != Unclassified {'],
 ["manual reset default","selection",'out[i].Manual = DefaultManual()','out[i].Manual = ManualState{}'],
 ["keyword trim","selection",'value := strings.TrimSpace(raw)','value := raw'],
 ["keyword UTF16 maximum","selection",'if UTF16Length(value) > 100 {','if UTF16Length(value) >= 100 {'],
 ["UTF16 surrogate pair","selection",'len(utf16.Encode([]rune(value)))','len([]rune(value))+0*len(utf16.Encode([]rune(value)))'],
 ["clone comment array","selection",'out[i].Comments = make([]Comment, len(value.Comments))','out[i].Comments = value.Comments'],
 ["clone parent pointer","selection",'value.ParentID = cloneString(value.ParentID)','value.ParentID = value.ParentID'],
 ["clone timestamp pointer","selection",'value.PostedAt = cloneTime(value.PostedAt)','value.PostedAt = value.PostedAt'],
 ["clone media array","selection",'value.MediaURLs = append([]string{}, value.MediaURLs...)','value.MediaURLs = value.MediaURLs'],
 ["clone filter cutoff","selection",'value.TimeCut = cloneTime(value.TimeCut)','value.TimeCut = value.TimeCut'],
 ["freeze author pointer","selection",'copy := *author\n\t\tauthorCopy = &copy','authorCopy = author'],
 ["freeze participant ownership","selection",'Evaluate(CloneParticipants(participants), checked, author)','Evaluate(participants, checked, author)'],
 ["initial eligible two","items",'(initial && eligible < 2)','(initial && eligible < 1)'],
 ["prize count zero","items",'item.Count < 1 || item.Count > 10','item.Count < 0 || item.Count > 10'],
 ["prize count ten","items",'item.Count < 1 || item.Count > 10','item.Count < 1 || item.Count >= 10'],
 ["prize eligible exact","items",'total > eligible','total >= eligible'],
 ["prize unique ID","items",'if _, duplicate := seen[item.ID]; duplicate {','if _, duplicate := seen[item.ID]; false && duplicate {'],
 ["prize empty name fallback","items",'if item.Name == "" {','if true {'],
 ["single configured ID","items",'id := items.SingleID','id := ""'],
 ["single configured name","items",'Name: items.SingleName','Name: ""'],
 ["prize list ownership","items",'selected = append([]Item{}, items.Multiple...)','selected = items.Multiple'],
 ["prize default display names","items",'if len(selected) == 1 {','if true {'],
 ["prize name twenty","items",'UTF16Length(item.Name) > 20','UTF16Length(item.Name) >= 20'],
 ["message twenty","items",'UTF16Length(message) > 20','UTF16Length(message) >= 20'],
 ["priority dccon before author","selection",'case filters.ExcludeAuthor && author != nil && value.Key == *author:\n\t\tgroup, reason = AutoExcluded, AuthorReason\n\tcase filters.ExcludeDcconOnly && !hasText:\n\t\tgroup, reason = AutoExcluded, DcconReason','case filters.ExcludeDcconOnly && !hasText:\n\t\tgroup, reason = AutoExcluded, DcconReason\n\tcase filters.ExcludeAuthor && author != nil && value.Key == *author:\n\t\tgroup, reason = AutoExcluded, AuthorReason'],
 ["priority time before dccon","selection",'case filters.ExcludeDcconOnly && !hasText:\n\t\tgroup, reason = AutoExcluded, DcconReason\n\tcase filters.TimeCut != nil && !beforeCut:\n\t\tif missingTime {\n\t\t\treturn Classification{}, invalid()\n\t\t}\n\t\tgroup, reason = AutoExcluded, TimeCutReason','case filters.TimeCut != nil && !beforeCut:\n\t\tif missingTime {\n\t\t\treturn Classification{}, invalid()\n\t\t}\n\t\tgroup, reason = AutoExcluded, TimeCutReason\n\tcase filters.ExcludeDcconOnly && !hasText:\n\t\tgroup, reason = AutoExcluded, DcconReason'],
 ["priority exclude before time","selection",'case filters.TimeCut != nil && !beforeCut:\n\t\tif missingTime {\n\t\t\treturn Classification{}, invalid()\n\t\t}\n\t\tgroup, reason = AutoExcluded, TimeCutReason\n\tcase contains(value.Comments, filters.ExcludeKeywords):\n\t\tgroup, reason = AutoExcluded, ExcludeKeywordReason','case contains(value.Comments, filters.ExcludeKeywords):\n\t\tgroup, reason = AutoExcluded, ExcludeKeywordReason\n\tcase filters.TimeCut != nil && !beforeCut:\n\t\tif missingTime {\n\t\t\treturn Classification{}, invalid()\n\t\t}\n\t\tgroup, reason = AutoExcluded, TimeCutReason'],
];
const selected=process.argv[2];const start=selected?.startsWith("from:")?cases.findIndex(([name])=>name===selected.slice(5)):-1;
const chosen=selected?.startsWith("from:")?(start<0?[]:cases.slice(start)):cases.filter(([name])=>selected===undefined||selected===name);
if(chosen.length===0)throw new Error("Unknown selection mutation");
const parent=resolve(root,".task","selection-mutations");const temporary=resolve(parent,"run-"+process.pid+"-"+Date.now());
if(!temporary.startsWith(parent+sep))throw new Error("Unsafe temporary mutation path");mkdirSync(temporary,{recursive:true});
let survivors=0;let killed=0;
try{
 for(const [name,kind,from,to]of chosen){
  const original=source[kind];if(original.split(from).length!==2)throw new Error("Mutation target drift: "+name);
  const altered=join(temporary,"mutant.go");const overlay=join(temporary,"overlay.json");
  writeFileSync(altered,original.replace(from,to));writeFileSync(overlay,JSON.stringify({Replace:{[paths[kind]]:altered}}));
  const result=spawnSync("go",["test","-json","-overlay="+overlay,"-count=1","-run=Test(Independent|Automatic|Manual|Keywords|Item|Multiple|Message|Single|Freeze|BuildGroups|BuildClones|Malformed|KST|AddRemove)","-timeout=10s","./internal/selection"],{cwd:root,encoding:"utf8",timeout:30000,windowsHide:true});
  const output=result.stdout+result.stderr;
  if(result.error||result.signal||/test timed out|\[build failed\]|syntax error/i.test(output))throw new Error("Mutation harness failure: "+name+"\n"+output);
  const events=result.stdout.split(/\r?\n/).flatMap((line)=>{try{return [JSON.parse(line)];}catch{return [];}});
  const testRuns=events.filter((event)=>event.Action==="run"&&typeof event.Test==="string");
  if(testRuns.length===0)throw new Error("No tests executed: "+name+"\n"+output);
  const failedTests=events.filter((event)=>event.Action==="fail"&&typeof event.Test==="string");
  if(result.status!==0&&failedTests.length===0)throw new Error("No assertion test failure: "+name+"\n"+output);
  if(result.status===0){survivors++;console.log("SURVIVED: "+name);}else{killed++;console.log("killed: "+name);}
 }
 console.log("Selection mutations: "+killed+" killed, "+survivors+" survived, "+chosen.length+" checked");
}finally{
 // This exact directory was freshly created above. Cleanup never targets a
 // caller supplied path or the shared original source tree.
 if(resolve(temporary)!==temporary||!temporary.startsWith(parent+sep))throw new Error("Unsafe mutation cleanup target");
 if(existsSync(temporary))rmSync(temporary,{recursive:true,force:true});
 for(const [kind,path]of Object.entries(paths))if(readFileSync(path,"utf8")!==originals[kind])throw new Error("Shared selection source changed during isolated run: "+kind);
}
if(survivors>0)process.exitCode=1;