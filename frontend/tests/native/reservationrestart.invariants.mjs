import assert from "node:assert/strict";
export function normalizeReservationReport(raw){
 assert.ok(raw!==null&&typeof raw==="object");
 const rawCounts=raw.counts??raw.Counts;
 assert.ok(rawCounts!==null&&typeof rawCounts==="object");
 const counts=Object.fromEntries(Object.entries(rawCounts).map(([key,value])=>{assert.ok(Number.isSafeInteger(value)&&value>=0);return[key[0].toLowerCase()+key.slice(1),value]}));
 for(const key of ["collections","operations","rounds","attempts","results","winners","participants"])assert.ok(Object.hasOwn(counts,key),"Required durable count missing");
 const fixtureCalls=raw.fixtureCalls??raw.FixtureCalls,fixtureBodiesClosed=raw.fixtureBodiesClosed??raw.FixtureBodiesClosed;
 assert.ok(Number.isSafeInteger(fixtureCalls)&&fixtureCalls>=0&&Number.isSafeInteger(fixtureBodiesClosed)&&fixtureBodiesClosed>=0);
 const latest=raw.latest??raw.Latest;
 assert.ok(Array.isArray(latest));
 const {Counts,FixtureCalls,FixtureBodiesClosed,Latest,...rest}=raw;
 return {...rest,counts,latest,fixtureCalls,fixtureBodiesClosed};
}
export function immutableReservationRound(round){
 assert.ok(round!==null&&typeof round==="object");
 // revision belongs to the current owner collection; roundVersion and all
 // durable input/outcome/time fields remain part of the immutable comparison.
 const {revision,...durable}=round;
 assert.ok(Number.isSafeInteger(revision)&&revision>0);
 return durable;
}
export function requireDistinctReservationWinners(collection){
 assert.ok(collection!==null&&typeof collection==="object"&&Array.isArray(collection.rounds));
 const ids=collection.rounds.flatMap(round=>round.winners.map(winner=>winner.participant.id));
 assert.ok(ids.every(id=>typeof id==="string"&&id.length>0));
 assert.equal(new Set(ids).size,ids.length);
 return ids;
}