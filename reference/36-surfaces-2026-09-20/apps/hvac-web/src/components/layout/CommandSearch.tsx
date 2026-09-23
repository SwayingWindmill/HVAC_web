import { ArrowRight } from 'lucide-react';
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command';
import type { AppNavigationEntry } from './app-navigation';

interface CommandSearchProps {
  readonly open: boolean;
  readonly entries: readonly AppNavigationEntry[];
  readonly onOpenChange: (open: boolean) => void;
  readonly onNavigate: (target: string) => void;
}

export function CommandSearch({ open, entries, onOpenChange, onNavigate }: CommandSearchProps) {
  return (
    <CommandDialog
      open={open}
      onOpenChange={onOpenChange}
      title="搜索页面和功能"
      description="搜索当前账号可以访问的智慧能源工作区。"
    >
      <CommandInput autoFocus aria-label="搜索页面和功能" placeholder="搜索页面和功能…" />
      <CommandList>
        <CommandEmpty>没有匹配的页面。</CommandEmpty>
        <CommandGroup heading="页面与功能">
          {entries.map((entry) => {
            const Icon = entry.icon;
            return (
              <CommandItem
                key={entry.id}
                value={`${entry.label} ${entry.group}`}
                onSelect={() => {
                  onNavigate(entry.path);
                  onOpenChange(false);
                }}
              >
                <Icon className="text-muted-foreground" aria-hidden="true" />
                <span className="min-w-0 flex-1 truncate">{entry.label}</span>
                <ArrowRight className="ml-auto text-muted-foreground" aria-hidden="true" />
              </CommandItem>
            );
          })}
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  );
}
