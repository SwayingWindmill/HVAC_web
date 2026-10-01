import { useState } from 'react';
import {
  Building2,
  Check,
  ChevronsUpDown,
  Compass,
  Globe2,
  MapPin,
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from '@/components/ui/command';
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover';
import { useScope, type ScopeItem, type ScopeType } from '@/hooks/use-scope';
import { cn } from '@/lib/utils';

function getScopeIcon(type: ScopeType) {
  switch (type) {
    case 'portfolio':
      return Globe2;
    case 'group':
      return Building2;
    case 'site':
      return MapPin;
    default:
      return Compass;
  }
}

export function ScopeSwitcher({ className }: { readonly className?: string }) {
  const [open, setOpen] = useState(false);
  const { currentScope, availableScopes, setScope } = useScope();

  const Icon = getScopeIcon(currentScope.type);

  const portfolioScopes = availableScopes.filter((s) => s.type === 'portfolio');
  const groupScopes = availableScopes.filter((s) => s.type === 'group');
  const siteScopes = availableScopes.filter((s) => s.type === 'site');

  const handleSelect = (item: ScopeItem) => {
    setScope(item.id);
    setOpen(false);
  };

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="outline"
          role="combobox"
          aria-expanded={open}
          aria-label="切换管理范围"
          className={cn(
            'flex h-9 w-60 items-center justify-between gap-2 rounded-md border-border/80 bg-background/80 px-2.5 text-xs font-normal shadow-xs transition-colors hover:bg-accent/60',
            className
          )}
        >
          <div className="flex min-w-0 items-center gap-2">
            <div className="flex size-5 shrink-0 items-center justify-center rounded-sm bg-primary/10 text-primary">
              <Icon className="size-3.5" />
            </div>
            <span className="truncate font-medium text-foreground">
              {currentScope.name}
            </span>
          </div>
          <div className="flex shrink-0 items-center gap-1.5">
            {currentScope.badge ? (
              <Badge variant="secondary" className="px-1.5 py-0 text-[10px] font-normal">
                {currentScope.badge}
              </Badge>
            ) : null}
            <ChevronsUpDown className="size-3.5 text-muted-foreground" />
          </div>
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-72 p-0" align="start">
        <Command>
          <CommandInput placeholder="搜索范围或站点..." className="h-9 text-xs" />
          <CommandList className="max-h-72">
            <CommandEmpty>未找到匹配的站点或组织</CommandEmpty>

            <CommandGroup heading="集团总览视角">
              {portfolioScopes.map((scope) => {
                const ScopeIcon = getScopeIcon(scope.type);
                const isSelected = currentScope.id === scope.id;
                return (
                  <CommandItem
                    key={scope.id}
                    value={scope.name}
                    onSelect={() => handleSelect(scope)}
                    className="cursor-pointer gap-2 py-2 text-xs"
                  >
                    <ScopeIcon className="size-4 shrink-0 text-primary" />
                    <div className="flex min-w-0 flex-1 flex-col">
                      <span className="font-medium">{scope.name}</span>
                      {scope.description ? (
                        <span className="truncate text-[11px] text-muted-foreground">
                          {scope.description}
                        </span>
                      ) : null}
                    </div>
                    {isSelected ? <Check className="size-4 shrink-0 text-primary" /> : null}
                  </CommandItem>
                );
              })}
            </CommandGroup>

            <CommandSeparator />

            <CommandGroup heading="区域综合群">
              {groupScopes.map((scope) => {
                const ScopeIcon = getScopeIcon(scope.type);
                const isSelected = currentScope.id === scope.id;
                return (
                  <CommandItem
                    key={scope.id}
                    value={scope.name}
                    onSelect={() => handleSelect(scope)}
                    className="cursor-pointer gap-2 py-2 text-xs"
                  >
                    <ScopeIcon className="size-4 shrink-0 text-muted-foreground" />
                    <div className="flex min-w-0 flex-1 flex-col">
                      <span className="font-medium">{scope.name}</span>
                      {scope.description ? (
                        <span className="truncate text-[11px] text-muted-foreground">
                          {scope.description}
                        </span>
                      ) : null}
                    </div>
                    {isSelected ? <Check className="size-4 shrink-0 text-primary" /> : null}
                  </CommandItem>
                );
              })}
            </CommandGroup>

            <CommandSeparator />

            <CommandGroup heading="具体用能站点">
              {siteScopes.map((scope) => {
                const ScopeIcon = getScopeIcon(scope.type);
                const isSelected = currentScope.id === scope.id;
                return (
                  <CommandItem
                    key={scope.id}
                    value={scope.name}
                    onSelect={() => handleSelect(scope)}
                    className="cursor-pointer gap-2 py-2 text-xs"
                  >
                    <ScopeIcon className="size-4 shrink-0 text-emerald-600 dark:text-emerald-400" />
                    <div className="flex min-w-0 flex-1 flex-col">
                      <span className="font-medium">{scope.name}</span>
                      {scope.description ? (
                        <span className="truncate text-[11px] text-muted-foreground">
                          {scope.description}
                        </span>
                      ) : null}
                    </div>
                    {isSelected ? <Check className="size-4 shrink-0 text-primary" /> : null}
                  </CommandItem>
                );
              })}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}
