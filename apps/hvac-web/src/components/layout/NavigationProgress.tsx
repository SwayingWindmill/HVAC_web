import { useRouterState } from '@tanstack/react-router';
import { cn } from '@/lib/utils';

/** A hairline across the top while a route loads; quick navigations finish before it shows. */
export function NavigationProgress() {
  const pending = useRouterState({ select: (state) => state.status === 'pending' });
  return (
    <div
      aria-hidden="true"
      className={cn(
        'pointer-events-none fixed inset-x-0 top-0 z-50 h-0.5 overflow-hidden transition-opacity duration-300',
        pending ? 'opacity-100 delay-150' : 'opacity-0',
      )}
    >
      <div className="h-full w-1/3 bg-primary animate-navigation-progress motion-reduce:w-full motion-reduce:animate-none" />
    </div>
  );
}

/** The first focusable element: keyboard users jump past the sidebar and header to the page. */
export function SkipToMain() {
  return (
    <a
      href="#content"
      className="fixed left-4 top-3 z-50 -translate-y-16 rounded-md bg-primary print:hidden px-3 py-2 text-sm font-medium text-primary-foreground shadow-sm transition-transform focus-visible:translate-y-0 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      跳到正文
    </a>
  );
}
