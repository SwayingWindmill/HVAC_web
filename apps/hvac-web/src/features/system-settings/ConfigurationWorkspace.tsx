import { Building2, Clock3 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useSearch, useNavigate } from "@tanstack/react-router";
import { useScope } from "@/hooks/use-scope";
import { CONSUMPTION_EXAMPLE_TARIFFS } from "@/features/energy-analysis/consumption/api/consumption-example";

import { Badge } from "@/components/ui/badge";

import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";

import {
  Table,
  TableHeader,
  TableBody,
  TableHead,
  TableRow,
  TableCell,
} from "@/components/ui/table";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart";
import { BarChart, Bar, XAxis, YAxis, CartesianGrid } from "recharts";
import {
  WorkspaceHeader,
  WorkspaceQueryState,
  isWorkspaceExample,
} from "@/components/analysis/workspace-parts";

const siteProfiles: Record<
  string,
  { city: string; area: number; climate: string }
> = {
  "site:site-01": { city: "上海", area: 268000, climate: "夏热冬冷" },
  "site:site-02": { city: "深圳", area: 185000, climate: "夏热冬暖" },
  "site:site-03": { city: "北京", area: 142000, climate: "寒冷" },
};
const tariff = CONSUMPTION_EXAMPLE_TARIFFS.map((item) => ({
  name: item.period,
  price: item.rate,
  hours: item.hoursDesc,
}));
export function ConfigurationWorkspace() {
  const { currentScope } = useScope();
  const search = useSearch({ from: "/_app/settings/" });
  const navigate = useNavigate({ from: "/settings/" });
  const query = useQuery({
    queryKey: ["configuration-review", currentScope.id],
    queryFn: async () => {
      if (!isWorkspaceExample) throw new Error("正式配置未接入");
      if (currentScope.type !== "site")
        throw new Error("请选择站点查看运行配置");
      return siteProfiles[currentScope.id];
    },
    retry: false,
  });
  const profile = query.data;
  return (
    <main className="mx-auto max-w-[1600px] space-y-5 p-6">
      <WorkspaceHeader title="系统配置" />
      <WorkspaceQueryState pending={query.isPending} error={query.error} />
      {profile && (
        <Tabs
          value={search.view ?? "site"}
          onValueChange={(view) =>
            void navigate({
              search: (prev) => ({ ...prev, view: view as "site" | "tariff" }),
            })
          }
        >
          <TabsList>
            <TabsTrigger value="site" className="inline-flex items-center gap-2"><Building2 className="size-4" aria-hidden="true" />站点与运行上下文</TabsTrigger>
            <TabsTrigger value="tariff" className="inline-flex items-center gap-2"><Clock3 className="size-4" aria-hidden="true" />电价与分时</TabsTrigger>
          </TabsList>
          <TabsContent value="site">
            <div className="grid grid-cols-12 gap-5">
              <Card className="col-span-7">
                <CardHeader>
                  <CardTitle>{currentScope.name}</CardTitle>
                </CardHeader>
                <CardContent>
                  <dl className="space-y-5">
                    {[
                      ["所在城市", profile.city],
                      [
                        "建筑面积",
                        profile.area.toLocaleString("zh-CN") + " m²",
                      ],
                      ["气候区", profile.climate],
                      ["业务时区", "Asia/Shanghai · UTC+8"],
                    ].map(([label, value]) => (
                      <div
                        key={label}
                        className="flex justify-between border-b pb-4 text-sm"
                      >
                        <dt>{label}</dt>
                        <dd className="font-medium">{value}</dd>
                      </div>
                    ))}
                  </dl>
                </CardContent>
              </Card>
              <Card className="col-span-5">
                <CardHeader>
                  <CardTitle>数据与运行配置</CardTitle>
                </CardHeader>
                <CardContent className="space-y-5">
                  <div className="flex justify-between text-sm">
                    <span>正式计量边界</span>
                    <Badge variant="outline">待接入</Badge>
                  </div>
                  <div className="flex justify-between text-sm">
                    <span>天气源绑定</span>
                    <Badge variant="outline">待接入</Badge>
                  </div>
                  <div className="flex justify-between text-sm">
                    <span>业务日历</span>
                    <Badge variant="outline">待接入</Badge>
                  </div>
                  <div className="flex justify-between text-sm">
                    <span>配置审批记录</span>
                    <Badge variant="outline">待接入</Badge>
                  </div>
                </CardContent>
              </Card>
            </div>
          </TabsContent>
          <TabsContent value="tariff" className="space-y-5">
            <Card>
              <CardHeader>
                <CardTitle className="flex items-center gap-2"><Clock3 className="size-4" aria-hidden="true" />分时电价 · 元/kWh</CardTitle>
              </CardHeader>
              <CardContent>
                <ChartContainer
                  className="h-[270px] w-full"
                  config={{
                    price: { label: "电价 · 元/kWh", color: "var(--chart-1)" },
                  }}
                >
                  <BarChart data={tariff} accessibilityLayer>
                    <CartesianGrid vertical={false} />
                    <XAxis dataKey="name" tickLine={false} axisLine={false} />
                    <YAxis tickLine={false} axisLine={false} />
                    <ChartTooltip content={<ChartTooltipContent />} />
                    <Bar
                      isAnimationActive={false}
                      dataKey="price"
                      fill="var(--color-price)"
                      radius={[4, 4, 0, 0]}
                      maxBarSize={80}
                    />
                  </BarChart>
                </ChartContainer>
              </CardContent>
            </Card>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>时段</TableHead>
                  <TableHead>每日时间</TableHead>
                  <TableHead>电价 · 元/kWh</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {tariff.map((x) => (
                  <TableRow key={x.name}>
                    <TableCell className="font-medium">{x.name}</TableCell>
                    <TableCell>{x.hours}</TableCell>
                    <TableCell>{x.price.toFixed(3)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </TabsContent>
        </Tabs>
      )}
    </main>
  );
}
