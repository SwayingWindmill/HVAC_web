// Pure TS registry of session-scoped cache cleaners invoked whenever a 401 or manual sign-out ends the Session.

const signedOutHandlers: (() => void)[] = [];

export function registerSignOutHandler(handler: () => void): void {
  if (!signedOutHandlers.includes(handler)) {
    signedOutHandlers.push(handler);
  }
}

export function runSignOutHandlers(): void {
  for (const handler of signedOutHandlers) {
    handler();
  }
}
