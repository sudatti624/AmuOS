import { defineConfig, devices } from "@playwright/test";

// Each spec starts its own control plane (see e2e/support.ts), so files can run
// in parallel without sharing state or authentication rate limits.
export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  // The github reporter turns failures into annotations readable without logs.
  reporter: process.env.CI ? [["list"], ["github"], ["html", { open: "never" }]] : [["list"]],
  use: {
    // The test certificate is self-signed and created per run.
    ignoreHTTPSErrors: true,
    locale: "ja-JP",
    trace: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
