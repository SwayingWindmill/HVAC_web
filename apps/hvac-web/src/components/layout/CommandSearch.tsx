import {
  ArrowRight,
  CalendarCheck2,
  Lightbulb,
  ServerCog,
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from '@/components/ui/command';
import { APP_NAVIGATION_CONFIG } from './app-navigation';

interface CommandSearchProps {
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
  readonly onNavigate: (target: string) => void;
}

interface SmartSearchItem {
  readonly id: string;
  readonly title: string;
  readonly subtitle?: string;
  readonly category: 'equipment' | 'opportunity' | 'work' | 'alarm';
  readonly path: string;
  readonly badge?: string;
}

const SMART_ENERGY_ENTITIES: readonly SmartSearchItem[] = [
  {
    id: 'ent-ch01',
    title: '1# 离心式冷水机组 (CH-01)',
    subtitle: '地下二层 制冷机房 · 运行中 · COP 5.42',
    category: 'equipment',
    path: '/operations/systems-devices?id=CH-01',
    badge: '冷水机组',
  },
  {
    id: 'ent-ch02',
    title: '2# 离心式冷水机组 (CH-02)',
    subtitle: '地下二层 制冷机房 · 备用',
    category: 'equipment',
    path: '/operations/systems-devices?id=CH-02',
    badge: '冷水机组',
  },
  {
    id: 'ent-ahu03',
    title: '东塔 8F 空调箱 (AHU-03)',
    subtitle: '东塔 8F 机房 · 运行中 · 送风 14.2°C',
    category: 'equipment',
    path: '/operations/systems-devices?id=AHU-03',
    badge: '空气处理机',
  },
  {
    id: 'opp-ahu-night',
    title: 'AHU-04 西塔非运营时间过度运行诊断',
    subtitle: '预估年省电量 42,000 kWh · 年节省 ¥33,600',
    category: 'opportunity',
    path: '/optimization/opportunities?id=OPP-001',
    badge: '节能机会',
  },
  {
    id: 'opp-chiller-cop',
    title: 'CH-01 冷水机组部分负荷 COP 偏离基准',
    subtitle: '预估年节省 ¥18,200 · 高置信度 94%',
    category: 'opportunity',
    path: '/optimization/opportunities?id=OPP-002',
    badge: '节能机会',
  },
  {
    id: 'wo-1028',
    title: 'WO-202609-001 冷机润滑油压差偏低复核工单',
    subtitle: '指派给：王工 · 截止：今日 14:00',
    category: 'work',
    path: '/operations/work-center?id=WO-202609-001',
    badge: '工单',
  },
  {
    id: 'al-temp',
    title: '冷水机组 CH-01 出水温度偏离设定值 > 1.5°C',
    subtitle: '发生时间：今天 09:30 · 严重级别：警告',
    category: 'alarm',
    path: '/operations/alarms?id=AL-902',
    badge: '告警',
  },
];

export function CommandSearch({ open, onOpenChange, onNavigate }: CommandSearchProps) {
  const handleSelect = (target: string) => {
    onNavigate(target);
    onOpenChange(false);
  };

  return (
    <CommandDialog
      open={open}
      onOpenChange={onOpenChange}
      title="全局搜索"
      description="搜索功能页面、设备资产、节能机会、告警事件或待办工单..."
    >
      <CommandInput autoFocus placeholder="输入关键字搜索 (如 CH-01, 能耗, 节能, 工单)..." />
      <CommandList className="max-h-96">
        <CommandEmpty>未找到匹配的页面或对象。</CommandEmpty>

        <CommandGroup heading="系统功能与页面">
          {APP_NAVIGATION_CONFIG.flatMap((g) => g.items).map((item) => {
            const Icon = item.icon;
            return (
              <CommandItem
                key={item.id}
                value={`${item.title} 页面 功能`}
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

        <CommandSeparator />

        <CommandGroup heading="设备与物理资产">
          {SMART_ENERGY_ENTITIES.filter((e) => e.category === 'equipment').map((e) => (
            <CommandItem
              key={e.id}
              value={`${e.title} ${e.subtitle} 设备`}
              onSelect={() => handleSelect(e.path)}
              className="gap-2 text-xs"
            >
              <ServerCog className="size-4 shrink-0 text-blue-500" />
              <div className="flex flex-1 flex-col">
                <span className="font-medium">{e.title}</span>
                <span className="text-[11px] text-muted-foreground">{e.subtitle}</span>
              </div>
              <Badge variant="outline" className="text-[10px] font-normal">{e.badge}</Badge>
            </CommandItem>
          ))}
        </CommandGroup>

        <CommandSeparator />

        <CommandGroup heading="节能机会与项目">
          {SMART_ENERGY_ENTITIES.filter((e) => e.category === 'opportunity').map((e) => (
            <CommandItem
              key={e.id}
              value={`${e.title} ${e.subtitle} 机会 节能`}
              onSelect={() => handleSelect(e.path)}
              className="gap-2 text-xs"
            >
              <Lightbulb className="size-4 shrink-0 text-amber-500" />
              <div className="flex flex-1 flex-col">
                <span className="font-medium">{e.title}</span>
                <span className="text-[11px] text-muted-foreground">{e.subtitle}</span>
              </div>
              <Badge variant="secondary" className="text-[10px] font-normal">{e.badge}</Badge>
            </CommandItem>
          ))}
        </CommandGroup>

        <CommandSeparator />

        <CommandGroup heading="现场工单与待办">
          {SMART_ENERGY_ENTITIES.filter((e) => e.category === 'work').map((e) => (
            <CommandItem
              key={e.id}
              value={`${e.title} ${e.subtitle} 工单`}
              onSelect={() => handleSelect(e.path)}
              className="gap-2 text-xs"
            >
              <CalendarCheck2 className="size-4 shrink-0 text-emerald-500" />
              <div className="flex flex-1 flex-col">
                <span className="font-medium">{e.title}</span>
                <span className="text-[11px] text-muted-foreground">{e.subtitle}</span>
              </div>
              <Badge variant="outline" className="text-[10px] font-normal">{e.badge}</Badge>
            </CommandItem>
          ))}
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  );
}
