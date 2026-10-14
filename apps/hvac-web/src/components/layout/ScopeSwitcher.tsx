import { useState } from 'react';
import { Check, ChevronsUpDown, MapPin } from 'lucide-react';
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { useScope } from '@/hooks/use-scope';

/** The Site the operator works in, in the top bar: every Site-scoped page reads this Site. */
export function ScopeSwitcher() {
  const [open, setOpen] = useState(false);
  const { currentScope, availableScopes, setScope } = useScope();
  const label = currentScope?.name ?? (availableScopes.length === 0 ? '暂无可访问站点' : '平台范围');

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          role="combobox"
          aria-expanded={open}
          aria-label={`当前站点：${label}，切换站点`}
          disabled={availableScopes.length === 0}
          className="flex h-8 min-w-0 items-center gap-2 rounded-lg px-2 text-sm font-semibold text-foreground transition-colors duration-100 outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring-soft disabled:opacity-50 data-[state=open]:bg-accent"
        >
          <span className={currentScope ? 'truncate' : 'truncate font-medium text-muted-foreground'}>{label}</span>
          <span className="rounded-full bg-secondary px-[7px] text-[11px] leading-[18px] font-medium text-secondary-foreground">
            {currentScope ? `${availableScopes.length} 个站点` : '不限站点'}
          </span>
          <ChevronsUpDown className="size-3.5 text-subtle-foreground" aria-hidden="true" />
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-64 p-0" align="start" sideOffset={6}>
        <Command>
          <CommandInput placeholder="搜索站点…" />
          <CommandList className="max-h-72">
            <CommandEmpty>未找到匹配的站点</CommandEmpty>
            <CommandGroup heading="站点">
              {availableScopes.map((scope) => (
                <CommandItem
                  key={scope.id}
                  value={scope.name}
                  onSelect={() => {
                    setScope(scope.siteId);
                    setOpen(false);
                  }}
                >
                  <span className="flex size-6 items-center justify-center rounded-md border bg-card">
                    <MapPin className="size-3.5 text-muted-foreground" />
                  </span>
                  <span className="min-w-0 flex-1 truncate">{scope.name}</span>
                  {currentScope?.id === scope.id ? <Check className="text-primary" /> : null}
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}
