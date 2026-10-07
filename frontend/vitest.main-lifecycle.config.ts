import { defineConfig } from "vitest/config";

export default defineConfig({
  resolve: { alias: process.env.JACKPOT_MAIN_MUTANT === undefined ? [] : [{
    find: /^.*\/src\/main$/,
    replacement: process.env.JACKPOT_MAIN_MUTANT.replaceAll("\\", "/"),
  }] },
  test: { include: ["tests/integration/main-screen-lifecycle.test.ts"], environment: "happy-dom" },
});
