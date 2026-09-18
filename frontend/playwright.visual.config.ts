import { defineConfig } from '@playwright/test';

// UI audit harness config. Kept apart from playwright.config.ts so the
// CI e2e job (testDir: ./e2e) does not run the screenshot suite.
export default defineConfig({
  testDir: './e2e-visual',
  timeout: 60_000,
  fullyParallel: true,
  retries: 0,
  use: {
    baseURL: 'http://127.0.0.1:4173',
    headless: true,
    viewport: { width: 1568, height: 980 },
    deviceScaleFactor: 1,
  },
  webServer: {
    command: 'npm run preview -- --port 4173 --strictPort --host 127.0.0.1',
    url: 'http://127.0.0.1:4173',
    reuseExistingServer: true,
    timeout: 60_000,
  },
  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
});
