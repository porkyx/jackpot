// Controls only the isolated cmd/wailssmoke subprocess; no OS input injection.
import assert from "node:assert/strict";
import { execFile, spawn } from "node:child_process";
import { access, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { createServer } from "node:net";
import { dirname, isAbsolute, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";
import { chromium, expect } from "@playwright/test";

const root = fileURLToPath(new URL("../../../", import.meta.url));
const options = parseArgs({ options: { exe: { type: "string" }, assets: { type: "string" }, report: { type: "string" } } }).values;
let step = "arguments";
let failureStep;
let nativeDiagnostics;
let nativeCleanupError;
const checks = [];
const verify = async (name, test) => { step = name; await test(); checks.push(name); };
const deadline = async (promise, milliseconds, name) => {
  let timer;
  try { return await Promise.race([promise, new Promise((_, reject) => { timer = setTimeout(() => reject(new Error(name)), milliseconds); })]); }
  finally { clearTimeout(timer); }
};
const inside = (path, expectedRoot) => isAbsolute(path) && resolve(path).startsWith(`${resolve(expectedRoot)}${sep}`);
async function unusedLoopbackPort() {
  const server = createServer();
  await new Promise((resolveListening, reject) => { server.once("error", reject); server.listen(0, "127.0.0.1", resolveListening); });
  const address = server.address(); assert.ok(address !== null && typeof address === "object");
  const port = address.port;
  await new Promise((resolveClosed, reject) => server.close((error) => error ? reject(error) : resolveClosed()));
  return port;
}

async function waitForNativeEngineProcesses(ids) {
  assert.equal(process.platform, "win32");
  assert.ok(ids.length > 0 && ids.every((id) => Number.isSafeInteger(id) && id > 0));
  const command = "$ErrorActionPreference='Stop'; $nativeKeyboardPids=@(" + ids.join(",") + "); $nativeKeyboardProcesses=Get-Process -Id $nativeKeyboardPids -ErrorAction SilentlyContinue; try { if($nativeKeyboardProcesses){$nativeKeyboardProcesses | Wait-Process -Timeout 20 -ErrorAction Stop} } catch { exit 1 }; exit 0";
  await new Promise((resolveExited, reject) => execFile("powershell.exe", ["-NoProfile", "-NonInteractive", "-Command", command], { windowsHide: true, timeout: 25000 }, (error, _stdout, stderr) => { if (error) { nativeCleanupError = { code: error.code, killed: error.killed, diagnostic: stderr.slice(0, 1000) }; reject(new Error("Native engine exit deadline")); } else resolveExited(); }));
}

async function main() {
  const { exe, assets, report } = options;
  assert.ok(exe && assets && report, "--exe --assets --report required");
  for (const path of [exe, assets, report]) assert.ok(inside(path, resolve(root, ".task")), "Only isolated .task native smoke paths are allowed");
  assert.equal(exe, resolve(root, ".task/wailssmoke.exe"));
  assert.equal(assets, resolve(root, ".task/native-smoke-assets"));
  await access(exe); await access(resolve(assets, "native-smoke.html")); await access(dirname(report));
  const port = await unusedLoopbackPort();
  const temporary = await mkdtemp(resolve(root, ".task/native-run-keyboard-"));
  const child = spawn(exe, ["-assets", assets, "-out", report, "-cdp-port", String(port), "-work-dir", temporary], { cwd: root, shell: false, windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
  const exited = new Promise((resolveExit, reject) => { child.once("error", reject); child.once("exit", (code, signal) => resolveExit({ code, signal })); });
  void exited.catch(() => {});
  const ready = new Promise((resolveReady, reject) => {
    let output = "";
    const onData = (chunk) => {
      output = (output + chunk.toString()).slice(-4096);
      if (output.includes("JACKPOT_NATIVE_KEYBOARD_READY")) { child.stdout.off("data", onData); resolveReady(); }
    };
    child.stdout.on("data", onData); child.once("error", reject);
    void exited.then(() => reject(new Error("Native process exited before readiness")), reject);
  });
  // Drain logs, but do not project raw runtime/driver errors into the report.
  child.stderr.resume();
  let browser; let page; let done = false; let evidence; let observations;
  let engineProcessIds = [];
  const ime = { level: "cdp-renderer-composition", verified: false, physicalWindowsIme: false };
  try {
    step = "native-ready"; await deadline(ready, 30000, "Native readiness deadline");
    step = "loopback-cdp";
    browser = await chromium.connectOverCDP(`http://127.0.0.1:${port}`, { timeout: 20000, isWebView: true });
    const engineSession = await browser.newBrowserCDPSession();
    try {
      const processes = await engineSession.send("SystemInfo.getProcessInfo");
      engineProcessIds = processes.processInfo.map((entry) => entry.id);
      assert.ok(engineProcessIds.length > 0);
    } finally { await engineSession.detach(); }
    const pages = browser.contexts().flatMap((context) => context.pages()).filter((candidate) => {
      const url = new URL(candidate.url()); return url.pathname === "/native-smoke.html" && url.searchParams.get("keyboard") === "1";
    });
    assert.equal(pages.length, 1, "Exactly one isolated keyboard target required"); page = pages[0];
    assert.equal(await page.evaluate(() => window.__jackpotNativeKeyboard?.phase), "ready");
    page.setDefaultTimeout(5000); page.setDefaultNavigationTimeout(10000);
    const focused = (selector) => expect(page.locator(selector)).toBeFocused({ timeout: 5000 });
    const stateIs = (predicate) => page.waitForFunction(predicate, undefined, { timeout: 5000 });

    await verify("trusted-tab-shift-tab-main-route-enter", async () => {
      await page.locator('nav a[href="#/create"]').focus();
      await expect(page.locator('nav a[href="#/history"]')).toHaveCount(0);
      await page.keyboard.press("Tab"); await focused('select[name="theme"]');
      await page.keyboard.press("Shift+Tab"); await focused('nav a[href="#/create"]');
      await page.keyboard.press("Enter");
      await expect(page.locator("#app h2")).toHaveText("일반 추첨", { timeout: 5000 });
      await page.evaluate(() => { window.location.hash = "#/history"; });
      await expect(page.locator("#app h2")).toHaveText("화면을 찾을 수 없습니다", { timeout: 5000 });
      await page.locator('nav a[href="#/create"]').focus(); await page.keyboard.press("Enter");
      await expect(page.locator("#app h2")).toHaveText("일반 추첨", { timeout: 5000 });
    });
    await verify("native-select-light-dark-and-visible-focus", async () => {
      await page.keyboard.press("Tab"); await focused('select[name="theme"]');
      const focus = await page.evaluate(() => ({ visible: document.activeElement.matches(":focus-visible"), width: getComputedStyle(document.activeElement).outlineWidth, style: getComputedStyle(document.activeElement).outlineStyle }));
      assert.equal(focus.visible, true); assert.equal(focus.style, "solid"); assert.equal(focus.width, "3px");
      await page.keyboard.press("Home"); await page.keyboard.press("ArrowDown");
      await expect(page.locator(".shell")).toHaveAttribute("data-appearance", "light");
      await page.keyboard.press("End"); await expect(page.locator(".shell")).toHaveAttribute("data-appearance", "dark");
    });
    await verify("native-form-invalid-required-label-error", async () => {
      await page.keyboard.press("Tab"); await focused("#native-keyboard-email");
      assert.equal(await page.locator("#native-keyboard-email").evaluate((input) => input.labels?.[0]?.htmlFor === input.id), true);
      await page.keyboard.type("invalid"); await page.keyboard.press("Enter");
      await stateIs(() => window.__jackpotNativeKeyboard.invalids === 1);
      assert.equal(await page.evaluate(() => window.__jackpotNativeKeyboard.submits), 0);
      await expect(page.locator("#native-keyboard-email")).toHaveAttribute("aria-invalid", "true");
      await expect(page.locator("#native-keyboard-email")).toHaveAttribute("aria-describedby", "native-keyboard-error");
      await expect(page.locator("#native-keyboard-error")).toHaveAttribute("role", "alert");
    });
    await verify("native-form-enter-and-button-submit", async () => {
      await page.keyboard.press("Control+A"); await page.keyboard.type("keyboard@example.invalid"); await page.keyboard.press("Enter");
      await stateIs(() => window.__jackpotNativeKeyboard.submits === 1);
      await expect(page.locator("#native-keyboard-email")).not.toHaveAttribute("aria-invalid", "true");
      await page.keyboard.press("Tab"); await focused("#native-keyboard-submit"); await page.keyboard.press("Enter");
      await stateIs(() => window.__jackpotNativeKeyboard.submits === 2);
    });
    await verify("native-checkbox-space", async () => {
      await page.keyboard.press("Tab"); await focused("#native-keyboard-dialog-trigger");
      await page.keyboard.press("Tab"); await focused("#native-keyboard-checkbox");
      await page.keyboard.press("Space"); await expect(page.locator("#native-keyboard-checkbox")).toBeChecked();
      await page.keyboard.press("Shift+Tab"); await focused("#native-keyboard-dialog-trigger");
    });
    await verify("native-modal-focus-trap-and-escape-cleanup", async () => {
      await page.keyboard.press("Enter"); await stateIs(() => window.__jackpotNativeKeyboard.dialogsOpened === 1);
      await focused("#native-keyboard-password"); await page.keyboard.type("ephemeral-check");
      await page.keyboard.press("Tab"); await focused("#native-keyboard-confirm");
      await page.keyboard.press("Shift+Tab"); await focused("#native-keyboard-password");
      await page.keyboard.press("Shift+Tab"); await focused("#native-keyboard-cancel");
      await page.keyboard.press("Escape"); await stateIs(() => window.__jackpotNativeKeyboard.dialogsClosed.length === 1);
      assert.deepEqual(await page.evaluate(() => window.__jackpotNativeKeyboard.dialogsClosed[0]), { reason: "cancel", secretCleared: true, focusRestored: true });
      await focused("#native-keyboard-dialog-trigger"); await expect(page.locator("#native-keyboard-dialog")).toHaveCount(0);
    });
    await verify("native-dialog-form-enter-cleanup", async () => {
      await page.keyboard.press("Enter"); await stateIs(() => window.__jackpotNativeKeyboard.dialogsOpened === 2);
      await focused("#native-keyboard-password"); await page.keyboard.type("ephemeral-check"); await page.keyboard.press("Enter");
      await stateIs(() => window.__jackpotNativeKeyboard.dialogsClosed.length === 2);
      assert.deepEqual(await page.evaluate(() => window.__jackpotNativeKeyboard.dialogsClosed[1]), { reason: "close", secretCleared: true, focusRestored: true });
      await focused("#native-keyboard-dialog-trigger");
    });
    await verify("cdp-korean-renderer-composition-not-physical-ime", async () => {
      await page.keyboard.press("Tab"); await page.keyboard.press("Tab"); await focused("#native-keyboard-ime");
      const session = await page.context().newCDPSession(page);
      try {
        await session.send("Input.imeSetComposition", { text: "하", selectionStart: 1, selectionEnd: 1 });
        await session.send("Input.imeSetComposition", { text: "한", selectionStart: 1, selectionEnd: 1 });
        await session.send("Input.insertText", { text: "한" });
        await expect(page.locator("#native-keyboard-ime")).toHaveValue("한");
        const events = await page.evaluate(() => window.__jackpotNativeKeyboard.events.filter((event) => event.type.startsWith("composition")));
        for (const type of ["compositionstart", "compositionupdate", "compositionend"]) assert.ok(events.some((event) => event.type === type && event.trusted));
        ime.verified = true;
      } finally { await session.detach(); }
    });
    await verify("trusted-default-events-and-private-observations", async () => {
      const events = await page.evaluate(() => window.__jackpotNativeKeyboard.events);
      for (const key of ["Tab", "Shift", "Enter", "Escape", " "]) assert.ok(events.some((event) => event.type === "keydown" && event.key === key && event.trusted));
      for (const [type, target] of [["invalid", "native-keyboard-email"], ["submit", "native-keyboard-form"], ["cancel", "native-keyboard-dialog"], ["click", "native-keyboard-dialog-trigger"]]) assert.ok(events.some((event) => event.type === type && event.target === target && event.trusted));
      assert.ok(events.every((event) => Object.keys(event).sort().join(",") === "composing,key,target,trusted,type"));
      assert.ok(!JSON.stringify(events).includes("ephemeral-check"));
    });
    await verify("open-dialog-scope-disposal-and-duplicate-cleanup", async () => {
      // Focus setup is confined to this isolated page; every activation is a trusted key.
      await page.locator("#native-keyboard-dialog-trigger").focus(); await page.keyboard.press("Enter");
      await stateIs(() => window.__jackpotNativeKeyboard.dialogsOpened === 3); await focused("#native-keyboard-password"); await page.keyboard.type("ephemeral-check");
      observations = await page.evaluate(async () => {
        const probe = window.__jackpotNativeKeyboard;
        await probe.finish(true);
        return { phase: probe.phase, failure: probe.failure, events: probe.events, invalids: probe.invalids, submits: probe.submits, dialogsOpened: probe.dialogsOpened, dialogsClosed: probe.dialogsClosed, activeListeners: probe.activeListeners, secretClearedOnDispose: probe.secretClearedOnDispose, probeRemoved: probe.probeRemoved };
      });
      assert.equal(observations.phase, "done"); assert.equal(observations.activeListeners, 0);
      assert.equal(observations.secretClearedOnDispose, true); assert.equal(observations.probeRemoved, true);
      done = true;
    });
    const result = await deadline(exited, 10000, "Native exit deadline"); assert.equal(result.code, 0); assert.equal(result.signal, null);
    const native = JSON.parse(await readFile(report, "utf8")); assert.equal(native.keyboardVerified, true); assert.equal(native.mainDisposed, true); assert.equal(native.failure, "");
    evidence = { level: "actual-hidden-wails-webview2-cdp", passed: true, playwright: "1.63.0", engineVersion: browser.version(), checks, ime, observations, nativeEngineProcessIds: engineProcessIds, nativeReport: report, physicalOsInput: false };

  } catch (error) {
    failureStep = step;
    if (page) nativeDiagnostics = await page.evaluate(() => {
      const probe = window.__jackpotNativeKeyboard;
      return { phase: probe?.phase, activeId: document.activeElement?.id, activeTag: document.activeElement?.tagName, documentHasFocus: document.hasFocus(), visibility: document.visibilityState, events: probe?.events, invalids: probe?.invalids, submits: probe?.submits, dialogsOpened: probe?.dialogsOpened, dialogsClosed: probe?.dialogsClosed };
    }).catch(() => undefined);
    nativeDiagnostics = { ...nativeDiagnostics, engineProcessIds };
    if (page && !done) await deadline(page.evaluate(async () => { await window.__jackpotNativeKeyboard?.finish(false); }), 5000, "Failure cleanup deadline").catch(() => {});
    await writeFile(`${report}.keyboard.json`, `${JSON.stringify({ level: "actual-hidden-wails-webview2-cdp", passed: false, failedStep: step, checks, ime, physicalOsInput: false }, null, 2)}\n`).catch(() => {});
    throw error;
  } finally {
    if (browser?.isConnected()) await browser.close().catch(() => {}); // CDP close disconnects this transport only.
    if (child.exitCode === null && child.signalCode === null) {
      await deadline(exited, 10000, "Native cleanup deadline").catch(() => { child.kill(); });
    }
    step = "native-process-cleanup";
    if (engineProcessIds.length > 0) await waitForNativeEngineProcesses(engineProcessIds);
    assert.ok(inside(temporary, resolve(root, ".task")) && dirname(temporary) === resolve(root, ".task") && temporary.startsWith(resolve(root, ".task/native-run-keyboard-")));
    await rm(temporary, { recursive: true });
  }
  evidence.nativeEngineProcessesExited = true; evidence.temporaryDirectoryRemoved = true;
  await writeFile(`${report}.keyboard.json`, `${JSON.stringify(evidence, null, 2)}\n`);
  console.log(JSON.stringify(evidence));
}

try { await main(); }
catch {
  if (options.report && inside(options.report, resolve(root, ".task"))) await writeFile(`${options.report}.keyboard.json`, `${JSON.stringify({ passed: false, failedStep: failureStep ?? step, checks, diagnostics: nativeDiagnostics, cleanupError: nativeCleanupError, physicalOsInput: false }, null, 2)}\n`).catch(() => {});
  console.error(`Native keyboard verification failed at ${failureStep ?? step}`); process.exitCode = 1;
}
