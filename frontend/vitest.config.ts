import { defineConfig } from "vitest/config";

export default defineConfig({
  resolve: {
    alias: process.env.JACKPOT_SCHEMA_MODULE === undefined ? [] : [{
      find: /^.*\/src\/contracts\/schemas$/,
      replacement: process.env.JACKPOT_SCHEMA_MODULE.replaceAll("\\", "/"),
    }],
  },
  test: {
    include: ["tests/**/*.test.ts"],
    environment: "happy-dom",
    coverage: {
      provider: "v8",
      include: ["src/contracts/schemas.ts"],
      reporter: ["text", "json"],
      thresholds: { statements: 100, branches: 100, functions: 100, lines: 100 },
    },
  },
});
