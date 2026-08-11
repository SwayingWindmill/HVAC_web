# 智慧能源系统 AI/MLOps 模型治理与上线规范

> 文档编号：SE-AI-003  
> 版本：V1.0  
> 状态：CURRENT  
> 适用范围：负荷预测、光伏预测、异常检测、能源基线、优化调度模型及其训练、评估、发布、回滚、漂移治理  
> 技术基线：Python AI Services + Go Backend + PostgreSQL + ClickHouse + Redis + Object Storage + CI/CD  
> 上位设计：预测系统详细设计、优化调度系统详细设计、CI/CD 与版本升级回滚设计、配置中心与版本发布设计、数据生命周期治理规范  
> 核心目标：建立“数据可追溯、实验可复现、模型可比较、上线可审批、运行可监控、异常可回滚”的统一模型治理体系

---

# 1. 设计目标

AI/MLOps 解决的不是：

```text
如何训练一个模型
```

而是：

```text
这个模型是谁训练的？

用了哪批数据？

用了哪些特征？

代码是哪一版？

参数是什么？

评估是否可信？

为什么允许上线？

上线以后表现怎样？

和旧模型相比怎样？

出现问题怎么退回？

三个月以后还能不能复现？
```

最终形成：

```text
Data
→ Feature
→ Experiment
→ Model
→ Validation
→ Registry
→ Shadow
→ Approval
→ Deployment
→ Monitoring
→ Retrain / Rollback
```

---

# 2. 治理对象

统一治理：

```text
Forecast Model
Optimization Model
Baseline Model
Anomaly Model
Energy Baseline Model
Feature Set
Training Dataset
Experiment
Model Artifact
Deployment
Evaluation
Drift Event
```

---

# 3. 模型分类

建议：

```text
PREDICTION
OPTIMIZATION
ANOMALY
BASELINE
CLASSIFICATION
REGRESSION
```

---

# 4. 风险等级

模型风险建议：

```text
MR1
MR2
MR3
MR4
```

---

# 5. MR1

例如：

```text
Dashboard展示型预测
非关键分析
```

无控制影响。

---

# 6. MR2

例如：

```text
运营告警辅助
能源分析
```

会影响人工判断。

---

# 7. MR3

例如：

```text
Optimization Recommendation
Assisted Dispatch
```

可能间接影响控制。

---

# 8. MR4

例如：

```text
Auto Limited / Auto Dispatch
```

具有真实控制后果。

要求最高。

---

# 9. 风险等级决定

不同等级决定：

```text
Approval
Shadow Duration
Test Scope
Rollback Requirement
Monitoring Level
Audit Depth
```

---

# 10. 模型生命周期

统一：

```text
DRAFT
EXPERIMENT
CANDIDATE
VALIDATING
SHADOW
APPROVED
ACTIVE
DEGRADED
ROLLED_BACK
RETIRED
REJECTED
```

---

# 11. DRAFT

模型定义已创建：

```text
但未开始正式实验
```

---

# 12. EXPERIMENT

正在：

```text
训练
调参
比较
```

---

# 13. CANDIDATE

达到最低评估要求：

```text
准备验证
```

---

# 14. VALIDATING

进行：

```text
离线评估
回测
数据泄漏检查
兼容性检查
```

---

# 15. SHADOW

线上真实运行：

```text
但不作为正式业务决策
```

---

# 16. APPROVED

已审批：

```text
可进入Production
```

---

# 17. ACTIVE

当前：

```text
正式生产模型
```

---

# 18. DEGRADED

模型仍运行：

```text
但输入或精度下降
```

---

# 19. ROLLED_BACK

被：

```text
上一稳定模型
```

替代。

---

# 20. RETIRED

不再用于新推理。

历史仍保留。

---

# 21. REJECTED

候选模型：

```text
未通过Gate
```

---

# 22. 模型实体

建立：

```text
model_definition
```

字段：

```text
model_code
name
domain
model_type
target
subject_type
risk_level
owner
description
```

---

# 23. Model Version

每次正式训练产生：

```text
model_version
```

不可覆盖。

---

# 24. Model Version 字段

```text
model_id
version
algorithm
feature_set_version
dataset_id
code_version
runtime_version
hyperparameters
artifact_uri
artifact_checksum
status
created_at
```

---

# 25. Code Version

必须记录：

```text
git_commit
```

