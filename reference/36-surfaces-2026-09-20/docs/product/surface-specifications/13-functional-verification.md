# 13 功能验证 / 持续调试 Surface Specification v1

> **状态：SELECTED / READY FOR WIREFRAME**  
> **日期：2026-09-14**  
> **Surface Catalog：** `13 功能验证 / 持续调试`  
> **Route intent：** `/sites/:siteId/verifications` 与 `/sites/:siteId/verifications/:verificationId`  
> **上游权威：** `PRODUCT.md` → `smart-energy-system-page-architecture-v2.md` → `global-navigation-context-interaction-contract-v1.md` → 本文件 → `DESIGN.md`  
> **设计输入声明：** 本文件不参考当前项目已有验证页、测试页、旧 commissioning 页面、旧 Ant/ProComponents 页面或旧设计稿。当前代码只可在实施阶段作为真实 Verification Requirement / Test / Evidence / Work / Strategy / Sequence / Alarm / Permission / Route contract 的候选证据来源。

---

# 1. Primary Job

功能验证 / 持续调试的唯一核心任务是：

> **针对一个明确的运行要求、控制 sequence、整改结果或策略发布，在满足测试条件的前提下，对比 Expected Behavior 与 Observed Behavior，用可追溯证据给出 Pass / Fail / Inconclusive，并在失败时形成 Corrective Action、Retest 与后续 Persistence Monitoring。**

本 Surface 是 **requirement-driven verification workspace**，不是：

- Work Order completion page；
- Alarm clear page；
- generic test checklist；
- Trend Analysis 的复制品；
- M&V savings page；
- Control Center；
- Rule/FDD page；
- “自动打勾全部通过”的 commissioning 报告生成器。

用户离开本页前应该知道：

1. 验证的 Requirement 是什么；
2. 这个 Requirement 来源于哪里、哪个版本；
3. Test Conditions 是否满足；
4. Expected Behavior 是什么；
5. Observed Behavior 实际是什么；
6. Evidence 是否完整、可信、覆盖所需窗口；
7. 当前结果是 Pass / Fail / Inconclusive 中哪一种；
8. Fail / Inconclusive 的原因是什么；
9. 是否需要 corrective work / sequence change / data repair；
10. 是否需要 Retest；
11. 是否需要持续监测其 persistence。

---

# 2. 主要用户

## Primary

### Commissioning / MBCx Engineer

负责执行、复核、记录 functional verification，并推动整改和 retest。

### Controls Engineer

验证 control sequence、setpoint reset、interlock、stage transition、command/readback 和 strategy implementation。

### HVAC Operations Engineer

确认工单或控制修改后系统确实恢复到预期运行。

## Secondary

- Maintenance Supervisor：查看 completed work 是否通过 verification；
- Energy Manager：确认节能措施 operationally verified 后再进入 M&V；
- Diagnosis Engineer：用 verification 结果支持/反驳 Hypothesis；
- Site Manager：查看 failed / overdue / recurring verification；
- Data Engineer：处理因为数据质量导致的 Inconclusive。

---

# 3. 外部最佳实践依据

## 3.1 ASHRAE Guideline 36-2024 — Functional Test 用来确认 Sequence Implementation

ASHRAE Guideline 36 的公开材料明确说明其目标包括高效 HVAC、控制稳定性和实时 FDD，并描述 functional tests；这些测试用于确认 sequence of operation 的实现。

来源：

- https://www.ashrae.org/technical-resources/standards-and-guidelines/read-only-versions-of-ashrae-standards
- https://www.ashrae.org/File%20Library/Professional%20Development/Learning%20Portal/Instructor-Led%20Training/Online%20Instructor-Led/FINAL_Guideline-36-Webinar_November-9-2020.pdf
- https://www.ashrae.org/professional-development/all-instructor-led-training/catalog-of-instructor-led-training/guideline-36-best-in-class-hvac-control-sequences

**本页采用：**

- Requirement 必须可追溯到 sequence / design intent / operating requirement；
- functional verification 比较 expected vs observed；
- sequence version 是一等事实；
- real-time FDD 不能替代 functional test。

## 3.2 ASHRAE Commissioning — Planning / Assessing / Implementing / Verifying / Documenting

