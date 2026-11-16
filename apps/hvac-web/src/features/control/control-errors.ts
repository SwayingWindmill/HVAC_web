// What a refused control request means to the operator. The Gateway's problem detail is written for
// engineers in English, so the page shows these sentences for the codes it knows and a plain fallback otherwise.

export type ControlErrorCode =
  | 'COMMAND_CONTROL_LOCKED'
  | 'SAFE_SHUTDOWN_PRECONDITION_FAILED'
  | 'COMMAND_INTERLOCK_BLOCKED'
  | 'COMMAND_INTERLOCK_UNVERIFIABLE'
  | 'STEP_UP_REQUIRED'
  | 'REGISTRY_REVISION_CONFLICT'
  | 'CONTROL_EXIT_FORBIDDEN'
  | 'CONTROL_EXIT_REQUEST_INVALID'
  | 'COMMAND_CANCEL_INVALID'
  | 'COMMAND_APPROVAL_INVALID'
  | 'COMMAND_RECONCILIATION_INVALID'
  | 'CONTROL_SHUTDOWN_LOCKED'
  | 'EMERGENCY_DO_RELEASE_UNAVAILABLE'
  | 'COMMAND_IDEMPOTENCY_CONFLICT';

export const CONTROL_ERROR_MESSAGE: Readonly<Record<ControlErrorCode, string>> = {
  COMMAND_CONTROL_LOCKED: '站点已切到人工接管或安全退出，或这台设备被锁定了，平台现在不下发指令。',
  SAFE_SHUTDOWN_PRECONDITION_FAILED: '还有指令正在下发或等现场确认，先处理完再安全退出。',
  COMMAND_INTERLOCK_BLOCKED: '被水路联锁拦下：对应的水泵、冷却塔或主机状态不允许这样操作。',
  COMMAND_INTERLOCK_UNVERIFIABLE: '读不到联锁设备的运行状态，为安全起见没有下发。等读数恢复后再试。',
  STEP_UP_REQUIRED: '这是高风险指令，需要先验证身份。',
  REGISTRY_REVISION_CONFLICT: '刚才有人改过控制方式或锁定，已刷新为最新状态，请确认后再操作。',
  CONTROL_EXIT_FORBIDDEN: '你没有切换控制方式或锁定设备的权限，请联系管理员。',
  CONTROL_EXIT_REQUEST_INVALID: '填写的内容不完整：原因不能为空，最多 1000 字。',
  COMMAND_CANCEL_INVALID: '这条指令已经在下发或已结束，不能再撤回。',
  COMMAND_APPROVAL_INVALID: '这条指令已经不在等审批了，刷新指令记录看看最新状态。',
  COMMAND_RECONCILIATION_INVALID: '这条指令已经有结果了，不需要再现场确认。',
  CONTROL_SHUTDOWN_LOCKED: '这组设备被锁定，或水泵、冷却塔还在等主机停稳，这次没有可以停的设备。',
  EMERGENCY_DO_RELEASE_UNAVAILABLE: '这个站点没有登记紧急全断点位，不能紧急全断。',
  COMMAND_IDEMPOTENCY_CONFLICT: '同一条指令重复提交了，请刷新指令记录看看是否已经提交。',
};

const isControlErrorCode = (code: string): code is ControlErrorCode => Object.prototype.hasOwnProperty.call(CONTROL_ERROR_MESSAGE, code);

/** The sentence for a refusal: the known code's, or a generic one; never the Gateway's own text. */
export function controlErrorMessage(code: string | undefined, fallback: string): string {
  return code !== undefined && isControlErrorCode(code) ? CONTROL_ERROR_MESSAGE[code] : fallback;
}
