import { Plus, Send } from 'lucide-react';
import { useEffect, useState, type ReactNode } from 'react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Spinner } from '@/components/ui/spinner';
import { Status, StatusIndicator, StatusLabel } from '@/components/ui/status';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { cn } from '@/lib/utils';
import { contrastGrade, contrastRatio, TOKEN_GROUPS, type ContrastGrade } from './design-tokens';

// The design system as the app ships it: colours read from the running theme with their measured
// contrast, the type steps and the component states the pages use. Nothing here is business data.

const TOKEN_NAMES = [...new Set(TOKEN_GROUPS.flatMap((group) => group.tokens.flatMap((token) => [token.name, token.against])))];

function readThemeColours(): ReadonlyMap<string, string> {
  const style = getComputedStyle(document.documentElement);
  return new Map(TOKEN_NAMES.map((name) => [name, style.getPropertyValue(`--${name}`).trim()]));
}

/** The theme's resolved colours, read again whenever the light/dark class changes. */
function useThemeColours(): ReadonlyMap<string, string> {
  const [colours, setColours] = useState(readThemeColours);
  useEffect(() => {
    const observer = new MutationObserver(() => setColours(readThemeColours()));
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class', 'data-theme'] });
    return () => observer.disconnect();
  }, []);
  return colours;
}

const GRADE_TONE: Record<ContrastGrade, string> = {
  AAA: 'bg-success/10 text-success',
  AA: 'bg-success/10 text-success',
  不足: 'bg-destructive/10 text-destructive',
};

function Section({ title, description, children }: { readonly title: string; readonly description: string; readonly children: ReactNode }) {
  return (
    <section aria-label={title} className="flex flex-col gap-4">
      <div>
        <h2 className="text-base font-semibold">{title}</h2>
        <p className="mt-0.5 text-sm text-muted-foreground">{description}</p>
      </div>
      {children}
    </section>
  );
}

