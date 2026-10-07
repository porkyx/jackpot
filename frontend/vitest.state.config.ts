import { defineConfig } from "vitest/config";

export default defineConfig({
  resolve: { alias: process.env.JACKPOT_STATE_MUTANT === undefined ? [] : [{
    find: new RegExp(`^.*\/${process.env.JACKPOT_STATE_SOURCE}$`),
    replacement: process.env.JACKPOT_STATE_MUTANT.replaceAll("\\", "/"),
  }] },
  test: {
    include: ["tests/unit/editor-model.test.ts", "tests/unit/owner-states.test.ts"],
    environment: "node",
    coverage: {
      provider: "v8", exclude: [], include: ["src/app/shellState.ts", "src/operations/coordinatorState.ts", "src/screens/create/editorModel.ts"],
      reporter: ["text", "json"], reportsDirectory: "../.task/state-coverage",
      thresholds: { statements: 100, branches: 100, functions: 100, lines: 100 },
    },
  },
});
