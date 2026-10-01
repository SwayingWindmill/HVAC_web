import { ArrowRight } from 'lucide-react';
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command';
import { useScope } from '@/hooks/use-scope';
import { APP_NAVIGATION_CONFIG } from './app-navigation';

interface CommandSearchProps {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly onNavigate: (target: string) => void;
}

export function CommandSearch({ open, onOpenChange, onNavigate }: CommandSearchProps) {
  const { currentScope } = useScope();
  const siteQuery = currentScope ? `?site=${currentScope.siteId}` : '';

  const handleSelect = (path: string) => {
    onNavigate(`${path}${siteQuery}`);
    onOpenChange(false);
  };

  return (
    <CommandDialog
      open={open}
      onOpenChange={onOpenChange}
      title="跳转"
      description="搜索功能页面"
    >
      <CommandInput autoFocus placeholder="输入页面名称，如 实时运行、能耗..." />
      <CommandList className="max-h-96">
        <CommandEmpty>未找到匹配的页面。</CommandEmpty>
        {APP_NAVIGATION_CONFIG.map((group) => (
          <CommandGroup key={group.id} heading={group.label}>
            {group.items.map((item) => {
              const Icon = item.icon;
              return (
                <CommandItem
                  key={item.id}
                  value={`${group.label} ${item.title}`}
                  onSelect={() => handleSelect(item.path)}
                  className="gap-2 text-xs"
                >
                  <Icon className="size-4 shrink-0 text-muted-foreground" />
                  <span className="flex-1 font-medium">{item.title}</span>
                  <ArrowRight className="size-3.5 text-muted-foreground" />
                </CommandItem>
              );
            })}
          </CommandGroup>
        ))}
      </CommandList>
    </CommandDialog>
  );
}
