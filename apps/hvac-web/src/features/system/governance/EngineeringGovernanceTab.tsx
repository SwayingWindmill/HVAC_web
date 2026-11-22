import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import type { Site } from '@/api/generated/platformGateway.gen';
import { siteAssetModelQuery } from '@/api/site-asset-model';
import { StateCard } from '@/blocks/state/state-card';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { AlarmRoutingPanel } from '@/features/alarms/components/AlarmRoutingPanel';
import { RuleActionDeadLettersPanel } from '@/features/alarms/components/RuleActionDeadLettersPanel';
import { ControlAlarmDeadLettersPanel } from '@/features/control/components/ControlAlarmDeadLettersPanel';
import { OptimizationGovernancePanel } from './OptimizationGovernancePanel';

export type GovernanceSection = 'control' | 'alarms' | 'optimization';

const GOVERNANCE_SECTIONS: readonly { readonly value: GovernanceSection; readonly label: string }[] = [
  { value: 'control', label: '控制告警死信' },
  { value: 'alarms', label: '告警通道与死信' },
  { value: 'optimization', label: '策略灰度与负样本' },
];

export function EngineeringGovernanceTab({
  sites,
  selectedSiteId,
  section,
  active,
  canManage,
  csrfToken,
  onSiteChange,
  onSectionChange,
}: {
  readonly sites: readonly Site[];
  readonly selectedSiteId: string | undefined;
  readonly section: GovernanceSection;
  readonly active: boolean;
  readonly canManage: boolean;
  readonly csrfToken: string;
  readonly onSiteChange: (siteId: string) => void;
  readonly onSectionChange: (section: GovernanceSection) => void;
}) {
  const site = useMemo(
    () => sites.find((candidate) => candidate.id === selectedSiteId) ?? sites[0] ?? null,
    [sites, selectedSiteId],
  );

  const assetModelQuery = useQuery(
    siteAssetModelQuery(site?.id ?? null, {
      enabled: active && section === 'control' && Boolean(site),
    }),
  );

  const deviceNames = useMemo(
    () => new Map((assetModelQuery.data?.devices ?? []).map((device) => [device.id, device.displayName])),
    [assetModelQuery.data?.devices],
  );

  if (!site) {
    return (
      <StateCard
        title={<h2 className="text-base font-semibold">没有可治理的站点</h2>}
        description="当前账号没有授权站点，暂无工程治理对象。"
      />
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border bg-card px-4 py-3 shadow-xs">
        <div className="flex items-center gap-2.5">
          <span className="text-xs font-medium text-muted-foreground">站点</span>
          <Select value={site.id} onValueChange={onSiteChange}>
            <SelectTrigger aria-label="治理站点" className="h-8 w-52">
              <SelectValue placeholder="选择站点" />
            </SelectTrigger>
            <SelectContent>
              {sites.map((candidate) => (
                <SelectItem key={candidate.id} value={candidate.id}>
                  {candidate.displayName}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <ToggleGroup
          type="single"
          value={section}
          onValueChange={(value) => {
            if (value === 'control' || value === 'alarms' || value === 'optimization') {
              onSectionChange(value);
            }
          }}
          aria-label="工程治理分类"
          className="rounded-lg border border-divider bg-muted p-0.5"
        >
          {GOVERNANCE_SECTIONS.map((option) => (
            <ToggleGroupItem
              key={option.value}
              value={option.value}
              className="h-7 rounded-md px-3 text-xs font-medium text-muted-foreground data-[state=on]:bg-card data-[state=on]:text-foreground data-[state=on]:shadow-xs"
            >
              {option.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      </div>

      {section === 'control' ? (
        <ControlAlarmDeadLettersPanel
          siteId={site.id}
          canManage={canManage}
          csrfToken={csrfToken}
          deviceNames={deviceNames}
          timeZone={site.timezone}
        />
      ) : section === 'alarms' ? (
        <div className="flex flex-col gap-4">
          <RuleActionDeadLettersPanel
            siteId={site.id}
            canManage={canManage}
            csrfToken={csrfToken}
            timeZone={site.timezone}
          />
          <AlarmRoutingPanel
            siteId={site.id}
            canManage={canManage}
            csrfToken={csrfToken}
            timeZone={site.timezone}
          />
        </div>
      ) : (
        <OptimizationGovernancePanel
          siteId={site.id}
          timeZone={site.timezone}
          active={active && section === 'optimization'}
          canManage={canManage}
          csrfToken={csrfToken}
        />
      )}
    </div>
  );
}
