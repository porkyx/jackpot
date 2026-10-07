import { expect, it } from "@effect/vitest";
import { Deferred, Effect, Exit, Fiber, Schema, Scope } from "effect";
import { CollectionData, type Collection } from "../../src/contracts/product";
import { makeWailsProductBackend, type WailsProductPort } from "../../src/platform/product";
import { assertQuerySize, MaximumQueryBytes } from "../../src/platform/querySize";
import { makeProductCoordinator } from "../../src/operations/productCoordinator";
import { makeCollectionCache } from "../../src/operations/collectionCache";
import { backendHarness, bootstrap, collection, reply } from "../helpers/productBackend";
import { draft } from "../helpers/productCommands";
import { round } from "../helpers/round";

function page(total = 101, offset = 0, limit = 50): Collection {
 const count = Math.min(limit, Math.max(0, total - offset));
 return { ...collection(), roundTotal: total, roundOffset: offset, latestRound: round({ roundId: "r" + total, number: total }),
  rounds: Array.from({ length: count }, (_, i) => round({ roundId: "r" + (total - offset - count + i + 1), number: total - offset - count + i + 1 })) };
}
function harness(data: unknown) {
 const calls: unknown[][] = []; let raw: unknown = { protocolVersion: 1, backendSessionId: "session", occurredAt: "2026-10-06T00:00:00Z", ok: true, data };
 const port = new Proxy({} as WailsProductPort, { get: () => (...args: unknown[]) => { calls.push(args); return Promise.resolve(raw); } });
 return { calls, backend: makeWailsProductBackend(port), set: (value: unknown) => { raw = value; } };
}
for (const [total, offset, limit] of [[1, 0, 0], [49, 0, 0], [50, 0, 0], [51, 0, 50], [101, 50, 50], [101, 100, 50], [101, 101, 50], [101, 4294967295, 50], [3, 1, 1]] as const)
 it.effect(`round query accepts total ${total} offset ${offset} limit ${limit} without hiding latest`, () => Effect.gen(function*() {
  const data = page(total, offset, limit || 50); const h = harness(data);
  const result = yield* h.backend.getCollection("collection", { roundOffset: offset, roundLimit: limit });
  expect(result.data).toEqual(data); expect(result.data.latestRound.number).toBe(total); expect(h.calls[0]?.slice(0, 2)).toEqual(["collection", { roundOffset: offset, roundLimit: limit }]);
 }));
it.effect("round anchor adopts resolved older page offset without changing the selected round", () => Effect.gen(function*() {
 const h = harness(page(101, 100)); const result = yield* h.backend.getCollection("collection", { roundId: "r1" });
 expect(result.data.roundOffset).toBe(100); expect(result.data.rounds.map(r => r.roundId)).toEqual(["r1"]); expect(result.data.latestRound.roundId).toBe("r101");
}));
for (const options of [{ roundOffset: -1 }, { roundOffset: 0.5 }, { roundOffset: 4294967296 }, { roundLimit: -1 }, { roundLimit: 51 }, { roundLimit: 1.5 }, { roundId: 1 as unknown as string }, { roundId: "r1", roundOffset: 1 }])
 it.effect("invalid round page request rejects before any IPC " + JSON.stringify(options), () => Effect.gen(function*() {
  const h = harness(page()); expect((yield* Effect.result(h.backend.getCollection("collection", options)))._tag).toBe("Failure"); expect(h.calls).toHaveLength(0);
 }));
for (const [name, change] of [["foreign collection", { collectionId: "other" }], ["missing anchor", { rounds: page().rounds }], ["wrong offset", { roundOffset: 50, rounds: page(101, 50).rounds }], ["missing rows", { rounds: [] }]] as const)
 it.effect("round query rejects " + name + " response", () => Effect.gen(function*() {
  const h = harness({ ...page(), ...change }); const options = name === "missing anchor" ? { roundId: "r1" } : {};
  expect((yield* Effect.result(h.backend.getCollection("collection", options)))._tag).toBe("Failure"); expect(h.calls).toHaveLength(1);
 }));
