import { defineConfig, devices } from "@playwright/test";

// Tests against a running stack (docker compose, dev sign-in), not mock mode:
//   docker compose -f ../deploy/docker-compose.yml up --build -d --wait
//   pnpm e2e:stack          (RVAV_WEB_URL defaults to http://localhost:3000)
export default defineConfig({
  testDir: "./e2e-stack",
  outputDir: "./test-results/stack",
  fullyParallel: false,
  retries: 0,
  reporter: [["list"]],
  use: {
    baseURL: process.env.RVAV_WEB_URL ?? "http://localhost:3000",
    trace: "retain-on-failure",
    viewport: { width: 1440, height: 1000 },
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 1000 } } }],
});
