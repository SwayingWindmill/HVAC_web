# 数字孪生的 3D 技术选型

2026-10-15，规格见 `docs/specs/digital-twin.md`。

| 方案 | 版本 | 结论 | 理由 |
| --- | --- | --- | --- |
| three.js | 0.186.1 (MIT) | ADOPT | 浏览器 3D 的事实标准 |
| React Three Fiber | 9.8.1 (MIT) | ADOPT | React 19 的 three.js 渲染器；场景按组件拆分，和现有 Router/Query 结构一致。参考演示（Dilum Sanjaya，2026-10-04）同样用它，作者明确建议不要用原生 JS 堆功能 |
| drei | 10.7.9 (MIT) | ADOPT（按需导入） | 相机控制、HTML 标签、轮廓等现成组件，避免自己重写 |
| Next.js | — | REJECT | 作者推荐 Next.js 主要为了按需加载和 SSR；本应用是登录后的 Vite SPA，TanStack Router 的路由级拆包已能按需加载，3D 场景本身也不支持 SSR |
| ThatOpen / xeokit / iTwin.js | — | REJECT | 面向 IFC/BIM 模型；站点没有 BIM 模型，xeokit 为 AGPL |
| GitHub 上的「HVAC digital twin」项目 | — | REJECT | 均为个人演示或研究代码（0–3 星），没有可复用的产品级实现 |

版本均为发布两周以上的精确版本。
