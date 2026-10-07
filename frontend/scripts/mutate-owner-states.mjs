import { readFile, writeFile, mkdir, unlink } from "node:fs/promises";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";

const editor = "src/screens/create/editorModel";
const coordinator = "src/operations/coordinatorState";
const shell = "src/app/shellState";
const mutations = [
  ["counter integer", editor, "!Number.isSafeInteger(value)", "false"],
  ["counter zero boundary", editor, "value < 0", "value <= 0"],
  ["counter max boundary", editor, "value >= Number.MAX_SAFE_INTEGER", "value > Number.MAX_SAFE_INTEGER"],
  ["minimum prize rows", editor, "values.multiple.length < 1", "values.multiple.length < 2"],
  ["maximum prize rows", editor, "values.multiple.length > 10", "values.multiple.length >= 10"],
  ["unique prize identities", editor, "size !== values.multiple.length", "size === values.multiple.length"],
  ["input version increments", editor, "inputVersion: next(previous.inputVersion)", "inputVersion: previous.inputVersion"],
  ["locked fields preserve raw", editor, "if (isEditorFieldLocked(model, target)) return model;", "if (!isEditorFieldLocked(model, target)) return model;"],
  ["session fence", editor, "model.identity.backendSessionId === fence.identity.backendSessionId", "model.identity.backendSessionId !== fence.identity.backendSessionId"],
  ["draft fence", editor, "model.identity.draftId === fence.identity.draftId", "model.identity.draftId !== fence.identity.draftId"],
  ["epoch fence", editor, "model.editorEpoch === fence.editorEpoch", "model.editorEpoch !== fence.editorEpoch"],
  ["input fence", editor, "getEditorField(model, target).inputVersion === fence.inputVersion", "getEditorField(model, target).inputVersion !== fence.inputVersion"],
  ["already admitted version", editor, "value.submitted?.inputVersion === value.inputVersion", "value.submitted?.inputVersion !== value.inputVersion"],
  ["receipt sequence match", editor, "submitted.receipt.sequence !== sequence", "submitted.receipt.sequence === sequence"],
  ["ack clears dirty", editor, "dirty: false, validation: valid", "dirty: true, validation: valid"],
  ["only valid dirty can auto admit", editor, "value.validation._tag === \"valid\"", "value.validation._tag !== \"valid\""],
  ["superseded replacement order", editor, "replacement.sequence <= fromSequence", "replacement.sequence < fromSequence"],
  ["reset epoch advances", editor, "editorEpoch: next(model.editorEpoch), lock: null", "editorEpoch: model.editorEpoch, lock: null"],
  ["article reset URL", editor, "model.lock.kind === \"article\" ? field(\"\") : model.url", "model.lock.kind !== \"article\" ? field(\"\") : model.url"],
  ["clean sync preserves dirty", editor, "current.dirty || isEditorFieldLocked(model, target)", "false || isEditorFieldLocked(model, target)"],
  ["inactive prize mode excluded", editor, "model.prizeMode.raw === \"single\"", "model.prizeMode.raw !== \"single\""],
  ["retained session identity", editor, "model.identity.backendSessionId === identity.backendSessionId", "model.identity.backendSessionId !== identity.backendSessionId"],
  ["retained draft identity", editor, "model.identity.draftId === identity.draftId", "model.identity.draftId !== identity.draftId"],
  ["confirmed session", coordinator, "sessionId !== state.backendSessionId", "sessionId === state.backendSessionId"],
  ["revision monotonicity", coordinator, "draft.revision < current.revision", "draft.revision <= current.revision"],
  ["generation monotonicity", coordinator, "draft.articleGeneration < current.articleGeneration", "draft.articleGeneration <= current.articleGeneration"],
  ["stale event boundary", coordinator, "notice.revision <= knownRevision", "notice.revision < knownRevision"],
  ["event max revision", coordinator, "previous.revision >= notice.revision", "previous.revision > notice.revision"],
  ["pending slot boundary", coordinator, "state.pending.length >= 64", "state.pending.length > 64"],
  ["stale hint cap", coordinator, "state.stale.length >= 64", "state.stale.length > 64"],
  ["stale blocks mutation", coordinator, "state.stale.length > 0", "state.stale.length > 1"],
  ["descriptor identity duplicates", coordinator, "unique.size !== data.pendingOperations.length", "unique.size === data.pendingOperations.length"],
  ["system theme projection", shell, "state.theme === \"system\"", "state.theme !== \"system\""],
  ["notice read identity", shell, "notice.id === action.noticeId", "notice.id !== action.noticeId"],
  ["notice cap", shell, ".slice(-MAX_APP_NOTICES)", ".slice(-(MAX_APP_NOTICES - 1))"],
  ["route strips structurally added fields", shell, 'case "result": return Object.freeze({ _tag: "result", collectionId: route.collectionId, roundId: route.roundId });', 'case "result": return Object.freeze({ ...route });'],
  ["confirmed draft strips structurally added fields", coordinator, "backendSessionId: draft.backendSessionId, draftId: draft.draftId", "...draft, backendSessionId: draft.backendSessionId, draftId: draft.draftId"],
  ["stale notice strips structurally added fields", coordinator, "backendSessionId: notice.backendSessionId, entityKind: notice.entityKind", "...notice, backendSessionId: notice.backendSessionId, entityKind: notice.entityKind"],
];
const root = resolve("../.task/owner-state-mutants");
await mkdir(root, { recursive: true });
const mutant = resolve(root, "current.ts");
let killed = 0;
try {
  for (const [name, module, from, to] of mutations) {
    const original = await readFile(`${module}.ts`, "utf8");
    if (!original.includes(from)) throw new Error(`Missing mutation target: ${name}`);
    await writeFile(mutant, original.replaceAll(from, to));
    const run = spawnSync(process.execPath, ["node_modules/vitest/vitest.mjs", "run", "--config", "vitest.state.config.ts"], {
      cwd: process.cwd(), encoding: "utf8", timeout: 20000,
      env: { ...process.env, NO_COLOR: "1", JACKPOT_STATE_SOURCE: module, JACKPOT_STATE_MUTANT: mutant },
    });
    const output = `${run.stdout ?? ""}\n${run.stderr ?? ""}`;
    if (run.error !== undefined || run.signal !== null || !/Tests\s+\d+ failed/.test(output)) {
      throw new Error(`Survived or invalid compile/import/timeout mutation: ${name}\n${output}`);
    }
    if (run.status === 0) throw new Error(`Mutation reported failures but returned success: ${name}`);
    killed++;
    console.log(`killed: ${name}`);
  }
} finally { await unlink(mutant).catch(() => {}); }
console.log(`${killed}/${mutations.length} owner-state mutations killed`);