ASHRAE Existing Building Commissioning 的公开说明把 commissioning 描述为系统化、质量导向的过程，涵盖 planning、assessing、investigating、implementing、verifying 和 documenting performance。

来源：

- https://www.ashrae.org/technical-resources/bookstore/commissioning
- https://www.ashrae.org/technical-resources/standards-and-guidelines/titles-purposes-and-scopes

**本页采用：**

- verification result 必须有 evidence；
- corrective action 后需要 retest / reverification；
- documentation 是 workflow 的组成部分，不是事后截图。

## 3.3 DOE Monitoring-Based Commissioning — Commissioning 是持续过程

DOE/FEMP 将 MBCx 定义为 commissioning process 对建筑/能源系统的持续应用，并强调持续监测可及早发现性能退化、支撑 savings persistence 和 O&M 改进。

来源：

- https://www.energy.gov/femp/articles/best-practices-enhancing-performance-contracts-monitoring-based-commissioning
- https://www.energy.gov/cmei/femp/articles/enhancing-performance-contracts-monitoring-based-commissioning
- https://betterbuildingssolutioncenter.energy.gov/smart-energy-analytics-guidance-reports

**本页采用：**

- Functional Verification 不止一次性 acceptance；
- 可有 recurring / persistence checks；
- 失败、退化、snapback 可以重新进入 corrective cycle；
- ongoing monitoring 与一次 test result 分开。

## 3.4 DOE EMIS Operations Support — Corrective Action 后必须 Verify Improvement

DOE EMIS Operations Support 的流程：

```text
Identify & Prioritize
→ Validate / Diagnose / Triage
→ Implement Corrective Actions
→ Verify Improvement
→ Monitor / Maintain
```

来源：

- https://www.energy.gov/cmei/femp/operations-support-energy-management-information-systems

**本页采用：**

- Work Completed ≠ improvement verified；
- Diagnosis / Work 的后续正式落在本页；
- verification 结果可以回流 Diagnosis / Work / Operations。

## 3.5 DOE OpenBuildingControl — Specification / Implementation / Verification 应形成正式链

DOE OpenBuildingControl 将 control sequence specification、simulation/evaluation、implementation 和 verification 连接起来，并强调 formal process 去验证 control implementation。

来源：

- https://www.energy.gov/cmei/buildings/openbuildingcontrol
- https://www.energy.gov/cmei/buildings/articles/open-building-control-simulation-specification-and-verification-control
- https://www.energy.gov/sites/default/files/2023-05/bto-peer-2023-obc.pdf

**本页采用：**

```text
Requirement / Sequence Spec
→ Implementation Revision
→ Functional Test Definition
→ Observed Execution
→ Verification Result
```

不能只保存“Pass”而丢掉 spec / implementation revision。

## 3.6 LBNL BOPTEST — Control Performance 需要可重复、条件明确的测试

BOPTEST 为建筑控制策略提供 simulation-based benchmarking/testing framework，强调标准化测试环境、可重复比较和明确性能评估。

来源：

- https://bies.lbl.gov/publications/building-optimization-testing

**本页采用：**

- test conditions 必须明确；
- automated/simulated test 与 field verification 分开标记；
- test result 必须可重放其条件和 evidence；
- 不允许在条件不满足时强行 Pass/Fail。

---

# 4. Verification Domain Vocabulary

## 4.1 Requirement

需要被验证的预期行为。

来源可以是：

```text
ASHRAE Guideline 36 sequence
Project control sequence
Owner / Current Facility Requirement
Work completion requirement
Strategy rollout requirement
Commissioning issue
Diagnosis next verification
Manufacturer/OEM requirement
```

至少包含：

- requirement identity；
- title / statement；
- source；
- version/revision；
- applicable system/object；
- effective window；
- verification method / criteria owner。

## 4.2 Test Definition

定义如何验证 Requirement：

- Preconditions；
- Test Conditions；
- Stimulus / operating scenario；
- Expected Behavior；
- Required Points / Evidence；
- Pass/Fail criteria；
- observation window；
- tolerance；
- safety / authorization constraints。

## 4.3 Test Run

一次具体执行：

```text
Run #VR-20260914-002
Started 13:20
Ended 13:44
```

