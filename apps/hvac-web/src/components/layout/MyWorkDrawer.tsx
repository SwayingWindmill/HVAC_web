import { useState } from 'react';
import {
  ChevronRight,
  Clock,
  ListTodo,
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover';
import { Separator } from '@/components/ui/separator';

export interface MyWorkItem {
  readonly id: string;
  readonly title: string;
  readonly type: 'inspection' | 'alarm' | 'opportunity' | 'maintenance';
  readonly priority: 'critical' | 'high' | 'medium';
  readonly target: string;
  readonly dueTime: string;
  readonly status: 'pending' | 'in_progress';
}

const MOCK_MY_WORK_ITEMS: readonly MyWorkItem[] = [
  {
    id: 'WO-202609-001',
    title: '冷水机组 CH-01 润滑油压差偏低复核',
    type: 'alarm',
    priority: 'critical',
    target: '地下二层 制冷机房 CH-01',
    dueTime: '今日 14:00 截止',
    status: 'in_progress',
  },
  {
    id: 'OPP-TASK-104',
    title: '调整西塔 AHU-04 晚间排程与新风比设定',
    type: 'opportunity',
    priority: 'high',
    target: '西塔 12F 空调机房',
    dueTime: '今日 18:00 前',
    status: 'pending',
  },
  {
    id: 'PM-2026-302',
    title: '冷却塔 CT-03 填料与布水管季度巡检保养',
    type: 'maintenance',
    priority: 'medium',
    target: '楼顶冷却塔区 CT-03',
    dueTime: '明天 12:00 前',
    status: 'pending',
  },
  {
    id: 'WO-202609-088',
    title: '冷冻水泵 P-02 变频器通讯信号时断时续排查',
    type: 'inspection',
    priority: 'medium',
    target: '冷站动力配电间',
    dueTime: '后天 17:00 前',
    status: 'pending',
  },
];

export function MyWorkDrawer({
  onNavigate,
}: {
  readonly onNavigate: (path: string) => void;
}) {
  const [open, setOpen] = useState(false);

  const pendingCount = MOCK_MY_WORK_ITEMS.length;

  const handleOpenAll = () => {
    setOpen(false);
    onNavigate('/operations/work-center');
  };

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          className="relative h-8 gap-1.5 border-border/80 px-2 text-xs font-normal"
          aria-label="查看我的待办工作"
        >
          <ListTodo className="size-3.5 text-muted-foreground" />
          <span className="hidden sm:inline">我的待办</span>
          <Badge
            variant="default"
            className="flex h-4 min-w-4 items-center justify-center rounded-full bg-primary px-1 text-[10px] text-primary-foreground"
          >
            {pendingCount}
          </Badge>
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-80 p-0" align="end">
        <div className="flex items-center justify-between border-b border-border/60 px-3 py-2.5">
          <div className="flex items-center gap-1.5">
            <ListTodo className="size-4 text-primary" />
            <span className="text-xs font-semibold">我的待办工作 ({pendingCount})</span>
          </div>
          <Button
            variant="ghost"
            size="sm"
            className="h-6 text-[11px] text-muted-foreground hover:text-foreground"
            onClick={handleOpenAll}
          >
            查看全部 <ChevronRight className="ml-0.5 size-3" />
          </Button>
        </div>

        <div className="max-h-80 divide-y divide-border/40 overflow-y-auto px-1 py-1">
          {MOCK_MY_WORK_ITEMS.map((item) => (
            <div
              key={item.id}
              className="flex cursor-pointer flex-col gap-1 rounded-md p-2 transition-colors hover:bg-muted/60"
              onClick={handleOpenAll}
            >
              <div className="flex items-start justify-between gap-1.5">
                <span className="line-clamp-1 text-xs font-medium text-foreground">
                  {item.title}
                </span>
                <Badge
                  variant={item.priority === 'critical' ? 'destructive' : 'outline'}
                  className="shrink-0 px-1 py-0 text-[10px] font-normal"
                >
                  {item.priority === 'critical' ? '紧急' : item.priority === 'high' ? '高优' : '普通'}
                </Badge>
              </div>

              <div className="flex items-center justify-between text-[11px] text-muted-foreground">
                <span className="truncate">{item.target}</span>
                <span className="flex items-center gap-1 text-amber-600 dark:text-amber-400">
                  <Clock className="size-3" />
                  {item.dueTime}
                </span>
              </div>
            </div>
          ))}
        </div>

        <Separator />
        <div className="p-2">
          <Button
            variant="secondary"
            size="sm"
            className="w-full text-xs font-normal"
            onClick={handleOpenAll}
          >
            前往工作中心处理
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}
