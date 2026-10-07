import { readFile, writeFile, mkdir, unlink } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { spawnSync } from "node:child_process";
const model = "src/screens/create/screenModel";
const input = "src/screens/create/inputModel";
const owner = "src/screens/create/screenOwner";
const view = "src/screens/create/view";
const mutations = [
  ["screen safe counter", model, "!Number.isSafeInteger(value)", "false"],
  ["screen zero boundary", model, "value < 0", "value <= 0"],
  ["screen maximum query generation", model, "value === Number.MAX_SAFE_INTEGER", "value > Number.MAX_SAFE_INTEGER"],
  ["screen session fence", model, "left.backendSessionId === right.backendSessionId", "left.backendSessionId !== right.backendSessionId"],
  ["screen draft fence", model, "left.draftId === right.draftId", "left.draftId !== right.draftId"],
  ["screen revision fence", model, "left.revision === right.revision", "left.revision !== right.revision"],
  ["screen article generation fence", model, "left.articleGeneration === right.articleGeneration", "left.articleGeneration !== right.articleGeneration"],
  ["route response fence", model, "model.routeGeneration === request.routeGeneration", "model.routeGeneration !== request.routeGeneration"],
  ["query response fence", model, "model.queryGeneration === request.queryGeneration", "model.queryGeneration !== request.queryGeneration"],
  ["page limit fence", model, "request.limit === 100", "request.limit !== 100"],
  ["group search key", model, "left.group === right.group", "left.group !== right.group"],
  ["search term key", model, "left.search === right.search", "left.search !== right.search"],
  ["offset search key", model, "left.offset === right.offset", "left.offset !== right.offset"],
  ["page row maximum", model, "page.rows.length > 100", "page.rows.length >= 100"],
  ["three page cache bound", model, ".slice(-3)", ".slice(-2)"],
  ["context invalidates cache", model, 'return ownScreen({ ...model, context, queryGeneration: next(model.queryGeneration), query: { _tag: "idle" }, visiblePage: null, cache: [] });', 'return ownScreen({ ...model, context, queryGeneration: next(model.queryGeneration), query: { _tag: "idle" }, visiblePage: model.visiblePage, cache: model.cache });'],
  ["composition blocks Enter", input, 'model.composition === "composing"', "false"],
  ["native composing event blocks Enter", input, "eventIsComposing ||", "false ||"],
  ["legacy composing key blocks Enter", input, "keyCode === 229", "keyCode === 230"],
  ["trailing Enter is swallowed", input, 'model.composition === "ended") return', 'model.composition === "idle") return'],
  ["caret ordering", input, "action.end < action.start", "action.end > action.start"],
  ["closed query admission", owner, 'scope.state._tag !== "Closed"', "true"],
  ["old route admission", owner, "!closed && scope.state._tag !== \"Closed\" && lease.isCurrent()", "!closed && scope.state._tag !== \"Closed\""],
  ["changed context cancels query", owner, "!== before) yield* interruptQuery", "=== before) yield* interruptQuery"],
  ["IME end does not duplicate raw", view, "if (port.readRaw() !== node.value)", "if (true)"],
  ["trailing input event is deduplicated", view, "if (trailingRaw === node.value)", "if (false)"],
  ["150ms exact readiness delay", view, 'Effect.sleep("150 millis")', 'Effect.sleep("149 millis")'],
  ["only candidate may be admitted", view, "if (fence !== null)", "if (true)"],
  ["unmount closes input model", view, 'model = reduceInputScreen(model, { _tag: "Closed" });', 'model = model;'],
  ["one trailing Enter in DOM", view, "if (decision.submit)", "if (true)"],
  ["new valid dirty restarts debounce", view, "if (editor.candidate() !== null) schedule();", "if (false) schedule();"],
];
const modules = [model, input, owner, view, "src/platform/dom", "src/ui/view"];
const directory = resolve(".task/screen-mutants");
const sources = new Map();
const copies = new Map();
for (const module of modules) {
  const copy = resolve(directory, `${module}.ts`); await mkdir(dirname(copy), { recursive: true });
  sources.set(module, await readFile(`${module}.ts`, "utf8")); copies.set(module, copy);
}
let killed = 0;
try {
  for (const [name, module, from, to] of mutations) {
    for (const dependency of modules) await writeFile(copies.get(dependency), sources.get(dependency));
    const original = sources.get(module);
    if (!original.includes(from)) throw new Error(`Missing screen mutation target: ${name}`);
    const mutant = copies.get(module); await writeFile(mutant, original.replaceAll(from, to));
    const file = module === model || module === input ? "tests/unit/create-screen-model.test.ts" : module === owner ? "tests/integration/create-screen-owner.test.ts" : "tests/dom/create-input.test.ts";
    const run = spawnSync(process.execPath, ["node_modules/vitest/vitest.mjs", "run", "--config", "vitest.screen.config.ts", file], {
      cwd: process.cwd(), encoding: "utf8", timeout: 20000, env: { ...process.env, NO_COLOR: "1", JACKPOT_SCREEN_SOURCE: module, JACKPOT_SCREEN_MUTANT: mutant },
    });
    const output = `${run.stdout ?? ""}\n${run.stderr ?? ""}`;
    if (run.error !== undefined || run.signal !== null || !/Tests\s+\d+ failed/.test(output)) throw new Error(`Survived or invalid compile/import/timeout screen mutation: ${name}\n${output}`);
    if (run.status === 0) throw new Error(`Screen mutation returned success despite failures: ${name}`);
    killed++; console.log(`killed: ${name}`);
  }
} finally { for (const path of copies.values()) await unlink(path).catch(() => {}); }
console.log(`${killed}/${mutations.length} screen mutations killed`);
