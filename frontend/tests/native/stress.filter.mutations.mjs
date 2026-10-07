import { readFileSync, writeFileSync, rmSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
const source = new URL("./stress.filter.mjs", import.meta.url);
const original = readFileSync(source, "utf8"), normalized = original.replaceAll("\r\n", "\n");
const temporary = new URL(".filter-metrics-mutant.mjs", import.meta.url);
const output = new URL("../../../.task/filter-keyword-sampling-mutations.json", import.meta.url);
const cases = [
  ["empty keyword baseline is confirmed", "  await confirm(false);\n  for", "  for"],
  ["twenty samples are required", "index < 20", "index < 19"],
  ["applied authoritative and DOM confirmation finish timing", "    await confirm(true);", ""],
  ["keyword deletion follows each sample", "    await restore();\n    await confirm(false);", "    await confirm(false);"],
  ["original filters are confirmed before next sample", "    await confirm(false);", ""],
  ["start clock must be finite", "Number.isFinite(started)", "true"],
  ["start clock cannot be negative", "started >= 0", "true"],
  ["end clock must be finite", "Number.isFinite(ended)", "true"],
  ["end clock cannot precede start", "ended >= started", "true"],
  ["duration uses the captured start", "samples.push(ended - started)", "samples.push(ended)"],
  ["keyword must exclude exactly one participant", "const excluded = applied ? 1 : 0;", "const excluded = 0;"],
  ["default unclassified rows exclude the matching participant", "rows: participants - excluded", "rows: participants"],
  ["matching participant is absent only while excluded", "matchedPersonRows: applied ? 0 : 1", "matchedPersonRows: applied ? 1 : 0"],
  ["matching included checkbox returns only after restoration", "matchedPersonIncluded: applied ? null : true", "matchedPersonIncluded: applied ? true : null"],
];
const results = [];
try {
  for (const [name, from, to] of cases) {
    if (normalized.split(from).length !== 2) throw Error("Exact filter anchor drift: " + name);
    writeFileSync(temporary, normalized.replace(from, to));
    const result = spawnSync(process.execPath, ["--test", fileURLToPath(new URL("./stress.filter.test.mjs", import.meta.url))], { windowsHide: true, encoding: "utf8", timeout: 5000, env: { ...process.env, JACKPOT_FILTER_METRICS_MODULE: temporary.href, NO_COLOR: "1" } });
    const text = result.stdout + result.stderr;
    if (result.error || result.signal || /cancelledByParent|timed out|testTimeoutFailure/.test(text)) throw Error("Infrastructure failure, kill0: " + name);
    if (result.status !== 1 || !/ERR_ASSERTION|AssertionError/.test(text) || !/fail [1-9]/.test(text)) throw Error("Survived or no assertion, kill0: " + name);
    results.push({ name, killedByAssertion: true }); console.log("killed by assertion: " + name);
  }
  writeFileSync(output, JSON.stringify({ sourceSHA256: createHash("sha256").update(original).digest("hex"), killed: results.length, total: cases.length, infrastructureKills: 0, results }, null, 2));
  console.log("PASS " + results.length + "/" + cases.length + " isolated assertion kills");
} finally {
  rmSync(temporary, { force: true });
  if (readFileSync(source, "utf8") !== original) throw Error("Filter helper original changed");
}
