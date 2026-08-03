// @ts-check
const { defineConfig } = require("@playwright/test");
const fs = require("fs");

// In the dev sandbox a Chromium is pre-installed outside playwright's
// registry; use it via executablePath. In CI `playwright install` provides
// the browser and this stays undefined.
const preinstalled = "/opt/pw-browsers/chromium";
const executablePath =
  process.env.PLANMAN_CHROMIUM ||
  (fs.existsSync(preinstalled) ? preinstalled : undefined);

module.exports = defineConfig({
  testDir: ".",
  timeout: 45_000,
  fullyParallel: true,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? "github" : "list",
  use: {
    headless: true,
    launchOptions: executablePath ? { executablePath } : {},
  },
});
