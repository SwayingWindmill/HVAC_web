import { existsSync } from 'node:fs';

import { chromium } from '@playwright/test';

const systemBrowserCandidates = Object.freeze([
  '/usr/bin/google-chrome-stable',
  '/usr/bin/google-chrome',
  '/usr/bin/chromium',
  '/usr/bin/chromium-browser',
]);

export function resolveLinuxBrowserExecutable() {
  if (process.platform !== 'linux') {
    throw new Error('Browser automation is Linux-only. Run it from the WSL/Linux workspace.');
  }

  const candidates = [
    process.env.BROWSER_BINARY,
    chromium.executablePath(),
    ...systemBrowserCandidates,
  ].filter(Boolean);

  const executable = candidates.find((candidate) => existsSync(candidate));
  if (executable) return executable;

  throw new Error(
    'Linux Chromium was not found. Run "npx playwright install chromium" from the WSL/Linux workspace.',
  );
}
