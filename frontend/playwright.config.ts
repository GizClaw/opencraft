import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  fullyParallel: true,
  retries: 2,
  use: {
    baseURL: 'http://127.0.0.1:4173',
    headless: true,
  },
  webServer: {
    command: 'npm run preview -- --port 4173 --strictPort --host 127.0.0.1',
    url: 'http://127.0.0.1:4173',
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
  },
  projects: [
    { name: 'chromium', use: { browserName: 'chromium' } },
    // WebKit runs only the perf budget spec: the desktop app ships
    // WKWebView (JSC), so that is where layout/GC budgets matter most,
    // but a full second matrix would double CI time for no extra
    // coverage of the interaction specs.
    {
      name: 'webkit-perf',
      use: { browserName: 'webkit' },
      testMatch: /perf\.spec\.ts/,
    },
  ],
});
