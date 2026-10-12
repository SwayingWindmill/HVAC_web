import { ArrowRight, Cpu } from 'lucide-react';
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
import { APP_NAVIGATION_CONFIG } from './app-navigation';

interface CommandSearchProps {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly onNavigate: (target: string) => void;
}

/** The current Site's equipment, opened in the device inspector. */
function EquipmentGroup({ siteId, onSelect }: { readonly siteId: string; readonly onSelect: (target: string) => void }) {
  const { deviceByAsset } = useSiteEquipment(siteId);
  const equipment = [...deviceByAsset.values()].sort((left, right) => left.name.localeCompare(right.name));
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

export function CommandSearch({ open, onOpenChange, onNavigate }: CommandSearchProps) {
  const { currentScope } = useScope();
  const siteQuery = currentScope ? `?site=${currentScope.siteId}` : '';

  const go = (target: string) => {
    onNavigate(target);
    onOpenChange(false);
  };

  return (
    <CommandDialog open={open} onOpenChange={onOpenChange} title="搜索" description="搜索页面与设备">
      <CommandInput autoFocus placeholder="输入页面或设备名称，如 能耗、CHILLER-01…" />
      <CommandList className="max-h-[420px]">
        <CommandEmpty>没有匹配的页面或设备。</CommandEmpty>
        {APP_NAVIGATION_CONFIG.map((group) => (
          <CommandGroup key={group.id} heading={group.label}>
            {group.items.map((item) => {
              const Icon = item.icon;
              return (
                <CommandItem key={item.id} value={`${group.label} ${item.title}`} onSelect={() => go(`${item.path}${siteQuery}`)}>
                  <Icon className="text-muted-foreground" />
                  <span className="flex-1">{item.title}</span>
                  <ArrowRight className="text-muted-foreground/60" />
                </CommandItem>
              );
            })}
          </CommandGroup>
        ))}
        {currentScope ? <EquipmentGroup siteId={currentScope.siteId} onSelect={go} /> : null}
      </CommandList>
    </CommandDialog>
  );
}