Test Definition ≠ Test Run。

## 4.4 Observed Behavior

本次 Test Run 中真实观测到的状态/响应。

## 4.5 Verification Result

只能是 owner-defined result，例如：

```text
PASS
FAIL
INCONCLUSIVE
NOT_RUN
CANCELLED
```

UI 不擅自压成 boolean。

## 4.6 Retest

针对同一 Requirement 在 corrective action 后执行的新 Test Run。

Retest 是新 run，不覆盖旧 run。

## 4.7 Persistence Check

在后续运行窗口重新确认已通过行为仍然持续成立。

Persistence Check ≠ M&V。

---

# 5. Mandatory Semantic Separation

以下全部禁止混同：

```text
Work Completed = Verification Passed
Alarm Cleared = Verification Passed
Finding Resolved = Verification Passed
Command ACK = Verification Passed
Readback matched once = Verification Passed
No alarm = Verification Passed
No fault finding = Verification Passed
Pass = Savings Verified
Functional Verification = M&V
Retest = overwrite previous test
Persistence Check = energy savings claim
Test not run = Pass
Missing evidence = Pass
Missing evidence = Fail
```

正确关系：

```text
Requirement
↓
Test Definition
↓
Test Run
↓
Observed Behavior + Evidence
↓
Pass / Fail / Inconclusive
↓
Corrective Action (if needed)
↓
Retest
↓
Persistence Monitoring
```

---

# 6. Primary Questions

## Q1 — 我们要验证什么？

显示 Requirement、来源、revision、scope。

## Q2 — 当前能不能有效执行这个测试？

显示 Test Preconditions / Conditions：

- operating mode；
- occupancy；
- load；
- weather/environment；
- equipment availability；
- sensor/data availability；
- safety / authorization；
- test window；
- required sequence/strategy revision。

## Q3 — 正确行为应该是什么？

显示 Expected Behavior，不能只显示“Pass criteria hidden in backend”。

## Q4 — 实际发生了什么？

显示 Observed Behavior、timeline、point evidence、command/readback/state transitions。

## Q5 — 结果是什么？

只能由 authoritative Verification owner 得出：

```text
Pass
Fail
Inconclusive
```

## Q6 — 如果失败，下一步是什么？

进入：

- Corrective Work；
- Diagnosis；
- Control/Strategy revision；
- Data Quality repair；
- Retest。

## Q7 — 通过后会不会再退化？

如果 requirement 需要 persistence：

- next persistence check；
- ongoing monitoring；
- snapback/reopen criteria。

---

# 7. Route / URL State Ownership

Canonical routes：

```text
/sites/:siteId/verifications
/sites/:siteId/verifications/:verificationId
```

推荐 Search Params：

```text
view           // queue | failed | inconclusive | retest | passed | persistence
q
system
asset
sourceType
result
owner
requirementType
due
from
to
sort
page
size
selectedRun
```

Detail route 使用 `selectedRun` 保存当前 Test Run 上下文。

不进入 URL：

- chart cursor；
- local disclosure；
- transient test-control dialog；
- unsaved note；
- temporary live-follow state。

---

# 8. Entry Contract

## 从 Work Order Detail

携带：

- Work identity；
- source issue；
- completion timestamp；
- execution summary；
- changed component/settings；
- verification requirement；
- relevant points；
- return-to-work context。

## 从 Diagnosis

携带：

- investigation；
- Hypothesis；
- expected behavior；
- required evidence；
- evidence window。

## 从 Strategy / Control

携带：

- strategy/revision；
- published execution；
- affected systems；
- expected control behavior；
- command/readback facts。

## 从 Alarm / Operations

携带 object/system、condition、time window 与 requirement source。

---

# 9. Exit Contract

主要出口：

```text
Verification
→ Work Order
→ Diagnosis
→ Trend Analysis
→ Device Detail
→ System Operations
→ Control / Strategy
→ Data Quality
→ M&V (after operational verification, when applicable)
```

所有出口保留 requirement / run / evidence / source context。

---

# 10. Responsibility Boundary

本 Surface 拥有：

- verification queue；
- requirement projection；
- test definition projection；
- test run lifecycle；
- precondition/condition visibility；
- observed behavior；
- evidence links；
- result；
- corrective-action handoff；
- retest history；
- persistence status。

