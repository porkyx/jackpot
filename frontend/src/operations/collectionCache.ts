import type { Collection } from "../contracts/product";
import type { ResultReference } from "../contracts/schemas";

export interface ConfirmedCollection {
 readonly collectionId: string;
 readonly revision: number;
 readonly title: string | null;
 readonly galleryName: string | null;
 readonly result: typeof ResultReference.Type | null;
}
// The cache contains only public summaries and result references. It never
// retains a full Collection, authority, password, participant or comment page.
export function makeCollectionCache() {
 const entries=new Map<string,ConfirmedCollection>();
 const store=(entry:ConfirmedCollection)=>{
  entries.delete(entry.collectionId);entries.set(entry.collectionId,Object.freeze(entry));
  if(entries.size>16){const oldest=entries.keys().next().value;if(oldest===undefined){entries.delete(entry.collectionId);throw new Error("Non-empty confirmed LRU has no oldest key");}entries.delete(oldest);}
 };
 const get=(id:string)=>{
  const entry=entries.get(id);
  if(entry!==undefined){entries.delete(id);entries.set(id,entry);}
  return entry;
 };
 const remember=(collection:Collection)=>{
  const current=entries.get(collection.collectionId);
  if(current!==undefined&&collection.revision<current.revision)return false;
  const latest=collection.latestRound;
  store({collectionId:collection.collectionId,revision:collection.revision,title:collection.article.title,galleryName:collection.article.galleryName,
   result:Object.freeze({collectionId:collection.collectionId,roundId:latest.roundId,revision:latest.revision})});
  return true;
 };
 const seed=(reference:typeof ResultReference.Type)=>{
  const current=entries.get(reference.collectionId);
  if(current!==undefined&&reference.revision<=current.revision)return;
  store({collectionId:reference.collectionId,revision:reference.revision,title:current?.title??null,galleryName:current?.galleryName??null,
   result:Object.freeze({collectionId:reference.collectionId,roundId:reference.roundId,revision:reference.revision})});
 };
 return {get,remember,seed,clear:()=>entries.clear(),size:()=>entries.size};
}