for (const [name, changed] of [["51 rows", { rounds: page(51, 0, 51).rounds }], ["null latest", { latestRound: null }], ["missing latest", { latestRound: undefined }], ["zero total", { roundTotal: 0 }], ["foreign latest", { latestRound: round({ collectionId: "foreign", number: 101 }) }], ["reversed rows", { rounds: [...page().rounds].reverse() }], ["duplicate row", { rounds: page().rounds.map(() => page().rounds[0]) }], ["duplicate identity", { rounds: page().rounds.map(row=>({...row,roundId:"same"})) }], ["different latest page identity", { latestRound:{...page().latestRound,roundId:"different"} }], ["different latest revision", { latestRound:{...page().latestRound,revision:2} }], ["different latest version", { latestRound:{...page().latestRound,roundVersion:2} }], ["different latest state", { latestRound:{...page().latestRound,state:"failed"} }]] as const)
 it("CollectionData rejects " + name, () => { expect(() => Schema.decodeUnknownSync(CollectionData)({ ...page(), ...changed })).toThrow(); });
it("CollectionData permits a past page while cache retains only latest management reference", () => {
 const data = page(101, 50); expect(Schema.decodeUnknownSync(CollectionData)(data)).toEqual(data);
 const cache = makeCollectionCache(); cache.remember(data); expect(cache.get("collection")?.result?.roundId).toBe("r101");
 expect(JSON.stringify(cache.get("collection"))).not.toContain("r51");
});
it("CollectionData fifty-one otherwise valid rounds exceed the independent page cap",()=>{expect(()=>Schema.decodeUnknownSync(CollectionData)(page(51,0,51))).toThrow();});
const escapedJson = (raw: unknown) => JSON.stringify(raw).replace(/[<>&\u2028\u2029]/g, value => "\\u" + value.charCodeAt(0).toString(16).padStart(4, "0"));
for (const raw of [null, true, false, 0, 4294967295, "", "plain ASCII !#%()/0123456789:;=?@[]^_`{|}~", "\u007f\u0080\u07ff\u0800", "한글😀\"\\\b\t\n\f\r\u0000<> &\u2028\u2029", { "키😀": [null, 1, "\u0001"], omitted: undefined }, [[], {}, ["x"]]])
 it("query byte counter matches independently encoded escaped UTF8 " + String(raw).slice(0, 20), () => {
  const bytes = new TextEncoder().encode(escapedJson(raw)).byteLength;
  expect(() => assertQuerySize(raw, bytes)).not.toThrow(); expect(() => assertQuerySize(raw, bytes - 1)).toThrow();
 });
