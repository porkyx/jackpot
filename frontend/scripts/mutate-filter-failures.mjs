// Mutations exist only in a private Vite transform. Production files are read-only.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, writeFileSync, rmSync, rmdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const frontend = fileURLToPath(new URL("../", import.meta.url));
const files = { owner: "src/screens/create/productOwner.ts", view: "src/features/create/view.ts" };
const originals = Object.fromEntries(Object.entries(files).map(([key, path]) => [key, readFileSync(resolve(frontend, path), "utf8")]));
const hash = value => createHash("sha256").update(value).digest("hex");
const tests = ["tests/integration/filter-commit-errors.test.ts", "tests/dom/filter-retry.test.ts"];
const fence = 'validateEditorField(current, target, fence, { _tag: "invalid", messageKey })';
const cases = [
  ["failure belongs to filters", "owner", 'if (group !== "filters" || state.closed', 'if (group === "filters" || state.closed'],
  ["pure interruption does not become rejection", "owner", 'state.closed || Cause.hasInterruptsOnly(cause) || state.editor === null', 'state.closed || state.editor === null'],
  ["local invalid raw retains correction classification", "owner", 'found.value instanceof CreateInputError ? "InvalidInput"', 'found.value instanceof CreateInputError ? "SubmissionRejected"'],
  ["expired receipt retains its classification", "owner", 'found.value.reason === "receipt_expired" ? "ReceiptExpired"', 'found.value.reason === "receipt_expired" ? "SubmissionRejected"'],
  ["late failure respects input version", "owner", fence, 'validateEditorField(current, target, { ...fence, inputVersion: getEditorField(current, target).inputVersion }, { _tag: "invalid", messageKey })'],
  ["late failure respects article epoch", "owner", fence, 'validateEditorField(current, target, { ...fence, editorEpoch: current.editorEpoch }, { _tag: "invalid", messageKey })'],
  ["late failure respects backend session", "owner", fence, 'validateEditorField(current, target, { ...fence, identity: { ...fence.identity, backendSessionId: current.identity.backendSessionId } }, { _tag: "invalid", messageKey })'],
  ["late failure respects draft identity", "owner", fence, 'validateEditorField(current, target, { ...fence, identity: { ...fence.identity, draftId: current.identity.draftId } }, { _tag: "invalid", messageKey })'],
  ["article replacement retires rejected submission", "owner", 'field.validation.messageKey === "SubmissionRejected" || field.validation.messageKey === "ReceiptExpired"', 'field.validation.messageKey === "ReceiptExpired"'],
  ["article replacement retires expired receipt", "owner", 'field.validation.messageKey === "SubmissionRejected" || field.validation.messageKey === "ReceiptExpired"', 'field.validation.messageKey === "SubmissionRejected"'],
  ["invalid input cannot advertise reapply", "view", '(field.validation.messageKey==="SubmissionRejected"||field.validation.messageKey==="ReceiptExpired")', 'true'],
  ["explicit native action executes user confirmation", "view", '()=>act(workspace.commitField({_tag:"filterToggle",field:"excludeAnonymous"}))', '()=>act(Effect.void)'],
  ["unadmitted retry obeys global guard", "view", '(blocked&&!observesOnly)', 'false'],
  ["blocked receipt-only confirmation stays readable", "view", '(blocked&&!observesOnly)', 'blocked'],
  ["mixed unsent group cannot use receipt allowance", "view", 'dirtyFilters.every(field=>field.submitted?.inputVersion===field.inputVersion)', 'dirtyFilters.some(field=>field.submitted?.inputVersion===field.inputVersion)'],
  ["local invalid input hides the reapply action", "view", 'retryFilters.hidden=!filterFailed||localFilterInvalid', 'retryFilters.hidden=!filterFailed'],
];
const chosen = process.argv[2] === undefined ? cases : cases.filter(([name]) => name === process.argv[2]);
const equivalent = new Map([
  ["invalid input cannot advertise reapply", "InvalidInput/TooManyWinners are independently covered by localFilterInvalid, which dominates every retry/message projection; accepting those same keys in filterFailed cannot change the projection."],
  ["local invalid input hides the reapply action", "Workspace serializes filter submissions. Each matching dirty field gets the same failure classification; a newer input resets its validation to unchecked and generation retires only submission failures. A current group cannot retain both local-invalid and submission-failed keys. localFilterInvalid is therefore redundant in this hidden expression for owner-produced snapshots, while its independent disabled/message priority remains enforced."],
]);
assert.ok(chosen.length > 0);
const temporary = mkdtempSync(resolve(frontend, "scripts/.filter-mutants-"));
assert.equal(dirname(temporary), resolve(frontend, "scripts"));
const config = resolve(temporary, "vitest.config.ts"), result = resolve(temporary, "result.json");
const report = { sourceHashes: Object.fromEntries(Object.entries(originals).map(([key, value]) => [files[key], hash(value)])), testHashes: Object.fromEntries(tests.map(path => [path, hash(readFileSync(resolve(frontend, path)))])), startedAt: new Date().toISOString(), baseline: null, cases: [], sourceUnchanged: false };
function execute(plugin) {
  writeFileSync(config, 'import{defineConfig}from"vitest/config";export default defineConfig({plugins:[' + plugin + '],test:{environment:"happy-dom",include:' + JSON.stringify(tests) + '}});');
  rmSync(result, { force: true });
  // The duplicate-action test awaits a callback whose intentional removal is a
  // timeout oracle; use the independent immediate state assertions for mutants.
  const pattern = plugin === "" ? [] : ["--testNamePattern", "^(?!duplicate explicit retry).*" ];
  const child = spawnSync(process.execPath, ["node_modules/vitest/vitest.mjs", "run", "--config", config, ...pattern, "--reporter=json", "--outputFile", result], { cwd: frontend, encoding: "utf8", windowsHide: true, timeout: 20000, env: { ...process.env, NO_COLOR: "1" } });
  const output = child.stdout + child.stderr;
  assert.ok(!child.error && !child.signal && !/Test timed out/i.test(output), "timeout/process failure is not a kill\n" + output);
  const parsed = JSON.parse(readFileSync(result, "utf8"));
  assert.equal(parsed.numRuntimeErrorTestSuites ?? 0, 0, "runtime/compile failure is not a kill");
  assert.ok(parsed.testResults.every(suite => suite.status !== "failed" || suite.assertionResults.length > 0), "failed import/compile suite is not a kill");
  return { status: child.status, parsed };
}
try {
  const baseline = execute("");
  assert.equal(baseline.status, 0); assert.equal(baseline.parsed.numFailedTests, 0);
  report.baseline = { passed: baseline.parsed.numPassedTests, status: "passed" };
  for (const [name, key, from, to] of chosen) {
    assert.equal(originals[key].split(from).length, 2, "anchor drift: " + name);
    const plugin = '{name:"private-filter-mutant",enforce:"pre",transform(code,id){if(id.replaceAll("\\\\","/").endsWith(' + JSON.stringify("/" + files[key]) + '))return{code:code.replace(' + JSON.stringify(from) + ',' + JSON.stringify(to) + '),map:null};}}';
    const child = execute(plugin);
    const failed = child.parsed.testResults.flatMap(suite => suite.assertionResults).filter(test => test.status === "failed");
    if (equivalent.has(name)) {
      assert.equal(child.status, 0, "equivalent-domain argument invalid: " + name);
      report.cases.push({ name, status: "equivalent-in-reachable-domain", reason: equivalent.get(name) });
      console.log("equivalent; not killed: " + name); continue;
    }
    assert.equal(child.status, 1, "survived: " + name);
    if (!(failed.length > 0 && failed.every(test => test.failureMessages.some(message => /AssertionError|expected/.test(message))))) report.rejected = { name, failed: failed.map(test => ({ name: test.fullName, messages: test.failureMessages })) };
    assert.ok(failed.length > 0 && failed.every(test => test.failureMessages.some(message => /AssertionError|expected/.test(message))), "no assertion kill: " + name);
    report.cases.push({ name, status: "assertion-killed", failedTests: failed.map(test => test.fullName) });
    console.log("assertion killed: " + name);
  }
  report.status = "passed";
} catch (error) {
  report.status = "failed"; report.error = String(error); throw error;
} finally {
  for (const [key, path] of Object.entries(files)) assert.equal(readFileSync(resolve(frontend, path), "utf8"), originals[key], "production source changed: " + path);
  report.sourceUnchanged = true;
  rmSync(config, { force: true }); rmSync(result, { force: true });
  // Only the two explicitly owned files were removed; the directory must be empty.
  rmdirSync(temporary);
  report.temporaryRemoved = true; report.endedAt = new Date().toISOString();
  writeFileSync(resolve(frontend, "../.task/filter-edit-failure-mutations.json"), JSON.stringify(report, null, 2) + "\n");
  console.log(JSON.stringify({ status: report.status, baseline: report.baseline, killed: report.cases.filter(entry => entry.status === "assertion-killed").length, equivalent: report.cases.filter(entry => entry.status === "equivalent-in-reachable-domain").length, planned: chosen.length, sourceUnchanged: report.sourceUnchanged, temporaryRemoved: report.temporaryRemoved }));
}
