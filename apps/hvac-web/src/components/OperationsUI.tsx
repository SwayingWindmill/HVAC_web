import { useCallback, useRef, type CSSProperties, type ReactNode } from 'react';
import { Card, Empty, Spin, Timeline } from 'antd';

export type OperationsMetricTone = 'default' | 'accent' | 'positive' | 'warning' | 'critical';

export type OperationsMetric = {
  key?: string;
  label: ReactNode;
  value: ReactNode;
  suffix?: ReactNode;
  detail?: ReactNode;
  icon?: ReactNode;
  tone?: OperationsMetricTone;
};

type OperationsMetricsProps = {
  items: OperationsMetric[];
  ariaLabel?: string;
  className?: string;
};

export function OperationsMetrics({ items, ariaLabel = '关键运营指标', className }: OperationsMetricsProps) {
  return (
    <section
      className={['ops-metrics', className].filter(Boolean).join(' ')}
      aria-label={ariaLabel}
      style={{ '--ops-metric-count': items.length } as CSSProperties}
    >
      {items.map((item, index) => {
        const tone = item.tone ?? 'default';
        return (
          <div className={`ops-metric is-${tone}`} key={item.key ?? `${index}-${String(item.label)}`}>
            <div className="ops-metric-head">
              <span className="ops-metric-label">{item.label}</span>
              {item.icon ? <span className="ops-metric-icon" aria-hidden="true">{item.icon}</span> : null}
            </div>
            <div className="ops-metric-value">
              <strong>{item.value}</strong>
              {item.suffix ? <span>{item.suffix}</span> : null}
            </div>
            {item.detail ? <div className="ops-metric-detail">{item.detail}</div> : null}
          </div>
        );
      })}
    </section>
  );
}

type OperationsPanelHeadingProps = {
  title: ReactNode;
  icon?: ReactNode;
  meta?: ReactNode;
};

export function OperationsPanelHeading({ title, icon, meta }: OperationsPanelHeadingProps) {
  return (
    <span className="ops-panel-heading">
      {icon ? <span className="ops-panel-heading-icon" aria-hidden="true">{icon}</span> : null}
      <span>{title}</span>
      {meta ? <span className="ops-panel-heading-meta">{meta}</span> : null}
    </span>
  );
}

type OperationsSectionIntroProps = {
  title: ReactNode;
  icon?: ReactNode;
  meta?: ReactNode;
  actions?: ReactNode;
  ariaLabel?: string;
};

export function OperationsSectionIntro({
  title,
  icon,
  meta,
  actions,
  ariaLabel,
}: OperationsSectionIntroProps) {
  return (
    <section className="ops-section-intro" aria-label={ariaLabel ?? String(title)}>
      <div className="ops-section-intro-copy">
        <OperationsPanelHeading title={title} icon={icon} meta={meta} />
      </div>
      {actions ? <div className="ops-section-intro-actions">{actions}</div> : null}
    </section>
  );
}

export function useOperationsDetailFocus() {
  const triggerRef = useRef<HTMLElement | null>(null);
  const triggerKeyRef = useRef<string | null>(null);

  const captureTrigger = useCallback((element?: HTMLElement | null, key?: string) => {
    const target = element ?? (document.activeElement instanceof HTMLElement ? document.activeElement : null);
    if (target) triggerRef.current = target;
    triggerKeyRef.current = key ?? target?.dataset.opsDetailTrigger ?? null;
  }, []);

  const restoreFocus = useCallback(() => {
    const focusTrigger = () => {
      const key = triggerKeyRef.current;
      const replacement = key
        ? Array.from(document.querySelectorAll<HTMLElement>('[data-ops-detail-trigger]'))
          .find((element) => element.dataset.opsDetailTrigger === key)
        : null;
      const target = triggerRef.current?.isConnected ? triggerRef.current : replacement;
      target?.focus({ preventScroll: true });
    };
    requestAnimationFrame(focusTrigger);
    window.setTimeout(focusTrigger, 120);
    window.setTimeout(focusTrigger, 420);
  }, []);

  return { captureTrigger, restoreFocus };
}

type OperationsDetailHeaderProps = {
  title: ReactNode;
  eyebrow?: ReactNode;
  subtitle?: ReactNode;
  status?: ReactNode;
  meta?: ReactNode;
};

export function OperationsDetailHeader({
  title,
  eyebrow,
  subtitle,
  status,
  meta,
}: OperationsDetailHeaderProps) {
  return (
    <div className="ops-detail-header">
      <div className="ops-detail-header-copy">
        {eyebrow ? <div className="ops-detail-eyebrow">{eyebrow}</div> : null}
        <div className="ops-detail-title-row">
          <strong className="ops-detail-title">{title}</strong>
          {status ? <span className="ops-detail-status">{status}</span> : null}
        </div>
        {subtitle ? <div className="ops-detail-subtitle">{subtitle}</div> : null}
      </div>
      {meta ? <div className="ops-detail-meta">{meta}</div> : null}
    </div>
  );
}

