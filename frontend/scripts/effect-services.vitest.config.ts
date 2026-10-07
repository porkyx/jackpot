import { defineConfig } from "vitest/config";

const mutant = process.env.JACKPOT_SERVICE_MUTANT;
const moduleName = process.env.JACKPOT_SERVICE_MODULE;

export default defineConfig({
  resolve: {
    alias: mutant === undefined || moduleName === undefined ? [] : [{
      find: new RegExp(`^.*\\/src\\/${moduleName}$`),
      replacement: mutant.replaceAll("\\", "/"),
    }],
  },
  test: {
    environment: "happy-dom",
    include: ["tests/integration/effect-layers.test.ts", "tests/unit/client-ids.test.ts", "tests/dom/dom-platform.test.ts", "tests/integration/runtime-lifecycle.test.ts"],
    coverage: {
      provider: "v8",
      include: ["src/app/layers.ts", "src/app/runtime.ts", "src/contracts/backend.ts", "src/operations/coordinator.ts", "src/platform/ids.ts", "src/platform/dom.ts", "src/platform/canvas.ts"],
      reportsDirectory: "coverage/effect-services",
      reporter: ["text", "json"],
      thresholds: { statements: 100, branches: 100, functions: 100, lines: 100 },
    },
  },
});
