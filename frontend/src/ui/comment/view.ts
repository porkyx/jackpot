import {Effect,Exit,Scope}from"effect";
import {ProtocolError}from"../../contracts/backend";
import {isSafeDcMediaUrl}from"../../contracts/media";
import type {CommentData}from"../../contracts/product";
import {DomError,DomPlatform}from"../../platform/dom";
import {patchText,type RowView,ViewUnavailable}from"../view";
type Comment=typeof CommentData.Type;
// A comment's persisted media identity is immutable within its pinned snapshot.
// The caller's keyed row Scope owns the network src and native event listeners.
export function makeCommentRow(comment:Comment,doc:Document,metadata:(value:Comment)=>string,tag:"div"|"li"="div"){
 return Effect.gen(function*(){
  const parent=yield*Scope.Scope;if(parent.state._tag==="Closed")return yield*Effect.fail(new ViewUnavailable({reason:"closed"}));
  const scope=yield*Scope.fork(parent,"sequential");
  return yield*Effect.gen(function*(){
   if(!comment.mediaUrls.every(isSafeDcMediaUrl))return yield*Effect.fail(new ProtocolError());
   const dom=yield*DomPlatform;const urls=[...comment.mediaUrls];const element=doc.createElement(tag);element.dataset.commentId=comment.id;
   const meta=doc.createElement("p");const body=doc.createElement("p");element.append(meta,body);const images:HTMLImageElement[]=[];let alive=true;
   yield*Effect.addFinalizer(()=>Effect.sync(()=>{alive=false;for(const image of images){image.removeAttribute("src");image.remove();}images.length=0;}));
   if(comment.kind==="dccon")for(const url of new Set(urls)){
    const image=doc.createElement("img");image.alt="디시콘 이미지";image.loading="lazy";image.decoding="async";image.referrerPolicy="no-referrer";image.width=100;image.height=100;images.push(image);let failed=false;
    yield*dom.listen(image,"load",()=>{if(alive&&!failed)image.hidden=false;});
    yield*dom.listen(image,"error",()=>{if(alive){failed=true;image.hidden=true;image.removeAttribute("src");}});
    image.src=url;element.append(image);
   }
   const patch=(value:Comment)=>{
    if(!alive)throw new ViewUnavailable({reason:"closed"});
    if(value.id!==comment.id||value.kind!==comment.kind||value.mediaUrls.length!==urls.length||value.mediaUrls.some((url,index)=>url!==urls[index]))throw new DomError();
    patchText(meta,metadata(value));const label=value.kind==="dccon"?"[디시콘]":value.kind==="voice"?"[보플]":"";
    patchText(body,label===""?value.text:value.text===""||value.text===label?label:label+" "+value.text);
   };
   patch(comment);return{element,value:{patch}}satisfies RowView<Comment>;
  }).pipe(Scope.provide(scope),Effect.onExit(exit=>Exit.isFailure(exit)?Scope.close(scope,exit):Effect.void));
 });
}