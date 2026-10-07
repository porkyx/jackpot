// Test-only instrumentation, installed before the copied development entry runs.
// Weak DOM references and primitive observations never store passwords or IDs.
export function installHmrProbe() {
  const details=new Map(),targets=new WeakMap(),callbacks=new WeakMap();let serial=0;
  const identity=(map,value)=>{let id=map.get(value);if(id===undefined){id=++serial;map.set(value,id);}return id;};
  const key=(target,type,callback,options)=>identity(targets,target)+":"+type+":"+identity(callbacks,callback)+":"+(typeof options==="boolean"?options:Boolean(options?.capture));
  const add=EventTarget.prototype.addEventListener,remove=EventTarget.prototype.removeEventListener;
  EventTarget.prototype.addEventListener=function(type,callback,options){const result=add.call(this,type,callback,options);if(callback!==null&&(this instanceof Node||this===window))details.set(key(this,type,callback,options),{target:new WeakRef(this),callback:new WeakRef(callback),type,capture:typeof options==="boolean"?options:Boolean(options?.capture)});return result;};
  EventTarget.prototype.removeEventListener=function(type,callback,options){const result=remove.call(this,type,callback,options);if(callback!==null&&(this instanceof Node||this===window))details.delete(key(this,type,callback,options));return result;};
  const frames=new Set(),raf=requestAnimationFrame,caf=cancelAnimationFrame;
  window.requestAnimationFrame=callback=>{const id=raf(time=>{frames.delete(id);callback(time);});frames.add(id);return id;};
  window.cancelAnimationFrame=id=>{caf(id);frames.delete(id);};
  const urls=new Set(),create=URL.createObjectURL,revoke=URL.revokeObjectURL;
  URL.createObjectURL=blob=>{const url=create.call(URL,blob);urls.add(url);return url;};
  URL.revokeObjectURL=url=>{revoke.call(URL,url);urls.delete(url);};
  const canvases=[],element=Document.prototype.createElement;
  Document.prototype.createElement=function(name,options){const result=element.call(this,name,options);if(name.toLowerCase()==="canvas")canvases.push(new WeakRef(result));return result;};
  const resources=()=>({listeners:details.size,frames:frames.size,urls:urls.size,canvasPixels:canvases.reduce((sum,ref)=>{const canvas=ref.deref();return sum+(canvas===undefined?0:canvas.width*canvas.height);},0),nodes:document.querySelectorAll("*").length});
  let opened=0,closed=0,active=0,maxActive=0,failures=0,current,workspace,permit,gateEntered=false,holding=false;
  const closures=[],coordinators=[];
  const emit=value=>{void window.__hmrRecord?.(value);};
  window.__hmrProbe={
    open(){active++;opened++;maxActive=Math.max(maxActive,active);return opened;},
    register(owner){current=owner;}, workspace(value){workspace=value;},
    hold(){holding=true;gateEntered=false;},release(){holding=false;permit?.();permit=undefined;},
    gate(){if(!holding)return Promise.resolve();gateEntered=true;return new Promise(resolve=>{permit=resolve;});},
    coordinator(value){coordinators.push(value);if(coordinators.length>110)coordinators.shift();},
    failure(){failures++;},
    closed(token,scopes,startupDone){active--;closed++;const editor=workspace?.read();const value={token,scopes,startupDone,workspaceClosed:editor?.closed===true,editorNull:editor?.editor===null,draftNull:editor?.draft===null,keywordEmpty:editor?.keywordRaw.include===""&&editor?.keywordRaw.exclude==="",resources:resources()};closures.push(value);if(closures.length>110)closures.shift();current=undefined;workspace=undefined;emit({kind:"closed",...value});},
    snapshot:()=>({opened,closed,active,maxActive,failures,gateEntered,phase:current?.phase(),closures,coordinators,resources:resources()}),
    nativeDetails:read=>[...details.values()].flatMap(entry=>{const target=entry.target.deref(),callback=entry.callback.deref();if(target===undefined||callback===undefined)return[];const registered=(read(target)[entry.type]??[]).some(value=>value.listener===callback&&value.useCapture===entry.capture);return[{registered,connected:target===window||target.isConnected===true}];}),
    dispose:()=>current?.dispose(),
  };
}
