import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve, dirname, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
const policyModule = process.env.JACKPOT_POLICY_MODULE ?? new URL("./policy.mjs", import.meta.url).href;
const { inspectManifest, inspectSource } = await import(policyModule);

const examples = [
  ["static framework import", "src/features/create/model.ts", "import R from 'react'", false],
  ["dynamic framework import", "src/main.ts", "import('vue')", false],
  ["dynamic variable import", "src/main.ts", "import(packageName)", false],
  ["dynamic template import", "src/main.ts", "import(`react`)", false],
  ["allowed dynamic import", "src/main.ts", "import('effect')", true],
  ["framework re-export", "src/main.ts", "export * from 'lit'", false],
  ["side effect import", "src/main.ts", "import 'jquery'", false],
  ["unknown package", "src/main.ts", "import R from 'unknown'", false],
  ["Wails submodule outside adapter", "src/main.ts", "import {Events} from '@wailsio/runtime/events'", false],
  ["Wails inside adapter", "src/platform/wails.ts", "import {Events} from '@wailsio/runtime'", true],
  ["bindings outside adapter", "src/features/create/model.ts", "import {B} from '../../../bindings/backend'", false],
  ["bindings inside adapter", "src/platform/wails.ts", "import {B} from '../../bindings/backend'", true],
  ["CSS relative import", "src/main.ts", "import './styles.css'", true],
  ["DOM in model", "src/features/create/model.ts", "window.document.body", false],
  ["DOM in view", "src/features/create/view.ts", "document.createElement('p')", true],
  ["DOM platform", "src/platform/dom.ts", "window.addEventListener('focus', fn)", true],
  ["canvas in model", "src/features/export/model.ts", "let c: HTMLCanvasElement", false],
  ["canvas platform", "src/platform/canvas.ts", "let c: HTMLCanvasElement", true],
  ["canvas export view", "src/features/export/view.ts", "let c: CanvasRenderingContext2D", true],
  ["Effect runner in action", "src/features/create/actions.ts", "Effect.runPromise(effect)", false],
  ["Effect runner in entry", "src/main.ts", "Effect.runSync(effect)", true],
  ["Effect runner in runtime", "src/app/runtime.ts", "runtime.runFork(effect)", true],
  ["Effect runner in dispatch", "src/app/dispatch.ts", "runtime.runPromise(effect)", true],
  ["any escape", "src/contracts/backend.ts", "let v: any", false],
  ["eval escape", "src/main.ts", "eval(code)", false],
  ["require escape", "src/main.ts", "require('react')", false],
  ["JSX extension", "src/features/create/view.tsx", "export const x = 1", false],
  ["comments and strings", "src/features/create/model.ts", "// document any runSync\nconst v = 'window import react'", true],
  ["empty file", "src/features/create/model.ts", "", true],
  ["route regexp is scanned as a literal", "src/app/routes.ts", String.raw`const route = /^#\/results\/([^/?#]+)$/.exec(hash)`, true],
  ["regexp contents are not policy identifiers", "src/features/create/model.ts", String.raw`const text = /document|window|eval|require|any|runPromise/.test(value)`, true],
  ["division does not conceal a forbidden operand", "src/features/create/model.ts", "const value = total / window.width / count", false],
  ["template interpolation does not conceal DOM", "src/features/create/model.ts", "const value = `a${document.body}b`", false],
  ["tokens after nested templates remain visible", "src/features/create/model.ts", "const value = `a${`b${{x:1}.x}c`}d`; window.close()", false],
  ["template text is not policy code", "src/features/create/model.ts", "const value = `document ${`window ${1}`} eval`;", true],
  ["regexp in template expression is rescanned", "src/features/create/model.ts", "const value = `a${/#/.test(value)}b`", true],
  ["zero width private token fails closed", "src/features/create/model.ts", "const value = #;", false],
  ["regexp equal prefix is rescanned", "src/features/create/model.ts", "const value = /=#/.test(text)", true],
  ["unterminated regexp fails closed", "src/features/create/model.ts", "const value = /#", false],
  ["unterminated string fails closed", "src/features/create/model.ts", "const value = 'text", false],
  ["valid private fields advance", "src/features/create/model.ts", "class C { #value = 1; read() { return this.#value } }", true],
];
for (const [behavior, path, source, allowed] of examples) {
  test(behavior, () => assert.equal(inspectSource(path, source).length === 0, allowed));
}
test("repeated findings are deduplicated", () => {
  assert.equal(inspectSource("src/main.ts", "document.body; document.head;").length, 1);
});
test("bounded scanner fuzz preserves permanent hash/template/regexp seeds without throwing", () => {
  const alphabet = "a#;{}()[]/\\`$'\" \n한글";
  let state = 0x1a1720;
  for (let run = 0; run < 2048; run++) {
    let source = "";
    for (let index = 0; index < run % 65; index++) {
      state = (Math.imul(state, 1664525) + 1013904223) >>> 0;
      source += alphabet[state % alphabet.length];
    }
    const findings = inspectSource("src/features/create/model.ts", source);
    assert.ok(Array.isArray(findings));
    assert.ok(findings.every(finding => typeof finding === "string"));
  }
});
test("production exact allowed dependencies pass", () => {
  assert.deepEqual(inspectManifest({dependencies:{effect:"4.0.1","@wailsio/runtime":"3.0.0-beta.28"},devDependencies:{vite:"8.3.3"}}), []);
});
test("absent dependency sections pass", () => assert.deepEqual(inspectManifest({}), []));
test("unknown production dependency fails", () => assert.equal(inspectManifest({dependencies:{react:"1.0.0"}}).length, 1));
test("floating production version fails", () => assert.equal(inspectManifest({dependencies:{effect:"^4.0.1"}}).length, 1));
test("floating development version fails", () => assert.equal(inspectManifest({devDependencies:{vite:"latest"}}).length, 1));

for (const allowed of [true, false]) {
  test(`CLI exits ${allowed ? "success" : "failure"} for an independent project fixture`, () => {
    const root = mkdtempSync(resolve(tmpdir(), "jackpot-policy-"));
    try {
      mkdirSync(resolve(root, "src", "features"), {recursive:true});
      writeFileSync(resolve(root, "package.json"), JSON.stringify({dependencies:{effect:"4.0.1"}}));
      writeFileSync(resolve(root, "src", "features", "model.ts"), allowed ? String.raw`import {Effect} from 'effect'; const route = /^#\/results\/([^/?#]+)$/` : "import React from 'react'");
      writeFileSync(resolve(root, "src", "style.css"), "ignored{}");
      const result = spawnSync(process.execPath, [fileURLToPath(policyModule)], {cwd:root,encoding:"utf8"});
      assert.equal(result.status, allowed ? 0 : 1, result.stderr);
      assert.equal(result.error, undefined);
    } finally {
      assert.ok(root.startsWith(resolve(tmpdir()) + sep));
      assert.equal(dirname(root), resolve(tmpdir()));
      rmSync(root, {recursive:true});
    }
  });
}