不拥有：

- Work execution；
- Alarm physical state；
- Root Cause confirmation；
- control strategy authoring；
- sequence specification editing；
- historian truth；
- M&V savings calculation；
- safety authorization；
- frontend-derived pass/fail。

---

# 11. Information Architecture

```text
Verification Queue
  Failed / Inconclusive / Due for Retest / New / Persistence Due

Selected Verification
  Context Header
  Requirement
  Source / Revision / Scope

  Test Definition
    Preconditions
    Test Conditions
    Expected Behavior
    Pass/Fail Criteria

  Active / Selected Test Run
    Run metadata
    Observed Behavior
    Evidence Workspace
    Event / State timeline

  Result
    Pass / Fail / Inconclusive
    Result rationale
    Reviewer / method

  Corrective Action / Retest
    related work
    retest requirement
    next run

  Persistence
    next check
    ongoing monitoring
    snapback history

  Audit / History
```

默认是 **Queue + Durable Verification Detail**，不是几十张测试卡片。

---

# 12. Verification Queue

默认优先显示：

```text
Failed
Inconclusive
Overdue
Due for Retest
Newly changed sequence / strategy
Persistence failed / due
```

核心列：

- Verification / Requirement；
- Scope；
- Source；
- Latest Result；
- Latest Run；
- Owner；
- Due；
- Corrective Action；
- Retest；
- Persistence。

默认排序必须可解释，不能用隐藏 risk score。

---

# 13. Requirement Contract

Requirement 必须显示：

```text
Requirement statement
Source
Revision/version
Applicable object/system
Effective date
Owner
```

示例：

> `AHU-03 supply-air temperature reset shall follow zone demand between configured bounds.`

如果 Requirement source/revision unavailable：

> `Requirement source unavailable`

不能根据当前 BAS logic 反推“设计意图”。

---

# 14. Test Definition Contract

Test Definition 是稳定、可版本化对象。

至少包含：

```text
Preconditions
Test Conditions
Stimulus / scenario
Expected Behavior
Observed variables
Pass criteria
Fail criteria
Inconclusive criteria
Evidence window
Timeout / duration
Safety constraints
```

Test definition revision 必须与 Test Run 绑定。

后续 definition 更新不能改变历史 run 的 criteria。

---

# 15. Preconditions Contract

Preconditions 回答：

> **是否具备开始测试的前置条件？**

例如：

- asset in service；
- required sensors GOOD；
- operator authorization；
- no conflicting override；
- correct sequence revision deployed；
- safe operating state；
- required occupancy/load window available。

`Preconditions satisfied` 只能来自 Verification/Test owner。

禁止前端：

```text
all points non-null → ready to test
no alarm → safe to test
asset online → preconditions satisfied
```

---

# 16. Test Conditions Contract

Test Conditions 是本次 Run 的实际环境，而不是 Test Definition 的目标条件。

必须能记录/引用：

```text
Operating mode
Load
Occupancy
Outdoor condition
Equipment availability
Overrides
Control authority
Point quality
Sequence/strategy revision
```

如果 required conditions 未满足：

```text
NOT RUN
or
INCONCLUSIVE
```

按 owner policy；不能强行判 Fail。

---

# 17. Expected Behavior Contract

Expected Behavior 必须 human-readable，同时可关联 machine-readable criteria。

示例：

```text
When zone cooling demand rises above threshold,
SAT setpoint shall reset downward within allowed rate,
remain within configured bounds,
and settle without sustained oscillation.
```

专业层可展开：

- transition condition；
- timing；
- tolerance；
- min/max bounds；
- reset logic；
- interlocks；
- fail-safe behavior。

不要只显示内部 rule expression。

---

# 18. Observed Behavior Contract

Observed Behavior 必须来自真实 Test Run evidence。

包含：

- state/mode transitions；
- setpoint trajectory；
- commands；
- readbacks；
- measured response；
- timing；
- alarms/findings；
- operator intervention；
- override；
- quality gaps。

必须保持：

```text
Intent ≠ Attempt ≠ ACK ≠ Readback ≠ Verified Behavior
```

---

# 19. Evidence Workspace

遵循 05 Trend Analysis 的 chart semantics。

