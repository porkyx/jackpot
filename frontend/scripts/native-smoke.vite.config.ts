import { defineConfig } from "vite";
export default defineConfig({
  build: { outDir: "../.task/native-smoke-assets", emptyOutDir: true, rollupOptions: { input: "native-smoke.html" } },
});
