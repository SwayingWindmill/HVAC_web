/*
  斜率（SLOPE）告警规则的持久化滑动采样窗口：在 alarm_evaluation_state 上增加 sample_window 字段，
  以有界 JSONB 数组保存时间窗内的可信数值采样点，支持跨评估周期和进程重启后的趋势斜率计算。
*/
BEGIN;

SET LOCAL ROLE s4_alarm_migrator;

ALTER TABLE alarm_runtime.alarm_evaluation_state
  ADD COLUMN IF NOT EXISTS sample_window jsonb NOT NULL DEFAULT '[]'::jsonb
  CHECK (jsonb_typeof(sample_window) = 'array');

COMMENT ON COLUMN alarm_runtime.alarm_evaluation_state.sample_window IS
  '斜率类告警规则在时间窗内保留的有序采样点窗口（JSON 数组），由告警评估器按输入修订与窗口上限裁剪并更新。';

RESET ROLE;

COMMIT;