默认展示少量 verification-critical series：

```text
Expected / Target
Observed
Relevant input/disturbance
State/mode
```

规则：

- same-unit overlay allowed；
- different units → aligned small multiples；
- no default dual-Y-axis；
- missing → gap；
- stale/suspect explicit；
- event lanes 显示 command/mode/work/test markers；
- exact-value table available；
- full analysis deep-link to Trend。

---

# 20. Pass / Fail / Inconclusive Contract

## Pass

所有 mandatory criteria 在适用条件下满足，evidence 足够。

## Fail

一个或多个 mandatory criteria 在有效 test conditions 下明确不满足。

## Inconclusive

例如：

- evidence incomplete；
- required operating condition 未出现；
- sensor quality insufficient；
- operator intervention污染 test；
- test interrupted；
- expected criteria ambiguous/outdated；
- conflicting evidence。

`Inconclusive` 是正式工程结果，不是 error state。

禁止：

```text
Inconclusive → Fail
Inconclusive → Pass
```

---

# 21. Result Authority Contract

Result 可以由：

```text
Automated criteria engine
Commissioning engineer review
Hybrid automated + reviewed workflow
```

但必须显示：

- result owner/method；
- test definition revision；
- run identity；
- evidence set；
- evaluated at；
- reviewed/approved by（若需要）；
- rationale / failed criteria。

Frontend 不自行计算最终 Pass/Fail。

---

# 22. Automated Test Boundary

自动化 test 可以：

- orchestrate allowed stimulus；
- collect evidence；
- evaluate machine-readable criteria；
- generate draft result。

但高风险 control stimulus 必须服从 Control/Safety authority。

Functional Verification 页面不能绕过：

- control permission；
- interlock；
- approval；
- operating constraint；
- safety workflow。

“为了测试”不是越权控制的理由。

---

# 23. Field / Manual Verification

某些 Requirement 需要现场人工验证：

- damper/valve physical movement；
- sensor reference measurement；
- actuator linkage；
- equipment rotation/noise；
- installation/configuration；
- inaccessible BAS state。

人工 evidence 必须记录：

- observer；
- timestamp；
- method；
- instrument/reference；
- observation；
- attachment；
- quality/limitation。

Manual observation 不自动变 telemetry truth。

---

# 24. Corrective Action Contract

Fail 后至少可以进入：

```text
Create / Link Work Order
Return to Diagnosis
Open Control / Strategy revision
Open Data Quality issue
```

Corrective Action 必须关联：

- failed requirement；
- failed criteria；
- run；
- evidence；
- recommended action（若有）。

Corrective Work 完成后不会自动把原 Verification 改 Pass，而是创建/要求 Retest。

---

# 25. Retest Contract

Retest 是新 Test Run。

必须保留：

```text
Original Run: FAIL
Corrective Work: WO-1032
Retest Run: PASS
```

历史不能变成：

```text
Latest = PASS
→ old fail disappears
```

Retest 可以使用：

- same test definition revision；
- newer approved revision（必须明确）。

如果 criteria 变化，要能解释为什么前后结果不可直接等价比较。

---

# 26. Persistence Monitoring Contract

某些 Verification 在通过后需要持续确认。

显示：

```text
Persistence required
Next check
Monitoring rule
Last persistence result
Snapback / regression events
```

Persistence fail 可以：

- reopen verification；
- create Diagnosis；
- create Work；
- trigger retest。

但不能自动修改历史 Pass run。

---

# 27. Functional Verification vs M&V

必须永久分离。

## Functional Verification

回答：

> 系统 / 设备 / sequence / strategy 是否按设计和整改要求运行？

证据：

- control/state behavior；
- sequence transitions；
- setpoint response；
- command/readback；
- functional test observations。

## M&V

回答：

> 实际节省了多少 energy / cost / demand？

需要：

- baseline；
- reporting period；
- adjustments；
- measurement boundary；
- uncertainty/model quality。

所以：

```text
Functional Verification PASS
≠ Savings Verified
```

但它可以成为进入 M&V 的 operational verification prerequisite。

---

# 28. Strategy / Control Verification

Control / Strategy 发布后可以创建 Verification Requirement，例如：