type OperationsSummaryStripProps = OperationsMetricsProps;

export function OperationsSummaryStrip({
  items,
  ariaLabel = '详情关键指标',
  className,
}: OperationsSummaryStripProps) {
  return (
    <OperationsMetrics
      items={items}
      ariaLabel={ariaLabel}
      className={['is-detail', className].filter(Boolean).join(' ')}
    />
  );
}

type OperationsDetailSectionProps = {
  title: ReactNode;
  children: ReactNode;
  description?: ReactNode;
  icon?: ReactNode;
  extra?: ReactNode;
  className?: string;
  ariaLabel?: string;
};

export function OperationsDetailSection({
  title,
  children,
  description,
  icon,
  extra,
  className,
  ariaLabel,
}: OperationsDetailSectionProps) {
  return (
    <section
      className={['ops-detail-section', className].filter(Boolean).join(' ')}
      aria-label={ariaLabel ?? String(title)}
    >
      <div className="ops-detail-section-head">
        <div className="ops-detail-section-copy">
          <OperationsPanelHeading title={title} icon={icon} />
          {description ? <div className="ops-detail-section-description">{description}</div> : null}
        </div>
        {extra ? <div className="ops-detail-section-extra">{extra}</div> : null}
      </div>
      <div className="ops-detail-section-body">{children}</div>
    </section>
  );
}

export type OperationsTimelineItem = {
  color?: string;
  children: ReactNode;
  dot?: ReactNode;
};

export function OperationsTimeline({ items }: { items: OperationsTimelineItem[] }) {
  return <Timeline className="ops-detail-timeline" items={items} />;
}

type OperationsActionFooterProps = {
  children: ReactNode;
  note?: ReactNode;
  ariaLabel?: string;
};

export function OperationsActionFooter({
  children,
  note,
  ariaLabel = '详情操作',
}: OperationsActionFooterProps) {
  return (
    <div className="ops-detail-footer" role="group" aria-label={ariaLabel}>
      {note ? <div className="ops-detail-footer-note">{note}</div> : <span />}
      <div className="ops-detail-footer-actions">{children}</div>
    </div>
  );
}

export type OperationsInsightTone = 'info' | 'positive' | 'warning' | 'critical';

export type OperationsInsight = {
  key?: string;
  text: ReactNode;
  tone?: OperationsInsightTone;
};

type OperationsInsightBandProps = {
  title: ReactNode;
  icon?: ReactNode;
  items: OperationsInsight[];
  ariaLabel?: string;
};

export function OperationsInsightBand({ title, icon, items, ariaLabel }: OperationsInsightBandProps) {
  return (
    <section className="ops-insight-band" aria-label={ariaLabel ?? String(title)}>
      <div className="ops-insight-heading">
        {icon ? <span aria-hidden="true">{icon}</span> : null}
        <strong>{title}</strong>
      </div>
      <div className="ops-insight-list">
        {items.map((item, index) => (
          <div className={`ops-insight-item is-${item.tone ?? 'info'}`} key={item.key ?? index}>
            <span className="ops-insight-dot" aria-hidden="true" />
            <span>{item.text}</span>
          </div>
        ))}
      </div>
    </section>
  );
}

type OperationsChartCardProps = {
  title: ReactNode;
  description?: ReactNode;
  meta?: ReactNode;
  extra?: ReactNode;
  children: ReactNode;
  height?: number;
  loading?: boolean;
  empty?: boolean;
  emptyDescription?: ReactNode;
  footer?: ReactNode;
  ariaLabel?: string;
  className?: string;
};

export function OperationsChartCard({
  title,
  description,
  meta,
  extra,
  children,
  height = 300,
  loading = false,
  empty = false,
  emptyDescription = '暂无可展示数据',
  footer,
  ariaLabel,
  className,
}: OperationsChartCardProps) {
  return (
    <Card
      variant="borderless"
      className={['ops-chart-card', className].filter(Boolean).join(' ')}
      title={
        <div className="ops-chart-title-block">
          <OperationsPanelHeading title={title} meta={meta} />
          {description ? <span className="ops-chart-description">{description}</span> : null}
        </div>
      }
      extra={extra}
    >
      <div
        className="ops-chart-frame"
        style={{ '--ops-chart-height': `${height}px` } as CSSProperties}
        aria-label={ariaLabel}
      >
        {loading ? (
          <div className="ops-chart-state"><Spin /></div>
        ) : empty ? (
          <div className="ops-chart-state">
            <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={emptyDescription} />
          </div>
        ) : children}
      </div>
      {footer ? <div className="ops-chart-footer">{footer}</div> : null}
    </Card>
  );
}
