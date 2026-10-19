import type { ReactNode } from 'react';
import ErrorBoundary from '@/components/ErrorBoundary';

export function RootErrorBoundary({ children }: { children: ReactNode }) {
  return (
    <ErrorBoundary
      fallback={(
        <main className="real-shell-state" data-shell-state="UNAVAILABLE" data-testid="real-global-error">
          <section className="real-shell-card" role="alert" aria-labelledby="real-global-error-title">
            <p className="real-shell-eyebrow">出错了</p>
            <h1 id="real-global-error-title">系统暂时不可用</h1>
            <p>页面出了意外错误，已停止显示业务数据，请刷新后重试。</p>
            <button type="button" onClick={() => window.location.reload()}>重新加载</button>
          </section>
        </main>
      )}
    >
      {children}
    </ErrorBoundary>
  );
}
