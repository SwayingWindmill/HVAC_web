import { Link2, Plus, Search } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group';
import { isOneOf } from '@/lib/typed';
import type { RegistryAdminResourceType } from './model';
import {
  RESOURCE_COPY,
  RESOURCE_STATUS_FILTERS,
  RESOURCE_TYPES,
  SCOPE_OPTIONS,
  type ResourceStatusFilter,
} from './resource-workbench-model';

export interface ResourceTypeOption {
  readonly key: RegistryAdminResourceType;
  readonly label: string;
  readonly count: number;
}

export interface ResourceFilterBarProps {
  readonly resourceType: RegistryAdminResourceType;
  readonly resourceTypeOptions: readonly ResourceTypeOption[];
  readonly writeAllowed: boolean;
  readonly bindingAllowed: boolean;
  readonly filterableType: boolean;
  readonly allSites: boolean;
  readonly statusFilter: ResourceStatusFilter;
  readonly templateKeyFilter: string;
  readonly templateKeys: readonly string[];
  readonly tagInput: string;
  readonly isServerFiltered: boolean;
  readonly tagFilterError: string | null;
  readonly onResourceTypeChange: (next: RegistryAdminResourceType) => void;
  readonly onCreateClick: () => void;
  readonly onRebindClick: () => void;
  readonly onAllSitesChange: (allSites: boolean) => void;
  readonly onStatusFilterChange: (status: ResourceStatusFilter) => void;
  readonly onTemplateKeyFilterChange: (templateKey: string) => void;
  readonly onTagInputChange: (value: string) => void;
  readonly onApplyTagFilter: () => void;
  readonly onResetFilters: () => void;
}

export function ResourceFilterBar({
  resourceType,
  resourceTypeOptions,
  writeAllowed,
  bindingAllowed,
  filterableType,
  allSites,
  statusFilter,
  templateKeyFilter,
  templateKeys,
  tagInput,
  isServerFiltered,
  tagFilterError,
  onResourceTypeChange,
  onCreateClick,
  onRebindClick,
  onAllSitesChange,
  onStatusFilterChange,
  onTemplateKeyFilterChange,
  onTagInputChange,
  onApplyTagFilter,
  onResetFilters,
}: ResourceFilterBarProps) {
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Tabs
          value={resourceType}
          onValueChange={(val) => {
            if (!isOneOf(RESOURCE_TYPES, val)) return;
            onResourceTypeChange(val);
          }}
        >
          <TabsList className="h-9">
            {resourceTypeOptions.map((option) => (
              <TabsTrigger key={option.key} value={option.key}>
                {option.label} {option.count}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
        <div className="flex flex-wrap gap-2">
          <Button size="sm" disabled={!writeAllowed} onClick={onCreateClick}>
            <Plus />
            {RESOURCE_COPY[resourceType].createLabel}
          </Button>
          <Button size="sm" variant="outline" disabled={!bindingAllowed} onClick={onRebindClick}>
            <Link2 />
            调整关系
          </Button>
        </div>
      </div>

      {filterableType ? (
        <div className="flex flex-wrap items-center gap-2 border-t border-divider pt-2.5">
          <ToggleGroup
            type="single"
            variant="outline"
            size="sm"
            value={allSites ? 'ALL_SITES' : 'SITE'}
            onValueChange={(value) => {
              if (!isOneOf(SCOPE_OPTIONS, value)) return;
              onAllSitesChange(value === 'ALL_SITES');
            }}
            aria-label="查询站点范围"
          >
            <ToggleGroupItem value="SITE" className="text-xs">当前站点</ToggleGroupItem>
            <ToggleGroupItem value="ALL_SITES" className="text-xs">全部授权站点</ToggleGroupItem>
          </ToggleGroup>

          <Select
            value={statusFilter}
            onValueChange={(value) => {
              if (!isOneOf(RESOURCE_STATUS_FILTERS, value)) return;
              onStatusFilterChange(value);
            }}
          >
            <SelectTrigger className="h-8 w-28 text-xs" aria-label="状态筛选">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="ALL">全部状态</SelectItem>
              <SelectItem value="ACTIVE">仅启用</SelectItem>
              <SelectItem value="INACTIVE">仅停用</SelectItem>
              <SelectItem value="RETIRED">仅已退役</SelectItem>
            </SelectContent>
          </Select>

          <Select
            value={templateKeyFilter}
            onValueChange={onTemplateKeyFilterChange}
          >
            <SelectTrigger className="h-8 w-44 text-xs" aria-label="模板筛选">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="ALL">全部模板</SelectItem>
              {templateKeys.map((key) => (
                <SelectItem key={key} value={key} className="font-mono text-xs">
                  {key}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>

          <div className="flex items-center gap-1.5">
            <Input
              aria-label="按标签筛选"
              placeholder="标签 key:value 或 key"
              value={tagInput}
              autoCorrect="off"
              autoCapitalize="none"
              spellCheck={false}
              onChange={(event) => onTagInputChange(event.currentTarget.value)}
              onKeyDown={(event) => {
                if (event.nativeEvent.isComposing || event.keyCode === 229) return;
                if (event.key === 'Enter') {
                  event.preventDefault();
                  onApplyTagFilter();
                }
              }}
              className="h-8 w-48 font-mono text-xs"
            />
            <Button type="button" variant="outline" size="sm" onClick={onApplyTagFilter}>
              <Search className="size-3.5" />
              筛选标签
            </Button>
          </div>

          {isServerFiltered || tagInput ? (
            <Button type="button" variant="ghost" size="sm" onClick={onResetFilters}>
              重置筛选
            </Button>
          ) : null}
        </div>
      ) : null}

      {tagFilterError ? (
        <p className="text-xs text-destructive-text">{tagFilterError}</p>
      ) : null}
    </div>
  );
}
