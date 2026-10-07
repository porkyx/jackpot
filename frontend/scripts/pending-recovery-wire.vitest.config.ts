import { defineConfig } from "vitest/config";
export default defineConfig({ test: { environment: "happy-dom", include: ["tests/integration/pending-recovery-wire.test.ts"] } });