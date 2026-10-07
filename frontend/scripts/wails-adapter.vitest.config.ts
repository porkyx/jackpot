import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    environment: "happy-dom",
    include: ["tests/integration/wails-backend.test.ts", "tests/integration/wails-live.test.ts", "tests/integration/operation-lookup.test.ts"],
    coverage: {
      provider: "v8", include: ["src/platform/backend.ts", "src/platform/wails.ts"],
      reportsDirectory: "coverage/wails-adapter", reporter: ["text", "json"],
    },
  },
});
