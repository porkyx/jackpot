// Ownership checks are shared by the native driver and independent negative tests.
import assert from "node:assert/strict";
import { win32 } from "node:path";

export function ownedWorkPath(work, workspace) {
  assert.equal(typeof work, "string"); assert.equal(typeof workspace, "string");
  const target = win32.resolve(work), task = win32.join(win32.resolve(workspace), ".task");
  assert.ok(win32.isAbsolute(work) && target.toLowerCase() === work.toLowerCase(), "Absolute canonical work path required");
  assert.equal(win32.dirname(target).toLowerCase(), task.toLowerCase(), "Workspace .task child required");
  assert.ok(win32.basename(target).startsWith("product-run-webview-") && win32.basename(target).length > 20, "Unique recovery work directory required");
  return target;
}
export function processRole(command) {
  assert.equal(typeof command, "string");
  const roles = [...command.matchAll(/(?:^|\s)--type=([^\s"]+)/g)].map(match => match[1]);
  assert.ok(roles.length <= 1, "Ambiguous process role");
  return roles[0] ?? "browser";
}
export function profileArgument(command) {
  const matches = [...command.matchAll(/(?:^|\s)--user-data-dir=(?:"([^"]+)"|([^\s"]+))/g)];
  assert.equal(matches.length, 1, "Exactly one profile path is required");
  return win32.resolve(matches[0][1] ?? matches[0][2]);
}
export function requireOwnedWebView(record, work, workspace, roles) {
  ownedWorkPath(work, workspace);
  assert.ok(record !== null && typeof record === "object", "Process record required");
  assert.ok(Number.isSafeInteger(record.id) && record.id > 0, "Positive PID required");
  assert.equal(record.name?.toLowerCase(), "msedgewebview2.exe", "Only WebView2 processes allowed");
  assert.equal(typeof record.command, "string");
  const profile=profileArgument(record.command).toLowerCase(),expected=win32.join(work,"webview-profile").toLowerCase();
  // WebView2 appends its fixed EBWebView child to the supplied user-data folder.
  assert.ok(profile===expected || profile===win32.join(expected,"EBWebView").toLowerCase(),"Exact isolated profile or fixed EBWebView child required");
  assert.ok(typeof record.created === "string" && Number.isFinite(Date.parse(record.created)), "Creation timestamp required");
  assert.ok(typeof record.started === "string" && Number.isFinite(Date.parse(record.started)), "Start timestamp required");
  const role = processRole(record.command);
  if (roles !== undefined) assert.ok(roles.includes(role), "Allowed process role required");
  return Object.freeze({ ...record, role });
}
export function selectCrashProcess(records, cdpProcesses, role, work, workspace) {
  assert.ok(role === "browser" || role === "renderer", "Crash role must be browser or renderer");
  assert.ok(Array.isArray(records) && Array.isArray(cdpProcesses), "Native and CDP records required");
  const cdps = cdpProcesses.filter(record => record.type === role);
  assert.ok(cdps.length > 0 && cdps.every(record => Number.isSafeInteger(record.id) && record.id > 0), "CDP process proof required");
  const matches = records.filter(record => cdps.some(cdp => cdp.id === record.id)).map(record => requireOwnedWebView(record, work, workspace, [role]));
  assert.equal(matches.length, 1, "Exactly one owned crash target required");
  return matches[0];
}
export function requireUnchangedGoState(before, after) {
  assert.equal(after.session, before.session, "WebView recovery must preserve Go session");
  assert.deepEqual(after.Counts ?? after.counts, before.Counts ?? before.counts, "Recovery must not write durable state");
  assert.deepEqual(after.latest, before.latest, "Results, revision and management authority must survive");
  assert.deepEqual(after.activeDraft, before.activeDraft, "Go draft identity/revision must survive");
  assert.equal(after.FixtureCalls ?? after.fixtureCalls, before.FixtureCalls ?? before.fixtureCalls, "Recovery must not recollect");
}
export function requireFreshBoot(before, after, expectedDraft) {
  assert.equal(after.ok, true); assert.equal(after.backendSessionId, before.backendSessionId);
  assert.deepEqual(after.data.activeDraft, expectedDraft);
  assert.deepEqual(after.data.pendingOperations, []); assert.equal(after.data.pendingCursor, null);
  assert.deepEqual(after.data.recentResults, before.data.recentResults);
}
export function psQuote(value) { assert.equal(typeof value, "string"); return "'" + value.replaceAll("'", "''") + "'"; }