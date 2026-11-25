---
date: 2026-11-25
category: 新增
---
- 部署脚本 `deploy.sh` 改为自动发现并依次应用 `deploy/sites/*.site.json` 下全部站点清单（支持 `HVAC_ONBOARDING_SITES` 定向覆盖）。
- 新增 `deploy/templates/devices/*.json` 标准 HVAC 设备模板目录（三相电表、超声波冷热量表、冷机 Modbus 控制器、水泵变频器、8 路温度模块、8 路继电器模块），并支持在站点清单中通过 `template` + `pointOverrides` 展开点位。
- 新增 `npm run site:init` 新站点清单脚手架生成器与 `npm run site:sync-api` Registry HTTP 幂等接入客户端。
