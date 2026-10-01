import type { ReactNode } from 'react';
import { AlertCircle, RefreshCw } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

export interface ErrorStateProps {
  readonly title?: ReactNode;
  readonly message?: ReactNode;
  readonly code?: string;
  readonly onRetry?: () => void;
  readonly action?: ReactNode;
  readonly className?: string;
}

export function ErrorState({
  title = '加载失败',
  message = '服务请求发生异常，请稍后重试',
  code,
  onRetry,
  action,
  className,
}: ErrorStateProps) {
  return (
    <div
      className={cn(
        'flex flex-col items-center justify-center rounded-lg border border-destructive/20 bg-destructive/5 p-8 text-center',
        className
      )}
    >
      <div className="flex size-12 items-center justify-center rounded-full bg-destructive/10 text-destructive mb-3">
        <AlertCircle className="size-6 stroke-[1.5]" />
      </div>
      <h3 className="text-sm font-semibold text-foreground">{title}</h3>
      {message && (
        <p className="mt-1 text-xs text-muted-foreground max-w-sm">{message}</p>
      )}
      {code && (
        <span className="mt-2 text-[10px] font-mono text-muted-foreground bg-muted/60 px-2 py-0.5 rounded">
          错误码: {code}
        </span>
      )}
      <div className="mt-4 flex items-center gap-2">
        {onRetry && (
          <Button variant="outline" size="sm" onClick={onRetry} className="h-8 text-xs gap-1.5">
            <RefreshCw className="size-3" />
            重试
          </Button>
        )}
        {action}
      </div>
    </div>
  );
}
