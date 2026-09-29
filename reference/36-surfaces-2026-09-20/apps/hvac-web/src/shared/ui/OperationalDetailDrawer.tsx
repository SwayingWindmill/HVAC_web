import type { ReactNode } from 'react';
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { cn } from '@/lib/utils';

export interface OperationalDetailTab {
  readonly key: string;
  readonly label: ReactNode;
  readonly children: ReactNode;
  readonly disabled?: boolean;
}

export interface OperationalDetailDrawerProps {
  readonly open: boolean;
  readonly onClose: () => void;
  readonly title: ReactNode;
  readonly subtitle?: ReactNode;
  readonly status?: ReactNode;
  readonly headerExtra?: ReactNode;
  readonly tabs?: readonly OperationalDetailTab[];
  readonly activeTabKey?: string;
  readonly onTabChange?: (key: string) => void;
  readonly children?: ReactNode;
  readonly footer?: ReactNode;
  readonly size?: number | string;
  readonly rootClassName?: string;
}

export function OperationalDetailDrawer({
  open,
  onClose,
  title,
  subtitle,
  status,
  headerExtra,
  tabs,
  activeTabKey,
  onTabChange,
  children,
  footer,
  size = 560,
  rootClassName,
}: OperationalDetailDrawerProps) {
  const width = typeof size === 'number' ? `${size}px` : size;

  return (
    <Sheet open={open} onOpenChange={(nextOpen) => { if (!nextOpen) onClose(); }}>
      <SheetContent
        side="right"
        className={cn('sm:max-w-none', rootClassName)}
        style={{ width, maxWidth: '92vw' }}
      >
        <SheetHeader className="gap-3">
          <div className="flex min-w-0 items-start justify-between gap-4 pr-8">
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <SheetTitle>{title}</SheetTitle>
                {status}
              </div>
              {subtitle != null ? <SheetDescription className="mt-1">{subtitle}</SheetDescription> : null}
            </div>
            {headerExtra != null ? <div className="shrink-0">{headerExtra}</div> : null}
          </div>
        </SheetHeader>
        <SheetBody className="p-0">
          {tabs?.length ? (
            <Tabs value={activeTabKey ?? tabs[0]?.key} onValueChange={onTabChange} className="h-full gap-0">
              <div className="overflow-x-auto border-b px-5">
                <TabsList className="w-max border-b-0">
                  {tabs.map((tab) => (
                    <TabsTrigger key={tab.key} value={tab.key} disabled={tab.disabled}>{tab.label}</TabsTrigger>
                  ))}
                </TabsList>
              </div>
              {tabs.map((tab) => (
                <TabsContent key={tab.key} value={tab.key} className="p-5">
                  {tab.children}
                </TabsContent>
              ))}
            </Tabs>
          ) : (
            <div className="p-5">{children}</div>
          )}
        </SheetBody>
        {footer != null ? <SheetFooter className="justify-end">{footer}</SheetFooter> : null}
      </SheetContent>
    </Sheet>
  );
}
