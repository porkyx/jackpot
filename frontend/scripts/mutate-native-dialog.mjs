import { readFileSync, writeFileSync, rmSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const mutations = [
  ["fields", "input id", "input.id.length === 0", "false"],
  ["fields", "error id", "error.id.length === 0", "false", 1],
  ["fields", "distinct ids", "input.id === error.id", "false"],
  ["fields", "idempotent label", "label.htmlFor !== input.id", "true"],
  ["fields", "idempotent alert", 'error.getAttribute("role") !== "alert"', "true"],
  ["fields", "error region id", "error.id.length === 0", "false", 2],
  ["fields", "actionable message", 'message !== null && message.trim().length === 0', "false"],
  ["fields", "error text", 'patchText(error, message ?? "");', "void 0;"],
  ["fields", "invalid association", 'patchAria(input, "aria-invalid", message === null ? null : "true");', "void 0;"],
  ["fields", "description association", 'patchAria(input, "aria-describedby", message === null ? null : error.id);', "void 0;"],
  ["fields", "idempotent hidden", "error.hidden !== (message === null)", "true"],
  ["dialog", "closed parent", 'parent.state._tag === "Closed"', "false"],
  ["dialog", "focus origin", 'previous instanceof HTMLElement ? previous : undefined', "undefined"],
  ["dialog", "idempotent finish", "owned = undefined;", "void 0;"],
  ["dialog", "native close", "if (current.dialog.open) current.dialog.close();", "void 0;"],
  ["dialog", "secret cleanup", 'input.value = "";', "void 0;"],
  ["dialog", "cleanup failure visible", 'failed ? Effect.fail(new DomError()) : Effect.succeed(reason)', 'Effect.succeed(reason)'],
  ["dialog", "foreign root", "element.parentNode !== null", "false"],
  ["dialog", "foreign input isolation", 'value.sensitiveInputs.filter((input) => element.contains(input))', "value.sensitiveInputs"],
  ["dialog", "preopened root", "element.open ||", "false ||"],
  ["dialog", "title id", "value.title.id.length === 0", "false"],
  ["dialog", "title text", "value.title.textContent.trim().length === 0", "false"],
  ["dialog", "title ownership", "!element.contains(value.title)", "false"],
  ["dialog", "focus ownership", "!element.contains(value.initialFocus)", "false"],
  ["dialog", "secret ownership", "value.sensitiveInputs.some((input) => !element.contains(input))", "false"],
  ["dialog", "title association", 'element.setAttribute("aria-labelledby", value.title.id);', "void 0;"],
  ["dialog", "native Escape", 'event.preventDefault(); finish("cancel");', 'finish("close");'],
  ["dialog", "show native modal", "element.showModal()", "void 0"],
  ["dialog", "open cancellation", "Deferred.isDoneUnsafe(end)", "false"],
  ["dialog", "initial focus", "yield* dom.focus(value.initialFocus);", "yield* Effect.void;"],
  ["dialog", "closed initial focus scope", 'scope.state._tag === "Closed"', "false"],
  ["dialog", "closed session cleanup", 'Deferred.await(end).pipe(Effect.ensuring(Scope.close(scope, Exit.void)))', 'Deferred.await(end)'],
  ["dialog", "startup failure rollback", "Exit.isFailure(exit) ? Scope.close(scope, exit)", "false ? Scope.close(scope, exit)"],
  ["dialog", "actual focus", "host.ownerDocument.activeElement !== value.initialFocus", "false"],
];
const mutant = new URL("./.native-dialog-mutant.ts", import.meta.url);
const selected = mutations.filter((entry) => process.argv[2] === undefined || entry[1] === process.argv[2]);
if (selected.length === 0) throw new Error("Unknown native dialog mutation");
let survivors = 0;
try {
  for (const [moduleName, name, from, to, occurrence] of selected) {
    let source = readFileSync(new URL(`../src/ui/${moduleName}/view.ts`, import.meta.url), "utf8");
    const matches = source.split(from).length - 1;
    if (matches !== (occurrence === undefined ? 1 : 2)) throw new Error(`Mutation target drifted: ${name} (${matches})`);
    let index = source.indexOf(from);
    if (occurrence === 2) index = source.indexOf(from, index + from.length);
    source = source.slice(0, index) + to + source.slice(index + from.length);
    source = source.replaceAll('"../../platform/dom"', '"../src/platform/dom"').replaceAll('"../view"', '"../src/ui/view"');
    writeFileSync(mutant, source);
    const result = spawnSync(process.execPath, ["node_modules/vitest/vitest.mjs", "run", "--config", "scripts/native-dialog.vitest.config.ts"], {
      cwd: fileURLToPath(new URL("../", import.meta.url)),
      env: { ...process.env, JACKPOT_NATIVE_MUTANT: fileURLToPath(mutant), JACKPOT_NATIVE_MODULE: moduleName, NO_COLOR: "1" },
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
