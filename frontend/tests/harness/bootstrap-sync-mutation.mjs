import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve, basename } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const frontend = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const source = readFileSync(join(frontend, "src/operations/bootstrapSync.ts"), "utf8");
const mutations = [
  ["known maximum", "Math.max(...revisions)", "Math.min(...revisions)"],
  ["draft identity", "state.activeDraft?.draftId === notice.entityId", "state.activeDraft?.draftId !== notice.entityId"],
  ["session monotonicity", "previous.backendSessionId !== candidate.backendSessionId", "false"],
  ["draft revision", "newDraft.revision < oldDraft.revision", "false"],
  ["article generation", "newDraft.articleGeneration < oldDraft.articleGeneration", "false"],
  ["draft independent guards", "newDraft.revision < oldDraft.revision || newDraft.articleGeneration < oldDraft.articleGeneration", "newDraft.revision < oldDraft.revision && newDraft.articleGeneration < oldDraft.articleGeneration"],
  ["result revision", "old.revision > result.revision", "false"],
  ["pending operation identity", "entry.descriptor.operationId === operation.descriptor.operationId", "true"],
  ["pending collection identity", "entry.descriptor.collectionId === operation.descriptor.collectionId", "true"],
  ["pending round identity", "entry.descriptor.roundId === operation.descriptor.roundId", "true"],
  ["pending revision", "old.descriptor.revision > operation.descriptor.revision", "false"],
  ["finalizer ownership", "Effect.addFinalizer(() => close)", "Effect.addFinalizer(() => Effect.void)"],
  ["initial failure cleanup", "Effect.onError(() => close)", "Effect.onError(() => Effect.void)"],
  ["initial subscription order", "yield* source.subscribeStateChanges", "yield* resync; yield* source.subscribeStateChanges"],
  ["catch-up maximum", "round < 3", "round < 4"],
  ["notice error before read", "if (noticeError !== null) { const error = noticeError; noticeError = null; return yield* Effect.fail(error); }", "if (false) { const error = noticeError!; noticeError = null; return yield* Effect.fail(error); }"],
  ["protocol version", "reply.protocolVersion !== 1", "false"],
  ["empty session", "reply.backendSessionId === \"\"", "false"],
  ["retired reply fence", "if (retired.has(candidate.backendSessionId)) continue;", "if (false) continue;"],
  ["in-flight session fence", "if ([...hints.values()].some((hint) => hint.sequence > requestedAt && hint.notice.backendSessionId !== reply.backendSessionId)) continue;", "if (false) continue;"],
  ["covered hint removal", "if (revision !== undefined && revision >= hint.notice.revision)", "if (false)"],
  ["unknown entity stale", "confirmed = markConfirmedEntityStale(confirmed, hint.notice) as ActiveCoordinatorState;", "// omitted stale marking"],
  ["known summary catch-up", "if (revision !== undefined) needsKnownRead = true;", "if (false) needsKnownRead = true;"],
  ["overflow visibility", "if (overflow) confirmed = Object.freeze({ ...confirmed, staleOverflow: true });", "// omitted overflow"],
  ["stale phase", "stale ? \"stale\" : \"ready\"", "\"ready\""],
  ["closed handle cancellation", "if (closed) return yield* Effect.interrupt;", "// no closed guard"],
  ["query single-flight", "if (completed !== epoch)", "if (false)"],
  ["shared failed request", "if (lastError !== null) return yield* Effect.fail(lastError);", "// no shared error"],
  ["retired notice fence", "closed || retired.has(notice.backendSessionId)", "closed"],
  ["duplicate hint fence", "previous.notice.revision >= notice.revision", "previous.notice.revision > notice.revision"],
  ["covered callback fence", "revision >= notice.revision", "revision > notice.revision"],
  ["hint cap", "hints.size < 65", "hints.size < 64"],
  ["highest revision hint", "hints.set(key, { notice: Object.freeze({ ...notice }), sequence })", "hints.set(key, { notice: Object.freeze({ ...notice, revision: 0 }), sequence })"],
  ["typed worker errors", "Effect.catch(() => Effect.void), Effect.catchDefect", "Effect.catchDefect"],
  ["queue boot wake consumed", "if (sequence === appliedSequence && noticeError === null) continue;", "if (false) continue;"],
];
const directory = mkdtempSync(join(tmpdir(), "jackpot-bootstrap-mutants-"));
assert.equal(dirname(resolve(directory)), resolve(tmpdir()));
assert.ok(basename(directory).startsWith("jackpot-bootstrap-mutants-"));
const pathLiteral = (value) => JSON.stringify(value.replaceAll("\\", "/"));
const failed = [];
const selected = mutations.slice(Number(process.argv[2] ?? 0), Number(process.argv[3] ?? mutations.length));
try {
  const mutantPath = join(directory, "mutant.ts");
  const configPath = join(directory, "vitest.config.mts");
  writeFileSync(configPath, `export default { root: ${pathLiteral(frontend)}, resolve: { alias: [{ find: /^.*\\/operations\\/bootstrapSync$/, replacement: ${pathLiteral(mutantPath)} }] }, test: { include: ["tests/integration/bootstrap-sync.test.ts"], environment: "node", testTimeout: 3000 } };`);
  for (const [name, before, after] of selected) {
    assert.ok(source.includes(before), `Missing mutation: ${name}`);
    const mutant = source.replaceAll(before, after)
      .replaceAll('"effect"', pathLiteral(join(frontend, "node_modules/effect/dist/index.js")))
      .replaceAll(/"(\.\.\/contracts\/backend|\.\.\/app\/errors|\.\/readRetry|\.\/coordinatorState)"/g, (_, relative) => pathLiteral(resolve(frontend, "src/operations", `${relative}.ts`)));
    writeFileSync(mutantPath, mutant);
    const args = ["node_modules/vitest/vitest.mjs", "run", "--config", configPath];
    // Use a bounded startup case for this looping mutant, so an assertion rather
    // than a test timeout proves the redundant request is observable.
    if (name === "queue boot wake consumed") args.push("-t", "merges startup notices");
    if (name === "initial subscription order") args.push("-t", "subscribes before Bootstrap");
    const run = spawnSync(process.execPath, args, { cwd: frontend, encoding: "utf8", timeout: 15000, windowsHide: true });
    const output = `${run.stdout ?? ""}\n${run.stderr ?? ""}`;
    assert.ok(!run.error && !/Test timed out|Failed to load|Failed to resolve|Parse failure|Transform failed|Startup Error/.test(output), `Invalid run ${name}: ${run.error ?? output}`);
    if (run.status !== 0 && /Tests\s+\d+ failed/.test(output)) process.stdout.write(`killed: ${name}\n`);
    else { failed.push(name); process.stdout.write(`SURVIVED: ${name}\n`); }
  }
  assert.deepEqual(failed, [], `Survivors: ${failed.join(", ")}`);
  process.stdout.write(`${selected.length}/${selected.length} behavioral mutations killed\n`);
} finally {
  // This resolved path is a freshly-created, verified child of tmpdir only.
  rmSync(directory, { recursive: true, force: true });
}