或等价不可变代码标识。

---

# 26. Environment

记录：

```text
python_version
dependency_lock_hash
container_digest
```

---

# 27. 禁止只记录 requirements.txt 文本

正式训练必须记录：

```text
可复现依赖锁定
```

而不是：

```text
“大概用了这些包”
```

---

# 28. Training Dataset

建立：

```text
training_dataset
```

---

# 29. Dataset Metadata

至少：

```text
dataset_id
target
subject_scope
start_time
end_time
row_count
feature_set_version
topology_version
metric_version
quality_summary
manifest_uri
checksum
```

---

# 30. Dataset Snapshot

训练数据必须做到：

```text
可重建
```

或者：

```text
可读取冻结Snapshot
```

---

# 31. Dataset 不要求永久复制所有Raw

可以使用：

```text
Manifest
+
Query Definition
+
Archive Reference
```

重建。

但：

```text
关键模型
```

建议保存必要 Snapshot。

---

# 32. Feature Set

建立：

```text
feature_set
```

---

# 33. Feature Definition

至少：

```text
feature_code
source
transformation
window
unit
missing_policy
version
```

---

# 34. Feature Contract

训练与推理必须共享：

```text
同一 Feature Contract
```

避免：

```text
Training / Serving Skew
```

---

# 35. Training-Serving Skew

常见：

```text
训练用真实天气
线上用天气预报

训练用修正后的完整数据
线上用实时未最终化数据

训练时Timezone逻辑不同
线上不同
```

必须检测。

---

# 36. Feature Store

第一阶段不强制建设复杂 Feature Store。

可采用：

```text
Feature Definition
+
Reusable Python Feature Builder
+
Feature Manifest
```

---

# 37. Feature Materialization

高频特征可：

```text
预计算
```

但必须保证：

```text
Source Lineage
```

---

# 38. Experiment

建立：

```text
ml_experiment
```

---

# 39. Experiment Run

字段：

```text
experiment_id
run_id
dataset_id
feature_set_version
code_version
algorithm
hyperparameters
random_seed
metrics
artifact_uri
status
```

---

# 40. Random Seed

所有可控随机过程：

```text
必须记录seed
```

---

# 41. Experiment Tracking

必须支持：

```text
Run Compare
```

比较：

```text
Dataset
Features
Parameters
Metrics
Artifact
```

---

# 42. Baseline

每个模型必须有：

```text
Baseline
```

---

# 43. Prediction Baseline

例如：

```text
Last Value
Yesterday
Last Week
Moving Average
```

---

# 44. Optimization Baseline

例如：

```text
No ESS
Rule-based ESS
Existing Operator Policy
```

---

# 45. Baseline 不能删除

长期保留：

```text
用于Regression
```

---

# 46. Validation

Candidate 至少通过：

```text
Schema Validation
Dataset Quality
Leakage Check
Metric Check
Baseline Comparison
Stability Check
Compatibility Check
```

---

# 47. Data Leakage Check

必须检查：

```text
未来Target
未来Weather Actual
未来Settlement Result
未来修正值
```

是否泄露。

---

# 48. Time Split

时间序列：

```text
禁止随机Shuffle后直接评估
```

使用：

```text
Temporal Split
Rolling Backtest
```

---

# 49. Evaluation Dataset

必须独立：

```text
Train
Validation
Test
```

---

# 50. Model Metric

预测常用：

```text
MAE
RMSE
WAPE
MAPE
Bias
Peak Error
```

---

# 51. Optimization Metric

常用：

```text
Expected Saving
Actual Saving
Constraint Violation
Execution Rate
Dispatch Deviation
Solve Time
```

---

# 52. Gate Threshold

阈值：

```text
按Target / Site / Model配置
```

不硬编码全局万能值。

---

# 53. Candidate Comparison

候选必须比较：

```text
Baseline
Current Champion
Previous Stable
```

---

# 54. Champion

当前：

```text
Production Active Model
```

---

# 55. Challenger

候选：

```text
Shadow / Experimental
```

---

# 56. Champion / Challenger Rule

Challenger 只有：

```text
显著更好
或
具备明确业务价值
```

才替换 Champion。

---

# 57. 不以单一离线指标升级模型

例如：

```text
WAPE下降0.1%
```

不一定值得上线。

还要看：

```text
稳定性
复杂度
成本
鲁棒性
```

