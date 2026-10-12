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
import { SidebarMenu, SidebarMenuButton, SidebarMenuItem, useSidebar } from '@/components/ui/sidebar';
import { useScope } from '@/hooks/use-scope';

/**
 * The product mark and the Site the operator works in, at the top of the sidebar: every
 * Site-scoped page reads this Site.
 */
export function ScopeSwitcher() {
  const [open, setOpen] = useState(false);
  const { isMobile } = useSidebar();
  const { currentScope, availableScopes, setScope } = useScope();
  const label = currentScope?.name ?? (availableScopes.length === 0 ? '暂无可访问站点' : '选择站点');

  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <Popover open={open} onOpenChange={setOpen}>
          <PopoverTrigger asChild>
            <SidebarMenuButton
              size="lg"
              role="combobox"
              aria-expanded={open}
              aria-label="切换站点"
              disabled={availableScopes.length === 0}
              tooltip={label}
              className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground"
            >
              <img src="/quanlaihe-mark.svg" alt="" className="size-8 shrink-0" />
              <div className="grid flex-1 text-left leading-tight">
                <span className="truncate text-sm font-semibold">{label}</span>
                <span className="truncate text-xs text-muted-foreground">泉来禾智慧能源</span>
              </div>
              <ChevronsUpDown className="ml-auto size-4 text-muted-foreground" />
            </SidebarMenuButton>
          </PopoverTrigger>
          <PopoverContent className="w-(--radix-popover-trigger-width) min-w-60 p-0" side={isMobile ? 'bottom' : 'right'} align="start" sideOffset={4}>
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
                      <span className="flex size-6 items-center justify-center rounded-md border bg-background">
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
      </SidebarMenuItem>
    </SidebarMenu>
  );
}
