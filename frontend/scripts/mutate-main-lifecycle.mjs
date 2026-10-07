import { readFile, writeFile, mkdir, unlink } from "node:fs/promises";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";

const source = resolve("src/main.ts");
const original = await readFile(source, "utf8");
const appObserver = /apply\(snapshot\);\r?\n    \}\)\)\.pipe\(Effect\.forkScoped\);/.exec(original)?.[0];
if (appObserver === undefined) throw new Error("Missing current app Bootstrap observer Scope boundary");
const directory = resolve(".task/main-lifecycle-mutants");
const mutant = resolve(directory, "main.ts");
const mutations = [
  ["pagehide listener ownership", 'yield* dom.listen(environment.events, "pagehide", () => stop());', 'yield* Effect.void;'],
  ["actual hash input", 'app.routes.navigate(environment.readHash())', 'app.routes.navigate("#/unrecognized")'],
  ["screen observer Scope", 'screen.updateContext(draftContext(snapshot)).pipe(Effect.catchTag("ScreenUnavailable", () => Effect.void))).pipe(Effect.forkScoped)', 'screen.updateContext(draftContext(snapshot)).pipe(Effect.catchTag("ScreenUnavailable", () => Effect.void))).pipe(Effect.forkDetach)'],
  ["app observer Scope", appObserver, appObserver.replace("Effect.forkScoped", "Effect.forkDetach")],
  ["Bootstrap draft context", 'draftContext(sync === null ? null : yield* sync.snapshot)', 'null', 'rejects late retired-session'],
  ["session context identity", 'backendSessionId: draft.backendSessionId, draftId: draft.draftId', 'backendSessionId: draft.draftId, draftId: draft.draftId', 'rejects late retired-session'],
];
await mkdir(directory, { recursive: true });
let killed = 0;
try {
  for (const [name, from, to, pattern] of mutations) {
    if (!original.includes(from)) throw new Error(`Missing main lifecycle mutation target: ${name}`);
    const copy = original.replace(from, to).replaceAll('from "./', 'from "../../src/').replace('import "./styles/base.css"', 'import "../../src/styles/base.css"');
    await writeFile(mutant, copy);
    const run = spawnSync(process.execPath, ["node_modules/vitest/vitest.mjs", "run", "--config", "vitest.main-lifecycle.config.ts", "--testNamePattern", pattern ?? "stable listener/subscription"], {
      cwd: process.cwd(), encoding: "utf8", timeout: 20000,
      env: { ...process.env, NO_COLOR: "1", JACKPOT_MAIN_MUTANT: mutant },
    });
    const output = `${run.stdout ?? ""}\n${run.stderr ?? ""}`;
    if (run.error !== undefined || run.signal !== null || /Test timed out|Cannot find module|Failed to resolve import|Transform failed|ReferenceError|SyntaxError/.test(output) || !/Tests\s+\d+ failed/.test(output) || run.status === 0) {
      throw new Error(`Survived or invalid compile/import/timeout main mutation: ${name}\n${output}`);
    }
    killed++; console.log(`killed: ${name}`);
  }
} finally {
  await unlink(mutant).catch(() => {});
  if (await readFile(source, "utf8") !== original) throw new Error("Shared main changed during the harness; repeat its validation with the final composition");
}
console.log(`${killed}/${mutations.length} main lifecycle mutations killed`);
