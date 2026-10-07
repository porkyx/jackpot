import { readFileSync, writeFileSync, rmSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const mutations = [
  ["view", "same text", "target.textContent !== value", "true"],
  ["view", "IME guard", "!composing &&", "true &&"],
  ["view", "same input value", "target.value !== value", "true"],
  ["view", "same disabled value", "target.disabled !== disabled", "true"],
  ["view", "same aria value", "target.getAttribute(attribute) === value", "false"],
  ["view", "aria removal", "target.removeAttribute(attribute)", "void 0"],
  ["view", "closed mount parent", 'parent.state._tag === "Closed"', "false", 1],
  ["view", "closed mount before append", 'if (scope.state._tag === "Closed") throw new ViewUnavailable({ reason: "closed" });', "if (false) throw new ViewUnavailable({ reason: \"closed\" });", 1],
  ["view", "foreign root ownership", "view.element.parentNode !== null", "false"],
  ["view", "partial append rollback", "catch (error) { view.element.remove(); throw error; }", "catch (error) { throw error; }"],
  ["view", "owned root release", "() => Effect.sync(() => view.element.remove())", "() => Effect.void"],
  ["view", "failed mount cleanup", "Exit.isFailure(exit) ? Scope.close(scope, exit)", "false ? Scope.close(scope, exit)"],
  ["view", "closed row parent", 'parent.state._tag === "Closed"', "false", 2],
  ["view", "row maximum integer", "!Number.isSafeInteger(maximum)", "false"],
  ["view", "row maximum minimum", "maximum < 1", "false"],
  ["view", "row maximum ceiling", "maximum > 100", "false"],
  ["view", "row host ownership", "host.childNodes.length !== 0", "false"],
  ["view", "row page overflow", "models.length > maximum", "false"],
  ["view", "row key type", 'typeof id !== "string"', "false"],
  ["view", "row key empty", "id.length === 0", "false"],
  ["view", "row key duplicate", "seen.has(id)", "false"],
  ["view", "row serialization", "lock.withPermit(Effect.gen", "(Effect.gen"],
  ["view", "stable row placement", "row.element !== cursor", "true"],
  ["view", "existing row patch", "if (existing) row.value.patch(model);", "void 0;"],
  ["view", "removed row cleanup", "if (!next.has(id)) yield* row.close", "if (false) yield* row.close"],
  ["view", "staged row rollback", "Effect.forEach(staged, (row) => row.close, { discard: true })", "Effect.void"],
  ["view", "commit failure closes list", "committing = true;", "committing = false;"],
  ["view", "closed row commit", 'if (scope.state._tag === "Closed") throw new ViewUnavailable({ reason: "closed" });', "if (false) throw new ViewUnavailable({ reason: \"closed\" });", 2],
  ["view", "closed row after cleanup", 'if (scope.state._tag === "Closed") return yield* Effect.fail(new ViewUnavailable({ reason: "closed" }));', 'if (false) return yield* Effect.fail(new ViewUnavailable({ reason: "closed" }));', 2],
  ["frame", "closed frame owner", 'scope.state._tag === "Closed"', "false"],
  ["frame", "bounded wake slot", "Queue.sliding<void>(1)", "Queue.sliding<void>(2)"],
  ["frame", "consumed wake drain", "Queue.takeUnsafe(wake);", "void 0;"],
  ["frame", "consumed projection release", "latest = undefined;\n          return value;", "return value;"],
  ["frame", "interruption is not render failure", "Cause.hasInterruptsOnly(cause)", "false"],
  ["frame", "render failure blocks admission", 'phase = "failed";', 'phase = "open";'],
  ["frame", "terminated worker closes admission", 'if (phase === "open") phase = "closed";', 'if (false) phase = "closed";'],
  ["frame", "closed or failed offer", 'phase !== "open"', "false"],
];
const mutant = new URL("./.dom-lifecycle-mutant.ts", import.meta.url);
let survivors = 0;
try {
  for (const [moduleName, name, from, to, occurrence] of mutations) {
    let source = readFileSync(new URL(`../src/ui/${moduleName}.ts`, import.meta.url), "utf8");
    const matches = source.split(from).length - 1;
    if (matches !== (occurrence === undefined ? 1 : 2)) throw new Error(`Mutation target drifted: ${name} (${matches})`);
    let index = source.indexOf(from);
    if (occurrence === 2) index = source.indexOf(from, index + from.length);
    source = source.slice(0, index) + to + source.slice(index + from.length);
    source = source.replaceAll('"../platform/dom"', '"../src/platform/dom"').replaceAll('"./view"', '"../src/ui/view"');
    writeFileSync(mutant, source);
    const result = spawnSync(process.execPath, ["node_modules/vitest/vitest.mjs", "run", "--config", "scripts/dom-lifecycle.vitest.config.ts"], {
      cwd: fileURLToPath(new URL("../", import.meta.url)),
      env: { ...process.env, JACKPOT_DOM_MUTANT: fileURLToPath(mutant), JACKPOT_DOM_MODULE: moduleName, NO_COLOR: "1" },
      encoding: "utf8", timeout: 20000,
    });
    if (result.error || result.signal || /Test timed out/i.test(result.stdout + result.stderr)) throw new Error(`Mutation harness failed: ${name}`);
    if (result.status !== 0 && !/Tests\s+\d+ failed/.test(result.stdout)) throw new Error(`Mutation tests did not execute: ${name}\n${result.stdout}\n${result.stderr}`);
    const killed = result.status !== 0;
    if (!killed) survivors++;
    console.log(`${killed ? "killed" : "SURVIVED"}: ${name}`);
  }
} finally { rmSync(mutant, { force: true }); }
if (survivors !== 0) process.exitCode = 1;
