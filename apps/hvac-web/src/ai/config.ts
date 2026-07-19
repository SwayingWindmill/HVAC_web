const configuredRuntimeUrl = import.meta.env.VITE_COPILOTKIT_RUNTIME_URL?.trim() ?? '';
const configuredAgentProfile = import.meta.env.VITE_AI_AGENT_PROFILE?.trim().toLowerCase() ?? 'hvac';

export const COPILOTKIT_RUNTIME_CONFIGURED = configuredRuntimeUrl.length > 0;
export const COPILOTKIT_RUNTIME_URL = configuredRuntimeUrl || '/api/v1/copilotkit';
export const AI_AGENT_PROFILE = configuredAgentProfile === 'energyagent' ? 'energyagent' : 'hvac';
export const ENERGY_AGENT_PROFILE_ENABLED = AI_AGENT_PROFILE === 'energyagent';
export const AI_ASSISTANT_NAME = '泉来禾 AI 运维助手';
