import { defineConfig } from "vitest/config";
const coordinator=process.env.JACKPOT_PRODUCT_COORDINATOR_MUTANT;
const queries=process.env.JACKPOT_PRODUCT_QUERIES_MUTANT;
export default defineConfig({
 resolve:{alias:[...(coordinator===undefined?[]:[{find:/^.*\/src\/operations\/productCoordinator$/,replacement:coordinator.replaceAll("\\","/")}]),...(queries===undefined?[]:[{find:/^.*\/src\/operations\/productQueries$/,replacement:queries.replaceAll("\\","/")}])]},
 test:{environment:"happy-dom",include:["tests/integration/product-coordinator.test.ts","tests/integration/product-queries.test.ts"],coverage:{provider:"v8",include:["src/operations/productCoordinator.ts","src/operations/productQueries.ts"],reportsDirectory:"coverage/product-coordinator",reporter:["text","json"]}},
});