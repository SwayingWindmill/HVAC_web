import { useEffect, useRef } from "react";
import { init, use } from "echarts/core";
import { LineChart, SankeyChart } from "echarts/charts";
import {
  AriaComponent,
  DataZoomComponent,
  GridComponent,
  LegendComponent,
  MarkLineComponent,
  TooltipComponent,
} from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";
import type { ECharts, EChartsOption } from "echarts";
use([
  LineChart,
  SankeyChart,
  AriaComponent,
  DataZoomComponent,
  GridComponent,
  LegendComponent,
  MarkLineComponent,
  TooltipComponent,
  CanvasRenderer,
]);
export function EngineeringChart({
  option,
  label,
  height = 320,
}: {
  readonly option: EChartsOption;
  readonly label: string;
  readonly height?: number;
}) {
  const element = useRef<HTMLDivElement>(null),
    chart = useRef<ECharts | null>(null);
  useEffect(() => {
    const instance = init(element.current!);
    chart.current = instance;
    const observer = new ResizeObserver(() => instance.resize());
    observer.observe(element.current!);
    return () => {
      observer.disconnect();
      instance.dispose();
      chart.current = null;
    };
  }, []);
  useEffect(() => {
    chart.current!.setOption(
      { ...option, animation: false, aria: { enabled: true } },
      true,
    );
  }, [option]);
  return (
    <div
      ref={element}
      role="img"
      aria-label={label}
      style={{ height }}
      className="w-full"
    />
  );
}
