export type AgentContractErrorCode =
  | 'SESSION_TRANSITION_INVALID'
  | 'TERMINAL_ARTIFACT_INVALID';

export class AgentContractError extends Error {
  readonly code: AgentContractErrorCode;

  constructor(code: AgentContractErrorCode, message: string) {
    super(message);
    this.name = 'AgentContractError';
    this.code = code;
  }
}

export type AgentToolErrorCode =
  | 'TOOL_ARGUMENTS_INVALID'
  | 'TOOL_UNAUTHORIZED'
  | 'TOOL_CANCELLED'
  | 'TOOL_TIMEOUT'
  | 'TOOL_CALL_LIMIT'
  | 'TOOL_CONCURRENCY_LIMIT'
  | 'TOOL_RESULT_TOO_LARGE'
  | 'TOOL_OWNER_REQUEST_REJECTED'
  | 'TOOL_OWNER_RESOURCE_NOT_FOUND'
  | 'TOOL_OWNER_TIMEOUT'
  | 'TOOL_OWNER_UNAVAILABLE'
  | 'TOOL_OWNER_RESPONSE_INVALID'
  | 'TOOL_EXECUTION_FAILED';

export class AgentToolError extends Error {
  readonly code: AgentToolErrorCode;

  constructor(code: AgentToolErrorCode, message: string = code) {
    super(message);
    this.name = 'AgentToolError';
    this.code = code;
  }
}