---

# 58. Shadow Mode

所有 MR3/MR4 模型：

```text
必须Shadow
```

---

# 59. Shadow Input

使用：

```text
真实生产输入
```

---

# 60. Shadow Output

保存：

```text
但不影响业务执行
```

---

# 61. Shadow Evaluation

观察：

```text
Accuracy
Failure Rate
Latency
Fallback Rate
Stability
```

---

# 62. Shadow Duration

由风险等级配置。

例如：

```text
MR1: 可无
MR2: 数天
MR3: 1~2周
MR4: 2~4周或更长
```

具体按项目决定。

---

# 63. Deployment

建立：

```text
model_deployment
```

---

# 64. Deployment Scope

支持：

```text
GLOBAL
TENANT
SITE
SUBJECT
```

---

# 65. Model Canary

模型上线同样采用：

```text
Canary
```

例如：

```text
1 Site
→ 5 Sites
→ 20%
→ 100%
```

---

# 66. Same Artifact Promotion

从：

```text
Staging
→ Production
```

必须：

```text
同一Model Artifact
```

不能重训后再上线。

---

# 67. Artifact Immutable

模型 Artifact：

```text
不可修改
```

---

# 68. Artifact Checksum

至少：

```text
SHA-256
```

---

# 69. Artifact Contents

建议：

```text
model binary
feature schema
preprocess definition
metadata
evaluation summary
compatibility
```

---

# 70. Model Package Manifest

示例：

```text
model_code
model_version
dataset_id
feature_set
git_commit
container_digest
artifact_checksum
created_at
```

---

# 71. Model Registry

PostgreSQL 保存：

```text
Model Definition
Version
Artifact
Evaluation
Status
Deployment
Approval
```

---

# 72. Object Storage

保存：

```text
Artifact
Dataset Manifest
Evaluation Report
Backtest Result
Explainability Output
```

---

# 73. Redis

只可缓存：

```text
Active Model Pointer
Latest Evaluation
```

Redis 不是 Registry Source of Truth。

---

# 74. Promotion Workflow

```text
Experiment
↓
Candidate
↓
Offline Validation
↓
Shadow
↓
Review
↓
Approval
↓
Canary
↓
Active
```

---

# 75. Approval Matrix

建议：

```text
MR1
Model Owner

MR2
Model Owner + Domain Reviewer

MR3
Model Owner + Domain + Platform

MR4
Model Owner + Domain + Safety/Control + Approver
```

---

# 76. Maker != Approver

MR3/MR4：

```text
训练者不能单独批准上线
```

---

# 77. Approval Evidence

记录：

```text
offline metrics
shadow metrics
known limitations
fallback
rollback plan
```

---

# 78. Known Limitation

模型上线必须声明：

```text
适用范围
不适用范围
数据要求
已知弱点
```

---

# 79. Model Card

建议每个 ACTIVE Model 生成：

```text
Model Card
```

---

# 80. Model Card 内容

```text
Purpose
Target
Owner
Training Data
Features
Algorithm
Metrics
Limitations
Risk
Fallback
Deployment Scope
```

---

# 81. Production Deployment

模型部署可采用：

```text
Embedded Artifact
Model Service
Batch Job
```

第一阶段预测：

```text
Python Forecast Service加载Artifact
```

优化：

```text
Optimization Policy + Solver
```

---

# 82. Deployment Version

应用版本和模型版本：

```text
分别管理
```

---

# 83. Compatibility Matrix

记录：

```text
Model Version
Feature Schema
Python Runtime
Service Version
Point Standard
Metric Version
Topology Version
```

---

# 84. Model 与 Point Standard

模型不能依赖：

```text
Vendor Point
```

只依赖：

```text
Standard Point / Metric
```

---

# 85. Metric Version Change

如果 Target / Feature Metric 语义变化：

```text
Model进入Compatibility Review
```

---

# 86. Topology Version Change

如果影响预测/优化 Boundary：

```text
Model进入REVIEW
```

---

# 87. Schema Compatibility

Feature 缺字段：

```text
不得静默填0
```

除非 Feature Contract 明确。

---

# 88. Fallback

每个 Production Model 必须定义：

```text
Fallback Chain
```

---

# 89. Forecast Fallback

```text
Active
→ Previous Stable
→ Baseline
→ INVALID
```

