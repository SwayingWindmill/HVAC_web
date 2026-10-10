import { useState } from 'react';
import { Check, ChevronsUpDown, MapPin } from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command';
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover';
import { useScope } from '@/hooks/use-scope';
import { cn } from '@/lib/utils';

export function ScopeSwitcher({ className }: { readonly className?: string }) {
  const [open, setOpen] = useState(false);
  const { currentScope, availableScopes, setScope } = useScope();

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="outline"
          role="combobox"
          aria-expanded={open}
          aria-label="切换站点"
          disabled={availableScopes.length === 0}
          className={cn(
            'h-8 w-56 justify-between gap-2 px-2.5 text-sm font-normal',
            className
          )}
        >
          <div className="flex min-w-0 items-center gap-2">
            <div className="flex size-5 shrink-0 items-center justify-center rounded-sm bg-primary/10 text-primary">
              <MapPin className="size-3.5" />
            </div>
            <span className="truncate font-medium text-foreground">
              {currentScope?.name ?? (availableScopes.length === 0 ? '暂无可访问站点' : '选择站点')}
            </span>
          </div>
          <ChevronsUpDown className="size-3.5 shrink-0 text-muted-foreground" />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-72 p-0" align="start">
        <Command>
          <CommandInput placeholder="搜索站点..." className="h-9 text-xs" />
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
                  className="cursor-pointer gap-2 py-2 text-xs"
                >
                  <MapPin className="size-4 shrink-0 text-muted-foreground" />
                  <span className="min-w-0 flex-1 truncate font-medium">{scope.name}</span>
                  {currentScope?.id === scope.id ? <Check className="size-4 shrink-0 text-primary" /> : null}
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}