- schedule applied；
- setpoint reset works；
- staging sequence works；
- command/readback consistent；
- comfort guardrails maintained；
- no unexpected override/conflict。

必须保留 strategy revision / publish / execution identity。

不能验证“当前策略”却把结果挂在旧 revision 上。

---

# 29. Alarm / Diagnosis Relationship

Verification result 可以成为新的 authoritative evidence：

```text
PASS
→ supports corrective effectiveness

FAIL
→ contradicts “issue resolved” assumption

INCONCLUSIVE
→ leaves diagnosis unresolved
```

但是：

```text
PASS ≠ Alarm ACK
PASS ≠ Alarm physical recovery
PASS ≠ Root Cause confirmation automatically
```

各 owner 独立更新。

---

# 30. Data Quality Contract

数据质量可能决定 Test Run 是否有效。

显示：

- coverage；
- missing；
- stale；
- suspect/bad；
- clock sync；
- point mapping；
- calibration/reference issues。

如果 mandatory evidence quality 不满足：

> `Inconclusive — required evidence quality not satisfied`

不能用 last-good 值填满测试。

---

# 31. Queue Views

可提供：

```text
Needs Attention
Failed
Inconclusive
Due for Retest
New / Ready to Test
Passed
Persistence Due
```

默认优先 `Needs Attention`，而不是 Passed history。

---

# 32. Data Authority Contract

## Requirement / Test Definition

Owner：Commissioning / Verification domain。

## Sequence / Strategy Revision

Owner：Control / Strategy domain。

## Work Completion

Owner：Work Order domain。

## Alarm state

Owner：Alarm domain。

## Finding / Hypothesis

Owner：Diagnosis domain。

## Telemetry / Historian Evidence

Owner：Telemetry / Historian。

## Result / Retest / Persistence

Owner：Functional Verification domain。

## M&V result

Owner：M&V domain。

Frontend 只做 verification-centered projection 与授权 mutation invocation。

---

# 33. Query / Read Model Contract

推荐：

```text
Verification Detail Projection
  + requirement + revision
  + source/work/strategy summary
  + test definition
  + latest/selected run
  + condition summary
  + evidence references
  + result/criteria
  + corrective work
  + retest chain
  + persistence summary
```

Queue 使用 server-side filtering/pagination。

Evidence 使用批量 historian/query owner。

禁止：

```text
50 criteria → 50 point requests
20 runs → 20 work requests
1 evidence point → 1 historian request
```

缺 read model 时修 domain contract，不做前端并发 workaround。

---

# 34. Test Run Lifecycle / Mutation Contract

可能 mutation：

- create verification；
- schedule run；
- start run；
- cancel run；
- capture manual observation；
- evaluate run；
- review/approve result；
- request corrective action；
- request retest；
- schedule persistence check。

所有 mutation：

1. explicit user action 或 owner-approved automation；
2. authoritative revision/state validation；
3. server-confirmed result；
4. audit trail。

不做 local-only Pass/Fail。

---

# 35. Realtime Contract

Active Test Run 可以使用 Snapshot + Stream：

```text
Snapshot = run + current evidence window
Stream = authorized point/state/test events
```

Realtime 要求：

- 不抢 focus；
- 不重置 chart zoom；
- 不自动判 Pass/Fail，除非 verification engine authoritative；
- stream disconnect 不等于 test Fail；
- evidence interrupted 时可进入 Inconclusive / interrupted state；
- reconnect 后 reconcile historian + run state。

---

# 36. Loading / Empty / Partial / Error

## No verification work

成功 empty：

> `当前没有待验证项目。`

## Verification service unavailable

> `功能验证数据暂不可用。`

不能显示 0 failed / 100% pass。

## Requirement unavailable

Run/history 可以保留，但 result context 标记 requirement unavailable。

不能从当前 code/telemetry 猜 requirement。

## Historian unavailable

不能用 current telemetry 伪造历史 evidence。

## Work unavailable

Verification 仍存在；corrective-work summary unavailable。

## Control/strategy metadata unavailable

不能猜 deployed revision。

---

# 37. Permission / Capability Gating

示例：

