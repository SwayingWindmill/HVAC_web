---
date: 2026-11-25
category: 改进
---
- 数字孪生末端楼宇平面改由 Registry（`core_registry.sites.building_plan` / `Site.buildingPlan`）和站点部署清单（`deploy/sites/<site>.site.json`）声明，移除前端写死的单站点楼宇布局，支持多站点按各自建筑尺寸与房间分区渲染三维末端场景。