---

# 90. Optimization Fallback

```text
Optimization
→ Previous Valid Plan
→ Rule Policy
→ Automation OFF
```

---

# 91. Rollback

必须支持：

```text
one-click / controlled rollback
```

---

# 92. Rollback Target

记录：

```text
previous_stable_version
```

---

# 93. Rollback Condition

例如：

```text
Accuracy sudden degradation
Model load failure
Feature missing surge
Latency failure
Safety rejection high
Business KPI worse
```

---

# 94. Auto Rollback

MR1/MR2 可配置：

```text
自动回滚
```

MR3/MR4：

```text
通常自动Freeze + 人工确认
```

更安全。

---

# 95. Freeze

优化自动控制场景：

```text
模型异常
```

优先：

```text
Freeze Automation
```

而不是强行切另一模型继续控制。

---

# 96. Monitoring

分：

```text
Service Health
Model Health
Data Health
Business Health
Safety Health
```

---

# 97. Service Health

```text
Inference Latency
Error Rate
Artifact Load
CPU
Memory
```

---

# 98. Model Health

```text
Accuracy
Bias
Fallback
Prediction Distribution
```

---

# 99. Data Health

```text
Missing Rate
Freshness
Feature Distribution
Schema Drift
```

---

# 100. Business Health

预测：

```text
Peak Warning Accuracy
```

优化：

```text
Actual Saving
Demand Reduction
```

---

# 101. Safety Health

自动调度：

```text
Safety Rejection Rate
Control Failure
Readback Mismatch
```

---

# 102. Drift

统一：

```text
DATA_DRIFT
FEATURE_DRIFT
TARGET_DRIFT
PERFORMANCE_DRIFT
CONCEPT_DRIFT
TOPOLOGY_DRIFT
```

---

# 103. Data Drift

输入分布改变。

例如：

```text
平均负荷上升30%
```

---

# 104. Feature Drift

某个 Feature：

```text
分布明显改变
```

---

# 105. Target Drift

目标值：

```text
整体水平改变
```

---

# 106. Performance Drift

模型误差持续变差。

---

# 107. Concept Drift

输入和输出关系改变。

例如：

```text
工厂生产模式改变
```

---

# 108. Topology Drift

能源边界变化。

---

# 109. Drift Detection

第一阶段采用：

```text
统计阈值
+
业务规则
```

即可。

不需要一开始引入复杂漂移算法。

---

# 110. Drift Window

例如：

```text
Recent 7d
vs
Reference 30d
```

---

# 111. Drift Level

建议：

```text
INFO
WARNING
CRITICAL
```

---

# 112. Drift Action

```text
INFO
Observe

WARNING
Retraining Review

CRITICAL
Model Degrade / Fallback
```

---

# 113. Retraining

触发：

```text
Scheduled
Drift
Performance
Topology Change
Manual
```

---

# 114. Retrain != Auto Promote

重新训练后：

```text
仍必须走Candidate → Validation
```

不能自动替换 Production。

---

# 115. Scheduled Retrain

例如：

```text
Weekly
Monthly
```

不是越频繁越好。

---

# 116. Training Queue

训练任务：

```text
异步
```

---

# 117. Training Resource Isolation

训练与在线推理：

```text
资源隔离
```

---

# 118. Experiment Environment

Dev / Test：

```text
允许快速实验
```

Production Registry：

```text
只接受经过Gate的Artifact
```

---

# 119. Environment Promotion

统一：

```text
DEV
→ TEST
→ STAGING
→ PROD
```

---

# 120. Model 不在 Production 重训

Production：

```text
不执行临时手工训练并直接激活
```

---

# 121. CI Pipeline

模型代码 PR：

```text
Lint
Unit
Feature Test
Golden Dataset
Training Smoke
Security Scan
```

---

# 122. Model CI

Candidate Build：

```text
Dataset Resolve
Training
Backtest
Evaluation
Artifact Build
Checksum
Registry Candidate
```

---

# 123. Model CD

```text
Candidate
→ Staging Shadow
→ Approval
→ Production Canary
→ Active
```

---

# 124. Golden Dataset

必须与：

```text
SE-TEST-004
```

集成。

---

# 125. Forecast Golden

验证：

```text
Baseline
Feature Missing
Near-zero MAPE
Fallback
```

---

# 126. Optimization Golden

验证：

