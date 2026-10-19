# 数字孪生的 3D 技术选型

2026-10-15，规格： `docs/specs/digital-twin.md`。

| 方案 | 版本 | 结论 | 理由 |
| --- | --- | --- | --- |
| three.js | 0.186.1 (MIT) | 采用 | 浏览器 3D 的通用做法 |
| React Three Fiber | 9.8.1 (MIT) | 采用 | React 19 的 three.js 渲染器；场景按组件拆分，和现有 Router/Query 结构一致。参考演示（Dilum Sanjaya，2026-10-04）同样用它，作者明确建议不要用原生 JS 堆功能 |
| drei | 10.7.9 (MIT) | 采用（按需导入） | 只用 CameraControls。场景里不放标签（信息在四角面板），所以不用 `Html`；`CameraControls.fitToBox` 会把视角吸附到坐标轴，取景改为按视场角自己计算距离 |
| @react-three/postprocessing + postprocessing | 3.1.3 / 6.39.5 (MIT) | 采用 | N8AO 环境光遮蔽和 Neutral 色调映射，接触阴影与立体感靠它；3D 分包 gzip 因此从约 263 KB 增到约 362 KB（只在本页加载）。弱 GPU 上由帧率守卫关闭 |
| GLB / glTF 模型 | — | 暂不采用 | 找不到授权清楚、尺度和材质一致的 HVAC 设备模型集；模型文件会增加首屏下载和解析时间。设备改用自建程序化资产库，以后有厂家模型时可替换单个资产 |
| Next.js | — | 不采用 | 作者推荐 Next.js 主要为了按需加载和 SSR；本应用是登录后的 Vite SPA，TanStack Router 的路由级拆包已能按需加载，3D 场景本身也不支持 SSR |
| ThatOpen / xeokit / iTwin.js | — | 不采用 | 面向 IFC/BIM 模型；站点没有 BIM 模型，xeokit 为 AGPL |
| GitHub 上的「HVAC digital twin」项目 | — | 不采用 | 均为个人演示或研究代码（0–3 星），没有可复用的产品级实现 |

版本均为发布两周以上的精确版本。
