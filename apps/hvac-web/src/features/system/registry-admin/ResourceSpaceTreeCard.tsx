import { useMemo, type ReactNode } from 'react';
import type { Space as RegistrySpace } from '@/api/generated/platformGateway.gen';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { cn } from '@/lib/utils';
import { spaceTypeLabel } from './resource-workbench-model';

export interface ResourceSpaceTreeCardProps {
  readonly spaces: readonly RegistrySpace[];
  readonly selectedSpaceId: string | null;
  readonly allSites: boolean;
  readonly spaceNameById: ReadonlyMap<string, string>;
  readonly onSelectSpace: (nextSpaceId: string | null) => void;
}

export function ResourceSpaceTreeCard({
  spaces,
  selectedSpaceId,
  allSites,
  spaceNameById,
  onSelectSpace,
}: ResourceSpaceTreeCardProps) {
  const spaceChildrenByParent = useMemo(() => {
    const map = new Map<string, RegistrySpace[]>();
    for (const space of spaces) {
      const key = space.parentSpaceId ?? '__root__';
      map.set(key, [...(map.get(key) ?? []), space]);
    }
    return map;
  }, [spaces]);

  const renderSpaceBranch = (parentId: string, depth = 0): ReactNode => {
    const children = spaceChildrenByParent.get(parentId) ?? [];
    return children.map((space) => {
      const selected = selectedSpaceId === space.id && !allSites;
      return (
        <div key={space.id} style={{ paddingLeft: depth * 14 }}>
          <button
            type="button"
            disabled={allSites}
            aria-pressed={selected}
            onClick={() => {
              if (allSites) return;
              onSelectSpace(selected ? null : space.id);
            }}
            className={cn(
              'flex w-full items-center justify-between gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors hover:bg-muted/50 disabled:cursor-not-allowed disabled:opacity-50',
              selected ? 'bg-primary-soft font-medium text-primary' : undefined,
            )}
          >
            <span className="min-w-0 truncate">{space.displayName}</span>
            <span className="shrink-0 text-xs text-muted-foreground">{spaceTypeLabel(space.spaceType)}</span>
          </button>
          {renderSpaceBranch(space.id, depth + 1)}
        </div>
      );
    });
  };

  return (
    <Card>
      <CardHeader>
        <div className="flex items-start justify-between gap-2">
          <div>
            <CardTitle>空间层级</CardTitle>
            <CardDescription>点击空间节点按递归层级筛选资产与设备。</CardDescription>
          </div>
          {selectedSpaceId && !allSites ? (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => onSelectSpace(null)}
            >
              清除空间
            </Button>
          ) : null}
        </div>
        {selectedSpaceId && !allSites ? (
          <p className="text-xs text-primary">
            当前空间范围：{spaceNameById.get(selectedSpaceId) ?? selectedSpaceId}
          </p>
        ) : null}
      </CardHeader>
      <CardContent>
        {spaces.length > 0 ? (
          <div className="space-y-1">{renderSpaceBranch('__root__')}</div>
        ) : (
          <div className="rounded-md border border-dashed p-4 text-sm text-muted-foreground">
            当前站点还没有空间登记
          </div>
        )}
      </CardContent>
    </Card>
  );
}
