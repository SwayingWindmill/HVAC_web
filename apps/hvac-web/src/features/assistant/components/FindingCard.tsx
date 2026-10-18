import { Link } from '@tanstack/react-router';
import { ArrowUpRight, Info } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import type { AssistantPart } from '../thread-model';
import { toolPresentation } from '../tool-labels';

type FindingData = Extract<AssistantPart, { type: 'data-finding' }>['data'];

/** Under the answer: whether the data supports it, the numbers it rests on, what the data could not tell, and where to look. */
export function FindingCard({ finding, evidenceTools, siteId }: FindingData & { readonly siteId: string }) {
  const supported = finding.outcome === 'SUPPORTED_FINDING';
  const pages = [...new Map(evidenceTools.flatMap((tool) => {
    const page = toolPresentation(tool).page;
    return page === null ? [] : [[page.to, page] as const];
  })).values()];

  return (
    <section aria-label="回答依据" className="flex flex-col gap-3 rounded-lg border bg-card p-3.5" data-testid="assistant-finding">
      <div className="flex items-center gap-2">
        <Badge variant={supported ? 'secondary' : 'outline'}>{supported ? '有数据依据' : '数据不足以下结论'}</Badge>
      </div>
      {finding.keyFigures !== undefined && finding.keyFigures.length > 0 ? (
        <dl className="m-0 grid grid-cols-2 gap-2">
          {finding.keyFigures.map((figure) => (
            <div key={figure.label} className="flex flex-col gap-0.5 rounded-md bg-muted px-2.5 py-2">
              <dt className="text-xs text-muted-foreground">{figure.label}</dt>
              <dd className="m-0 text-base font-semibold tabular-nums">
                {figure.value}
                {figure.unit === undefined ? null : <span className="ml-1 text-xs font-normal text-muted-foreground">{figure.unit}</span>}
              </dd>
            </div>
          ))}
        </dl>
      ) : null}
      {finding.limitations.map((limitation) => (
        <p key={limitation} className="m-0 flex gap-2 text-xs leading-relaxed text-muted-foreground">
          <Info className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
          <span>{limitation}</span>
        </p>
      ))}
      {pages.length > 0 ? (
        <div className="flex flex-wrap items-center gap-1.5 text-xs">
          <span className="text-muted-foreground">依据</span>
          {pages.map((page) => (
            <Link
              key={page.to}
              to={page.to}
              search={{ site: siteId }}
              className="inline-flex items-center gap-0.5 rounded-sm px-1 text-primary hover:text-primary-hover focus-visible:ring-3 focus-visible:ring-ring-soft focus-visible:outline-none"
            >
              {page.label}
              <ArrowUpRight className="size-3" aria-hidden="true" />
            </Link>
          ))}
        </div>
      ) : null}
    </section>
  );
}
