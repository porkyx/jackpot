import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { readFileSync, unlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const harness = fileURLToPath(new URL("./observe-classification.mjs", import.meta.url));
const fixture = fileURLToPath(new URL("./classification.json", import.meta.url));
const run = (args) => spawnSync(process.execPath, [harness, ...args], { encoding: "utf8", timeout: 5000 });

test("omitting the inspected source path refuses execution and preserves existing observations", () => {
  const before = readFileSync(fixture);
  const result = run([]);
  assert.equal(result.status, 1); assert.equal(result.error, undefined);
  assert.match(result.stderr, /Provide the local, previously inspected S04 bundle path/);
  assert.deepEqual(readFileSync(fixture), before);
});
test("a missing source fails instead of generating invented model observations", () => {
  const before = readFileSync(fixture);
  const result = run([join(tmpdir(), `jackpot-missing-source-${randomUUID()}.js`)]);
  assert.equal(result.status, 1); assert.equal(result.error, undefined); assert.match(result.stderr, /ENOENT/);
  assert.deepEqual(readFileSync(fixture), before);
});
test("a different source hash cannot execute code or replace the parity fixture", () => {
  const path = join(tmpdir(), `jackpot-parity-refusal-${randomUUID()}.js`);
  const before = readFileSync(fixture);
  writeFileSync(path, "throw new Error('uninspected source ran')", { flag: "wx" });
  try {
    const result = run([path]);
    assert.equal(result.status, 1); assert.equal(result.error, undefined);
    assert.match(result.stderr, /AssertionError/); assert.doesNotMatch(result.stderr, /Error: uninspected source ran/);
    assert.deepEqual(readFileSync(fixture), before);
  } finally { unlinkSync(path); }
});
