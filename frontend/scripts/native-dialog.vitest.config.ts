import { defineConfig } from "vitest/config";
const mutant = process.env.JACKPOT_NATIVE_MUTANT;
const moduleName = process.env.JACKPOT_NATIVE_MODULE;
export default defineConfig({
  resolve: { alias: mutant === undefined || moduleName === undefined ? [] : [{
    find: new RegExp(`^.*\\/src\\/ui\\/${moduleName}\\/view$`), replacement: mutant.replaceAll("\\", "/"),
  }] },
  test: {
    environment: "happy-dom", include: ["tests/dom/native-fields-dialog.test.ts"],
    coverage: {
      provider: "v8", include: ["src/ui/fields/view.ts", "src/ui/dialog/view.ts"], reportsDirectory: "coverage/native-dialog",
      reporter: ["text", "json"], thresholds: { statements: 100, branches: 100, functions: 100, lines: 100 },
    },
  },
});
