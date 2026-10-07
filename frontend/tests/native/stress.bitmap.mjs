// Shadow-source diagnostic only. Observe native ownership transfer/close;
// never replace the PNG decoder, transfer, display or memory allocator.
export function installBitmapProbe(host=window){
 if(host.__jackpotBitmapProbe!==undefined)throw Error("Bitmap probe already owned");
 const create=host.createImageBitmap,close=host.ImageBitmap.prototype.close,transfer=host.ImageBitmapRenderingContext.prototype.transferFromImageBitmap;
 const references=[],owned=new WeakMap();let alive=true,created=0,closed=0,transferred=0,failures=0,pending=0;
 const wrappedCreate=function(...args){const result=Reflect.apply(create,this,args);if(alive&&args[0] instanceof host.Blob&&args[0].type==="image/png"){pending++;void Promise.resolve(result).then(bitmap=>{pending--;if(!alive)return;const entry={width:bitmap.width,height:bitmap.height,closed:false,transferred:false,detachedAfterTransfer:false};owned.set(bitmap,entry);references.push({reference:new WeakRef(bitmap),entry});if(references.length>128)references.shift();created++;},()=>{pending--;if(alive)failures++;});}return result;};
 const wrappedClose=function(...args){const result=Reflect.apply(close,this,args);const entry=owned.get(this);if(alive&&entry!==undefined&&!entry.closed){entry.closed=true;closed++;}return result;};
 const wrappedTransfer=function(bitmap){const result=Reflect.apply(transfer,this,[bitmap]);const entry=owned.get(bitmap);if(alive&&entry!==undefined){entry.transferred=true;entry.detachedAfterTransfer=bitmap.width===0&&bitmap.height===0;transferred++;}return result;};
 host.createImageBitmap=wrappedCreate;host.ImageBitmap.prototype.close=wrappedClose;host.ImageBitmapRenderingContext.prototype.transferFromImageBitmap=wrappedTransfer;
 const owner={snapshot:()=>({created,closed,transferred,failures,pending,decodedLive:references.filter(({reference})=>{const bitmap=reference.deref();return bitmap!==undefined&&bitmap.width*bitmap.height>0;}).length,records:references.map(({entry})=>({...entry}))}),dispose:()=>{if(!alive)return;alive=false;if(host.createImageBitmap!==wrappedCreate||host.ImageBitmap.prototype.close!==wrappedClose||host.ImageBitmapRenderingContext.prototype.transferFromImageBitmap!==wrappedTransfer)throw Error("Bitmap probe ownership lost");host.createImageBitmap=create;host.ImageBitmap.prototype.close=close;host.ImageBitmapRenderingContext.prototype.transferFromImageBitmap=transfer;references.length=0;delete host.__jackpotBitmapProbe;}};
 host.__jackpotBitmapProbe=owner;return owner;
}
export async function readPreviewCanvas(canvas,host=window){
 if(canvas.tagName!=="CANVAS"||canvas.width!==1200||!Number.isSafeInteger(canvas.height)||canvas.height<=0||canvas.height>8192||canvas.hidden||!canvas.isConnected)throw Error("Actual visible bounded preview Canvas required");
 const width=canvas.width,height=canvas.height;const copy=new host.OffscreenCanvas(width,height);
 try{const context=copy.getContext("2d",{willReadFrequently:true});if(context===null)throw Error("Preview pixel readback unavailable");context.drawImage(canvas,0,0);const pixels=context.getImageData(0,0,width,height).data;if(pixels.byteLength!==width*height*4)throw Error("Preview RGBA dimensions mismatch");const digest=await host.crypto.subtle.digest("SHA-256",pixels);return{width,height,rgbaBytes:pixels.byteLength,rgbaSHA256:[...new Uint8Array(digest)].map(byte=>byte.toString(16).padStart(2,"0")).join("")};}
 finally{copy.width=0;copy.height=0;}
}