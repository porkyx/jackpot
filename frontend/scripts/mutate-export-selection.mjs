import { createHash } from "node:crypto";
import { readFileSync, writeFileSync, rmSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
const root = fileURLToPath(new URL("../", import.meta.url));
const source = new URL("../src/features/export/view.ts", import.meta.url);
const original = readFileSync(source, "utf8");
const sha256 = value => createHash("sha256").update(value).digest("hex");
const cases = [
  ["selection invalidates displayed preview", "if (nextKey !== selectedKey)", "if (false)"],
  ["collection identity independently invalidates", "[target.collection.collectionId, target.round.roundId, target.round.roundVersion]", "[\"same-collection\", target.round.roundId, target.round.roundVersion]"],
  ["round identity independently invalidates", "[target.collection.collectionId, target.round.roundId, target.round.roundVersion]", "[target.collection.collectionId, \"same-round\", target.round.roundVersion]"],
  ["round version independently invalidates", "[target.collection.collectionId, target.round.roundId, target.round.roundVersion]", "[target.collection.collectionId, target.round.roundId, 1]"],
  ["ABA and close advance render ownership", "previewEpoch++;", "previewEpoch += 0;"],
  ["late success checks render ownership", "if (requestEpoch !== previewEpoch) return;", "if (false) return;"],
  ["late success observes unpatched selection", "} else if (alive) {\n          patch();", "} else if (alive) {"],
  ["late failure checks render ownership", "if (requestEpoch !== undefined && requestEpoch !== previewEpoch) return;", "if (false) return;"],
  ["late failure observes unpatched selection", "if (kind === \"preview\") { patch();", "if (kind === \"preview\") {"],
  ["explicit close releases URL", 'yield* dom.listen(close, "click", revoke);', 'yield* dom.listen(close, "click", () => {});'],
  ["scope close releases URL", "alive = false; revoke();", "alive = false;"],
  ["closed retained patch cannot read selection", "if (!alive) return;\n      const target = selected();", "const target = selected();"],
  ["current assignment failure releases allocated URL", "      revoke();\n      if (failure instanceof", "      if (failure instanceof"],
];
const config = new URL(".export-mutant.config.ts", import.meta.url);
const output = new URL(".export-mutant.json", import.meta.url);
const record = new URL("../../.task/result-export-selection-mutations.json", import.meta.url);
const results = [];
try {
  for (const [name, from, to] of cases) {
    const normalized = original.replaceAll("\r\n", "\n");
    if (normalized.split(from).length !== 2) throw Error("Exact anchor drift: " + name);
    writeFileSync(config, 'import{defineConfig}from"vitest/config";export default defineConfig({plugins:[{name:"isolated-export",enforce:"pre",transform(code,id){if(id.replaceAll("\\\\","/").endsWith("/src/features/export/view.ts"))return{code:code.replaceAll("\\r\\n","\\n").replace(' + JSON.stringify(from) + ',' + JSON.stringify(to) + '),map:null};}}],test:{environment:"happy-dom",include:["tests/dom/result-export-selection.test.ts"]}});');
    rmSync(output, { force: true });
    const run = spawnSync(process.execPath, ["node_modules/vitest/vitest.mjs", "run", "--config", fileURLToPath(config), "--reporter=json", "--outputFile", fileURLToPath(output)], { cwd: root, encoding: "utf8", windowsHide: true, timeout: 20000, env: { ...process.env, NO_COLOR: "1" } });
    const text = run.stdout + run.stderr;
    if (run.error || run.signal || /Test timed out/i.test(text)) throw Error("Infrastructure failure, kill0: " + name);
    const report = JSON.parse(readFileSync(output, "utf8"));
    const failed = report.testResults.flatMap(item => item.assertionResults).filter(item => item.status === "failed");
    const assertions = failed.filter(item => item.failureMessages.some(message => /AssertionError|expected/.test(message)));
    if (run.status === 0 || assertions.length === 0) throw Error("Survived or no assertion, kill0: " + name);
    results.push({ name, killed: true, assertions: assertions.map(item => item.fullName) });
    console.log("killed by assertion: " + name);
  }
  writeFileSync(record, JSON.stringify({ sourceSha256: sha256(original), killed: results.length, total: cases.length, compileTimeoutNoTestsKills: 0, results }, null, 2));
  console.log("PASS " + results.length + "/" + cases.length + " isolated assertion kills; production source unchanged");
} finally {
  rmSync(config, { force: true }); rmSync(output, { force: true });
  if (readFileSync(source, "utf8") !== original) throw Error("Production export source changed during isolated mutations");
}
