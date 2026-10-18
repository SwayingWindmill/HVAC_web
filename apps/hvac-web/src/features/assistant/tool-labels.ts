// How each read Tool is named to the operator, and the page its evidence links back to.

interface ToolPresentation {
  readonly label: string;
  /** The Site page that shows the same data, for the finding's evidence links. */
  readonly page: { readonly label: string; readonly to: string } | null;
}

const ENERGY_PAGE = { label: '能耗分析', to: '/energy-analysis/consumption' } as const;
const DEVICES_PAGE = { label: '系统与设备', to: '/operations/systems-devices' } as const;
const ALARMS_PAGE = { label: '告警', to: '/operations/alarms' } as const;

const tools: Record<string, ToolPresentation> = {
  site_get_context: { label: '读取站点信息', page: null },
  assets_list: { label: '读取机组清单', page: DEVICES_PAGE },
  equipment_list: { label: '读取设备和点位', page: DEVICES_PAGE },
  telemetry_get_current: { label: '读取当前读数', page: DEVICES_PAGE },
  telemetry_query_history: { label: '读取设备读数历史', page: DEVICES_PAGE },
  energy_query_series: { label: '读取用电曲线', page: ENERGY_PAGE },
  energy_compare_periods: { label: '对比两个时段', page: ENERGY_PAGE },
  energy_by_equipment: { label: '读取各设备用电', page: ENERGY_PAGE },
  energy_semantic_query: { label: '查询能耗指标', page: ENERGY_PAGE },
  alarm_list: { label: '读取告警', page: ALARMS_PAGE },
};

export const toolPresentation = (toolName: string): ToolPresentation => (
  tools[toolName] ?? { label: '读取平台数据', page: null }
);