```text
SOC Constraint
Power Limit
Infeasible
Expired Interval
Safety Rejection
```

---

# 127. Regression

每个严重模型 Bug：

```text
新增Regression Case
```

---

# 128. Experiment Reproducibility

同：

```text
dataset
code
dependencies
seed
parameters
```

应：

```text
得到相同或容差内结果
```

---

# 129. Solver Reproducibility

优化器：

```text
固定solver version
tolerance
seed if applicable
```

---

# 130. Float Tolerance

不能要求：

```text
所有平台bit-level相同
```

应定义：

```text
numeric tolerance
```

---

# 131. Explainability

模型可提供：

```text
Feature Importance
SHAP-like explanation
Rule Trace
Constraint Explanation
```

---

# 132. Explainability 边界

解释：

```text
不是因果证明
```

必须避免误导。

---

# 133. Prediction Explain

至少能回答：

```text
哪个模型？
哪些关键输入？
当时数据是否完整？
```

---

# 134. Optimization Explain

至少：

```text
Objective
Constraints
Why Charge
Why Discharge
```

---

# 135. Human Override

高风险模型必须允许：

```text
人工禁用
```

---

# 136. Model Kill Switch

预测：

```text
Disable Model
Fallback Baseline
```

优化：

```text
Disable Automation
```

---

# 137. Kill Switch 权限

仅：

```text
Authorized Operator
```

---

# 138. Kill Switch Audit

记录：

```text
who
reason
time
scope
```

---

# 139. Data Privacy

训练数据必须遵守：

```text
Tenant Isolation
Data Minimization
```

---

# 140. Cross-Tenant Training

默认：

```text
禁止
```

除非：

```text
明确授权与治理
```

---

# 141. PII

不应将：

```text
姓名
手机号
邮箱
```

直接作为能源模型特征。

---

# 142. Sensitive Feature

任何敏感业务 Feature：

```text
需要Owner和用途说明
```

---

# 143. Security

Model Artifact：

```text
只读
签名/Checksum
```

---

# 144. Supply Chain

记录：

```text
Container Digest
Dependency Lock
Artifact Provenance
```

---

# 145. Secret

API Key：

```text
Secret Manager
```

不能进入：

```text
Notebook
Model Artifact
Dataset
```

---

# 146. Notebook

Notebook 可用于：

```text
Experiment
```

但 Production 训练逻辑必须：

```text
脚本化 / Pipeline化
```

---

# 147. 禁止 Notebook 直接生产

不能：

```text
Notebook Run
→ Save Model
→ 手工复制到Production
```

---

# 148. Training Pipeline

建议：

```text
Resolve Dataset
↓
Validate
↓
Build Features
↓
Train
↓
Backtest
↓
Evaluate
↓
Package
↓
Register
```

---

# 149. Deployment Pipeline

```text
Fetch Candidate
↓
Validate Artifact
↓
Deploy Staging
↓
Shadow
↓
Approve
↓
Canary
↓
Promote
```

---

# 150. Audit

必须记录：

```text
Experiment
Training
Register
Approve
Deploy
Promote
Rollback
Retire
Kill Switch
```

---

# 151. Model Owner

每个模型必须：

```text
owner
```

例如：

```text
Energy AI
Optimization Team
```

---

# 152. Business Owner

MR2+ 建议：

```text
business_owner
```

例如：

```text
Energy Manager
Operations
```

---

# 153. Technical Reviewer

负责：

```text
代码
性能
兼容性
```

---

# 154. Domain Reviewer

负责：

```text
目标语义
数据边界
业务合理性
```

---

# 155. Safety Reviewer

MR4：

```text
必须
```

---

# 156. Model Review Meeting

高风险模型上线前评审：

```text
Purpose
Data
Metrics
Failure Modes
Fallback
Rollback
Safety
```

---

# 157. Failure Mode

模型卡必须描述：

```text
输入缺失
极端天气
新Site
拓扑变化
设备异常
```

---

# 158. Out-of-Distribution

当输入明显超出训练分布：

```text
OOD
```

建议：

```text
DEGRADED / FALLBACK
```

---

# 159. OOD 检测

第一阶段可以：

```text
Feature Range
Quantile
Business Bounds
```

---

# 160. OOD 不自动猜正常

不能：

```text
照常返回VALID
```

---

# 161. Model SLA

定义：

