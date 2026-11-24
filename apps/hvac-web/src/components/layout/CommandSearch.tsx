import { ArrowRight, Bot, Cpu, MapPin, Monitor, Moon, Sun } from 'lucide-react';
import { useShellSnapshot } from '@/app/ShellRuntimeContext';
import { useAppTheme } from '@/app/ThemeGate';
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from '@/components/ui/command';
import { useSiteEquipment } from '@/features/assets/use-device-names';
import { useScope } from '@/hooks/use-scope';
import { APP_NAVIGATION_CONFIG, filterNavigationGroups, navHref } from './app-navigation';

interface CommandSearchProps {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly onNavigate: (target: string) => void;
  readonly onToggleAssistant?: () => void;
  readonly assistantOpen?: boolean;
}

/** The current Site's equipment, opened in the device inspector. */
function EquipmentGroup({ siteId, onSelect }: { readonly siteId: string; readonly onSelect: (target: string) => void }) {
  const { deviceByAsset, isLoading } = useSiteEquipment(siteId);
  const equipment = [...deviceByAsset.values()].sort((left, right) => left.name.localeCompare(right.name));
  if (isLoading && equipment.length === 0) {
    return (
      <>
        <CommandSeparator />
        <CommandGroup heading="设备">
          <div className="px-2 py-3 text-xs text-muted-foreground">正在加载设备…</div>
        </CommandGroup>
      </>
    );
  }
  if (equipment.length === 0) return null;
  return (
    <>
      <CommandSeparator />
      <CommandGroup heading="设备">
        {equipment.map((item) => (
          <CommandItem
            key={item.deviceId}
            value={`设备 ${item.name}`}
            onSelect={() => onSelect(`/operations/systems-devices?site=${siteId}&inspect=${item.deviceId}`)}
          >
            <Cpu className="text-muted-foreground" />
            <span className="flex-1">{item.name}</span>
            <span className="text-xs text-muted-foreground">查看详情</span>
          </CommandItem>
        ))}
      </CommandGroup>
    </>
  );
}

export function CommandSearch({ open, onOpenChange, onNavigate, onToggleAssistant, assistantOpen }: CommandSearchProps) {
  const { currentScope, availableScopes, setScope } = useScope();
  const { mode, setMode } = useAppTheme();
  const snapshot = useShellSnapshot();
  const capabilities = snapshot.principal?.authorization.capabilities;
  const visibleGroups = filterNavigationGroups(APP_NAVIGATION_CONFIG, capabilities);
  const equipmentSiteId = currentScope?.siteId ?? snapshot.lastSiteId ?? snapshot.sites?.items?.[0]?.id;

  const go = (target: string) => {
    onNavigate(target);
    onOpenChange(false);
  };

  return (
    <CommandDialog open={open} onOpenChange={onOpenChange} title="搜索" description="搜索页面、设备或快捷操作">
      <CommandInput autoFocus placeholder="输入页面、设备或操作名称，如 能耗、冷机…" />
      <CommandList className="max-h-[420px]">
        <CommandEmpty>没有匹配的页面、设备或操作。</CommandEmpty>
        {visibleGroups.map((group) => (
          <CommandGroup key={group.id} heading={group.label}>
            {group.items.map((item) => {
              const Icon = item.icon;
              return (
                <CommandItem key={item.id} value={`${group.label} ${item.title}`} onSelect={() => go(navHref(item, equipmentSiteId))}>
                  <Icon className="text-muted-foreground" />
                  <span className="flex-1">{item.title}</span>
                  <ArrowRight className="text-muted-foreground/60" />
                </CommandItem>
              );
            })}
          </CommandGroup>
        ))}
        {equipmentSiteId && capabilities?.includes('device.list') ? <EquipmentGroup siteId={equipmentSiteId} onSelect={go} /> : null}
        <CommandSeparator />
        <CommandGroup heading="快捷操作">
          {onToggleAssistant ? (
            <CommandItem
              value="AI 助手 打开 关闭 对话"
              onSelect={() => {
                onToggleAssistant();
                onOpenChange(false);
              }}
            >
              <Bot className="text-muted-foreground" />
              <span className="flex-1">{assistantOpen ? '关闭 AI 助手' : '打开 AI 助手'}</span>
            </CommandItem>
          ) : null}
          {availableScopes.length > 1 ? availableScopes.map((scope) => (
            <CommandItem
              key={scope.id}
              value={`切换站点 ${scope.name}`}
              onSelect={() => {
                setScope(scope.siteId);
                onOpenChange(false);
              }}
            >
              <MapPin className="text-muted-foreground" />
              <span className="flex-1">切换到 {scope.name}</span>
              {currentScope?.siteId === scope.siteId ? <span className="text-xs text-primary">当前</span> : null}
            </CommandItem>
          )) : null}
          {mode !== 'light' ? (
            <CommandItem value="外观 切换浅色模式" onSelect={() => { setMode('light'); onOpenChange(false); }}>
              <Sun className="text-muted-foreground" />
              <span className="flex-1">切换到浅色外观</span>
            </CommandItem>
          ) : null}
          {mode !== 'dark' ? (
            <CommandItem value="外观 切换深色模式" onSelect={() => { setMode('dark'); onOpenChange(false); }}>
              <Moon className="text-muted-foreground" />
              <span className="flex-1">切换到深色外观</span>
            </CommandItem>
          ) : null}
          {mode !== 'system' ? (
            <CommandItem value="外观 跟随系统" onSelect={() => { setMode('system'); onOpenChange(false); }}>
              <Monitor className="text-muted-foreground" />
              <span className="flex-1">外观跟随系统</span>
            </CommandItem>
          ) : null}
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  );
}
