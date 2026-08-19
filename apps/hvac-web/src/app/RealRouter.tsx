import type { ReactNode } from 'react';
import { BrowserRouter } from 'react-router';

/**
 * Real mode owns one router boundary.  Domain pages read location through
 * React Router instead of coordinating browser history themselves.
 */
export function RealRouter({ children }: { children: ReactNode }) {
  return <BrowserRouter>{children}</BrowserRouter>;
}
