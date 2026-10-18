import { defineConfig } from "vitest/config";

// Browser smoke test (e2e/smoke.test.ts, README "Smoke test"). Separate from
// `npm test` because it needs the Go binary and Chromium.
export default defineConfig({
  test: {
    environment: "node",
    include: ["e2e/**/*.test.ts"],
    testTimeout: 60_000,
    hookTimeout: 120_000,
    fileParallelism: false,
  },
});
