import { readFileSync, writeFileSync, rmSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
const source = readFileSync(new URL("../src/operations/pendingRecovery.ts", import.meta.url), "utf8");
const mutations = [
  ["phase saturation", 'snapshot.pending.length >= 64 ? "saturated"', 'false ? "saturated"'],
  ["enumeration writes gate", '!snapshot.enumerationComplete ||', 'false ||'],
  ["capacity writes gate", '|| snapshot.pending.length >= 64,', '|| false,'],
  ["protocol header", 'reply.reply.protocolVersion !== 1', 'false'],
  ["empty session header", 'reply.reply.backendSessionId.length === 0', 'false'],
  ["finite timestamp", '!Number.isFinite(reply.receivedAtMillis)', 'false'],
  ["negative timestamp", 'reply.receivedAtMillis < 0', 'false'],
  ["duplicate descriptor", 'seen.has(descriptor.operationId)', 'false'],
  ["terminal descriptor ignored", 'terminal.some((item) => item.operationId === descriptor.operationId)', 'false'],
  ["descriptor kind fence", 'previous.descriptor.kind !== descriptor.kind', 'false'],
  ["descriptor collection fence", 'previous.descriptor.collectionId !== descriptor.collectionId', 'false'],
  ["descriptor round fence", 'previous.descriptor.roundId !== descriptor.roundId', 'false'],
  ["descriptor revision monotonicity", 'descriptor.revision > previous.descriptor.revision', 'true'],
  ["closed construction", 'parent.state._tag === "Closed"', 'false'],
  ["metadata disposal", 'pending: [], terminal: [], cursor: null, enumerationComplete: false, failure: null, readsAllowed: false', 'pending: SubscriptionRef.getUnsafe(state).pending, terminal: [], cursor: null, enumerationComplete: false, failure: null, readsAllowed: false'],
  ["generation admission fence", 'token !== generation || owner.state._tag === "Closed" ? Effect.interrupt', 'false ? Effect.interrupt'],
  ["generation publication fence", 'token !== generation || owner.state._tag === "Closed" ? current : change(current)', 'false ? current : change(current)'],
  ["stage failure keeps session fence", 'error._tag === "RecoveryUnavailable" ? Effect.void', 'false ? Effect.void'],
  ["retired Bootstrap fence", 'retired.has(received.reply.backendSessionId)', 'false'],
  ["nil pending invariant", 'data.pendingOperations === null ||', 'false ||'],
  ["nil results invariant", 'data.recentResults === null ||', 'false ||'],
  ["draft session fence", 'data.activeDraft.backendSessionId !== received.reply.backendSessionId', 'false'],
  ["same session unknown ownership", 'const previousEntries = same ? previous.pending : previous.pending.map((entry) => Object.freeze({ ...entry, observation: null, failure: null }));', 'const previousEntries = [];'],
  ["session observation invalidation", 'observation: null, failure: null }));', 'observation: entry.observation, failure: entry.failure }));'],
  ["overflow admission", 'combined.length > 64', 'false'],
  ["overflow enumeration stays incomplete", 'enumerationComplete: !overflow && data.pendingCursor === null', 'enumerationComplete: data.pendingCursor === null'],
  ["retired session bound", 'if (retired.size > 64)', 'if (false)'],
  ["work scope ownership", 'work = yield* Scope.fork(owner, "sequential");\n    yield* SubscriptionRef.set', 'work = yield* Scope.fork(parent, "sequential");\n    yield* SubscriptionRef.set'],
  ["external subscription cleanup", 'Effect.andThen(PubSub.shutdown(state.pubsub))', 'Effect.andThen(Effect.void)'],
  ["old query cancellation", 'yield* Scope.close(old, Exit.void);', 'yield* Effect.void;'],
  ["observation deadline", 'duration: 15000', 'duration: 15001'],
  ["operation session fence", 'reply.reply.backendSessionId !== original.backendSessionId', 'false'],
  ["observed revision monotonicity", 'Math.max(descriptor.revision, entry.observation?.revision ?? 0)', 'descriptor.revision'],
  ["operation identity fence", 'data.operationId !== id', 'false'],
  ["operation kind fence", 'data.kind !== descriptor.kind', 'false'],
  ["operation collection fence", 'data.collectionId !== descriptor.collectionId', 'false'],
  ["operation round fence", 'data.roundId !== descriptor.roundId', 'false'],
  ["known revision invariant", 'data.revision === null', 'false'],
  ["operation minimum revision", 'data.revision < minimum', 'false'],
  ["unknown is not terminal", 'observation.state === "succeeded" || observation.state === "failed"', 'observation.state !== "pending"'],
  ["terminal slot release", 'pending: current.pending.filter((item) => item.descriptor.operationId !== id)', 'pending: current.pending'],
  ["terminal metadata bound", '.slice(-128)', '.slice(-129)'],
  ["sweep page bound", 'pages < 64', 'pages < 63'],
  ["empty-slot page limit", 'const limit = 64 - current.pending.length;', 'const limit = 64;'],
  ["page session fence", 'reply.reply.backendSessionId !== current.backendSessionId', 'false'],
  ["nil page invariant", 'page.operations === null ||', 'false ||'],
  ["page requested limit", 'page.operations.length > limit', 'false'],
  ["cursor progress", '(page.cursor !== null && page.cursor === current.cursor)', 'false'],
  ["page failure keeps accepted descriptors", 'observe(entry.descriptor.operationId, token).pipe(Effect.catch(() => Effect.void))', 'observe(entry.descriptor.operationId, token)'],
  ["operation single flight", 'if (existing !== undefined) return Deferred.await(existing);', 'if (false) return Deferred.await(existing);'],
  ["completed flight release", 'flights.delete(id);', 'void 0;'],
  ["pending collection writes gate", '!current.pending.some((entry) => entry.descriptor.collectionId === id)', 'true'],
];
const selected = mutations.filter((entry) => process.argv[2] === undefined || entry[0] === process.argv[2]);
if (selected.length === 0) throw new Error("Unknown recovery mutation");
const mutant = new URL("./.pending-recovery-mutant.ts", import.meta.url);
let survivors = 0;
// These two redundant guards are deliberately retained as production assertions.
// With a deliberately faulty decoder, null still throws at iteration/length
// access and the same catch produces ProtocolError before any state is adopted.
// The real Schema rejects both values before these guards; removing just one
// guard cannot change the public result. Do not count equivalents as kills.
const equivalents = new Set(["nil pending invariant", "nil page invariant"]);
try {
  for (const [name, from, to] of selected) {
    const matches = source.split(from).length - 1;
    if (matches !== 1) throw new Error(`Mutation target drifted: ${name} (${matches})`);
    const altered = source.replace(from, to).replaceAll('"../app/errors"', '"../src/app/errors"').replaceAll('"../contracts/backend"', '"../src/contracts/backend"').replaceAll('"../contracts/schemas"', '"../src/contracts/schemas"');
    writeFileSync(mutant, altered);
    const result = spawnSync(process.execPath, ["node_modules/vitest/vitest.mjs", "run", "--config", "scripts/pending-recovery.vitest.config.ts"], {
      cwd: fileURLToPath(new URL("../", import.meta.url)), encoding: "utf8", timeout: 20000,
      env: { ...process.env, JACKPOT_RECOVERY_MUTANT: fileURLToPath(mutant), NO_COLOR: "1" },
    });
    if (result.error || result.signal || /Test timed out/i.test(result.stdout + result.stderr)) throw new Error(`Mutation harness failed: ${name}\n${result.stdout}\n${result.stderr}`);
    if (result.status !== 0 && !/Tests\s+\d+ failed/.test(result.stdout)) throw new Error(`Mutation tests did not execute: ${name}\n${result.stdout}\n${result.stderr}`);
    const killed = result.status !== 0;
    if (!killed && !equivalents.has(name)) survivors++;
    console.log(`${killed ? "killed" : equivalents.has(name) ? "equivalent (redundant null assertion)" : "SURVIVED"}: ${name}`);
  }
} finally { rmSync(mutant, { force: true }); }
if (survivors !== 0) process.exitCode = 1;
