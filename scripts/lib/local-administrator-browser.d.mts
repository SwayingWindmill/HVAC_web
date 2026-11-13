import type { Browser, BrowserContext, Page } from '@playwright/test';

export interface LocalAdministratorSession {
  readonly browser: Browser;
  readonly context: BrowserContext;
  readonly page: Page;
  readonly principal: unknown;
}

export function localAdministratorBrowser(
  origin: string,
  credentialsFile?: string,
): Promise<LocalAdministratorSession>;
