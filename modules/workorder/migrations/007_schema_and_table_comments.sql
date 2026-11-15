\set ON_ERROR_STOP on

BEGIN;

SET LOCAL ROLE s5_work_order_migrator;

COMMENT ON SCHEMA work_order_runtime IS '运维工单全生命周期流转、子任务跟踪与完工凭证域，由工单服务负责写入。';
COMMENT ON TABLE work_order_runtime.work_order_attachment_metadata IS '工单上传图片与文件的对象存储路径、媒体类型、大小及校验散列元数据，由工单服务维护。';
COMMENT ON TABLE work_order_runtime.work_order_completion_evidence IS '工单闭环时关联的现场完工凭证类型、对象引用与采集时间，由工单服务写入。';
COMMENT ON TABLE work_order_runtime.work_order_current IS '运维工单当前状态主表，记录优先级、指派责任人、排期、任务统计与乐观锁版本号，由工单服务维护。';
COMMENT ON TABLE work_order_runtime.work_order_idempotency IS '工单创建、分派与状态推进写操作的接口幂等键记录，由工单服务维护。';
COMMENT ON TABLE work_order_runtime.work_order_mutation_audit IS '工单每次核心写操作提交的操作人、授权策略版本与审计关联流水，由工单服务写入。';
COMMENT ON TABLE work_order_runtime.work_order_note IS '值班与运维人员在工单处置过程中记录的工作备注，由工单服务写入。';
COMMENT ON TABLE work_order_runtime.work_order_source_reference IS '工单与上游触发源告警或故障诊断条目之间的关联关系，由工单服务维护。';
COMMENT ON TABLE work_order_runtime.work_order_task IS '工单拆解后的现场检查或处置子任务步骤、顺序与完成状态，由工单服务维护。';
COMMENT ON TABLE work_order_runtime.work_order_timeline IS '工单从创建、受理、阻断、完工到取消的只追加状态变更时间线，由工单服务写入。';

RESET ROLE;

COMMIT;
