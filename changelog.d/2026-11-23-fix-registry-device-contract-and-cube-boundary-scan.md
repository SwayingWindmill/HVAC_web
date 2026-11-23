---
date: 2026-11-23
category: 修复
---

- **修复平台网关与档案库 `Device.connectivityMode` 契约漂移导致的站点总览与设备页面 `503` 中断**：补齐 `contracts/http/templates/` 中的 Go（`platformapi.Device`）与 TypeScript（`deviceSchema`）生成的 `connectivityMode` 字段并补充 OpenAPI 与网关结构体字段一致性回归测试，使 `asset-model`、`devices` 与 `data-gaps` 接口恢复正常返回，消除总览、实时运行、运行计划、用电负荷、站点对标及运行控制页设备清单不可用与无限等待问题。
- **消除能源语义层整月回退扫描，显著加快月报与能耗查询**：在 `semantic/cube/model/cubes/energy_facts.yml` 的 `site_energy_facts`、`equipment_energy_facts` 与 `site_energy_cost_facts` 中将 `tenant_id` 下推至全部 `UNION ALL` 分支及子查询，并将明细表 `energy_interval_facts_cube` 残余时段回退收敛为严格的起止边界小时与分时跨越小时分支，避免 ClickHouse 因 `NOT (整点判断)` 无法剪枝而整月重复扫描。
