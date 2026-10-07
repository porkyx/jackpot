import { defineConfig } from "vitest/config";

export default defineConfig({
  resolve: { alias: process.env.JACKPOT_ROUTE_MUTANT === undefined ? [] : [{
    find: new RegExp(`^.*\/${process.env.JACKPOT_ROUTE_SOURCE}$`),
    replacement: process.env.JACKPOT_ROUTE_MUTANT.replaceAll("\\", "/"),
  }] },
  test: {
    include: ["tests/unit/routes.test.ts", "tests/integration/route-manager.test.ts"],
    environment: "happy-dom",
    coverage: {
      provider: "v8", exclude: [], include: ["src/app/routes.ts", "src/app/routeManager.ts"],
      reporter: ["text", "json"], reportsDirectory: "../.task/route-coverage",
      thresholds: { statements: 100, branches: 100, functions: 100, lines: 100 },
    },
  },
});
