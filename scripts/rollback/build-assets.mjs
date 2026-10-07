import fs from 'node:fs';
import path from 'node:path';
import {createRequire} from 'node:module';
import {pathToFileURL} from 'node:url';
const [workspace,oldSource]=process.argv.slice(2);
const frontend=path.join(oldSource,'frontend');
const installed=path.join(workspace,'frontend','node_modules');
const pkg=JSON.parse(fs.readFileSync(path.join(frontend,'package.json'),'utf8'));
const require=createRequire(path.join(workspace,'frontend','package.json'));
for(const [name,version] of Object.entries(pkg.dependencies)){
 const actual=JSON.parse(fs.readFileSync(path.join(installed,name,'package.json'),'utf8')).version;
 if(actual!==version)throw new Error(`Historical dependency mismatch ${name}: ${actual} != ${version}`);
}
const vite=await import(pathToFileURL(path.join(installed,'vite','dist','node','index.js')).href);
await vite.build({root:frontend,configFile:false,plugins:[{name:'historical-pinned-dependencies',enforce:'pre',resolveId(id){
 if(Object.keys(pkg.dependencies).some(name=>id===name || id.startsWith(name+'/')))return require.resolve(id);
}}],build:{outDir:path.join(frontend,'dist'),emptyOutDir:true},logLevel:'warn'});
console.log('Historical frontend exact source/pinned dependency build PASS');