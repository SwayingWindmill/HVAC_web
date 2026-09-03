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
