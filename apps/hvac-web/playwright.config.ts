import { existsSync } from 'node:fs';
import { chromium, defineConfig } from '@playwright/test';

const port = 5190;

// Playwright's Chromium when installed, otherwise the system Chrome the CI runners ship.
const executablePath = [process.env.BROWSER_BINARY, chromium.executablePath(), '/usr/bin/google-chrome-stable', '/usr/bin/chromium']
  .find((candidate) => candidate && existsSync(candidate));

// Browser tests drive the real SPA against a recorded Platform Gateway (e2e/gateway.ts),
// so they need no backend and run in the affected-unit PR gate.
export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: Boolean(process.env.CI),
  reporter: process.env.CI ? 'line' : 'list',
  // The first loads of each worker fetch the lazy route chunks.
  expect: { timeout: 15_000 },
  use: {
    baseURL: `http://localhost:${port}`,
    viewport: { width: 1440, height: 900 },
    locale: 'zh-CN',
    timezoneId: 'Asia/Shanghai',
    launchOptions: { executablePath },
    trace: 'retain-on-failure',
  },
  // The production bundle, as served to operators.
  webServer: {
    command: `node ../../node_modules/vite/bin/vite.js build --config vite.config.ts --outDir dist-e2e && node ../../node_modules/vite/bin/vite.js preview --config vite.config.ts --outDir dist-e2e --port ${port} --strictPort`,
    url: `http://localhost:${port}`,
    reuseExistingServer: !process.env.CI,
    timeout: 240_000,
  },
});
