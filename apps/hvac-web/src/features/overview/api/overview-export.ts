import type { OverviewDashboardData } from "./overview-types";

export function buildOverviewCsv(data: OverviewDashboardData) {
  const rows: (string | number | null)[][] = [
    [
      "节能运营摘要",
      data.mode === "example"
        ? "示例数据 · 非核证报告"
        : "平台概况 · 收益待验证",
    ],
    ["范围", data.scopeName],
    ["期间", data.dateRange],
    ["截至时间", data.asOf],
    ["实际用电(kWh)", data.actualKWh],
    ["基线用电(kWh)", data.baselineKWh],
    ["节省电量(kWh)", data.savingsKWh],
    ["节能率(%)", data.savingsRate],
    ["估算节约费用(元)", data.savingsCny],
    ["舒适合规率(%)", data.comfort.rate],
    ["舒适数据覆盖率(%)", data.comfort.coverage],
    ["基线方法", data.baselineMethod],
    ["验证状态", "待验证"],
  ];
  return (
    "\uFEFF" +
    rows
      .map((row) =>
        row
          .map((cell) => {
            if (typeof cell === "number") return String(cell);
            const text = cell ?? "未提供";
            // CSV is opened by spreadsheets; external object labels must not execute formulas.
            const safe = /^[\s]*[=+@-]/.test(text) ? "'" + text : text;
            return '"' + safe.replace(/"/g, '""') + '"';
          })
          .join(","),
      )
      .join("\r\n")
  );
}

export function downloadOverviewSummary(data: OverviewDashboardData) {
  const url = URL.createObjectURL(
    new Blob([buildOverviewCsv(data)], { type: "text/csv;charset=utf-8" }),
  );
  const link = document.createElement("a");
  link.href = url;
  link.download = `节能运营摘要-${data.period}-${data.mode}.csv`;
  link.click();
  URL.revokeObjectURL(url);
}
