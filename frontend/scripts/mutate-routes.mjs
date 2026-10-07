import { readFile, writeFile, mkdir, unlink } from "node:fs/promises";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";

const routes = "src/app/routes";
const manager = "src/app/routeManager";
const mutations = [
  ["empty initial hash", routes, 'hash === "" || hash === "#/create"', 'hash === "#/create"'],
  ["unknown input preserved", routes, '{ _tag: "not_found", hash }', '{ _tag: "not_found", hash: "changed" }'],
  ["collection decoding", routes, 'decodeURIComponent(match[1]!)', 'match[1]!'],
  ["component encoding", routes, 'encodeURIComponent(route.collectionId)', 'route.collectionId'],
  ["control characters", routes, '!/[\\u0000-\\u001f\\u007f]/.test(id)', 'true'],
  ["nonempty collection identity", routes, 'id.length > 0', 'id.length > 1'],
  ["round is not inferred", routes, 'roundId: null', 'roundId: "invented"'],
  ["strict collection path", routes, '([^/?#]+)', '([^?#]+)'],
  ["create formatting", routes, 'case "create": return "#/create";', 'case "create": return "#/unrecognized";'],
  ["unknown formatting invariant", routes, 'throw new Error(`Unknown route: ${String(route)}`)', 'return "#/create"'],
  ["safe generation integer", manager, '!Number.isSafeInteger(current)', 'false'],
  ["generation zero boundary", manager, 'current < 0', 'current <= 0'],
  ["generation maximum boundary", manager, 'current === Number.MAX_SAFE_INTEGER', 'current > Number.MAX_SAFE_INTEGER'],
  ["generation monotonicity", manager, 'return current + 1;', 'return current + 2;', 'route generation begins'],
  ["reject closed allocation", manager, 'if (parent.state._tag === "Closed")', 'if (false)'],
  ["reject closed admission", manager, 'if (closed || parent.state._tag === "Closed")', 'if (parent.state._tag === "Closed")'],
  ["latest request wins", manager, 'if (closed || requested !== generation) return;', 'if (closed) return;'],
  ["failed lease becomes inactive", manager, 'active = false;', 'active = true;'],
  ["latest lease fence", manager, 'requested === generation });', 'requested !== generation });'],
  ["cleanup defect preserves original failure", manager, 'Cause.combine(cause, cleanup.cause)', 'cleanup.cause'],
  ["interruption is not screen failure", manager, '!Cause.hasInterruptsOnly(failure)', 'true'],
];
const directory = resolve(".task/route-mutants");
await mkdir(directory, { recursive: true });
const mutant = resolve(directory, "current.ts");
const dependency = resolve(directory, "routes.ts");
await writeFile(dependency, await readFile(`${routes}.ts`, "utf8"));
let killed = 0;
try {
  for (const [name, module, from, to, pattern] of mutations) {
    const original = await readFile(`${module}.ts`, "utf8");
    if (!original.includes(from)) throw new Error(`Missing route mutation target: ${name}`);
    await writeFile(mutant, original.replaceAll(from, to));
    const run = spawnSync(process.execPath, ["node_modules/vitest/vitest.mjs", "run", "--config", "vitest.routes.config.ts", ...(pattern === undefined ? [] : ["--testNamePattern", pattern])], {
      cwd: process.cwd(), encoding: "utf8", timeout: 20000,
      env: { ...process.env, NO_COLOR: "1", JACKPOT_ROUTE_SOURCE: module, JACKPOT_ROUTE_MUTANT: mutant },
    });
    const output = `${run.stdout ?? ""}\n${run.stderr ?? ""}`;
    if (run.error !== undefined || run.signal !== null || !/Tests\s+\d+ failed/.test(output)) throw new Error(`Survived or invalid compile/import/timeout route mutation: ${name}\n${output}`);
    if (run.status === 0) throw new Error(`Route mutation returned success despite failures: ${name}`);
    killed++;
    console.log(`killed: ${name}`);
  }
} finally { await unlink(mutant).catch(() => {}); await unlink(dependency).catch(() => {}); }
console.log(`${killed}/${mutations.length} route mutations killed`);
