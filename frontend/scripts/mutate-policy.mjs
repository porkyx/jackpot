import { readFileSync, writeFileSync, unlinkSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";
import { spawnSync } from "node:child_process";

const source = readFileSync(new URL("./policy.mjs", import.meta.url), "utf8");
// Every listed mutation must be distinguished by public observations, including CLI exit status.
const mutations = [
  ["JSX", 'if (/\\.(tsx|jsx)$/.test(path))', 'if (false)'],
  ["require", 'if (token.text === "require")', 'if (false)'],
  ["dynamic import", 'tokens[index + 2]?.kind !== SyntaxKind.StringLiteral', 'false'],
  ["dependency import", 'if (!allowedPackages.has(packageName))', 'if (false)'],
  ["Wails adapter", 'packageName === "@wailsio/runtime" && !wailsAdapter', 'false'],
  ["bindings adapter", '/bindings\\//.test(module) && !wailsAdapter', 'false'],
  ["any", 'token.kind === SyntaxKind.AnyKeyword', 'false'],
  ["DOM", '["document", "window"].includes(token.text) && !domAllowed', 'false'],
  ["Canvas", '["CanvasRenderingContext2D", "HTMLCanvasElement"].includes(token.text) && !canvasAllowed', 'false'],
  ["Effect runner", 'effectRunners.test(token.text) && !runAllowed', 'false'],
  ["eval", 'if (token.text === "eval")', 'if (false)'],
  ["manifest dependency", 'if (!allowedPackages.has(name))', 'if (false)'],
  ["production version", 'if (!/^\\d+\\.\\d+\\.\\d+(?:-beta\\.\\d+)?$/.test(version))', 'if (false)'],
  ["development version", 'if (!/^\\d+\\.\\d+\\.\\d+$/.test(version))', 'if (false)'],
  ["CLI failure exit", 'process.exitCode = 1', 'process.exitCode = 0'],
  ["regexp rescan", 'kind = scanner.reScanSlashToken();', 'kind = SyntaxKind.SlashToken;'],
  ["division visibility", 'previous === undefined || expressionStarts.has(previous)', 'true'],
  ["template rescan", 'kind = scanner.reScanTemplateToken(false);', 'kind = SyntaxKind.CloseBraceToken;'],
  ["unterminated fail closed", ' || scanner.isUnterminated()', ''],
];
const mutant = new URL("./.policy-mutant.mjs", import.meta.url);
const outcomes = [];
try {
  for (const [name, from, to] of mutations) {
    if (!source.includes(from)) throw new Error(`Mutation target missing: ${name}`);
    writeFileSync(mutant, source.replace(from, to));
    const result = spawnSync(process.execPath, ["--test", fileURLToPath(new URL("./policy.test.mjs", import.meta.url))], {
      env: {...process.env, JACKPOT_POLICY_MODULE:pathToFileURL(fileURLToPath(mutant)).href}, encoding:"utf8", timeout:15000,
    });
    if (result.error || result.signal) throw new Error(`Mutation harness failed: ${name}`);
    const killed = result.status !== 0 && result.stdout.includes("not ok");
    outcomes.push({name,killed});
    console.log(`${killed ? "killed" : "SURVIVED"}: ${name}`);
  }
} finally {
  unlinkSync(mutant);
}
if (outcomes.some(outcome => !outcome.killed)) process.exitCode = 1;
