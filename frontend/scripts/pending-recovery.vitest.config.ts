import { defineConfig } from "vitest/config";
const mutant = process.env.JACKPOT_RECOVERY_MUTANT;
export default defineConfig({
  resolve: { alias: mutant === undefined ? [] : [{ find: /^.*\/src\/operations\/pendingRecovery$/, replacement: mutant.replaceAll("\\", "/") }] },
  test: { environment: "happy-dom", include: ["tests/integration/pending-recovery.test.ts"], coverage: {
    provider: "v8", include: ["src/operations/pendingRecovery.ts"], reportsDirectory: "coverage/pending-recovery", reporter: ["text", "json"],
    thresholds: { statements: 100, branches: 100, functions: 100, lines: 100 },
  } },
});