- `verification.read` → queue/detail；
- `verification.run` → test execution；
- `verification.evaluate` → result evaluation；
- `verification.approve` → reviewed/approved result；
- `verification.retest` → create retest；
- `work.create` → corrective work；
- `control.execute` → only when automated test requires authorized control；
- `mv.read/create` → M&V handoff。

无权限动作默认不显示。

---

# 38. Visual / UX Contract

视觉必须突出：

```text
Requirement
Expected
Observed
Result
Next Action
```

而不是测试日志海洋。

## Result Visuals

Pass / Fail / Inconclusive 必须同时有文字，不只靠绿/红/黄。

## Evidence

Evidence 的视觉权重高于“漂亮的 Pass 圆环”。

禁止：

- Verification score 98/100；
- Pass-rate 大圆环占首屏；
- 自动把所有 failed criteria 折叠；
- 把 Inconclusive 弱化成灰色小字。

---

# 39. Component Mapping

```text
Queue                         → shadcn Table + TanStack Table
Result/state                  → Badge + text
Requirement                   → semantic section / definition list
Test Conditions               → structured facts
Expected vs Observed          → paired semantic sections
Evidence                      → dedicated ECharts + exact-value table
Criteria                      → semantic checklist/table, not plain checkbox form
Run history                   → ordered list/table
Corrective action             → linked work summary
Retest chain                  → timeline/list
Persistence                   → status + schedule facts
Mutations                     → Dialog / AlertDialog where appropriate
```

避免 giant Test Wizard；同一个 durable verification 页面按 section 导航即可。

---

