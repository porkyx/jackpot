import {mkdtempSync,writeFileSync,rmSync} from "node:fs";
import {join,resolve} from "node:path";
import {fileURLToPath} from "node:url";
import {spawnSync} from "node:child_process";
const root=fileURLToPath(new URL("../../",import.meta.url));
const temporary=mkdtempSync(join(root,".task","preview-oracle-"));
const source=join(temporary,"main.go");
const program=`package main
import("encoding/json";"os";"strings";"github.com/porkyx/jackpot/internal/contracts")
func main(){ cases:=[]string{"",strings.Repeat("가",256),strings.Repeat("가",257),strings.Repeat("😀",129),strings.Repeat("a",250)+"👩‍👩‍👧‍👦",strings.Repeat("a",255)+"e\\u0301",strings.Repeat("a",255)+"\\r\\n",strings.Repeat("a",253)+"a\\U0001e5eexx",strings.Repeat("a",253)+"a\\U0001e6e3xx",strings.Repeat("a",253)+"\\U0001e5e8xx","a"+strings.Repeat("\\u0301",300),strings.Repeat("a",250)+"🏴\\U000e0067\\U000e0062\\U000e0065\\U000e006e\\U000e0067\\U000e007f"}; rows:=make([]map[string]string,0,len(cases));for _,input:=range cases{preview,err:=contracts.PreviewText(input);if err!=nil{panic(err)};rows=append(rows,map[string]string{"input":input,"preview":preview})};if err:=json.NewEncoder(os.Stdout).Encode(rows);err!=nil{panic(err)}}`;
try{
 writeFileSync(source,program);
 const run=spawnSync("go",["run",source],{cwd:root,encoding:"utf8",timeout:60000,windowsHide:true});
 if(run.error||run.signal||run.status!==0)throw new Error("Go oracle production call failed: "+run.stdout+run.stderr);
 const rows=JSON.parse(run.stdout);const segmenter=new Intl.Segmenter("ko",{granularity:"grapheme"});
 for(const row of rows){
  if(row.preview.length>256)throw new Error("UTF16 bound");
  if(row.input.length<=256){if(row.preview!==row.input)throw new Error("Unnecessary truncation");continue;}
  if(!row.preview.endsWith("…"))throw new Error("Missing ellipsis");
  const prefix=row.preview.slice(0,-1);const ends=new Set([0,...[...segmenter.segment(row.input)].map(s=>s.index+s.segment.length)]);
  if(!row.input.startsWith(prefix)||!ends.has(prefix.length))throw new Error("Independent Intl boundary violation "+JSON.stringify(row));
 }
 const report={node:process.version,unicode:process.versions.unicode,icu:process.versions.icu,supportsUnicode16Extend:[...segmenter.segment("a\u{1e5ee}")].length===1,supportsUnicode17Extend:[...segmenter.segment("a\u{1e6e3}")].length===1,rows};
 writeFileSync(resolve(root,".task/participant-preview-oracle.json"),JSON.stringify(report,null,2));
 console.log("PASS "+rows.length+" Go production previews checked by Node Intl.Segmenter; "+JSON.stringify({...report,rows:undefined}));
}finally{rmSync(temporary,{recursive:true,force:true});}