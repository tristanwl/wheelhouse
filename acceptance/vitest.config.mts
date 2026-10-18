import { defineConfig } from "vitest/config";

// Black-box HTTP acceptance suite. Run with `npm run test:acceptance`; it is
// not part of `npm test`. See acceptance/README.md.
export default defineConfig({
  test: {
    environment: "node",
    include: ["acceptance/**/*.test.ts"],
    // Starting a server and waiting out upstream timeouts is slow by nature.
    testTimeout: 60_000,
    hookTimeout: 90_000,
  },
});
