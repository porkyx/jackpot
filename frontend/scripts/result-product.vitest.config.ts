import { defineConfig } from "vitest/config";
import { existsSync } from "node:fs";
import { dirname, resolve } from "node:path";
const moduleName = process.env.JACKPOT_RESULT_MODULE;
const mutant = process.env.JACKPOT_RESULT_MUTANT;
const original = moduleName === undefined ? undefined : resolve("src", moduleName + ".ts");
const copy = mutant === undefined ? undefined : resolve(mutant);
export default defineConfig({
  plugins: original === undefined || copy === undefined ? [] : [{
    name: "isolated-result-copy", enforce: "pre",
    resolveId(source, importer) {
      if (!source.startsWith(".") || importer === undefined) return undefined;
      const current = resolve(importer.split("?")[0]!);
      const from = current === copy ? original : current;
      const bare = resolve(dirname(from), source);
      const target = bare.endsWith(".ts") ? bare : bare + ".ts";
      if (target.toLowerCase() === original.toLowerCase()) return copy;
      if (current === copy && existsSync(target)) return target;
      return undefined;
    },
  }],
  test: {
    environment: "happy-dom",
    include: ["tests/dom/result-product.test.ts", "tests/dom/frozen-product.test.ts", "tests/dom/result-canvas.test.ts"],
    coverage: {
      provider: "v8",
      include: ["src/platform/canvas.ts", "src/features/results/view.ts", "src/features/results/frozen/view.ts", "src/features/export/view.ts"],
      reportsDirectory: "coverage/result-product", reporter: ["text", "json"],
      thresholds: { statements: 0, branches: 0, functions: 0, lines: 0 },
    },
  },
});
