import { defineConfig } from "vitest/config";

const mutant = process.env.JACKPOT_DOM_MUTANT;
const moduleName = process.env.JACKPOT_DOM_MODULE;
export default defineConfig({
  resolve: { alias: mutant === undefined || moduleName === undefined ? [] : [{
    find: new RegExp(`^.*\\/src\\/ui\\/${moduleName}$`),
    replacement: mutant.replaceAll("\\", "/"),
  }] },
  test: {
    environment: "happy-dom",
    include: ["tests/dom/scoped-view.test.ts", "tests/dom/frame-patcher.test.ts"],
    coverage: {
      provider: "v8", include: ["src/ui/view.ts", "src/ui/frame.ts"],
      reportsDirectory: "coverage/dom-lifecycle", reporter: ["text", "json"],
      thresholds: { statements: 100, branches: 100, functions: 100, lines: 100 },
    },
  },
});