```text
Availability
Latency
Freshness
Accuracy Monitoring
```

---

# 162. Prediction SLA

例如：

```text
Day-ahead在规定时间前生成
```

---

# 163. Optimization SLA

例如：

```text
Rolling Optimization在执行窗口前完成
```

---

# 164. Model SLO

监控：

```text
job success
latency
fallback rate
freshness
```

---

# 165. Error Budget

如果：

```text
Fallback Rate持续超标
```

暂停：

```text
新模型Promotion
```

---

# 166. Capacity

记录：

```text
models loaded
inference qps
training jobs
artifact size
```

---

# 167. Model Cache

在线服务：

```text
Active/Recent Model Cache
```

---

# 168. Cache Miss

从：

```text
Object Storage
```

重新加载。

---

# 169. Artifact Load Failure

立即：

```text
Fallback
```

并告警。

---

# 170. Model Retention

ACTIVE / Previous Stable：

```text
长期保留
```

---

# 171. Retired Model

至少保留：

```text
覆盖历史预测/调度审计周期
```

---

# 172. Artifact Delete

只有：

```text
无历史引用
超过Retention
无Legal Hold
```

才允许。

---

# 173. Experiment Retention

失败实验可：

```text
较短保留
```

但正式 Candidate：

```text
长期保留
```

---

# 174. Model Registry API

建议：

```text
GET  /api/v1/models
GET  /api/v1/models/{id}
GET  /api/v1/models/{id}/versions

POST /api/v1/models/{id}/versions/{version}/validate
POST /api/v1/models/{id}/versions/{version}/approve
POST /api/v1/models/{id}/versions/{version}/deploy
POST /api/v1/models/{id}/versions/{version}/rollback
```

---

# 175. Experiment API

```text
GET /api/v1/ml/experiments
GET /api/v1/ml/experiments/{id}
```

---

# 176. Evaluation API

```text
GET /api/v1/models/{id}/evaluation
```

---

# 177. Drift API

```text
GET /api/v1/models/{id}/drift
```

---

# 178. Model Card API

```text
GET /api/v1/models/{id}/card
```

---

# 179. Frontend

建议页面：

```text
Model Registry
Model Version
Experiment Compare
Evaluation
Shadow Compare
Deployment
Drift
Audit
```

---

# 180. Model Registry 页面

显示：

```text
Model
Target
Champion
Challenger
Status
Owner
Risk
```

---

# 181. Version 页面

显示：

```text
Dataset
Feature Set
Metrics
Artifact
Code Version
Deployment History
```

---

# 182. Shadow Compare

展示：

```text
Champion
vs
Challenger
vs
Actual
```

---

# 183. Drift 页面

展示：

```text
Feature Distribution
Performance Trend
Fallback
```

---

# 184. Model Health Dashboard

统一：

```text
ACTIVE
DEGRADED
FALLBACK
FAILED
```

---

# 185. Alert

建议：

```text
MODEL_INFERENCE_FAILED
MODEL_ARTIFACT_LOAD_FAILED
MODEL_ACCURACY_DEGRADED
MODEL_DRIFT_WARNING
MODEL_DRIFT_CRITICAL
MODEL_FALLBACK_HIGH
MODEL_STALE
MODEL_OOD_HIGH
```

---

# 186. Incident

MR4 模型导致：

```text
Unsafe / unexpected control
```

按：

```text
SEV-1
```

处理。

---

# 187. Incident 后

必须：

```text
Freeze
Rollback
Root Cause
Regression Test
Model Review
```

---

# 188. Drift 不一定是模型问题

例如：

```text
Point单位变了
CT/PT变了
Topology改了
```

也会表现成 Drift。

因此：

```text
先检查Data Contract
```

---

# 189. Monitoring Correlation

模型 Dashboard 应关联：

```text
Config Release
Model Deployment
Topology Change
Metric Version
```

---

# 190. Deployment Marker

可观测平台显示：

```text
Model V18 activated
```

方便定位性能变化。

---

# 191. P0 第一阶段实现

```text
Model Registry
Model Version
Training Run
Dataset Manifest
Feature Set Version
Artifact Checksum
Baseline Compare
Offline Validation
Shadow
Manual Approval
Rollback
Model Health
```

---

# 192. P1

```text
Champion/Challenger
Canary
Drift Detection
Model Card
Experiment UI
Automated Evaluation
```

