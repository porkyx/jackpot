import { readFileSync, writeFileSync, rmSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const mutations = [
  ["ids", "ID type", 'typeof value !== "string"', "false"],
  ["ids", "ID emptiness", "value.length === 0", "false"],
  ["ids", "sequence snapshot", "const copy = [...values]", "const copy = values"],
  ["ids", "sequence advance", "copy[cursor++]", "copy[cursor]"],
  ["ids", "operation source", "operationId: next", 'operationId: Effect.succeed("invented")'],
  ["ids", "intent source", "intentId: next", 'intentId: Effect.succeed("invented")'],
  ["dom", "event name", "type.length === 0", "false"],
  ["dom", "listener capture", "options.capture ?? false", "false"],
  ["dom", "partial add rollback", "target.removeEventListener(type, listener, capture);", "void 0;"],
  ["dom", "scope release", "() => Effect.sync(() => target.removeEventListener(type, listener, capture))", "() => Effect.void"],
  ["dom", "frame finite", "Number.isFinite(time)", "true"],
  ["dom", "frame nonnegative", "time >= 0", "true"],
  ["dom", "frame interruption cleanup", "Effect.sync(() => frames.cancel(id))", "Effect.void"],
  ["dom", "focus effect", "try: () => target.focus()", "try: () => undefined"],
  ["app/runtime", "dispose idempotency", "if (disposal !== undefined)", "if (false)"],
  ["app/runtime", "admission closes before cleanup", 'phase = "stopping";', 'phase = "running";'],
  ["app/runtime", "initialize fiber interruption", "Fiber.interrupt(startup)", "Effect.void"],
  ["app/runtime", "screen cleanup before app", "Scope.close(screens, Exit.void)", "Effect.void"],
  ["app/runtime", "stopped phase", 'phase = "stopped";', 'phase = "stopping";'],
  ["app/runtime", "startup disposal race", 'if (phase !== "starting")', "if (false)"],
  ["app/runtime", "startup failure cleanup", "await dispose();", "await Promise.resolve();"],
  ["app/runtime", "both startup and cleanup errors", 'throw new AggregateError([error, cleanupError], "앱 초기화와 자원 정리가 실패했습니다.");', "throw cleanupError;"],
  ["app/runtime", "run owned by app", "runtime.runPromise(effect.pipe(Effect.forkIn(appScope), Effect.flatMap(Fiber.join)))", "runtime.runPromise(effect)"],
  ["app/runtime", "mount owned by screen", "runtime.runPromise(effect.pipe(Scope.provide(screens), Effect.forkIn(screens), Effect.flatMap(Fiber.join)))", "runtime.runPromise(effect.pipe(Scope.provide(screens)))"],
  ["app/runtime", "HMR wait for old shutdown", "await hot?.data.jackpotPreviousDispose;", "await undefined;"],
  ["app/runtime", "HMR keeps shutdown promise", "data.jackpotPreviousDispose = disposal;", "void 0;"],
  ["app/runtime", "HMR disposal error observation", "void disposal.catch(onDisposeFailure);", "void disposal;"],
  ["app/runtime", "HMR full reload cleanup registration", 'hot?.on?.("vite:beforeFullReload", () => dispose(hot.data));', "void 0;"],
  ["app/runtime", "HMR full reload awaits pending cleanup", "() => dispose(hot.data)", "() => { void dispose(hot.data); return Promise.resolve(); }"],
  ["app/runtime", "HMR repeated cleanup failure observation", "if (disposal === undefined)", "if (true)"],
];
const mutant = new URL("./.effect-service-mutant.ts", import.meta.url);
let survivors = 0;
try {
  for (const [moduleName, name, from, to] of mutations.filter((mutation) => process.env.JACKPOT_SERVICE_MUTATION_FILTER === undefined || mutation[1].includes(process.env.JACKPOT_SERVICE_MUTATION_FILTER))) {
    const modulePath = moduleName.includes("/") ? moduleName : `platform/${moduleName}`;
    const source = readFileSync(new URL(`../src/${modulePath}.ts`, import.meta.url), "utf8");
    if (source.split(from).length !== 2) throw new Error(`Mutation target must be unique: ${name}`);
    writeFileSync(mutant, source.replace(from, to));
    const result = spawnSync(process.execPath, ["node_modules/vitest/vitest.mjs", "run", "--config", "scripts/effect-services.vitest.config.ts"], {
      cwd: fileURLToPath(new URL("../", import.meta.url)),
      env: { ...process.env, JACKPOT_SERVICE_MUTANT: fileURLToPath(mutant), JACKPOT_SERVICE_MODULE: modulePath, NO_COLOR: "1" },
      encoding: "utf8", timeout: 20000,
    });
    if (result.error || result.signal) throw new Error(`Mutation harness failed: ${name}`);
    if (result.status !== 0 && !/Tests\s+\d+ failed/.test(result.stdout)) {
      throw new Error(`Mutation suite did not execute: ${name}\n${result.stdout}\n${result.stderr}`);
    }
    const killed = result.status !== 0 && /Tests\s+\d+ failed/.test(result.stdout);
    if (!killed) survivors++;
    console.log(`${killed ? "killed" : "SURVIVED"}: ${name}`);
  }
} finally {
  rmSync(mutant, { force: true });
}
if (survivors !== 0) process.exitCode = 1;
