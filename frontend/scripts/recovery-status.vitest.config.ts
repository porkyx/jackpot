import { defineConfig } from "vitest/config";
const mutant = process.env.JACKPOT_RECOVERY_VIEW_MUTANT;
export default defineConfig({
  resolve: { alias: mutant === undefined ? [] : [{ find: /^.*\/src\/ui\/recovery\/view$/, replacement: mutant.replaceAll("\\", "/") }] },
  test: { environment: "happy-dom", include: ["tests/dom/recovery-status.test.ts"], coverage: {
    provider: "v8", include: ["src/ui/recovery/view.ts"], reportsDirectory: "coverage/recovery-status", reporter: ["text", "json"],
    thresholds: { statements: 100, branches: 100, functions: 100, lines: 100 },
  } },
});