# 40. Wireframe Intent

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ 功能验证 · AHU-03 SAT Reset                              Result: FAIL        │
│ 来源：WO-1032 · Sequence G36-AHU v7 · Run VR-204                           │
├──────────────────────────────────────────────────────────────────────────────┤
│ Requirement                                                                  │
│ SAT setpoint shall reset with zone demand within configured bounds.          │
├──────────────────────────────────────────────────────────────────────────────┤
│ Test Conditions                                                              │
│ Occupied · Cooling · Load 72% · Override none · Data quality GOOD            │
├──────────────────────────────────────────────────────────────────────────────┤
│ Expected Behavior                 │ Observed Behavior                         │
│ reset 12.8→11.2°C within 8 min    │ reset stopped at 12.3°C after 10 min     │
│ stable, no sustained oscillation  │ oscillation ±0.7°C                       │
├──────────────────────────────────────────────────────────────────────────────┤
│ Evidence                                                                      │
│ Demand        ─────────────────────────────────────────────────────────────   │
│ SAT SP target ─────────────────────────────────────────────────────────────   │
│ SAT SP actual ─────────────────────────────────────────────────────────────   │
│ SAT           ─────────────────────────────────────────────────────────────   │
├──────────────────────────────────────────────────────────────────────────────┤
│ Failed Criteria                                                               │
│ ✕ response time > 8 min                                                       │
│ ✕ setpoint did not reach expected bound                                       │
│ ✓ required conditions valid                                                    │
├──────────────────────────────────────────────────────────────────────────────┤
│ Corrective Action: WO-1048 · Control sequence review                          │
│ Retest: Required · not scheduled                                              │
│                                      [Open Work] [Schedule Retest] [Trend]    │
└──────────────────────────────────────────────────────────────────────────────┘
```

Wireframe 只表达职责、证据层级与闭环关系，不是 pixel specification。

---

# 41. Accessibility

必须：

- queue/criteria 使用 semantic table/list；
- Pass/Fail/Inconclusive 不只靠颜色；
- Expected / Observed 文本可读；
- chart 有 text/data alternative；
- keyboard 可执行 queue selection / run / evidence / deep-link；
- active run realtime 不抢 focus；
- exact values 不依赖 hover；
- error / inconclusive reason 可由读屏获取；
- dialog focus management 正确。

---

# 42. Responsive Behavior

## 1440–1720 px

首屏必须看到：

- Requirement；
- source/revision；
- latest result；
- Test Conditions；
- Expected vs Observed；
- Evidence；
- Next Action。

## 1024–1439 px

Expected/Observed 可上下排列；metadata 下沉。

## Around 768 px

仍必须能：

- 找到 failed/inconclusive verification；
- 看 requirement/conditions；
- 比较 expected/observed；
- 看 evidence/result；
- 进入 corrective work/retest/trend。

无 hover-only interaction，无 page-level horizontal overflow。

---

# 43. No Defensive Programming / No Compatibility Design

明确禁止：

```text
work completed → verification passed
alarm cleared → verification passed
no alarm → verification passed
no finding → verification passed
command ACK → verification passed
one readback sample → verification passed
missing evidence → pass
missing evidence → fail
conditions not met → fail automatically
historian unavailable → use current telemetry as test evidence
bad quality → use last good silently
requirement unavailable → infer from current BAS logic
sequence revision unavailable → assume latest
verification unavailable → not required
inconclusive → coerce fail/pass
retest pass → overwrite old fail
persistence fail → mutate historical pass
functional verification pass → savings verified
frontend evaluate criteria → authoritative result
multiple verification APIs → first success wins
old commissioning page adapter
old test checklist fallback
old boolean passed/failed compatibility
```

不建立：

```text
new Verification unavailable
→ fallback legacy checklist
```

原则：

> **One requirement → one authoritative test definition. One test run → one immutable evidence context. Expected and observed stay separate. Inconclusive is a valid result. Completion is not verification. Verification is not M&V. Unknown stays unknown.**

---

# 44. Browser Acceptance Criteria

## Requirement / Revision

- Requirement source/revision 可见；
- unavailable 不猜；
- historical run 保留当时 definition revision；
- sequence/strategy revision 与 run 对应。

## Conditions

- required conditions 明确；
- actual Test Conditions 可见；
- conditions not met 不误判 Fail；
- overrides / quality / mode 可见。

## Expected vs Observed

- 两者独立；
- observed 来自 authoritative evidence；
- command/readback/state 不混；
- exact timestamps 可查。

## Result

- Pass / Fail / Inconclusive 均支持；
- result owner/method 可见；
- failed criteria 可定位；
- Inconclusive 有明确 reason；
- frontend 不自行生成最终 result。

## Retest / History

- old runs immutable；
- retest 是新 run；
- corrective Work link 可追溯；
- criteria revision change 可见；
- latest pass 不隐藏历史 fail。

## Persistence

- persistence requirement 可见；
- next check/status 可见；
- regression 不改写历史 run；
- persistence fail 可进入 new corrective cycle。

## Functional Verification vs M&V

- Verification PASS 不显示 savings verified；
- M&V deep-link 是独立 workflow；
- energy outcome 与 functional behavior 分开。

## Partial / Error

- service unavailable 不显示 100% pass；
- historian unavailable 不伪造 evidence；
- work/strategy metadata partial failure 不改 result；
- Unknown 保持 Unknown。

## Responsive / Accessibility

- 1440–1720 px 是 coherent verification workspace；
- around 768 px 核心任务完整；
- no color-only result；
- no hover-only exact evidence；
- semantic table/list；
- no page-level horizontal overflow。

## Implementation Integrity

- 无 Ant Design / ProComponents runtime DOM；
- 无 legacy commissioning/checklist compatibility adapter；
- 无 frontend-derived Pass/Fail；
- 无 Work Completed → Pass shortcut；
- 无 Verification → M&V shortcut；
- 无 N+1 evidence queries；
- review scenario 无 runtime/network error。

---

# 45. Explicit Non-Goals

本页不是：

- Work execution page；
- root-cause investigation page；
- alarm acknowledgement page；
- control-sequence authoring IDE；
- M&V savings calculator；
- generic QA checklist；
- safety authorization system；
- raw historian；
- strategy publishing page。

---

# 46. READY FOR WIREFRAME Decision

本 Surface 满足以下条件即可进入 wireframe：

- Requirement / Test Definition / Test Run 已分离；
- Preconditions / Test Conditions 已分离；
- Expected Behavior / Observed Behavior 已分离；
- Pass / Fail / Inconclusive 语义已接受；
- result owner / evidence contract 明确；
- corrective-action / retest chain 明确；
- persistence monitoring contract 明确；
- Work Completed ≠ Verification Passed 已接受；
- Functional Verification ≠ M&V 已接受；
- strategy/sequence revision traceability 明确；
- old commissioning/checklist pages 没有设计权威。

当前决定：**SELECTED / READY FOR WIREFRAME**。