function Colours() {
  const colours = useThemeColours();
  return (
    <div className="grid gap-4 @5xl/main:grid-cols-2">
      {TOKEN_GROUPS.map((group) => (
        <Card key={group.title} className="pb-0">
          <CardHeader>
            <CardTitle>{group.title}</CardTitle>
            <CardDescription>{group.description}</CardDescription>
          </CardHeader>
          <CardContent className="border-t px-0">
            <Table aria-label={`${group.title}颜色`}>
              <TableHeader>
                <TableRow>
                  <TableHead className="pl-6">令牌</TableHead>
                  <TableHead>用途</TableHead>
                  <TableHead className="text-right">对比度</TableHead>
                  <TableHead className="pr-6 text-right">等级</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {group.tokens.map((token) => {
                  const value = colours.get(token.name) ?? '';
                  const against = colours.get(token.against) ?? '';
                  const ratio = value && against ? contrastRatio(value, against) : null;
                  const grade = ratio === null || token.decorative ? null : contrastGrade(ratio, token.large);
                  return (
                    <TableRow key={token.name}>
                      <TableCell className="pl-6">
                        <span className="flex items-center gap-3">
                          <span className="size-7 shrink-0 rounded-md border shadow-xs" style={{ background: `var(--${token.name})` }} aria-hidden="true" />
                          <span className="flex flex-col">
                            <code className="font-mono text-xs">--{token.name}</code>
                            <span className="font-mono text-[11px] text-muted-foreground uppercase">{value}</span>
                          </span>
                        </span>
                      </TableCell>
                      <TableCell className="text-muted-foreground">{token.role}</TableCell>
                      <TableCell className="text-right tabular-nums" title={`对 --${token.against}`}>
                        {ratio === null ? '—' : `${ratio.toFixed(2)}:1`}
                      </TableCell>
                      <TableCell className="pr-6 text-right">
                        {grade ? (
                          <Badge variant="outline" className={cn('h-5.5 rounded-md border-transparent px-1.5', GRADE_TONE[grade])}>{grade}</Badge>
                        ) : <span className="text-xs text-muted-foreground">装饰，不要求</span>}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      ))}
    </div>
  );
}

const TYPE_STEPS = [
  { name: '页面标题', classes: 'text-2xl leading-tight font-semibold tracking-tight', sample: '节能项目' },
  { name: '指标数值', classes: 'text-[1.75rem] leading-none font-medium tracking-tight tabular-nums', sample: '12,165.4' },
  { name: '卡片标题', classes: 'text-base leading-snug font-medium', sample: '措施台账' },
  { name: '正文', classes: 'text-sm', sample: '以实施前 14 天的运行数据为基线，逐日核算节电量。' },
  { name: '辅助文字', classes: 'text-xs text-muted-foreground', sample: '数据时间 10/12 17:21' },
] as const;

function Typography() {
  return (
    <Card className="py-0">
      <CardContent className="divide-y px-0">
        {TYPE_STEPS.map((step) => (
          <div key={step.name} className="grid grid-cols-[8rem_minmax(0,1fr)_minmax(0,22rem)] items-baseline gap-6 px-6 py-4">
            <span className="text-sm text-muted-foreground">{step.name}</span>
            <span className={step.classes}>{step.sample}</span>
            <code className="truncate font-mono text-xs text-muted-foreground">{step.classes}</code>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

function Specimen({ label, children }: { readonly label: string; readonly children: ReactNode }) {
  return (
    <div className="grid grid-cols-[8rem_minmax(0,1fr)] items-center gap-6 px-6 py-4">
      <span className="text-sm text-muted-foreground">{label}</span>
      <div className="flex flex-wrap items-center gap-3">{children}</div>
    </div>
  );
}

function Components() {
  return (
    <Card className="py-0">
      <CardContent className="divide-y px-0">
        <Specimen label="按钮">
          <Button size="sm"><Plus data-icon="inline-start" />主要操作</Button>
          <Button size="sm" variant="secondary">次要</Button>
          <Button size="sm" variant="outline">描边</Button>
          <Button size="sm" variant="ghost">幽灵</Button>
          <Button size="sm" variant="destructive">危险操作</Button>
          <Button size="sm" variant="link">文字链接</Button>
        </Specimen>
        <Specimen label="按钮状态">
          <Button size="sm" disabled>不可用</Button>
          <Button size="sm" disabled><Spinner data-icon="inline-start" />处理中</Button>
          <Button size="sm"><Send data-icon="inline-start" />带图标</Button>
          <Button size="xs" variant="outline">小号</Button>
          <Button size="lg" variant="outline">大号</Button>
        </Specimen>
        <Specimen label="状态">
          <Status variant="success"><StatusIndicator /><StatusLabel>正常</StatusLabel></Status>
          <Status variant="warning"><StatusIndicator /><StatusLabel>需关注</StatusLabel></Status>
          <Status variant="error"><StatusIndicator /><StatusLabel>故障</StatusLabel></Status>
          <Status variant="info"><StatusIndicator /><StatusLabel>进行中</StatusLabel></Status>
          <Status><StatusIndicator /><StatusLabel>未知</StatusLabel></Status>
        </Specimen>
        <Specimen label="徽标">
          <Badge>默认</Badge>
          <Badge variant="secondary">次要</Badge>
          <Badge variant="outline">描边</Badge>
          <Badge variant="destructive">危险</Badge>
        </Specimen>
        <Specimen label="输入">
          <div className="grid w-full max-w-2xl grid-cols-3 gap-4">
            <div className="space-y-1.5">
              <Label htmlFor="specimen-input">默认</Label>
              <Input id="specimen-input" placeholder="请输入" />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="specimen-disabled">不可用</Label>
              <Input id="specimen-disabled" disabled defaultValue="已锁定" />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="specimen-invalid">校验失败</Label>
              <Input id="specimen-invalid" aria-invalid defaultValue="-3" />
              <p className="text-xs text-destructive">请填写不小于 0 的数字</p>
            </div>
          </div>
        </Specimen>
      </CardContent>
    </Card>
  );
}

const RADII = [
  { name: 'radius-sm', px: 4, use: '小徽标' },
  { name: 'radius-md', px: 6, use: '按钮、输入框、状态标签' },
  { name: 'radius-lg', px: 8, use: '弹出层' },
  { name: 'radius-xl', px: 10, use: '卡片、指标条' },
] as const;
const SPACES = [4, 8, 12, 16, 24, 32] as const;

function Shape() {
  return (
    <div className="grid gap-4 @5xl/main:grid-cols-2">
      <Card>
        <CardHeader>
          <CardTitle>圆角</CardTitle>
          <CardDescription>越大的容器圆角越大，同层元素保持一致</CardDescription>
        </CardHeader>
        <CardContent className="grid grid-cols-4 gap-4">
          {RADII.map((radius) => (
            <div key={radius.name} className="flex flex-col gap-2">
              <div className="h-14 border bg-muted/60" style={{ borderRadius: radius.px }} aria-hidden="true" />
              <code className="font-mono text-xs">--{radius.name} · {radius.px}px</code>
              <span className="text-xs text-muted-foreground">{radius.use}</span>
            </div>
          ))}
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>间距</CardTitle>
          <CardDescription>以 4px 为单位；组内紧、组间松，标题上方留白多于下方</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-2.5">
          {SPACES.map((space) => (
            <div key={space} className="grid grid-cols-[4rem_minmax(0,1fr)] items-center gap-4">
              <code className="font-mono text-xs text-muted-foreground">{space}px</code>
              <div className="h-2.5 rounded-sm bg-primary/70" style={{ width: space * 4 }} aria-hidden="true" />
            </div>
          ))}
        </CardContent>
      </Card>
    </div>
  );
}

export function DesignSystem() {
  return (
    <div className="flex flex-col gap-10">
      <Section title="颜色" description="读取当前主题的实际颜色，对比度按 WCAG 2.2 计算：正文不低于 4.5:1，图形与大字不低于 3:1">
        <Colours />
      </Section>
      <Section title="字体层级" description="界面字体为 Geist 与系统中文字体；数字一律等宽对齐">
        <Typography />
      </Section>
      <Section title="组件与状态" description="各页面使用的同一套组件；以下为样式示例，不是业务数据">
        <Components />
      </Section>
      <Section title="圆角与间距" description="与页面布局使用的值一致">
        <Shape />
      </Section>
    </div>
  );
}
