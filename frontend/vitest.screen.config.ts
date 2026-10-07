import { defineConfig } from "vitest/config";
export default defineConfig({ resolve: { alias: process.env.JACKPOT_SCREEN_MUTANT === undefined ? [] : [{ find: new RegExp(`^.*\/${process.env.JACKPOT_SCREEN_SOURCE}$`), replacement: process.env.JACKPOT_SCREEN_MUTANT.replaceAll("\\", "/") }] }, test: {
  include: ["tests/unit/create-screen-model.test.ts", "tests/integration/create-screen-owner.test.ts", "tests/integration/route-editor-observer.test.ts", "tests/dom/create-input.test.ts"], environment: "happy-dom",
  coverage: { provider: "v8", exclude: [], include: ["src/screens/create/screenModel.ts", "src/screens/create/screenOwner.ts", "src/screens/create/inputModel.ts", "src/screens/create/view.ts"], reporter: ["text", "json"], reportsDirectory: "../.task/screen-coverage", thresholds: { statements: 100, branches: 100, functions: 100, lines: 100 } },
} });
