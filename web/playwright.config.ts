import { defineConfig, devices } from "@playwright/test";

// End-to-end tests run the app in mock mode (MSW service worker), with a fast
// provisioning simulator so a request reaches Ready in a few seconds.
const PORT = 5180;

export default defineConfig({
  testDir: "./e2e",
  outputDir: "./test-results",
  fullyParallel: false,
  retries: 0,
  reporter: [["list"]],
  use: {
    baseURL: `http://localhost:${PORT}`,
    trace: "retain-on-failure",
    viewport: { width: 1440, height: 1000 },
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 1000 } } }],
  webServer: {
    command: `pnpm exec vite --port ${PORT} --strictPort`,
    url: `http://localhost:${PORT}`,
    reuseExistingServer: !process.env.CI,
    env: { VITE_API_MODE: "mock", VITE_MOCK_TICK_MS: "150", VITE_MOCK_LATENCY_MS: "40" },
    timeout: 60_000,
  },
});
