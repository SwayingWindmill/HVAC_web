export {
  AgentContractError,
  AgentToolError,
  type AgentContractErrorCode,
  type AgentToolErrorCode,
} from './internal/errors.js';

export {
  INVESTIGATION_COMPLETE_TOOL_NAME,
  INVESTIGATION_REQUEST_INPUT_TOOL_NAME,
  parseInvestigationComplete,
  parseInvestigationRequestInput,
  type AgentArtifact,
  type AgentEvidenceRef,
  type AgentEvidenceRefArtifact,
  type AgentFindingArtifact,
  type AgentInputRequestArtifact,
  type AgentInputResponseArtifact,
  type AgentLimitationArtifact,
  type AgentOwner,
  type AgentProposalArtifact,
  type AgentTerminalArtifact,
  type AgentTerminalToolName,
  type InvestigationComplete,
  type InvestigationInputChoice,
  type InvestigationInputResponse,
  type InvestigationOutcome,
  type InvestigationRequestInput,
} from './internal/artifacts.js';

export {
  HVAC_AGENT_EVENT_VERSION,
  type AgentEvent,
  type AgentEventSink,
  type AgentEventType,
  type AgentSessionSnapshot,
} from './internal/events.js';

export {
  type AgentEngine,
  type AgentEngineInput,
  type AgentEngineResult,
} from './internal/engine.js';

export {
  type AgentModelRef,
  type AgentRunBudget,
  type AgentRunContext,
  type AgentTool,
  type AgentToolDefinition,
  type AgentToolExecutionMode,
  type AgentToolExecutionRequest,
  type AgentToolInputSchema,
  type AgentToolReplayPolicy,
} from './internal/policy.js';

export {
  transitionAgentSession,
  type ActiveAgentSession,
  type AgentMessage,
  type AgentMessageRole,
  type AgentRun,
  type AgentRunStatus,
  type AgentRunTerminalStatus,
  type AgentRunUsage,
  type AgentSession,
  type AgentSessionStatus,
  type AgentSessionTransition,
  type AgentToolExecution,
  type AgentToolExecutionStatus,
  type InactiveAgentSession,
} from './internal/session.js';

export const agentModule = Object.freeze({
  name: 'agent',
  layer: 'domain',
  dependencies: [],
} as const);

export type AgentModule = typeof agentModule;