---

# 193. P2

```text
Advanced Feature Store
Auto Retrain
Global Model Governance
Probabilistic Model Governance
Robust Optimization MLOps
```

---

# 194. 第一阶段不做

```text
全自动AutoML直接上线
自动调参后无审批Promote
跨Tenant默认训练
复杂Feature Store平台
```

---

# 195. Production Gate

任何模型进入 Production 至少：

```text
Owner明确

Risk Level明确

Dataset可追溯

Feature Set版本化

Code Version可追溯

Dependency可复现

Random Seed记录

No Data Leakage

Baseline存在

Candidate评估通过

Artifact Checksum通过

Compatibility通过

Fallback存在

Rollback存在

Monitoring存在

Audit存在
```

---

# 196. MR3/MR4 Additional Gate

```text
Shadow通过

Business Review通过

Safety Review通过

Kill Switch通过

Control Boundary明确

No Direct MQTT

No Device Credential

Incident Runbook存在
```

---

# 197. Model Release Checklist

```text
[ ] Model Card
[ ] Dataset
[ ] Feature Set
[ ] Code Commit
[ ] Artifact Digest
[ ] Offline Metrics
[ ] Baseline Compare
[ ] Shadow Metrics
[ ] Known Limitations
[ ] Fallback
[ ] Rollback
[ ] Owner
[ ] Approver
```

---

# 198. Rollback Checklist

```text
[ ] Freeze new promotion
[ ] Activate previous stable
[ ] Verify inference
[ ] Verify business output
[ ] Monitor fallback
[ ] Record audit
```

优化自动控制额外：

```text
[ ] Freeze Automation
[ ] Verify no stale commands
```

---

# 199. Model Retirement

流程：

```text
Disable New Use
↓
Verify No Active Deployment
↓
Mark RETIRED
↓
Retention Period
↓
Artifact Deletion if eligible
```

---

# 200. 模型版本关系

```text
Model Definition
    ↓
Version 1
Version 2
Version 3
    ↓
Deployment
    ↓
Champion / Challenger
```

---

# 201. 数据版本关系

```text
Point Standard
↓
Metric Version
↓
Topology Version
↓
Dataset Snapshot
↓
Feature Set
↓
Model Version
```

---

# 202. 完整可追溯链

预测：

```text
Forecast Result
↓
Model Version
↓
Training Run
↓
Dataset
↓
Feature Set
↓
Metric / Point / Topology
```

优化：

```text
Dispatch Plan
↓
Optimization Policy / Solver Version
↓
Forecast Versions
↓
Topology / Tariff
↓
Control Command
↓
Readback
```

---

# 203. 最终治理闭环

```text
Data
 ↓
Dataset
 ↓
Feature
 ↓
Experiment
 ↓
Candidate
 ↓
Validate
 ↓
Shadow
 ↓
Approve
 ↓
Deploy
 ↓
Monitor
 ↓
Drift / Incident
 ↓
Retrain / Rollback
```

---

# 204. 最终冻结原则

第一：

```text
没有Dataset版本
就没有可复现模型
```

第二：

```text
没有Feature版本
就没有稳定Serving
```

第三：

```text
没有Baseline
就无法证明复杂模型有价值
```

第四：

```text
没有Shadow
MR3/MR4模型不得直接上线
```

第五：

```text
模型Artifact不可变
```

第六：

```text
Staging到Production使用同一Artifact
```

第七：

```text
Retrain不等于Promote
```

第八：

```text
Drift发生时先检查数据语义和Topology
```

第九：

```text
所有Production Model必须有Fallback和Rollback
```

第十：

```text
任何具有控制影响的AI模型
都不能绕过Safety和Control
```

最终平台必须能回答：

```text
现在生产上跑的是哪个模型？

为什么是它？

谁批准的？

用了什么数据训练？

用了什么Feature？

代码是哪一版？

和上一个模型相比好在哪里？

Shadow运行了多久？

最近误差有没有恶化？

有没有出现数据漂移？

如果模型坏了会回退到哪里？

如果自动优化异常，怎么立即停止？

半年以后还能不能复现这个模型和它当时的上线决策？
```

当这些问题都能够通过系统记录直接回答，而不是依赖个人记忆时，智慧能源系统才真正具备可持续的 AI/MLOps 生产治理能力。
