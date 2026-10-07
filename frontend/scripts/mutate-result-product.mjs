import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { resolve, join } from "node:path";
import { spawnSync } from "node:child_process";
const cases = [
 ["platform/canvas", "literal-newline", 'lines.join("\\n")', 'lines.join("\\\\n")'],
 ["platform/canvas", "font-deadline", 'duration: "5 seconds"', 'duration: "50 seconds"'],
 ["platform/canvas", "png-deadline", 'duration: "15 seconds"', 'duration: "150 seconds"'],
 ["platform/canvas", "png-size", "blob.size > RESULT_PNG_LIMIT", "false"],
 ["platform/canvas", "png-inclusive", "blob.size > RESULT_PNG_LIMIT", "blob.size >= RESULT_PNG_LIMIT"],
 ["platform/canvas", "png-type", 'blob.type !== "image/png"', "false"],
 ["platform/canvas", "png-empty", "blob.size === 0", "false"],
 ["platform/canvas", "canvas-release", "canvas.width = 0; canvas.height = 0;", "canvas.width = canvas.width; canvas.height = canvas.height;"],
 ["platform/canvas", "width-bound", "width < 256 || width > 2048", "width < 0 || width > 4096"],
 ["platform/canvas", "height-bound", "height > 8192 || width * height > 16000000", "false"],
 ["features/results/frozen/view", "page100", "offset, limit: 100", "offset, limit: 101"],
 ["features/results/frozen/view", "comments50", "offset, limit: 50", "offset, limit: 51"],
 ["features/results/frozen/view", "session-fence", "reply.backendSessionId !== session", "false"],
 ["features/results/frozen/view", "revision-fence", "revision !== expected", "false"],
 ["features/results/frozen/view", "revision-reversal", "revision < confirmed.revision", "false"],
 ["features/results/frozen/view", "comment-owner", "page.participantId !== chosen.id", "false"],
 ["features/results/frozen/view", "comment-limit", "page.rows.length > 50", "false"],
 ["features/results/frozen/view", "participant-count", "page.matched > page.total", "false"],
 ["features/results/frozen/view", "participant-duplicate", "new Set(page.rows.map((row) => row.id)).size !== page.rows.length", "false"],
 ["features/results/frozen/view", "selected-projection", 'row.included ? "확정 선택" : "선택 안 됨"', 'row.included ? "선택 안 됨" : "확정 선택"'],
 ["features/results/view", "kst-offset", "- 9 * 3600000", "- 8 * 3600000"],
 ["features/results/view", "cancel-ten-seconds", "Date.parse(last.scheduledAt) - 10000", "Date.parse(last.scheduledAt) - 9999"],
 ["features/results/view", "accepted-delay", "quickDelaySeconds:seconds", "quickDelaySeconds:10"],
 ["features/export/view", "preview-release", "alive = false; revoke();", "alive = false;"],
 ["features/export/view", "busy-admission", "if (admitted) { pending = false;", "if (true) { pending = false;"],
];
const target = resolve(".task", "result-mutants"); mkdirSync(target, { recursive: true });
const selected = process.argv.slice(2);
const results = selected.length === 0 ? [] : JSON.parse(readFileSync(join(target, "summary.json"), "utf8")).filter((row) => !selected.includes(row.name));
for (const [moduleName, name, before, after] of cases.filter((row) => selected.length === 0 || selected.includes(row[1]))) {
  const original = resolve("src", moduleName + ".ts");
  const source = readFileSync(original, "utf8");
  if (!source.includes(before)) throw new Error("Missing mutation anchor: " + name);
  const file = join(target, name + ".ts"); writeFileSync(file, source.replace(before, after));
  const run = spawnSync(process.execPath, ["node_modules/vitest/vitest.mjs", "run", "--config", "scripts/result-product.vitest.config.ts", "--reporter=json", "--outputFile=" + join(target, name + ".json")], {
    cwd: process.cwd(), env: { ...process.env, JACKPOT_RESULT_MODULE: moduleName, JACKPOT_RESULT_MUTANT: file },
    encoding: "utf8", timeout: 25000, windowsHide: true,
  });
  const output = (run.stdout ?? "") + (run.stderr ?? "");
  let killed = false, failed = 0;
  try {
    const report = JSON.parse(readFileSync(join(target, name + ".json"), "utf8"));
    failed = report.numFailedTests; killed = failed > 0 && report.numTotalTests > 0;
  } catch {}
  results.push({ name, moduleName, killed, failed, exitCode: run.status, error: run.error?.message });
  console.log(name + ": " + (killed ? "KILLED" : "SURVIVED/INVALID") + " (" + failed + " assertions)");
  if (!killed) writeFileSync(join(target, name + ".output.txt"), output);
}
writeFileSync(join(target, "summary.json"), JSON.stringify(results, null, 2));
console.log(results.filter((r) => r.killed).length + "/" + results.length + " assertion mutants killed; original source never changed.");
process.exitCode = results.every((r) => r.killed) ? 0 : 1;
