import { expect, test } from "vitest";
import { hashForRoute, parseHashRoute } from "../../src/app/routes";
import type { Route } from "../../src/app/shellState";

test.each(["", "#/create"])("create route %j is explicit and immutable", (hash) => {
  const route = parseHashRoute(hash);
  expect(route).toEqual({ _tag: "create" });
  expect(Object.isFrozen(route)).toBe(true);
  expect(hashForRoute({ _tag: "create" })).toBe("#/create");
});
test("removed history route is explanatory and cannot become a product route", () => {
  expect(parseHashRoute("#/history")).toEqual({ _tag: "not_found", hash: "#/history" });
  expect(() => hashForRoute({ _tag: "history" } as unknown as Exclude<Route, { readonly _tag: "not_found" }>)).toThrow("Unknown route");
});
test.each(["c-1", "한글 품목", "a/b", "?x#part", "100%", "<script>", "+", "💫"])("result ID %j round-trips as one URI component", (id) => {
  const hash = hashForRoute({ _tag: "result", collectionId: id, roundId: null });
  expect(parseHashRoute(hash)).toEqual({ _tag: "result", collectionId: id, roundId: null });
  expect(Object.isFrozen(parseHashRoute(hash))).toBe(true);
});
test.each(["#", "#/", "create", "#/Create", "#/create/", "#/create?x=1", "#/history/", "#/result/c", "#/results/", "#/results/a/b", "#/results/a?b", "#/results/a#b", "https://example.com/#/create", "//example.com/#/history", "javascript:alert(1)", "#/results/%", "#/results/%0", "#/results/%GG", "#/results/%C0%AF", "#/results/%ED%A0%80", "#/results/%00", "#/results/%1F", "#/results/%7f", "#/results/a%0Ab"])("unknown or malformed route %j remains explanatory without navigation", (hash) => {
  const route = parseHashRoute(hash);
  expect(route).toEqual({ _tag: "not_found", hash });
  expect(Object.isFrozen(route)).toBe(true);
});
test.each(["", "\u0000", "a\u001fb", "\u007f"])("invalid result identity %j fails loudly when making a link", (id) => {
  expect(() => hashForRoute({ _tag: "result", collectionId: id, roundId: null })).toThrow("identity is invalid");
});
test("unknown typed tag and unpaired surrogate do not produce a misleading result link", () => {
  expect(() => hashForRoute({ _tag: "unexpected" } as unknown as Exclude<Route, { readonly _tag: "not_found" }>)).toThrow("Unknown route");
  expect(() => hashForRoute({ _tag: "result", collectionId: "\ud800", roundId: null })).toThrow(URIError);
});
test("explicit round anchor remains a read-only identity in the collection route", () => {
  expect(hashForRoute({ _tag: "result", collectionId: "collection", roundId: "round-2" })).toBe("#/results/collection/rounds/round-2");
  expect(parseHashRoute("#/results/collection/rounds/round-2")).toEqual({_tag:"result",collectionId:"collection",roundId:"round-2"});
});
test.each(["r-1","한글","a/b","?x#y","💫","100%","javascript:alert(1)"])("explicit round ID %j is encoded as one local URI component",roundId=>{const route={_tag:"result"as const,collectionId:"c/한글",roundId};const hash=hashForRoute(route);expect(hash.startsWith("#/results/")).toBe(true);expect(parseHashRoute(hash)).toEqual(route);expect(Object.isFrozen(parseHashRoute(hash))).toBe(true);});
test.each(["", "\u0000", "a\u001fb", "\u007f"])("invalid anchor identity %j cannot create a link",roundId=>{expect(()=>hashForRoute({_tag:"result",collectionId:"c",roundId})).toThrow("Round route identity is invalid");});
test.each(["#/results/c/rounds/","#/results/c/rounds","#/results/c/rounds/r/extra","#/results/c/rounds/r?x","#/results/c/rounds/r#part","#/results/c/rounds/%","#/results/c/rounds/%0","#/results/c/rounds/%GG","#/results/c/rounds/%C0%AF","#/results/c/rounds/%ED%A0%80","#/results/c/rounds/%00","#/results/c/rounds/%1f","#/results/c/rounds/%7F","#/results/c/rounds/r%0Ar","https://example.com/#/results/c/rounds/r"])("malformed explicit anchor %j remains a local explanatory route",hash=>{expect(parseHashRoute(hash)).toEqual({_tag:"not_found",hash});});
test("unpaired round surrogate fails loudly instead of producing a lossy anchor",()=>{expect(()=>hashForRoute({_tag:"result",collectionId:"c",roundId:"\ud800"})).toThrow(URIError);});
test("seeded malformed hash corpus is deterministic, total and preserves unknown input", () => {
  const alphabet = "#/%?ABC012한글\u0000\u007f\ud800";
  let seed = 0x7331;
  for (let index = 0; index < 4096; index++) {
    seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0;
    const size = seed % 256;
    let hash = index % 2 === 0 ? "#/results/" : "";
    for (let char = 0; char < size; char++) {
      seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0;
      hash += alphabet[seed % alphabet.length]!;
    }
    const route = parseHashRoute(hash);
    expect(Object.isFrozen(route)).toBe(true);
    if (route._tag === "not_found") expect(route.hash).toBe(hash);
    else if (route._tag === "result") expect(route.collectionId.length).toBeGreaterThan(0);
    else expect(route._tag).toBe("create");
  }
});
test("large hash is processed without recursive parsing or lossy truncation", () => {
  const hash = "https://example.com/" + "x".repeat(1_000_000);
  expect(parseHashRoute(hash)).toEqual({ _tag: "not_found", hash });
  const id = "a".repeat(16_384);
  expect(parseHashRoute(`#/results/${id}`)).toEqual({ _tag: "result", collectionId: id, roundId: null });
});
