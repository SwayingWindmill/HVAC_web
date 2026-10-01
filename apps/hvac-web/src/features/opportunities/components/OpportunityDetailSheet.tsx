import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
} from "@/components/ui/sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Timeline,
  TimelineItem,
  TimelineRail,
  TimelineMarker,
  TimelineConnector,
  TimelineContent,
} from "@/components/ui/timeline";
import type { EcoOpportunity } from "../api/opportunity-types";
import type { EnergySavingProject } from "@/features/projects/api/project-types";
import { ArrowUpRight } from "lucide-react";

export function OpportunityDetailSheet({
  opportunity: item,
  open,
  onClose,
  relatedProject,
  onViewProject,
}: {
  readonly opportunity: EcoOpportunity | null;
  readonly open: boolean;
  readonly onClose: () => void;
  readonly relatedProject?: EnergySavingProject;
  readonly onViewProject: () => void;
}) {
  return (
    <Sheet
      modal={false}
      open={open}
      onOpenChange={(value) => {
        if (!value) onClose();
      }}
    >
      <SheetContent
        showOverlay={false}
        className="w-[600px] sm:max-w-[600px] overflow-y-auto"
      >
        {item && (
          <>
            <SheetHeader>
              <SheetTitle>{item.title}</SheetTitle>
              <SheetDescription>
                {item.targetObject} · {item.location}
              </SheetDescription>
            </SheetHeader>
            <div className="space-y-5 p-4">
              <div className="grid grid-cols-3 divide-x rounded-lg border py-4">
                {[
                  {
                    label: "年费用潜力",
                    value:
                      "¥ " + item.annualSavingsCostCNY.toLocaleString("zh-CN"),
                  },
                  {
                    label: "年节电潜力",
                    value: (item.annualSavingsKWh / 1000).toFixed(1) + " MWh",
                  },
                  { label: "置信度", value: item.confidence + "%" },
                ].map((metric) => (
                  <div key={metric.label} className="px-4">
                    <p className="mb-2 text-xs">{metric.label}</p>
                    <p className="font-semibold tabular-nums">{metric.value}</p>
                  </div>
                ))}
              </div>
              <Tabs defaultValue="evidence">
                <TabsList>
                  <TabsTrigger value="evidence">分析证据</TabsTrigger>
                  <TabsTrigger value="economics">收益与风险</TabsTrigger>
                  <TabsTrigger value="activity">推进记录</TabsTrigger>
                </TabsList>
                <TabsContent value="evidence" className="space-y-5 pt-4">
                  <p className="text-sm leading-relaxed">{item.summary}</p>
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>指标</TableHead>
                        <TableHead>基准</TableHead>
                        <TableHead>观测</TableHead>
                        <TableHead>差异</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {item.evidenceMetrics.map((metric) => (
                        <TableRow key={metric.label}>
                          <TableCell>{metric.label}</TableCell>
                          <TableCell>
                            {metric.baseline} {metric.unit}
                          </TableCell>
                          <TableCell>
                            {metric.actual} {metric.unit}
                          </TableCell>
                          <TableCell>{metric.delta}</TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                  <h3 className="text-sm font-semibold">改善机制</h3>
                  <p className="text-sm leading-relaxed">
                    {item.mechanismAnalysis}
                  </p>
                </TabsContent>
                <TabsContent value="economics" className="space-y-5 pt-4">
                  <h3 className="text-sm font-semibold">
                    计算依据 <Badge variant="outline">估算</Badge>
                  </h3>
                  <p className="text-sm leading-relaxed">
                    {item.calculationMethod}
                  </p>
                  <dl className="grid grid-cols-2 gap-4 text-sm">
                    <div>
                      <dt>投资费用</dt>
                      <dd className="mt-2 font-semibold">
                        ¥ {item.investmentCostCNY.toLocaleString("zh-CN")}
                      </dd>
                    </div>
                    <div>
                      <dt>回收期</dt>
                      <dd className="mt-2 font-semibold">
                        {item.investmentCostCNY === 0
                          ? "零成本"
                          : item.paybackMonths + " 个月"}
                      </dd>
                    </div>
                  </dl>
                  <h3 className="text-sm font-semibold">建议工况</h3>
                  <p className="text-sm leading-relaxed">
                    {item.proposedCondition}
                  </p>
                  <div className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-4 text-sm leading-relaxed">
                    {item.riskNotice}
                  </div>
                </TabsContent>
                <TabsContent value="activity" className="pt-4">
                  <Timeline>
                    {item.auditHistory.map((event) => (
                      <TimelineItem key={event.time + event.action}>
                        <TimelineRail>
                          <TimelineMarker />
                          <TimelineConnector />
                        </TimelineRail>
                        <TimelineContent className="pb-5">
                          <p className="text-sm font-medium">
                            {event.action} · {event.actor}
                          </p>
                          <p className="mt-1 text-xs text-muted-foreground">
                            {event.time}
                          </p>
                          <p className="mt-2 text-sm">{event.note}</p>
                        </TimelineContent>
                      </TimelineItem>
                    ))}
                  </Timeline>
                </TabsContent>
              </Tabs>
              <section className="space-y-3 border-t pt-4">
                <h3 className="text-sm font-semibold">项目推进</h3>
                {relatedProject ? (
                  <>
                    <p className="text-sm">{relatedProject.title}</p>
                    <p className="text-sm">负责人：{relatedProject.owner}</p>
                  </>
                ) : (
                  <p className="text-sm">尚未立项</p>
                )}
                <Button variant="outline" onClick={onViewProject}>
                  <ArrowUpRight aria-hidden="true" />
                  {relatedProject ? "查看关联项目" : "查看项目组合"}
                </Button>
              </section>
            </div>
          </>
        )}
      </SheetContent>
    </Sheet>
  );
}
