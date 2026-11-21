# ai-solver-service

独立部署的 Python 外部暖通优化推理服务，通过标准 HTTP JSON 契约（见 [ai-solver-service.v1.schema.json](../../contracts/http/ai-solver-service.v1.schema.json)）为 Go 端 `optimization-service` 的 `RoutingSolverAdapter` 提供远端求解建议。

## 接口

- `GET /healthz`：存活探针，返回 `{"status":"ok","service":"ai-solver-service"}`
- `GET /readyz`：就绪探针，返回 `{"status":"ready","service":"ai-solver-service","solverVersion":"python-hvac-v1"}`
- `POST /v1/solve`：接收 `optimization.Request` 工况与冻结约束，执行满足冷冻水防冻下限（`>= 5.0 °C`）、单步爬坡限幅与室温 P90 舒适区间的细粒度物理搜索，返回 `DRAFT` 建议及可选水泵/风机协调目标。服务端不直接下发设备指令，所有结果仍须经过 Go 端确定性物理安全护栏链校验。

## 环境变量

- `AI_SOLVER_HTTP_ADDR`：监听地址（默认 `:19099`）

## 本地运行与测试

```bash
python3 -m unittest discover -s services/ai-solver-service/tests -v
AI_SOLVER_HTTP_ADDR=127.0.0.1:19099 PYTHONPATH=services/ai-solver-service python3 -m ai_solver.main
```
