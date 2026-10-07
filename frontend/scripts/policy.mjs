import { readFile, readdir } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import { resolve } from "node:path";
import { createScanner, SyntaxKind } from "typescript/unstable/ast";

const allowedPackages = new Set(["effect", "@wailsio/runtime"]);
const effectRunners = /^run(Sync|Promise|Fork|Callback|SyncExit|PromiseExit)$/;
const expressionStarts = new Set([
  SyntaxKind.EqualsToken, SyntaxKind.OpenParenToken, SyntaxKind.OpenBracketToken,
  SyntaxKind.OpenBraceToken, SyntaxKind.CommaToken, SyntaxKind.ColonToken,
  SyntaxKind.SemicolonToken, SyntaxKind.ReturnKeyword, SyntaxKind.ThrowKeyword,
  SyntaxKind.EqualsGreaterThanToken, SyntaxKind.QuestionToken, SyntaxKind.BarBarToken,
  SyntaxKind.AmpersandAmpersandToken, SyntaxKind.QuestionQuestionToken,
  SyntaxKind.ExclamationToken, SyntaxKind.TemplateHead, SyntaxKind.TemplateMiddle,
]);

export function inspectSource(path, source) {
  const findings = [];
  if (/\.(tsx|jsx)$/.test(path)) findings.push("JSX 파일 금지");
  const scanner = createScanner(true, undefined, source);
  const tokens = [];
  const templates = [];
  let braces = 0;
  let previous;
  let end = 0;
  for (let kind = scanner.scan(); kind !== SyntaxKind.EndOfFile; kind = scanner.scan()) {
    // scan() alone treats regexp contents as TypeScript tokens. In TS7 a bare
    // '#' then has zero width, causing an unbounded loop. Rescan only where an
    // expression can start; division operands must remain visible to policy.
    if ((kind === SyntaxKind.SlashToken || kind === SyntaxKind.SlashEqualsToken) && (previous === undefined || expressionStarts.has(previous))) kind = scanner.reScanSlashToken();
    if (kind === SyntaxKind.CloseBraceToken && templates.at(-1) === braces) {
      kind = scanner.reScanTemplateToken(false);
      if (kind === SyntaxKind.TemplateTail) templates.pop();
    } else if (kind === SyntaxKind.CloseBraceToken) braces--;
    if (kind === SyntaxKind.OpenBraceToken) braces++;
    if (kind === SyntaxKind.TemplateHead) templates.push(braces);
    // Every non-EOF token must consume source bytes. Reject unknown scanner
    // states instead of accepting a partial scan or growing memory forever.
    if (scanner.getTokenEnd() <= scanner.getTokenStart() || scanner.getTokenEnd() <= end || scanner.getTokenEnd() > source.length || tokens.length >= source.length || scanner.isUnterminated()) {
      findings.push("소스 토큰 분석 실패");
      return findings;
    }
    end = scanner.getTokenEnd();
    tokens.push({ kind, value: scanner.getTokenValue(), text: scanner.getTokenText() });
    previous = kind;
  }
  const wailsAdapter = path === "src/platform/wails.ts";
  const domAllowed = /\/view\.ts$/.test(path) || path === "src/platform/dom.ts";
  const canvasAllowed = path === "src/platform/canvas.ts" || path === "src/features/export/view.ts";
  const runAllowed = path === "src/main.ts" || /^src\/app\/(runtime|dispatch)\.ts$/.test(path);
  for (let index = 0; index < tokens.length; index++) {
    const token = tokens[index];
    if (token.text === "require") findings.push("동적 코드/require 금지");
    if (token.text === "import" && tokens[index + 1]?.text === "(" && tokens[index + 2]?.kind !== SyntaxKind.StringLiteral) {
      findings.push("동적 import는 문자열 상수만 허용");
    }
    if (token.kind === SyntaxKind.StringLiteral) {
      const before = tokens[index - 1]?.text;
      const before2 = tokens[index - 2]?.text;
      if (before === "from" || before === "import" || (before === "(" && before2 === "import")) {
        const module = token.value;
        if (module.startsWith(".")) {
          if (/bindings\//.test(module) && !wailsAdapter) findings.push("bindings는 Wails adapter 전용");
        } else {
          const packageName = module.startsWith("@") ? module.split("/").slice(0, 2).join("/") : module.split("/")[0];
          if (!allowedPackages.has(packageName)) findings.push(`금지 의존성: ${packageName}`);
          if (packageName === "@wailsio/runtime" && !wailsAdapter) findings.push("Wails runtime은 adapter 전용");
        }
      }
    }
    if (token.kind === SyntaxKind.AnyKeyword) findings.push("any 금지");
    if (token.kind === SyntaxKind.Identifier) {
      if (["document", "window"].includes(token.text) && !domAllowed) findings.push("DOM은 view/platform 전용");
      if (["CanvasRenderingContext2D", "HTMLCanvasElement"].includes(token.text) && !canvasAllowed) findings.push("Canvas는 adapter/export 전용");
      if (effectRunners.test(token.text) && !runAllowed) findings.push("Effect 실행은 entrypoint 전용");
      if (token.text === "eval") findings.push("동적 코드/require 금지");
    }
  }
  return [...new Set(findings)];
}

export function inspectManifest(manifest) {
  const findings = [];
  for (const [name, version] of Object.entries(manifest.dependencies ?? {})) {
    if (!allowedPackages.has(name)) findings.push(`금지 의존성: ${name}`);
    if (!/^\d+\.\d+\.\d+(?:-beta\.\d+)?$/.test(version)) findings.push(`정확한 버전 필요: ${name}`);
  }
  for (const [name, version] of Object.entries(manifest.devDependencies ?? {})) {
    if (!/^\d+\.\d+\.\d+$/.test(version)) findings.push(`정확한 개발 버전 필요: ${name}`);
  }
  return findings;
}

async function scanDirectory(directory) {
  const findings = [];
  for (const item of await readdir(directory, { withFileTypes: true })) {
    const path = `${directory}/${item.name}`;
    if (item.isDirectory()) findings.push(...await scanDirectory(path));
    else if (/\.[cm]?[jt]sx?$/.test(path)) {
      for (const finding of inspectSource(path, await readFile(path, "utf8"))) findings.push(`${path}: ${finding}`);
    }
  }
  return findings;
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const manifest = JSON.parse(await readFile("package.json", "utf8"));
  const findings = [...inspectManifest(manifest), ...await scanDirectory("src")];
  if (findings.length) { console.error(findings.join("\n")); process.exitCode = 1; }
  else console.log("의존성/import 정책 통과");
}
