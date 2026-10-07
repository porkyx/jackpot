import { readFileSync, writeFileSync, rmSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const source = readFileSync(new URL("../src/contracts/schemas.ts", import.meta.url), "utf8");
const mutations = [
  ["safe integer", "Number.isSafeInteger(value) && value >= 0", "value >= 0"],
  ["nonnegative", "Number.isSafeInteger(value) && value >= 0", "Number.isSafeInteger(value)"],
  ["year minimum", "year < 1 ||", "false ||"],
  ["month minimum", "month < 1 ||", "false ||"],
  ["month maximum", "month > 12 ||", "false ||"],
  ["day minimum", "day < 1 ||", "false ||"],
  ["hour maximum", "hour > 23 ||", "false ||"],
  ["minute maximum", "minute > 59 ||", "false ||"],
  ["second maximum", "second > 59", "false"],
  ["leap divisible by four", "year % 4 === 0", "true"],
  ["leap century", "year % 100 !== 0", "true"],
  ["leap four centuries", "year % 400 === 0", "false"],
  ["month days", "day > maxDay", "false"],
  ["zero UTC", "return !/^0001", "return /^0001"],
  ["zero enum", 'value !== ""', "true"],
  ["descriptor allowed keys", "Object.hasOwn(OperationDescriptorFields.fields, key)", "true"],
  ["descriptor pending", 'status === "pending"', "true"],
  ["nonnull array", "value !== null", "true"],
  ["maximum pending", "pendingOperations: boundedRequiredArray(OperationDescriptor, 64)", "pendingOperations: boundedRequiredArray(OperationDescriptor, 65)"],
  ["maximum recovery", "operations: boundedRequiredArray(OperationDescriptor, 64)", "operations: boundedRequiredArray(OperationDescriptor, 65)"],
  ["known kind", ".includes(kind)", ".includes(kind) || true"],
  ["known theme", ".includes(theme)", ".includes(theme) || true"],
  ["snapshot pages", "pages: count(20)", "pages: count(21)"],
  ["snapshot accepted", "acceptedComments: count(100000)", "acceptedComments: count(100001)"],
  ["snapshot unsupported", "unsupportedComments: count(100000)", "unsupportedComments: count(100001)"],
  ["maximum recent", "boundedRequiredArray(ResultReference, 16)", "boundedRequiredArray(ResultReference, 17)"],
  ["protocol", "version === 1", "true"],
  ["operation null", "response.operationId === null", "false"],
  ["revision null", "response.revision === null", "false"],
  ["success missing", "response.data === undefined ||", "false ||"],
  ["success null", "response.data === null ||", "false ||"],
  ["success code", "response.code !== undefined ||", "false ||"],
  ["success message", "response.messageKey !== undefined", "false"],
  ["failure data", "response.data !== undefined ||", "false ||"],
  ["failure code", "response.code === undefined ||", "false ||"],
  ["failure key", "response.messageKey !== response.code", "false"],
  ["observation allowed keys", "Object.hasOwn(OperationObservationFields.fields, key)", "true"],
  ["unknown kind absent", "value.kind === null &&", "true &&"],
  ["unknown collection absent", "value.collectionId === null &&", "true &&"],
  ["unknown round absent", "value.roundId === null &&", "true &&"],
  ["unknown revision absent", "value.revision === null &&", "true &&"],
  ["unknown failure absent", 'value.failureCode === null ? undefined : "unknown operation has metadata"', 'true ? undefined : "unknown operation has metadata"'],
  ["known kind required", "value.kind === null ||", "false ||"],
  ["known collection required", "value.collectionId === null ||", "false ||"],
  ["known round required", "value.roundId === null ||", "false ||"],
  ["known revision required", 'value.revision === null) return "known operation target required"', 'false) return "known operation target required"'],
  ["failed code required", 'value.failureCode !== null ? undefined : "failure code required"', 'true ? undefined : "failure code required"'],
  ["nonfailed code absent", 'value.failureCode === null ? undefined : "unexpected failure code"', 'true ? undefined : "unexpected failure code"'],
];
const mutant = new URL("./.schema-mutant.ts", import.meta.url);
let survivors = 0;
try {
  for (const [name, from, to] of mutations) {
    if (source.split(from).length !== 2) throw new Error(`Mutation target must be unique: ${name}`);
    writeFileSync(mutant, source.replace(from, to));
    const result = spawnSync(process.execPath, ["node_modules/vitest/vitest.mjs", "run", "tests/integration/schema-contract.test.ts", "tests/integration/operation-lookup.test.ts"], {
      cwd: fileURLToPath(new URL("../", import.meta.url)),
      env: { ...process.env, JACKPOT_SCHEMA_MODULE: fileURLToPath(mutant), NO_COLOR: "1" },
      encoding: "utf8", timeout: 20000,
    });
    if (result.error || result.signal) throw new Error(`Mutation harness failed: ${name}`);
    if (result.status !== 0 && !/Tests\s+\d+ failed/.test(result.stdout)) {
      throw new Error(`Mutation suite did not execute: ${name}\n${result.stdout}\n${result.stderr}`);
    }
    const killed = result.status !== 0 && /Tests\s+\d+ failed/.test(result.stdout);
    if (!killed) survivors++;
    console.log(`${killed ? "killed" : "SURVIVED"}: ${name}`);
  }
} finally {
  rmSync(mutant, { force: true });
}
if (survivors !== 0) process.exitCode = 1;