it("query size accepts exactly 8MiB and rejects 8MiB plus one before schema decoding", () => {
 expect(MaximumQueryBytes).toBe(8388608);
 expect(() => assertQuerySize({ x: "a".repeat(8388608 - 8) })).not.toThrow();
 expect(() => assertQuerySize({ x: "a".repeat(8388608 - 7) })).toThrow();
});
it("query byte counting rejects cycles, excessive depth and non-JSON values without recursion overflow", () => {
 const cycle: Record<string, unknown> = {}; cycle.self = cycle;
 let deep: unknown = null; for (let i = 0; i < 66; i++) deep = [deep];
 for (const value of [cycle, deep, undefined, Infinity, 1n, () => {}]) expect(() => assertQuerySize(value)).toThrow();
 expect(() => assertQuerySize(["\ud800", "\udfff"], 19)).not.toThrow();
 for(const maximum of[-1,NaN,Infinity,0.5])expect(()=>assertQuerySize(null,maximum)).toThrow();
 const reused={x:"y"};expect(()=>assertQuerySize([reused,reused])).not.toThrow();
 const inherited=Object.create({notOwned:"x".repeat(100)}) as Record<string,unknown>;inherited.own=true;expect(()=>assertQuerySize(inherited,12)).not.toThrow();
});
it.effect("anchored reply rejects an unaligned resolved page despite containing the requested round",()=>Effect.gen(function*(){const h=harness(page(101,17));expect((yield*Effect.result(h.backend.getCollection("collection",{roundId:"r70"})))._tag).toBe("Failure");}));
it.effect("pending collection transport is aborted once on query owner cancellation",()=>Effect.gen(function*(){
 const started=yield*Deferred.make<AbortSignal>();let calls=0,cancel=0;
 const port=new Proxy({}as WailsProductPort,{get:()=> (...args:unknown[])=>{calls++;const signal=args.at(-1)as AbortSignal;Deferred.doneUnsafe(started,Effect.succeed(signal));return new Promise((_resolve,reject)=>signal.addEventListener("abort",()=>{cancel++;reject(new Error("private failure"));},{once:true}));}});
 const read=yield*makeWailsProductBackend(port).getCollection("collection",{roundOffset:50}).pipe(Effect.forkScoped);const signal=yield*Deferred.await(started);yield*Fiber.interrupt(read);expect(signal.aborted).toBe(true);expect([calls,cancel]).toEqual([1,1]);
}));
for(const failing of[[1],[2],[1,2,3]])it.effect("query first/Nth/continuous malformed response preserves bounded request count "+failing.join(","),()=>Effect.gen(function*(){
 const h=harness(page());for(let call=1;call<=3;call++){h.set(failing.includes(call)?null:{protocolVersion:1,backendSessionId:"session",occurredAt:"2026-10-06T00:00:00Z",ok:true,data:page()});expect((yield*Effect.result(h.backend.getCollection("collection")))._tag).toBe(failing.includes(call)?"Failure":"Success");}expect(h.calls).toHaveLength(3);
}));
it.effect("oversized query response never reaches Schema while mutation outcome keeps its durable semantics", () => Effect.gen(function*() {
 const h = harness(collection()); const raw = { protocolVersion: 1, backendSessionId: "session", occurredAt: "2026-10-06T00:00:00Z", ok: true, operationId: "operation", data: collection(), extra: ">".repeat(Math.ceil(MaximumQueryBytes / 6)) };
 h.set(raw); const read = yield* Effect.result(h.backend.getCollection("collection")); expect(read._tag).toBe("Failure"); if (read._tag === "Failure") expect(read.failure._tag).toBe("ProtocolError");
 const result = yield* h.backend.rerun({ protocolVersion: 1, backendSessionId: "session", operationId: "operation", expectedRevision: 1, collectionId: "collection", roundId: "round", expectedVersion: 1, prizes: [], message: "", mode: "immediate", scheduledAt: null });
 expect(result.data.collectionId).toBe("collection"); expect(h.calls).toHaveLength(2);
}));
it.effect("collection page read is fenced by backend session replacement and closes owned pending read", () => Effect.gen(function*() {
 const source = backendHarness(); const scope = yield* Scope.make(); const owner = yield* makeProductCoordinator(source).pipe(Scope.provide(scope));
 try {
  yield* owner.acceptBootstrap(bootstrap()); const held = yield* Deferred.make<ReturnType<typeof reply<Collection>>>();
  source.override("getCollection", Deferred.await(held)); const fiber = yield* owner.getCollection("collection", { roundOffset: 50 }).pipe(Effect.forkScoped); yield* source.wait("getCollection");
  const next = draft({ summary: { ...draft().summary, backendSessionId: "next", draftId: "next-draft" } }); source.set(next); yield* owner.acceptBootstrap(bootstrap(next, "next"));
  yield* Deferred.succeed(held, reply(page(101, 50))); expect((yield* Effect.result(Fiber.join(fiber)))._tag).toBe("Failure");
  yield* Scope.close(scope, Exit.void); expect((yield* Effect.result(owner.getCollection("collection")))._tag).toBe("Failure"); expect(source.count("getCollection")).toBe(1);
 } finally { yield* Scope.close(scope, Exit.void); }
}));
