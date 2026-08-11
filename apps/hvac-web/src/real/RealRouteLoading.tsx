interface RealRouteLoadingProps {
  label: string;
  testId?: string;
  routeState?: string;
  siteId?: string;
  variant?: 'route' | 'shell';
}

export function RealRouteLoading({
  label,
  testId,
  routeState,
  siteId,
  variant = 'route',
}: RealRouteLoadingProps) {
  return (
    <section
      className={`real-route-loading real-route-loading--${variant}`}
      data-testid={testId}
      data-route-state={routeState}
      data-site-id={siteId}
    >
      <span className="real-shell-sr-only" role="status" aria-live="polite">{label}</span>
      <div className="real-route-loading__heading" aria-hidden="true">
        <span className="real-route-loading__bar real-route-loading__bar--title" />
        <span className="real-route-loading__bar real-route-loading__bar--subtitle" />
      </div>
      <div className="real-route-loading__metrics" aria-hidden="true">
        {Array.from({ length: 4 }, (_, index) => (
          <span className="real-route-loading__metric" key={index} />
        ))}
      </div>
      <div className="real-route-loading__workspace" aria-hidden="true">
        <div className="real-route-loading__rail">
          {Array.from({ length: 6 }, (_, index) => (
            <span className="real-route-loading__bar" key={index} />
          ))}
        </div>
        <div className="real-route-loading__content">
          <span className="real-route-loading__bar real-route-loading__bar--toolbar" />
          {Array.from({ length: 7 }, (_, index) => (
            <span className="real-route-loading__row" key={index} />
          ))}
        </div>
      </div>
    </section>
  );
}
