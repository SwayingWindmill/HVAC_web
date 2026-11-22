import type { ReactNode } from 'react';
import { BrandMark } from '@/components/brand-mark';
import ErrorBoundary from '@/components/ErrorBoundary';
import { Button } from '@/components/ui/button';

export function RootErrorBoundary({ children }: { children: ReactNode }) {
  return (
    <ErrorBoundary
      fallback={(
        <main className="grid min-h-svh place-items-center bg-background p-6" data-shell-state="UNAVAILABLE" data-testid="real-global-error">
          <section className="flex w-full max-w-md flex-col gap-4 rounded-xl border border-border bg-card p-6 shadow-xs" role="alert" aria-labelledby="real-global-error-title">
            <div className="flex items-center gap-2.5 text-primary">
              <BrandMark className="size-6 shrink-0" />
              <p className="text-xs font-medium text-destructive-text">出错了</p>
            </div>
            <div className="space-y-1">
              <h1 id="real-global-error-title" className="text-lg font-semibold tracking-tight text-foreground">系统暂时不可用</h1>
              <p className="text-sm text-muted-foreground">页面遇到意外错误，已停止显示业务数据，请刷新后重试。</p>
            </div>
            <div className="flex items-center gap-2">
              <Button type="button" onClick={() => window.location.reload()}>重新加载</Button>
            </div>
          </section>
        </main>
      )}
    >
      {children}
    </ErrorBoundary>
  );
}
