import type { ReactNode } from 'react';
import { cn } from '@/lib/utils';
import './OperationsUI.css';

interface PageScaffoldTab {
  readonly key: string;
  readonly tab: ReactNode;
}

interface PageScaffoldProps {
  title: ReactNode;
  /** Optional authoritative heading node, used when the route owns focus restoration. */
  heading?: ReactNode;
  /** Compact object identity shown beside the primary title. */
  subTitle?: ReactNode;
  /** Secondary object context shown below the title and above tabs. */
  content?: ReactNode;
  readonly breadcrumbItems?: readonly { readonly title: ReactNode }[];
  readonly tabList?: readonly PageScaffoldTab[];
  readonly tabActiveKey?: string;
  readonly onTabChange?: (key: string) => void;
  /** Right-side slot for context, filters, and primary actions. */
  extra?: ReactNode;
  /** Optional root class for fixed or specialized operational workspaces. */
  className?: string;
  children: ReactNode;
}

/**
 * Migration scaffold for legacy Surfaces that have not yet been rebuilt on PageIntro/Main.
 * It intentionally contains no Ant/Pro component dependency and must not be used as the
 * composition template for a new or materially redesigned Surface.
 */
export default function PageScaffold({
  title,
  heading,
  subTitle,
  content,
  breadcrumbItems,
  tabList,
  tabActiveKey,
  onTabChange,
  extra,
  className,
  children,
}: PageScaffoldProps) {
  return (
    <section className={cn('ops-page', className)}>
      <header className="ops-page-header">
        <div className="ops-page-heading">
          {breadcrumbItems?.length ? (
            <nav aria-label="面包屑" className="mb-2 flex flex-wrap items-center gap-1 text-xs text-muted-foreground">
              {breadcrumbItems.map((item, index) => (
                <span key={index} className="flex items-center gap-1">
                  {index > 0 ? <span aria-hidden="true">/</span> : null}
                  <span>{item.title}</span>
                </span>
              ))}
            </nav>
          ) : null}
          <div className="flex min-w-0 flex-wrap items-baseline gap-2">
            {heading ?? <h1 className="ops-page-title text-2xl font-semibold tracking-tight">{title}</h1>}
            {subTitle != null ? <span className="text-sm text-muted-foreground">{subTitle}</span> : null}
          </div>
          {content != null ? <div className="mt-2 max-w-4xl text-sm leading-6 text-muted-foreground">{content}</div> : null}
        </div>
        {extra != null ? <div className="ops-page-actions">{extra}</div> : null}
      </header>

      {tabList?.length ? (
        <div role="tablist" aria-label="页面分区" className="flex min-w-0 gap-1 overflow-x-auto border-b">
          {tabList.map((tab) => {
            const active = tab.key === tabActiveKey;
            return (
              <button
                key={tab.key}
                type="button"
                role="tab"
                aria-selected={active}
                className={cn(
                  'relative h-9 shrink-0 px-2 text-sm font-medium text-muted-foreground outline-none transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring',
                  active && 'text-foreground after:absolute after:inset-x-0 after:bottom-0 after:h-0.5 after:bg-primary',
                )}
                onClick={() => onTabChange?.(tab.key)}
              >
                {tab.tab}
              </button>
            );
          })}
        </div>
      ) : null}

      <main className="ops-page-content">{children}</main>
    </section>
  );
}
